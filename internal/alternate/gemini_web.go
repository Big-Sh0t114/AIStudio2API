package alternate

import (
 "bytes"
 "context"
 "crypto/rand"
 "encoding/base64"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "regexp"
 "strconv"
 "strings"
 "time"

 "google.golang.org/protobuf/encoding/protowire"
)

const webOrigin="https://gemini.google.com"
const webChannel="https://geminiweb-pa.clients6.google.com/v1/processSession"
var webField=map[string]*regexp.Regexp{}
var webBL=regexp.MustCompile(`"cfb2h":"([^"]+)"`)
var webAT=regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
var webFSID=regexp.MustCompile(`"(?:FdrFJe|f\\.sid)":"([^"]+)"`)
var webLang=regexp.MustCompile(`"TuX5cc":"([^"]+)"`)
var webAPIKey=regexp.MustCompile(`"d2zJAe":"([^"]+)"`)
var webServer=regexp.MustCompile(`"PI9WOb":"([^"]+)"`)
var webSID=regexp.MustCompile(`\["c","([^"]+)"`)
var webSlug=regexp.MustCompile(`[^a-z0-9.]+`)
type webBootstrap struct {AT,BL,FSID,Language,APIKey,ServerToken string}
type webModel struct {ID,Name,Hash,Variant string;Mode int; Levels []int; Default bool}
func webMatch(data []byte,re *regexp.Regexp) string {
 m:=re.FindSubmatch(data);if len(m)<2{return ""};return string(m[1])
}
func webArray(v any) []any {v0,_:=v.([]any);return v0}
func webIndex(v any,indices ...int) any {
 for _,i:=range indices {a:=webArray(v);if i<0||i>=len(a){return nil};v=a[i]};return v
}
func webString(v any,indices ...int) string {s,_:=webIndex(v,indices...).(string);return s}
func webInt(v any,indices ...int) int {n,_:=webIndex(v,indices...).(float64);return int(n)}
func webName(short,title,subtitle string) string {
 if strings.Contains(subtitle,short)&&!strings.Contains(title,short) {return subtitle}
 return title
}
func webModelID(name string) string {
 slug:=strings.Trim(webSlug.ReplaceAllString(strings.ToLower(name),"-"),"-")
 if strings.HasPrefix(slug,"gemini-"){return slug}
 return "gemini-"+slug
}
func parseWebCatalog(payload []any) ([]webModel,error) {
 rows:=webArray(webIndex(payload,15))
 if len(rows)==0{return nil,errors.New("Gemini Web model catalog is empty")}
 defaultMode:=webInt(payload,0)
 var models []webModel
 known:=map[string]bool{}
 add:=func(m webModel){
  if m.ID==""||m.Hash==""||known[m.ID]{return}
  known[m.ID]=true;models=append(models,m)
 }
 for _,v:=range rows{
  row:=webArray(v)
  hash,short,title:=webString(row,0),webString(row,1),webString(row,11)
  if title==""{title=webString(row,19)}
  mode:=webInt(row,17)
  if hash==""||title==""||mode==0{continue}
  name:=webName(short,title,webString(row,12))
  add(webModel{ID:webModelID(name),Name:name,Hash:hash,Mode:mode,Default:mode==defaultMode})
 }
 // Official model policies include variants which share a mode but differ
 // in the model hash. Do not present upgrade-only entries as available.
 for _,v:=range webArray(webIndex(payload,24,1)) {
  row:=webArray(v)
  header:=webArray(webIndex(row,0))
  surface:=webInt(webArray(webIndex(row,3)),0)
  if surface!=1||webIndex(header,14)!=nil{continue}
  variant:=webString(header,3);if variant==""{continue}
  short,title:=webString(header,4),webString(header,12)
  name:=webName(short,title,webString(header,5))
  if name==""{continue}
  var levels []int
  for _,raw:=range webArray(webIndex(row,1)) {
   if n:=webInt(webArray(raw),0);n!=0{levels=append(levels,n)}
  }
  add(webModel{ID:webModelID(name),Name:name,Hash:variant,Variant:variant,Mode:webInt(header,1),Levels:levels})
 }
 if len(models)==0{return nil,errors.New("Gemini Web returned no recognizable model modes")}
 foundDefault:=false
 for _,m:=range models{if m.Default{foundDefault=true}}
 if !foundDefault{models[0].Default=true}
 return models,nil
}
func webInit(ctx context.Context,page WebTransport) (webCatalog,error) {
 head:=make(http.Header);head.Set("Accept","text/html")
 res,err:=page.Fetch(ctx,"GET",webOrigin+"/app",head,nil);if err!=nil{return webCatalog{},err}
 if err=checkStatus(res,"Gemini Web bootstrap");err!=nil{return webCatalog{},err}
 b:=webBootstrap{
  AT:webMatch(res.Body,webAT),BL:webMatch(res.Body,webBL),FSID:webMatch(res.Body,webFSID),
  Language:webMatch(res.Body,webLang),APIKey:webMatch(res.Body,webAPIKey),ServerToken:webMatch(res.Body,webServer),
 }
 if b.AT==""||b.BL==""||b.FSID=="" {return webCatalog{},fail(401,"authentication_required","Gemini Web bootstrap is missing signed-in tokens")}
 if b.Language==""{b.Language="en"}
 parsed,err:=webBatch(ctx,page,b,"otAQ7b","[1]")
 if err!=nil{return webCatalog{},err}
 models,err:=parseWebCatalog(parsed);if err!=nil{return webCatalog{},err}
 return webCatalog{models:models,bootstrap:b,expires:time.Now().Add(5*time.Minute)},nil
}
func webBatch(ctx context.Context,page WebTransport,b webBootstrap,rpcID,payload string) ([]any,error) {
 wire,err:=webJSON([]any{[]any{[]any{rpcID,payload,nil,"generic"}}});if err!=nil{return nil,err}
 body:=url.Values{"f.req":{wire},"at":{b.AT}}.Encode()
 query:=url.Values{"rpcids":{rpcID},"source-path":{"/app"},"bl":{b.BL},"f.sid":{b.FSID},"hl":{"en"},"_reqid":{strconv.FormatInt(time.Now().UnixMilli()%900000+100000,10)},"rt":{"c"}}.Encode()
 head:=make(http.Header)
 head.Set("Content-Type","application/x-www-form-urlencoded;charset=utf-8")
 head.Set("Accept","*/*")
 head.Set("X-Same-Domain","1")
 generic,_:=webJSON([]any{1,nil,nil,nil,nil,nil,b.Language,nil,[]int{4,33,22,5,6,8,16,19,20,38},nil,nil,nil,nil,nil,nil,nil,randomID(),[]string{b.BL}})
 head.Set("x-goog-ext-525001261-jspb",generic)
 head.Set("x-goog-ext-73010989-jspb","[]")
 res,err:=page.Fetch(ctx,"POST",webOrigin+"/_/BardChatUi/data/batchexecute?"+query,head,[]byte(body))
 if err!=nil{return nil,err}
 if err=checkStatus(res,"Gemini Web model catalog");err!=nil{return nil,err}
 // batchexecute sends a sequence of length-prefixed JSON arrays. Search
 // valid array boundaries rather than assuming that the first line is JSON.
 for _,line:=range bytes.Split(res.Body,[]byte("\n")){
  line=bytes.TrimSpace(line)
  if len(line)==0||line[0]!='['{continue}
  var value any
  if json.Unmarshal(line,&value)!=nil{continue}
  if str,ok:=findRPC(webArray(value),rpcID);ok{
   var result []any
   if err=json.Unmarshal([]byte(str),&result);err!=nil{return nil,err}
   return result,nil
  }
 }
 return nil,fail(502,"invalid_rpc","Gemini Web "+rpcID+" response not found")
}
func findRPC(value []any,id string)(string,bool) {
 if len(value)>=3{
  tag,_:=value[0].(string)
  rpc,_:=value[1].(string)
  if tag=="wrb.fr" && rpc==id {s,ok:=value[2].(string);return s,ok}
 }
 for _,v:=range value{if sub,ok:=v.([]any);ok{if s,found:=findRPC(sub,id);found{return s,true}}}
 return "",false
}
func webJSON(value any) (string,error) {
 var b bytes.Buffer
 encoder:=json.NewEncoder(&b);encoder.SetEscapeHTML(false)
 if err:=encoder.Encode(value);err!=nil{return "",err}
 return strings.TrimSpace(b.String()),nil
}
func webProto(values []any) []byte {
 var out []byte
 for i,v:=range values{
  field:=protowire.Number(i+1)
  switch x:=v.(type) {
  case nil:
  case int:out=protowire.AppendVarint(protowire.AppendTag(out,field,protowire.VarintType),uint64(x))
  case int64:out=protowire.AppendVarint(protowire.AppendTag(out,field,protowire.VarintType),uint64(x))
  case string:out=protowire.AppendString(protowire.AppendTag(out,field,protowire.BytesType),x)
  case []string:
   nested:=make([]any,len(x));for j,s:=range x{nested[j]=s}
   out=protowire.AppendBytes(protowire.AppendTag(out,field,protowire.BytesType),webProto(nested))
  case []int:
   var packed []byte;for _,n:=range x{packed=protowire.AppendVarint(packed,uint64(n))}
   out=protowire.AppendBytes(protowire.AppendTag(out,field,protowire.BytesType),packed)
  case []any:out=protowire.AppendBytes(protowire.AppendTag(out,field,protowire.BytesType),webProto(x))
  }
 }
 return out
}
func webModelHeader(m webModel,b webBootstrap) []byte {
 clientID:=randomID()
 header:=[]any{1,nil,nil,nil,m.Hash,nil,b.Language,0,[]int{4,33,22,5,6,8,16,19,20,38},nil,nil,0,nil,nil,m.Mode,nil,clientID,[]string{b.BL},nil,[]any{[]any{0,0}}}
 if m.Variant!="" {for len(header)<24{header=append(header,nil)};header=append(header,m.Variant)}
 return webProto(header)
}
func webGeneratePayload(m webModel,b webBootstrap,rid,cid string) []any {
 inner:=make([]any,99)
 inner[0]=[]any{"",0,nil,nil,nil,nil,0}
 inner[1]=[]any{b.Language}
 inner[4]=cid
 inner[6]=[]any{0}
 inner[7]=1
 inner[10]=1
 inner[11]=0
 inner[17]=[]any{[]any{0}}
 inner[18]=0
 inner[27]=1
 inner[30]=[]any{4,27,28,16,19,12}
 inner[41]=[]any{2}
 inner[49]=0
 inner[53]=0
 inner[59]=rid
 inner[61]=[]any{}
 inner[68]=1
 inner[79]=m.Mode
 inner[91]=0
 inner[96]=1
 inner[98]=1
 return inner
}
func webQueryEscape(value string)string {
 return strings.NewReplacer("+","%20","%21","!","%27","'","%28","(","%29",")","%2A","*").Replace(url.QueryEscape(value))
}
func webZX()string {return randomID()}
func webChannelRequest(ctx context.Context,page WebTransport,method,query,body string) (HTTPResponse,error) {
 head:=make(http.Header)
 head.Set("Accept","*/*")
 head.Set("Origin",webOrigin)
 head.Set("Referer",webOrigin+"/")
 if method=="POST" {head.Set("Content-Type","application/x-www-form-urlencoded")}
 return page.Fetch(ctx,method,webChannel+"?"+query,head,[]byte(body))
}
type channelState struct {gSession,sid string; rid,ofs,aid int}
func webOpenChannel(ctx context.Context,page WebTransport,m webModel,b webBootstrap,requestID string) (channelState,error) {
 auth,err:=page.Authorization();if err!=nil{return channelState{},err}
 encode:=base64.StdEncoding.EncodeToString
 headers:=strings.Join([]string{
  "Authorization:"+auth,
  "X-Goog-AuthUser:0",
  "X-Goog-Api-Key:"+b.APIKey,
  "X-Server-Token:"+b.ServerToken,
  "x-goog-ext-525001261-bin:"+encode(webModelHeader(m,b)),
  "x-goog-ext-73010989-bin:"+encode(webProto([]any{0})),
  "x-goog-ext-525005358-bin:"+encode(webProto([]any{requestID,1})),
  "x-goog-ext-73010990-bin:"+encode(webProto([]any{0,0,0})),
  "X-WebChannel-Content-Type:application/json+protobuf",
 },"\r\n")+"\r\n"
 rid:=time.Now().Nanosecond()%100000
 query:="VER=8&RID="+strconv.Itoa(rid)+"&CVER=22&X-HTTP-Session-Id=gsessionid&%24httpHeaders="+webQueryEscape(headers)+"&zx="+webZX()+"&t=1"
 res,err:=webChannelRequest(ctx,page,"POST",query,"count=0");if err!=nil{return channelState{},err}
 if err=checkStatus(res,"Gemini Web channel handshake");err!=nil{return channelState{},err}
 match:=webSID.FindSubmatch(res.Body)
 gSession:=res.Headers.Get("X-HTTP-Session-Id")
 if len(match)<2||gSession=="" {return channelState{},fail(502,"invalid_channel","Gemini Web handshake has no SID or gsessionid")}
 return channelState{gSession:gSession,sid:string(match[1]),rid:rid+1},nil
}
func webSend(ctx context.Context,page WebTransport,ch *channelState,messages ...any) error {
 var body strings.Builder
 body.WriteString("count="+strconv.Itoa(len(messages))+"&ofs="+strconv.Itoa(ch.ofs))
 for i,v:=range messages {
  s,err:=webJSON(v);if err!=nil{return err}
  body.WriteString("&req"+strconv.Itoa(i)+"___data__="+webQueryEscape(s))
 }
 query:="VER=8&gsessionid="+webQueryEscape(ch.gSession)+"&SID="+webQueryEscape(ch.sid)+"&RID="+strconv.Itoa(ch.rid)+"&AID="+strconv.Itoa(ch.aid)+"&zx="+webZX()+"&t=1"
 ch.rid++;ch.ofs+=len(messages)
 res,err:=webChannelRequest(ctx,page,"POST",query,body.String());if err!=nil{return err}
 return checkStatus(res,"Gemini Web channel send")
}
func webPoll(ctx context.Context,page WebTransport,ch *channelState) ([][]any,error) {
 query:="gsessionid="+webQueryEscape(ch.gSession)+"&VER=8&RID=rpc&SID="+webQueryEscape(ch.sid)+"&AID="+strconv.Itoa(ch.aid)+"&CI=0&TYPE=xmlhttp&zx="+webZX()+"&t=1"
 res,err:=webChannelRequest(ctx,page,"GET",query,"");if err!=nil{return nil,err}
 if err=checkStatus(res,"Gemini Web channel poll");err!=nil{return nil,err}
 decoder:=json.NewDecoder(bytes.NewReader(res.Body))
 var frames [][]any
 for {
  var length json.Number
  if err=decoder.Decode(&length);err!=nil {if errors.Is(err,io.EOF){break};return nil,err}
  var items [][]json.RawMessage
  if err=decoder.Decode(&items);err!=nil{return nil,err}
  for _,item:=range items {
   if len(item)<2{continue}
   var id int;_ =json.Unmarshal(item[0],&id);if id>ch.aid{ch.aid=id}
   var frame []any
   if json.Unmarshal(item[1],&frame)==nil{frames=append(frames,frame)}
  }
 }
 return frames,nil
}
func webFramePayload(frame []any) []any {
 message:=webIndex(frame,0)
 entries:=webArray(webIndex(message,1,6))
 for _,entry:=range entries {
  root:=webArray(webIndex(entry,3,3))
  if len(root)>0{return root}
 }
 return nil
}
func webExtractText(payload []any) (string,bool) {
 items:=webArray(webIndex(payload,4))
 for _,raw:=range items {
  candidate:=webArray(raw)
  if len(candidate)==0{continue}
  text:=webString(candidate,1,0)
  phase:=webInt(candidate,8,0)
  return text,phase==2||phase==3
 }
 return "",false
}
func (w *Web) Chat(ctx context.Context,id string,messages []Message) (string,error) {
 sourceID,requested,err:=splitWebID(id);if err!=nil{return "",err}
 prompt,err:=safeText(messages);if err!=nil{return "",err}
 catalog,err:=w.catalog(ctx,sourceID,false);if err!=nil{return "",err}
 var selected *webModel
 for i:=range catalog.models {
  if catalog.models[i].ID==requested || requested=="default"&&catalog.models[i].Default {selected=&catalog.models[i];break}
 }
 if selected==nil{return "",fail(404,"model_not_found","Gemini Web model not in live account catalog: "+requested)}
 page,err:=w.source(ctx,sourceID);if err!=nil{return "",err}
 defer page.Close()
 b:=catalog.bootstrap
 if b.APIKey==""||b.ServerToken=="" {return "",fail(502,"invalid_bootstrap","Gemini Web processSession credentials missing")}
 requestID,contextID:=randomID(),randomID()
 ch,err:=webOpenChannel(ctx,page,*selected,b,requestID);if err!=nil{return "",err}
 payload:=webGeneratePayload(*selected,b,requestID,contextID)
 msgA:=[]any{[]any{[]any{1,selected.Hash,b.Language,requestID},0,nil,nil,nil,nil,nil,[]any{nil,nil,nil,nil,[]any{payload,nil,nil,1}}}}
 msgB:=[]any{nil,[]any{1,nil,nil,nil,nil,nil,nil,nil,[]any{prompt}}}
 if err=webSend(ctx,page,&ch,msgA,msgB);err!=nil{return "",err}
 answer:="";complete:=false;ended:=false
 for attempts:=0;attempts<80;attempts++ {
  if err=ctx.Err();err!=nil{return "",err}
  frames,e:=webPoll(ctx,page,&ch);if e!=nil{return "",e}
  for _,frame:=range frames {
   tag,_:=webIndex(frame,0).(string)
   if tag=="close"{ended=true;break}
   payload:=webFramePayload(frame)
   if len(payload)==0{continue}
   txt,done:=webExtractText(payload)
   if txt!="" {answer=txt}
   complete=complete||done
   token:=webString(payload,25)
   if token=="response_committed" { _=webSend(ctx,page,&ch,[]any{nil,nil,nil,[]any{1}}) }
   if token!=""&&token!="response_committed"{ended=true;break}
  }
  if ended{break}
 }
 if answer=="" {return "",fail(502,"empty_response","Gemini Web returned no text")}
 if !ended&&!complete {return "",fail(502,"incomplete_stream","Gemini Web processSession did not complete")}
 return answer,nil
}
func webNonce()string {var raw [12]byte;_,_=rand.Read(raw[:]);return base64.RawURLEncoding.EncodeToString(raw[:])}
var _=webNonce
