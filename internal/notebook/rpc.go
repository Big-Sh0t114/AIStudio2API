package notebook

import(
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "regexp"
 "strconv"
 "strings"
 "time"
)
var opts=[]any{2,nil,nil,[]any{1,nil,nil,nil,nil,nil,nil,nil,nil,nil,[]any{1}}}
const chatPath="/_/LabsTailwindUi/data/google.internal.labs.tailwind.orchestration.v1.LabsTailwindOrchestrationService/GenerateFreeFormStreamed"
const requestJS=`(async function(a) {
 const W=window.WIZ_global_data||{};
 if (!W.SNlM0e||!W.oPEP7c) return {signedOut:true,url:location.href};
 const q=new URLSearchParams({bl:W.cfb2h||'', 'f.sid':W.FdrFJe||'',hl:document.documentElement.lang||'en',_reqid:String(Math.floor(Math.random()*900000)+100000),rt:'c'});
 if(a.rpcid){q.set('rpcids',a.rpcid);q.set('source-path',a.sourcePath||'/');}
 const body=a.rpcid?JSON.stringify([[[a.rpcid,JSON.stringify(a.params),null,'generic']]]):JSON.stringify([null,JSON.stringify(a.params)]);
 const response=await fetch((a.path||'/_/LabsTailwindUi/data/batchexecute')+'?'+q.toString(),{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded;charset=UTF-8','x-same-domain':'1'},body:new URLSearchParams({'f.req':body,at:W.SNlM0e}),credentials:'include'});
 return {status:response.status,text:await response.text()};
})`
type browserResponse struct {SignedOut bool `json:"signedOut"`;URL string `json:"url"`;Status int `json:"status"`;Text string `json:"text"`}
func(p *Provider)execute(ctx context.Context,a *account,rpcID string,params any,sourcePath,path string)(browserResponse,error){
 arg,_:=json.Marshal(map[string]any{"rpcid":rpcID,"params":params,"sourcePath":sourcePath,"path":path})
 raw,err:=a.browser.evaluate(ctx,requestJS+"("+string(arg)+")");if err!=nil{return browserResponse{},err}
 var result browserResponse;if err=json.Unmarshal(raw,&result);err!=nil{return result,err}
 if result.SignedOut{return result,&APIError{Status:401,Code:"auth_required",Message:"Notebook sign-in expired; reconnect account"}}
 if result.Status==429{return result,&APIError{Status:429,Code:"rate_limit_exceeded",Message:"Notebook HTTP 429",RetryAt:time.Now().Add(10*time.Minute)}}
 if result.Status>=400{return result,&APIError{Status:502,Code:"upstream_error",Message:fmt.Sprintf("Notebook returned HTTP %d",result.Status)}}
 return result,nil
}
func parseFrames(body string)[][]any{
 var result [][]any;body=strings.TrimPrefix(body,")]}'")
 for _,line:=range strings.Split(body,"\n"){
  line=strings.TrimSpace(line);if !strings.HasPrefix(line,"["){continue}
  var rows []any;if json.Unmarshal([]byte(line),&rows)!=nil{continue}
  for _,row:=range rows{if frame,ok:=row.([]any);ok{result=append(result,frame)}}
 }
 return result
}
func at(v any,indexes ...int)any{
 for _,i:=range indexes{a,ok:=v.([]any);if !ok||i<0||i>=len(a){return nil};v=a[i]};return v
}
func array(v any)[]any{a,_:=v.([]any);return a}
func str(v any)string{s,_:=v.(string);return s}
func number(v any)int{f,_:=v.(float64);return int(f)}
func parseErr(status any,method string)*APIError{
 code:=number(at(status,0))
 if code==8{
  until:=time.Now().Add(10*time.Minute)
  match:=regexp.MustCompile(`\[\[\d\],\[(\d{10}),`).FindStringSubmatch(statusJSON(status))
  if len(match)==2{if epoch,e:=strconv.ParseInt(match[1],10,64);e==nil{candidate:=time.Unix(epoch,0);if candidate.After(time.Now())&&candidate.Before(time.Now().Add(7*24*time.Hour)){until=candidate}}}
  return &APIError{Status:429,Code:"rate_limit_exceeded",Message:method+": Notebook quota exhausted",RetryAt:until}
 }
 if code==16{return &APIError{Status:401,Code:"auth_required",Message:method+": Notebook session invalid"}}
 if code==5{return &APIError{Status:404,Code:"not_found",Message:method+": Notebook resource not found"}}
 if code==3{return &APIError{Status:400,Code:"invalid_request",Message:method+": Notebook rejected request or source not ready"}}
 return &APIError{Status:502,Code:"upstream_error",Message:fmt.Sprintf("%s: Notebook RPC failed (code %d)",method,code)}
}
func statusJSON(v any)string{b,_:=json.Marshal(v);return string(b)}
func decodeRPC(body,rpcID string)(any,error){
 for _,f:=range parseFrames(body){
  if str(at(f,0))!="wrb.fr"||str(at(f,1))!=rpcID{continue}
  payload:=at(f,2)
  if payload==nil{if status:=at(f,5);status!=nil{return nil,parseErr(status,rpcID)};return nil,nil}
  var value any;if e:=json.Unmarshal([]byte(str(payload)),&value);e!=nil{return nil,e};return value,nil
 }
 return nil,&APIError{Status:502,Code:"invalid_upstream_response",Message:"Notebook RPC response had no matching frame: "+rpcID}
}
func(p *Provider)rpc(ctx context.Context,a *account,method string,params any,sourcePath string)(any,error){
 for attempt:=0;attempt<2;attempt++{
  r,err:=p.execute(ctx,a,method,params,sourcePath,"")
  if err!=nil{
   var api *APIError
   if attempt==0&&errors.As(err,&api)&&api.Status==502&&strings.Contains(api.Message,"HTTP 403"){
    if e:=a.browser.navigate(ctx,"https://notebook.google.com/");e==nil{time.Sleep(700*time.Millisecond);continue}
   }
   return nil,err
  }
  return decodeRPC(r.Text,method)
 }
 return nil,errors.New("Notebook RPC retries exhausted")
}
func parseUsage(raw any)*Usage{
 u:=&Usage{MeteringEnabled:number(at(raw,0))==1,Windows:[]Window{}}
 for _,v:=range array(at(raw,1)){
  kind:=fmt.Sprintf("kind_%d",number(at(v,4)))
  switch number(at(v,4)){case 1:kind="5_hour";case 2:kind="weekly"}
  reset:="";if ts:=at(v,5,0);number(ts)>0{reset=time.Unix(int64(number(ts)),0).UTC().Format(time.RFC3339)}
  remaining:=100.0;if at(v,7)!=nil{remaining=floatValue(at(v,7))}
  u.Windows=append(u.Windows,Window{Window:kind,UsedPercent:floatValue(at(v,6)),RemainingPercent:remaining,ResetsAt:reset})
 }
 return u
}
func floatValue(v any)float64{f,_:=v.(float64);return f}
func(p *Provider)chatLocked(ctx context.Context,a *account,id,question string,history []any)(Answer,error){
 sourcePath:="/notebook/"+id
 raw,err:=p.rpc(ctx,a,"rLM1Ne",[]any{id,nil,opts,nil,0},sourcePath);if err!=nil{return Answer{},err}
 row:=at(raw,0);if row==nil{return Answer{},&APIError{Status:404,Code:"not_found",Message:"Notebook not found"}}
 var ids []any;titles:=map[string]string{}
 for _,s:=range array(at(row,1)){
  if number(at(s,3,1))!=2{continue}
  sid:=str(at(s,0,0));if sid==""{continue}
  ids=append(ids,[]any{[]any{sid}});titles[sid]=str(at(s,1))
 }
 if len(ids)==0{return Answer{},&APIError{Status:400,Code:"sources_not_ready",Message:"This Notebook has no ready sources"}}
 params:=[]any{ids,question,history,[]any{2,nil,[]any{1},[]any{1}},nil,nil,nil,id,1}
 r,err:=p.execute(ctx,a,"",params,sourcePath,chatPath);if err!=nil{return Answer{},err}
 var chosen []any;var rejected any
 for _,f:=range parseFrames(r.Text){
  if str(at(f,0))!="wrb.fr"{continue}
  if at(f,2)==nil{if at(f,5)!=nil{rejected=at(f,5)};continue}
  var v any;if json.Unmarshal([]byte(str(at(f,2))),&v)!=nil{continue}
  if len(array(at(v,0)))==0{continue}
  block:=array(v)
  if chosen==nil||at(block,4)==true||len(str(at(block,0,0)))>len(str(at(chosen,0,0))){chosen=block}
 }
 if chosen==nil{if rejected!=nil{return Answer{},parseErr(rejected,"chat")};return Answer{},&APIError{Status:502,Code:"empty_response",Message:"Notebook returned no answer"}}
 ans:=at(chosen,0);reply:=str(at(ans,0))
 if strings.TrimSpace(reply)==""{return Answer{},&APIError{Status:502,Code:"empty_response",Message:"Notebook returned an empty answer"}}
 out:=Answer{Text:reply,Citations:[]Citation{}}
 for i,c:=range array(at(ans,4,3)){sid:=str(at(c,1,5,0,0,0));out.Citations=append(out.Citations,Citation{Number:i+1,SourceID:sid,SourceTitle:titles[sid]})}
 return out,nil
}
