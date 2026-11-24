package api

import (
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"

 "github.com/Mag1cFall/AIStudio2API/internal/alternate"
)

func TestAlternateBodyProbePreservesRequest(t *testing.T) {
 for _,model:=range []string{"antigravity:gemini-4-argon","gemini-web:user@gmail.com:gemini-4-carbon"}{
  original:=`{"model":"`+model+`","messages":[{"role":"user","content":"test"}]}`
  req:=httptest.NewRequest("POST","/v1/chat/completions",strings.NewReader(original))
  if !alternateModelInBody(req){t.Fatalf("%q not recognized",model)}
  restored,err:=io.ReadAll(req.Body)
  if err!=nil||string(restored)!=original{t.Fatalf("request body lost after probe: %v %s",err,restored)}
 }
}
func TestAlternateBodyProbeRejectsUnrelatedModels(t *testing.T){
 req:=httptest.NewRequest("POST","/v1/chat/completions",strings.NewReader(`{"model":"gemini-4-argon"}`))
 if alternateModelInBody(req){t.Fatal("native Playground model intercepted")}
}
func TestAlternateOAuthRequiresAdminAuth(t *testing.T){
 s:=&server{config:Config{Alternate:alternate.New(alternate.Config{})}}
 rec:=httptest.NewRecorder()
 s.handleAlternateOAuth(rec,httptest.NewRequest(http.MethodPost,"/api/alternate/antigravity/oauth/start",nil))
 if rec.Code!=http.StatusForbidden{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
}
func TestAlternateChatRejectsUnsupportedTools(t *testing.T){
 s:=&server{config:Config{Alternate:alternate.New(alternate.Config{})}}
 rec:=httptest.NewRecorder()
 body:=`{"model":"antigravity:gemini-4-argon","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"search"}}]}`
 s.handleAlternateOrChat(rec,httptest.NewRequest(http.MethodPost,"/v1/chat/completions",strings.NewReader(body)))
 if rec.Code!=http.StatusBadRequest{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
}
