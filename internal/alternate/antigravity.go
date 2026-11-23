package alternate

import (
 "bufio"
 "bytes"
 "context"
 "crypto/rand"
 "crypto/sha256"
 "encoding/base64"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net"
 "net/http"
 "net/url"
 "os"
 "path/filepath"
 "sort"
 "strings"
 "sync"
 "time"
)

const agDaily="https://daily-cloudcode-pa.googleapis.com"
const agProd="https://cloudcode-pa.googleapis.com"
const agSessionID="-3750763034362895579"

// Google desktop OAuth clients are configured externally rather than shipping an application's credentials.
type agCredentials struct {
 AccessToken string `json:"access_token"`
 RefreshToken string `json:"refresh_token"`
 TokenType string `json:"token_type,omitempty"`
 Expiry time.Time `json:"expiry,omitempty"`
 ClientID string `json:"client_id,omitempty"`
 ClientSecret string `json:"client_secret,omitempty"`
 ProjectID string `json:"project_id,omitempty"`
 ManagedBy string `json:"managed_by,omitempty"`
}
type agModel struct {
 ID string
 Model string `json:"model"`
 Name string `json:"displayName"`
 Provider string `json:"modelProvider"`
 MaxOutput int `json:"maxOutputTokens"`
 ThinkingLevel int `json:"thinkingLevel"`
 ThinkingBudget *int `json:"thinkingBudget"`
 Disabled bool `json:"disabled"`
 Internal bool `json:"isInternal"`
}
type Antigravity struct {
 file,clientID,clientSecret,projectID string
 client *http.Client
 mu sync.Mutex
 cached []agModel
 cacheUntil time.Time
 oauthMu sync.Mutex
 oauthActive bool
 lifecycle context.Context
}
func NewAntigravity(cfg Config) *Antigravity {
 transport:=http.DefaultTransport.(*http.Transport).Clone()
 if raw:=strings.TrimSpace(strings.Split(cfg.ProxyURL,",")[0]);raw!="" {
  if target,err:=url.Parse(raw);err==nil && (target.Scheme=="http"||target.Scheme=="https"||target.Scheme=="socks5") {
   transport.Proxy=http.ProxyURL(target)
  }
 }
 lifecycle:=cfg.Lifecycle
 if lifecycle==nil {lifecycle=context.Background()}
 return &Antigravity{file:cfg.OAuthFile,clientID:cfg.ClientID,clientSecret:cfg.ClientSecret,projectID:cfg.ProjectID,client:&http.Client{Transport:transport,Timeout:120*time.Second},lifecycle:lifecycle}
}
func (p *Antigravity) read() (agCredentials,error) {
 if strings.TrimSpace(p.file)=="" {return agCredentials{},fail(503,"oauth_not_configured","Set ANTIGRAVITY_OAUTH_FILE before using this provider")}
 raw,err:=os.ReadFile(p.file);if err!=nil{return agCredentials{},fail(401,"authentication_required","Antigravity credentials not found; start OAuth login")}
 var c agCredentials
 if err=json.Unmarshal(raw,&c);err!=nil{return c,err}
 if c.AccessToken=="" {
  var nested struct {Token agCredentials `json:"token"`}
  if err=json.Unmarshal(raw,&nested);err==nil && nested.Token.AccessToken!="" {
   nested.Token.ClientID=c.ClientID; nested.Token.ClientSecret=c.ClientSecret
   c=nested.Token
  }
 }
 if c.AccessToken==""&&c.RefreshToken=="" {return c,fail(401,"authentication_required","Antigravity OAuth token missing")}
 return c,nil
}
func (p *Antigravity) save(c agCredentials) error {
 if c.ManagedBy!="aistudio2api" {return nil} // never overwrite imported credentials
 if err:=os.MkdirAll(filepath.Dir(p.file),0700);err!=nil{return err}
 data,err:=json.MarshalIndent(c,"","  ");if err!=nil{return err}
 f,err:=os.CreateTemp(filepath.Dir(p.file),".ag-*.tmp");if err!=nil{return err}
 temp:=f.Name();defer os.Remove(temp)
 if err=f.Chmod(0600);err!=nil{f.Close();return err}
 if _,err=f.Write(data);err!=nil{f.Close();return err}
 if err=f.Sync();err!=nil{f.Close();return err}
 if err=f.Close();err!=nil{return err}
 return os.Rename(temp,p.file)
}
func (p *Antigravity) token(ctx context.Context) (string,error) {
 p.mu.Lock();defer p.mu.Unlock()
 c,err:=p.read();if err!=nil{return "",err}
 if c.AccessToken!="" && (c.Expiry.IsZero() || time.Until(c.Expiry)>5*time.Minute) {return c.AccessToken,nil}
 id:=c.ClientID;if id==""{id=p.clientID}
 secret:=c.ClientSecret;if secret==""{secret=p.clientSecret}
 if c.RefreshToken==""||id=="" {return "",fail(401,"oauth_expired","Refresh token or OAuth client_id is missing")}
 form:=url.Values{"client_id":{id},"client_secret":{secret},"refresh_token":{c.RefreshToken},"grant_type":{"refresh_token"}}
 req,err:=http.NewRequestWithContext(ctx,"POST","https://oauth2.googleapis.com/token",strings.NewReader(form.Encode()));if err!=nil{return "",err}
 req.Header.Set("Content-Type","application/x-www-form-urlencoded")
 resp,err:=p.client.Do(req);if err!=nil{return "",err};defer resp.Body.Close()
 body,err:=io.ReadAll(io.LimitReader(resp.Body,1<<20));if err!=nil{return "",err}
 if resp.StatusCode!=200 {return "",fail(401,"oauth_refresh_failed",fmt.Sprintf("Google OAuth refresh HTTP %d",resp.StatusCode))}
 var result struct {AccessToken string `json:"access_token"`; ExpiresIn int `json:"expires_in"`; RefreshToken string `json:"refresh_token"`}
 if err=json.Unmarshal(body,&result);err!=nil{return "",err}
 if result.AccessToken=="" {return "",fail(502,"upstream_error","OAuth refresh returned no access token")}
 c.AccessToken=result.AccessToken;c.Expiry=time.Now().Add(time.Duration(result.ExpiresIn)*time.Second)
 if result.RefreshToken!=""{c.RefreshToken=result.RefreshToken}
 if err=p.save(c);err!=nil{return "",err}
 return c.AccessToken,nil
}
func (p *Antigravity) request(ctx context.Context,base,path string,payload any,out any) error {
 token,err:=p.token(ctx);if err!=nil{return err}
 raw,err:=json.Marshal(payload);if err!=nil{return err}
 req,err:=http.NewRequestWithContext(ctx,"POST",base+path,bytes.NewReader(raw));if err!=nil{return err}
 req.Header.Set("Authorization","Bearer "+token)
 req.Header.Set("Content-Type","application/json")
 req.Header.Set("User-Agent","antigravity")
 resp,err:=p.client.Do(req);if err!=nil{return err}
 defer resp.Body.Close()
 body,err:=io.ReadAll(io.LimitReader(resp.Body,16<<20));if err!=nil{return err}
 if resp.StatusCode!=200 {
  if resp.StatusCode==401 {return fail(401,"oauth_expired","Antigravity upstream rejected OAuth token")}
  return fail(resp.StatusCode,"upstream_error",fmt.Sprintf("Antigravity %s HTTP %d: %.350s",path,resp.StatusCode,string(body)))
 }
 if out!=nil{return json.Unmarshal(body,out)}
 return nil
}
func (p *Antigravity) project(ctx context.Context) (string,error) {
 if p.projectID!="" {return p.projectID,nil}
 c,err:=p.read();if err!=nil{return "",err}
 if c.ProjectID!=""{return c.ProjectID,nil}
 var assist struct{ Project string `json:"cloudaicompanionProject"`}
 payload:=map[string]any{"metadata":map[string]string{"ideType":"ANTIGRAVITY"}}
 err=p.request(ctx,agProd,"/v1internal:loadCodeAssist",payload,&assist)
 if err!=nil || assist.Project=="" {err=p.request(ctx,agDaily,"/v1internal:loadCodeAssist",payload,&assist)}
 if err!=nil{return "",err}
 if assist.Project=="" {return "",fail(503,"project_unavailable","Google did not return a Cloud AI Companion project")}
 return assist.Project,nil
}
func (p *Antigravity) catalog(ctx context.Context,refresh bool) ([]agModel,error) {
 p.mu.Lock()
 if !refresh && time.Now().Before(p.cacheUntil) {found:=append([]agModel(nil),p.cached...);p.mu.Unlock();return found,nil}
 p.mu.Unlock()
 project,err:=p.project(ctx);if err!=nil{return nil,err}
 var result struct {
  Models map[string]agModel `json:"models"`
  Sorts []struct{Groups []struct{ModelIDs []string `json:"modelIds"`} `json:"groups"`} `json:"agentModelSorts"`
 }
 err=p.request(ctx,agDaily,"/v1internal:fetchAvailableModels",map[string]string{"project":project},&result)
 if err!=nil{return nil,err}
 listed:=map[string]bool{}
 var models []agModel
 appendModel:=func(id string) {
  id=strings.TrimPrefix(id,"models/")
  if listed[id]{return};m,ok:=result.Models[id];if !ok {m,ok=result.Models["models/"+id]}
  if !ok||m.Disabled||m.Internal||m.MaxOutput<=0{return}
  listed[id]=true;m.ID=id
  if m.Name==""{m.Name=id}
  models=append(models,m)
 }
 for _,s:=range result.Sorts {for _,g:=range s.Groups {for _,id:=range g.ModelIDs {appendModel(id)}}}
 keys:=make([]string,0,len(result.Models));for id:=range result.Models{keys=append(keys,id)}
 sort.Strings(keys)
 for _,id:=range keys {appendModel(id)}
 if len(models)==0 {return nil,fail(502,"invalid_catalog","Antigravity did not return any usable models")}
 p.mu.Lock();p.cached=append([]agModel(nil),models...);p.cacheUntil=time.Now().Add(5*time.Minute);p.mu.Unlock()
 return models,nil
}
func (p *Antigravity) Models(ctx context.Context) ([]Model,error) {
 if p.file=="" {return nil,nil}
 if _,err:=os.Stat(p.file);err!=nil{return nil,nil}
 catalog,err:=p.catalog(ctx,false);if err!=nil{return nil,err}
 out:=make([]Model,0,len(catalog))
 for _,m:=range catalog {out=append(out,Model{ID:"antigravity:"+m.ID,Name:"Antigravity / "+m.Name,Owner:"antigravity"})}
 return out,nil
}
func (p *Antigravity) Chat(ctx context.Context,id string,messages []Message) (string,error) {
 catalog,err:=p.catalog(ctx,false);if err!=nil{return "",err}
 var selected *agModel
 for i:=range catalog{if catalog[i].ID==id {selected=&catalog[i];break}}
 if selected==nil {return "",fail(404,"model_not_found","Antigravity model not in live account catalog: "+id)}
 project,err:=p.project(ctx);if err!=nil{return "",err}
 var contents []map[string]any
 system:=""
 for _,m:=range messages{
  if m.Role=="system"||m.Role=="developer" {system+=m.Content+"\n";continue}
  role:="user";if m.Role=="assistant"{role="model"}else if m.Role!="user"{return "",fail(400,"invalid_request","Unsupported message role: "+m.Role)}
  contents=append(contents,map[string]any{"role":role,"parts":[]any{map[string]any{"text":m.Content}}})
 }
 if len(contents)==0 {return "",fail(400,"invalid_request","At least one user/assistant message is required")}
 typeID:=randomID()
 request:=map[string]any{"contents":contents,"sessionId":agSessionID,"generationConfig":map[string]any{"maxOutputTokens":selected.MaxOutput,"thinkingConfig":map[string]any{"includeThoughts":false}}}
 think:=request["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
 if selected.ThinkingLevel>0 {think["thinkingLevel"]=map[int]any{1:"LOW",2:"MEDIUM",3:"HIGH"}[selected.ThinkingLevel];if think["thinkingLevel"]==nil{think["thinkingLevel"]=selected.ThinkingLevel}}
 if selected.ThinkingBudget!=nil {think["thinkingBudget"]=*selected.ThinkingBudget}
 if system!="" {request["systemInstruction"]=map[string]any{"role":"user","parts":[]any{map[string]string{"text":system}}}}
 envelope:=map[string]any{
  "project":project,"model":selected.ID,"requestType":"agent","userAgent":"antigravity",
  "requestId":"agent/"+typeID+"/"+fmt.Sprint(time.Now().UnixMilli())+"/"+typeID+"/1",
  "request":request,
 }
 token,err:=p.token(ctx);if err!=nil{return "",err}
 body,err:=json.Marshal(envelope);if err!=nil{return "",err}
 req,err:=http.NewRequestWithContext(ctx,"POST",agDaily+"/v1internal:streamGenerateContent?alt=sse",bytes.NewReader(body));if err!=nil{return "",err}
 req.Header.Set("Content-Type","application/json")
 req.Header.Set("User-Agent","antigravity")
 req.Header.Set("Authorization","Bearer "+token)
 resp,err:=p.client.Do(req);if err!=nil{return "",err}
 defer resp.Body.Close()
 if resp.StatusCode!=200 {
  raw,_:=io.ReadAll(io.LimitReader(resp.Body,2048))
  return "",fail(resp.StatusCode,"upstream_error",fmt.Sprintf("Antigravity stream HTTP %d: %s",resp.StatusCode,raw))
 }
 scanner:=bufio.NewScanner(resp.Body);scanner.Buffer(make([]byte,64<<10),8<<20)
 answer:="";finished:=false
 for scanner.Scan(){
  line:=strings.TrimSpace(scanner.Text())
  if !strings.HasPrefix(line,"data:"){continue}
  data:=strings.TrimSpace(strings.TrimPrefix(line,"data:"))
  if data==""||data=="[DONE]"{continue}
  var root struct{Response struct{
   Candidates []struct{Content struct{Parts []struct{Text string `json:"text"`;Thought bool `json:"thought"`} `json:"parts"`} `json:"content"`;FinishReason string `json:"finishReason"`} `json:"candidates"`
  } `json:"response"`}
  if json.Unmarshal([]byte(data),&root)!=nil{continue}
  for _,c:=range root.Response.Candidates {
   var text string;for _,p:=range c.Content.Parts {if !p.Thought {text+=p.Text}}
   if strings.HasPrefix(text,answer){answer=text}else if !strings.HasSuffix(answer,text){answer+=text}
   if c.FinishReason!=""{finished=true}
  }
 }
 if err:=scanner.Err();err!=nil{return "",err}
 if answer=="" {return "",fail(502,"empty_response","Antigravity returned no text")}
 if !finished {return "",fail(502,"incomplete_stream","Antigravity stream closed without a finish reason")}
 return answer,nil
}
func randomID() string {var b [16]byte;_,_=rand.Read(b[:]);return fmt.Sprintf("%x-%x",b[:8],b[8:])}

// StartOAuth opens a bounded localhost PKCE callback. The URL must only be delivered
// through the authenticated administrator route, never through public model APIs.
func (p *Antigravity) StartOAuth(ctx context.Context) (string,error) {
 if p.file==""||p.clientID=="" {return "",fail(400,"oauth_not_configured","ANTIGRAVITY_OAUTH_FILE and ANTIGRAVITY_CLIENT_ID are required")}
 p.oauthMu.Lock()
 if p.oauthActive {p.oauthMu.Unlock();return "",fail(409,"oauth_in_progress","An OAuth login is already in progress")}
 if c,err:=p.read();err==nil && c.ManagedBy!="aistudio2api" {p.oauthMu.Unlock();return "",fail(409,"file_owned_by_other_app","Refusing to replace imported OAuth credentials")}
 listener,err:=net.Listen("tcp","127.0.0.1:51121");if err!=nil{p.oauthMu.Unlock();return "",err}
 p.oauthActive=true;p.oauthMu.Unlock()
 state:=randomID();verifier:=randomID()+randomID()
 digest:=sha256.Sum256([]byte(verifier))
 challenge:=base64.RawURLEncoding.EncodeToString(digest[:])
 redirect:="http://localhost:51121/oauth-callback"
 scopes:=strings.Join([]string{"https://www.googleapis.com/auth/cloud-platform","https://www.googleapis.com/auth/userinfo.email","https://www.googleapis.com/auth/userinfo.profile","https://www.googleapis.com/auth/cclog","https://www.googleapis.com/auth/experimentsandconfigs","https://www.googleapis.com/auth/aicode"}," ")
 query:=url.Values{"client_id":{p.clientID},"response_type":{"code"},"redirect_uri":{redirect},"scope":{scopes},"state":{state},"code_challenge":{challenge},"code_challenge_method":{"S256"},"access_type":{"offline"},"prompt":{"consent"}}
 server:=&http.Server{ReadHeaderTimeout:5*time.Second}
 done:=make(chan struct{})
 server.Handler=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.Method!="GET"||r.URL.Path!="/oauth-callback" {http.NotFound(w,r);return}
  if r.URL.Query().Get("state")!=state {http.Error(w,"Invalid OAuth state",http.StatusForbidden);return}
  code:=r.URL.Query().Get("code");if code==""{http.Error(w,"Missing authorization code",400);return}
  select {case <-done:http.Error(w,"OAuth callback already processed",409);return;default:close(done)}
  go func(){
   runctx,cancel:=context.WithTimeout(p.lifecycle,45*time.Second);defer cancel()
   form:=url.Values{"client_id":{p.clientID},"client_secret":{p.clientSecret},"grant_type":{"authorization_code"},"redirect_uri":{redirect},"code":{code},"code_verifier":{verifier}}
   req,err:=http.NewRequestWithContext(runctx,"POST","https://oauth2.googleapis.com/token",strings.NewReader(form.Encode()))
   if err==nil{req.Header.Set("Content-Type","application/x-www-form-urlencoded")}
   if err==nil{
    resp,e:=p.client.Do(req)
    if e==nil {
     data,_:=io.ReadAll(io.LimitReader(resp.Body,1<<20));resp.Body.Close()
     if resp.StatusCode==200 {
      var token struct{AccessToken string `json:"access_token"`;RefreshToken string `json:"refresh_token"`;ExpiresIn int `json:"expires_in"`}
      if json.Unmarshal(data,&token)==nil&&token.AccessToken!=""&&token.RefreshToken!="" {
       _=p.save(agCredentials{AccessToken:token.AccessToken,RefreshToken:token.RefreshToken,Expiry:time.Now().Add(time.Duration(token.ExpiresIn)*time.Second),ClientID:p.clientID,ClientSecret:p.clientSecret,ManagedBy:"aistudio2api"})
      }
     }
    }
   }
   _=server.Shutdown(context.Background())
  }()
  w.Header().Set("Content-Type","text/html; charset=utf-8")
  _,_=io.WriteString(w,"<html><body>Google authorization received. Return to AIStudio2API; check the credentials file before sending requests.</body></html>")
 })
 go func(){
  ctxTimeout,cancel:=context.WithTimeout(p.lifecycle,5*time.Minute);defer cancel()
  go func(){<-ctxTimeout.Done();_ =server.Close()}()
  _=server.Serve(listener)
  p.oauthMu.Lock();p.oauthActive=false;p.oauthMu.Unlock()
 }()
 return "https://accounts.google.com/o/oauth2/v2/auth?"+query.Encode(),nil
}
var _ = errors.New
