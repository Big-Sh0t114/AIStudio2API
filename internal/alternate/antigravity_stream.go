package alternate

import (
 "bufio"
 "bytes"
 "encoding/json"
 "fmt"
 "io"
 "strings"
)

const agMaxEventBytes=32<<20

type agStreamFrame struct {
 Error *struct{
  Code int `json:"code"`
  Message string `json:"message"`
 } `json:"error"`
 Response struct {
  PromptFeedback *struct {
   BlockReason string `json:"blockReason"`
   BlockReasonMessage string `json:"blockReasonMessage"`
  } `json:"promptFeedback"`
  Candidates []struct {
   Content struct {
    Role string `json:"role"`
    Parts []struct {
     Text string `json:"text"`
     Thought bool `json:"thought"`
     FunctionCall json.RawMessage `json:"functionCall"`
    } `json:"parts"`
   } `json:"content"`
   FinishReason string `json:"finishReason"`
   ContinuationToken string `json:"continuationToken"`
  } `json:"candidates"`
 } `json:"response"`
}
type agStreamResult struct {
 Text string
 FinishReason string
 ContinuationToken string
}
func decodeAntigravitySSE(reader io.Reader) (agStreamResult,error) {
 scanner:=bufio.NewScanner(reader)
 scanner.Buffer(make([]byte,32<<10),agMaxEventBytes)
 var out agStreamResult
 for scanner.Scan() {
  line:=bytes.TrimSpace(scanner.Bytes())
  if bytes.HasPrefix(line,[]byte("data:")){
   line=bytes.TrimSpace(line[len("data:"):])
  }else if len(line)==0||line[0]!='{'{
   continue
  }
  if len(line)==0||bytes.Equal(line,[]byte("[DONE]")){continue}
  var frame agStreamFrame
  if err:=json.Unmarshal(line,&frame);err!=nil{
   return out,fmt.Errorf("decode Antigravity SSE: %w",err)
  }
  if frame.Error!=nil {
   return out,fail(502,"upstream_error",fmt.Sprintf("Antigravity SSE error %d: %s",frame.Error.Code,frame.Error.Message))
  }
  if feedback:=frame.Response.PromptFeedback;feedback!=nil&&feedback.BlockReason!=""&&feedback.BlockReason!="BLOCK_REASON_UNSPECIFIED"{
   return out,fail(400,"blocked_prompt",fmt.Sprintf("Antigravity blocked the prompt: %s",feedback.BlockReason))
  }
  for _,candidate:=range frame.Response.Candidates {
   if candidate.Content.Role==""&&len(candidate.Content.Parts)==1&&strings.HasPrefix(candidate.Content.Parts[0].Text,"This request was blocked by Gemini's filters."){
    return out,fail(400,"content_filtered","Antigravity blocked the response")
   }
   var emitted string
   for _,part:=range candidate.Content.Parts {
    if part.Thought{continue}
    if len(part.FunctionCall)>0&&string(part.FunctionCall)!="null"{
     return out,fail(400,"unsupported_feature","Antigravity returned a function call to a text-only client")
    }
    emitted+=part.Text
   }
   if emitted!=""{
    if strings.HasPrefix(emitted,out.Text) {out.Text=emitted} else if !strings.HasSuffix(out.Text,emitted) {out.Text+=emitted}
   }
   if reason:=candidate.FinishReason;reason!=""&&reason!="FINISH_REASON_UNSPECIFIED"{
    out.FinishReason=reason
    out.ContinuationToken=candidate.ContinuationToken
   }
  }
 }
 if err:=scanner.Err();err!=nil{return out,err}
 if out.FinishReason==""{return out,fail(502,"incomplete_stream","Antigravity SSE ended without a finish reason")}
 if out.FinishReason=="CONTINUATION"&&out.ContinuationToken==""{
  return out,fail(502,"continuation_missing","Antigravity requested continuation but supplied no token")
 }
 switch out.FinishReason {
 case "STOP","CONTINUATION","MAX_TOKENS":
  return out,nil
 default:
  return out,fail(400,"generation_stopped","Antigravity stopped generation: "+out.FinishReason)
 }
}
