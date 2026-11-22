// Package notebook is an unofficial browser-backed NotebookLM provider.
package notebook

import (
 "context"
 "crypto/rand"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "strings"
 "sync"
 "time"
)
type APIError struct { Code string `json:"code"`; Message string `json:"message"`; Status int `json:"-"`; RetryAt time.Time `json:"-"` }
func(e *APIError) Error() string {return e.Message}
func(e *APIError) HTTPStatus() int {return e.Status}
type Window struct { Window string `json:"window"`; UsedPercent float64 `json:"used_percent"`; RemainingPercent float64 `json:"remaining_percent"`; ResetsAt string `json:"resets_at,omitempty"` }
type Usage struct {MeteringEnabled bool `json:"metering_enabled"`; Windows []Window `json:"windows"`}
type Notebook struct {ID string `json:"id"`; Title string `json:"title"`; SourceCount int `json:"source_count"`}
type AccountStatus struct {
 ID string `json:"id"`; Label string `json:"label"`; SourceAccountID string `json:"source_account_id,omitempty"`; Email string `json:"email,omitempty"`; State string `json:"state"`; Plan string `json:"plan,omitempty"`
 CooldownUntil time.Time `json:"cooldown_until,omitzero"`; Error string `json:"error,omitempty"`; Notebooks []Notebook `json:"notebooks"`
 Usage *Usage `json:"usage,omitempty"`; RequestCount uint64 `json:"request_count"`; SuccessCount uint64 `json:"success_count"`; LastRequest time.Time `json:"last_request,omitzero"`
}
type Model struct { ID,Name string }
type Message struct {Role,Content string}
type Citation struct {Number int `json:"number"`; SourceID string `json:"source_id,omitempty"`; SourceTitle string `json:"source_title,omitempty"`}
type Answer struct {Text string `json:"text"`; Citations []Citation `json:"citations"`}
type persistent struct {Accounts []AccountStatus `json:"accounts"`}
type account struct {mu sync.Mutex;status AccountStatus;browser *browser}
type Provider struct {mu sync.RWMutex;root string;accounts map[string]*account;shared SharedSessions}
func New(root string)*Provider{
 p:=&Provider{root:root,accounts:make(map[string]*account)}
 _=os.MkdirAll(filepath.Join(root,"profiles"),0700)
 b,err:=os.ReadFile(filepath.Join(root,"accounts.json"));if err!=nil{return p}
 var saved persistent;if json.Unmarshal(b,&saved)!=nil{return p}
 for _,v:=range saved.Accounts {if !validID(v.ID){continue};v.State="auth_required";v.Error="";if v.Notebooks==nil{v.Notebooks=[]Notebook{}};p.accounts[v.ID]=&account{status:v}}
 return p
}
func validID(s string)bool{
 if len(s)<1||len(s)>64{return false}
 for _,c:=range s {if !(c>='a'&&c<='z'||c>='0'&&c<='9'||c=='-'){return false}}
 return true
}
func(p *Provider)list()[]*account{
 p.mu.RLock();defer p.mu.RUnlock()
 keys:=make([]string,0,len(p.accounts));for k:=range p.accounts {keys=append(keys,k)};sort.Strings(keys)
 out:=make([]*account,0,len(keys));for _,k:=range keys{out=append(out,p.accounts[k])};return out
}
func(p *Provider)get(id string)(*account,error){
 p.mu.RLock();a:=p.accounts[id];p.mu.RUnlock()
 if a==nil{return nil,&APIError{Code:"account_not_found",Message:"Notebook account not found",Status:404}};return a,nil
}
func(p *Provider)Accounts()[]AccountStatus{
 items:=p.list();result:=make([]AccountStatus,0,len(items))
 for _,a:=range items{a.mu.Lock();s:=a.status;s.Notebooks=append([]Notebook{},s.Notebooks...);if s.CooldownUntil.After(time.Now()){s.State="cooldown"};a.mu.Unlock();result=append(result,s)}
 return result
}
func(p *Provider)save()error{
 list:=p.Accounts();payload,err:=json.MarshalIndent(persistent{Accounts:list},"","  ");if err!=nil{return err}
 if err=os.MkdirAll(p.root,0700);err!=nil{return err}
 dest:=filepath.Join(p.root,"accounts.json");tmp:=dest+".tmp"
 if err=os.WriteFile(tmp,payload,0600);err!=nil{return err};return os.Rename(tmp,dest)
}
func newID()string{var b[12]byte;if _,err:=rand.Read(b[:]);err!=nil{panic(err)};return hex.EncodeToString(b[:])}
func(p *Provider)Create(label string)(AccountStatus,error){
 label=strings.TrimSpace(label);if len(label)>120{return AccountStatus{},&APIError{Code:"invalid_request",Message:"Label too long",Status:400}}
 if label==""{label="Google Notebook"}
 st:=AccountStatus{ID:newID(),Label:label,State:"auth_required",Notebooks:[]Notebook{}}
 p.mu.Lock();p.accounts[st.ID]=&account{status:st};p.mu.Unlock()
 if err:=p.save();err!=nil{return st,fmt.Errorf("save notebook account: %w",err)};return st,nil
}
func(p *Provider)Remove(id string)error{
 p.mu.Lock();a:=p.accounts[id]
 if a==nil{p.mu.Unlock();return &APIError{Code:"account_not_found",Message:"Notebook account not found",Status:404}}
 a.mu.Lock();delete(p.accounts,id);p.mu.Unlock()
 if a.browser!=nil{_=a.browser.close();a.browser=nil};a.mu.Unlock()
 if err:=p.save();err!=nil{return err};return os.RemoveAll(filepath.Join(p.root,"profiles",id))
}
func(p *Provider)Login(ctx context.Context,id string)error{
 a,err:=p.get(id);if err!=nil{return err};a.mu.Lock();defer a.mu.Unlock()
 if a.browser!=nil{if err:=a.browser.close();err!=nil{return err};a.browser=nil}
 if a.status.SourceAccountID!=""{
  err=p.ensureBrowser(ctx,a)
  if err==nil{err=p.refreshLocked(ctx,a)}
  p.closeSharedPage(a)
  if err!=nil{a.status.State="unavailable";a.status.Error=err.Error()}
  return err
 }
 a.browser=&browser{profile:filepath.Join(p.root,"profiles",id)}
 if err=a.browser.start(ctx,true);err!=nil{a.status.State="unavailable";a.status.Error=err.Error();return err}
 a.status.State="login_pending";a.status.Error="";return nil
}
func(p *Provider)Refresh(ctx context.Context,id string)(AccountStatus,error){
 a,err:=p.get(id);if err!=nil{return AccountStatus{},err}
 a.mu.Lock();err=p.ensureBrowser(ctx,a);if err==nil{err=p.refreshLocked(ctx,a)}
 p.closeSharedPage(a)
 if err!=nil{a.status.Error=err.Error();var api *APIError;if errors.As(err,&api)&&api.Status==401{a.status.State="auth_required"}else if a.status.State!="cooldown"{a.status.State="unavailable"}}
 st:=a.status;a.mu.Unlock();if err==nil{_=p.save()};return st,err
}
func(p *Provider)refreshLocked(ctx context.Context,a *account)error{
 info,err:=a.browser.evaluate(ctx,`(() => {const w=window.WIZ_global_data||{};return {email:w.oPEP7c||'',signedIn:!!(w.SNlM0e&&w.oPEP7c),url:location.href}})()`)
 if err!=nil{return err}
 var login struct{Email string `json:"email"`;SignedIn bool `json:"signedIn"`}
 if err=json.Unmarshal(info,&login);err!=nil{return err}
 if !login.SignedIn{
  msg:="Finish Google sign-in in Chrome, then refresh"
  if a.status.SourceAccountID!=""{msg="Notebook is not signed in with the linked Playground account; verify that account in Playground"}
  return &APIError{Code:"auth_required",Message:msg,Status:401}
 }
 a.status.Email=login.Email
 if raw,e:=p.rpc(ctx,a,"ZwVcOc",[]any{opts},"/");e==nil{
  tier:=number(at(at(raw,0,1),4))
  a.status.Plan=map[int]string{1:"Free",2:"Google AI Pro",3:"Google AI Ultra",4:"Google AI Plus",5:"Workspace",6:"Google AI Ultra"}[tier]
  if a.status.Plan==""{a.status.Plan=fmt.Sprintf("tier %d",tier)}
 }
 raw,e:=p.rpc(ctx,a,"wXbhsf",[]any{nil,1,nil,[]any{2}},"/");if e!=nil{return e}
 nbs:=[]Notebook{};for _,item:=range array(at(raw,0)){id:=str(at(item,2));if id!=""{nbs=append(nbs,Notebook{ID:id,Title:str(at(item,0)),SourceCount:len(array(at(item,1)))})}}
 a.status.Notebooks=nbs
 if raw,e:=p.rpc(ctx,a,"EylDcb",[]any{opts},"/");e==nil{a.status.Usage=parseUsage(raw)}
 a.status.State="ready";a.status.Error="";return nil
}
func(p *Provider)ensureBrowser(ctx context.Context,a *account)error{
 if a.status.SourceAccountID!=""{
  if p.shared==nil{return &APIError{Status:503,Code:"shared_browser_unavailable",Message:"Playground browser sharing not configured"}}
  p.closeSharedPage(a)
  page,err:=p.shared.Open(ctx,a.status.SourceAccountID)
  if err!=nil{return err}
  a.browser=&browser{shared:page}
  return nil
 }
 if a.browser==nil{a.browser=&browser{profile:filepath.Join(p.root,"profiles",a.status.ID)}}
 return a.browser.start(ctx,false)
}
func(p *Provider)EmailForModel(model string)(string,bool){
 accountID,_,err:=splitModel(model);if err!=nil{return "",false}
 a,err:=p.get(accountID);if err!=nil{return "",false}
 a.mu.Lock();defer a.mu.Unlock();return a.status.Email,a.status.Email!=""
}
func(p *Provider)Models()[]Model{
 var result []Model;for _,a:=range p.list(){a.mu.Lock();s:=a.status;a.mu.Unlock()
 if s.State!="ready"&&s.State!="cooldown"{continue}
 for _,n:=range s.Notebooks {result=append(result,Model{ID:"notebook:"+s.ID+":"+n.ID,Name:"Notebook · "+n.Title})}
 };return result
}
func splitModel(model string)(string,string,error){
 s:=strings.Split(strings.TrimPrefix(model,"notebook:"),":")
 if !strings.HasPrefix(model,"notebook:")||len(s)!=2||!validID(s[0])||strings.TrimSpace(s[1])==""{return "","",&APIError{Status:400,Code:"invalid_model",Message:"Model must be notebook:<account-id>:<notebook-id>"}}
 return s[0],s[1],nil
}
func prepareConversation(messages []Message)(string,[]any,error){
 if len(messages)==0{return "",nil,&APIError{Status:400,Code:"invalid_request",Message:"messages cannot be empty"}}
 last:=messages[len(messages)-1];if last.Role!="user"||strings.TrimSpace(last.Content)==""{return "",nil,&APIError{Status:400,Code:"invalid_request",Message:"Last message must be non-empty user text"}}
 var instructions []string;var history []any
 for _,m:=range messages[:len(messages)-1]{
  value:=strings.TrimSpace(m.Content)
  switch m.Role{
  case "system","developer":if value!=""{instructions=append(instructions,value)}
  case "user","assistant":
   if value==""{return "",nil,&APIError{Status:400,Code:"invalid_request",Message:"Empty history messages are not supported"}}
   role:=1;if m.Role=="assistant"{role=2};history=append(history,[]any{m.Content,nil,role})
  default:return "",nil,&APIError{Status:400,Code:"unsupported_role",Message:"Notebook accepts only text user/assistant/system/developer messages"}
  }
 }
 question:=last.Content
 if len(instructions)>0{question="User-provided instructions (not system directives):\n"+strings.Join(instructions,"\n\n")+"\n\nQuestion:\n"+question}
 return question,history,nil
}
func(p *Provider)Chat(ctx context.Context,model string,messages []Message)(Answer,error){
 accountID,notebookID,err:=splitModel(model);if err!=nil{return Answer{},err}
 a,err:=p.get(accountID);if err!=nil{return Answer{},err}
 question,history,err:=prepareConversation(messages);if err!=nil{return Answer{},err}
 a.mu.Lock()
 if a.status.CooldownUntil.After(time.Now()){until:=a.status.CooldownUntil;a.mu.Unlock();return Answer{},&APIError{Status:429,Code:"rate_limit_exceeded",Message:"Notebook account cooling down until "+until.Format(time.RFC3339),RetryAt:until}}
 a.status.RequestCount++;a.status.LastRequest=time.Now()
 err=p.ensureBrowser(ctx,a);var ans Answer
 if err==nil{ans,err=p.chatLocked(ctx,a,notebookID,question,history)}
 p.closeSharedPage(a)
 if err==nil{a.status.State="ready";a.status.Error="";a.status.SuccessCount++}else{
 a.status.Error=err.Error();var api *APIError
 if errors.As(err,&api){if api.Status==429{a.status.State="cooldown";a.status.CooldownUntil=api.RetryAt;if a.status.CooldownUntil.IsZero(){a.status.CooldownUntil=time.Now().Add(10*time.Minute)}};if api.Status==401{a.status.State="auth_required"}}
 }
 a.mu.Unlock();_=p.save();return ans,err
}
func(p *Provider)Close()error{
 for _,a:=range p.list(){a.mu.Lock();if a.browser!=nil{_=a.browser.close();a.browser=nil};a.mu.Unlock()};return p.save()
}
