package api

import (
 "bytes"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "time"

 "github.com/Mag1cFall/AIStudio2API/internal/alternate"
 "github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func alternateModelInBody(r *http.Request) bool {
 if r.Body==nil{return false}
 original:=r.Body
 prefix,_:=io.ReadAll(io.LimitReader(original,64<<10))
 r.Body=struct{io.Reader;io.Closer}{Reader:io.MultiReader(bytes.NewReader(prefix),original),Closer:original}
 var body struct{Model string `json:"model"`}
 if json.Unmarshal(prefix,&body)==nil{return alternate.Handles(body.Model)}
 return bytes.Contains(prefix,[]byte("antigravity:"))||bytes.Contains(prefix,[]byte("gemini-web:"))
}
func (s *server) alternateAuthorized(r *http.Request,model string) bool {
 if s.config.Alternate==nil{return false}
 if s.config.AlternateAuthorized==nil{return true}
 source:="antigravity"
 if strings.HasPrefix(model,"gemini-web:"){
  source=strings.SplitN(strings.TrimPrefix(model,"gemini-web:"),":",2)[0]
 }
 key:=""
 for _,candidate:=range apiKeyCandidates(r) {
  for _,configured:=range apiKeys(s.config.APIKey) {
   if candidate==configured {key=candidate;break}
  }
  if key!=""{break}
 }
 return s.config.AlternateAuthorized(key,source)
}
func (s *server) handleAlternateOrChat(w http.ResponseWriter,r *http.Request) {
 if s.config.Alternate==nil||!alternateModelInBody(r) {
  s.handleNotebookOrChat(w,r);return
 }
 var input struct{
  Model string `json:"model"`
  Messages []struct{Role string `json:"role"`;Content json.RawMessage `json:"content"`} `json:"messages"`
  Stream bool `json:"stream"`
  N int `json:"n"`
  Tools json.RawMessage `json:"tools"`
  ToolChoice json.RawMessage `json:"tool_choice"`
 }
 if err:=json.NewDecoder(io.LimitReader(r.Body,8<<20)).Decode(&input);err!=nil {
  writeOpenAIError(w,400,"invalid_request",err.Error());return
 }
 if !s.alternateAuthorized(r,input.Model) {
  writeOpenAIError(w,403,"permission_denied","Account not permitted for this API key");return
 }
 if input.N>1 {
  writeOpenAIError(w,400,"unsupported_feature","Alternate providers support n=1 only");return
 }
 if len(input.Tools)>0&&string(input.Tools)!="null"&&string(input.Tools)!="[]" ||
  len(input.ToolChoice)>0&&string(input.ToolChoice)!="null"&&string(input.ToolChoice)!="\"none\"" {
  writeOpenAIError(w,400,"unsupported_feature","Tools are not implemented for alternate backends");return
 }
 var messages []alternate.Message
 for _,m:=range input.Messages {
  content,err:=notebookTextContent(m.Content)
  if err!=nil{writeOpenAIError(w,400,"unsupported_content",err.Error());return}
  messages=append(messages,alternate.Message{Role:m.Role,Content:content})
 }
 SetAccessLogChannel(r.Context(),"alternate")
 SetAccessLogTarget(r.Context(),input.Model,input.Model)
 content,err:=s.config.Alternate.Chat(r.Context(),input.Model,messages)
 if err!=nil {
  var e *alternate.Error
  if errors.As(err,&e) {
   writeOpenAIError(w,e.Status,e.Code,e.Message)
  }else{writeOpenAIError(w,502,"upstream_error",err.Error())}
  return
 }
 var prompt int64
 for _,m:=range messages {prompt+=notebookTextTokens(m.Content)}
 completion:=notebookTextTokens(content)
 usage:=map[string]any{"prompt_tokens":prompt,"completion_tokens":completion,"total_tokens":prompt+completion,"estimated":true}
 SetAccessLogGenerationResult(r.Context(), &aistudio.Usage{InputTokens:prompt,OutputTokens:completion,TotalTokens:prompt+completion}, 0)
 SetAccessLogFinishReason(r.Context(),"stop")
 w.Header().Set("X-Token-Usage-Estimated","true")
 if input.Stream {
  w.Header().Set("Content-Type","text/event-stream")
  w.Header().Set("Cache-Control","no-cache")
  chunk:=map[string]any{"id":newID("chatcmpl"),"object":"chat.completion.chunk","created":time.Now().Unix(),"model":input.Model,"choices":[]any{map[string]any{"index":0,"delta":map[string]any{"role":"assistant","content":content},"finish_reason":nil}}}
  data,_:=json.Marshal(chunk);_,_=fmt.Fprintf(w,"data: %s\n\n",data)
  chunk["choices"]=[]any{map[string]any{"index":0,"delta":map[string]any{},"finish_reason":"stop"}}
  chunk["usage"]=usage
  data,_=json.Marshal(chunk);_,_=fmt.Fprintf(w,"data: %s\n\ndata: [DONE]\n\n",data)
  return
 }
 writeJSON(w,200,map[string]any{
  "id":newID("chatcmpl"),"object":"chat.completion","created":time.Now().Unix(),"model":input.Model,
  "choices":[]any{map[string]any{"index":0,"message":map[string]any{"role":"assistant","content":content},"finish_reason":"stop"}},
  "usage":usage,
 })
}
func (s *server) handleAlternateOrModels(w http.ResponseWriter,r *http.Request) {
 if s.config.Alternate==nil {s.handleNotebookOrModels(w,r);return}
 recorder:=httptest.NewRecorder()
 s.handleNotebookOrModels(recorder,r)
 var body struct {Data []map[string]any `json:"data"`}
 if recorder.Code>=200&&recorder.Code<300{_ =json.Unmarshal(recorder.Body.Bytes(),&body)}
 if body.Data==nil{body.Data=[]map[string]any{}}
 for _,m:=range s.config.Alternate.Models(r.Context()) {
  if !s.alternateAuthorized(r,m.ID){continue}
  body.Data=append(body.Data,map[string]any{"id":m.ID,"name":m.Name,"object":"model","owned_by":m.Owner})
 }
 if len(body.Data)==0&&recorder.Code>=400 {
  w.WriteHeader(recorder.Code);_,_=w.Write(recorder.Body.Bytes());return
 }
 writeJSON(w,200,map[string]any{"object":"list","data":body.Data})
}
func (s *server) handleAlternateOrModel(w http.ResponseWriter,r *http.Request) {
 id:=r.PathValue("model")
 if s.config.Alternate==nil||!alternate.Handles(id){s.handleNotebookOrModel(w,r);return}
 if !s.alternateAuthorized(r,id) {writeOpenAIError(w,403,"permission_denied","Account not permitted");return}
 for _,m:=range s.config.Alternate.Models(r.Context()) {
  if m.ID==id {
   writeJSON(w,200,map[string]any{"id":m.ID,"name":m.Name,"object":"model","owned_by":m.Owner});return
  }
 }
 writeOpenAIError(w,404,"model_not_found","Alternate model not available for current account")
}
func (s *server) handleAlternateOAuth(w http.ResponseWriter,r *http.Request) {
 // AdminAuthEnabled must be true so credentials cannot be reissued from an
 // accidentally public management UI.
 if !s.config.AdminAuthEnabled||s.config.Alternate==nil {
  writeOpenAIError(w,403,"admin_auth_required","Enable administrator authentication to start OAuth")
  return
 }
 oauthURL,err:=s.config.Alternate.StartOAuth(r.Context())
 if err!=nil{
  var api *alternate.Error
  if errors.As(err,&api){writeOpenAIError(w,api.Status,api.Code,api.Message)}else{writeOpenAIError(w,503,"oauth_unavailable",err.Error())}
  return
 }
 writeJSON(w,200,map[string]any{"authorization_url":oauthURL,"callback":"http://localhost:51121/oauth-callback"})
}
