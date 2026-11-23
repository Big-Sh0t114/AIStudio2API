package api

import (
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

func TestBridgeRoutingPreservesChatBody(t *testing.T) {
    for _,tc:=range []struct{ model string; want bool }{
        {"antigravity:gemini-3.8-flash",true},
        {"gemini-web:account:default",true},
        {"notebook:account:notebook",false},
        {"gemini-4-argon",false},
    } {
        original:="{\"model\":\""+tc.model+"\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}"
        req:=httptest.NewRequest(http.MethodPost,"/v1/chat/completions",strings.NewReader(original))
        if got:=bridgeModelInBody(req);got!=tc.want {t.Fatalf("model %q detection=%v, want %v",tc.model,got,tc.want)}
        body,err:=io.ReadAll(req.Body)
        if err!=nil||string(body)!=original {t.Fatalf("model %q request body not preserved",tc.model)}
    }
}

func TestOAuthAdminAuthCannotBeBypassed(t *testing.T) {
    server:=&server{config:Config{AdminAuthEnabled:false}}
    response:=httptest.NewRecorder()
    server.handleAntigravityOAuthStart(response,httptest.NewRequest(http.MethodPost,"/api/bridge/antigravity/oauth/start",nil))
    if response.Code!=http.StatusForbidden {
        t.Fatalf("expected 403 with admin auth disabled, got %d: %s",response.Code,response.Body.String())
    }
}
