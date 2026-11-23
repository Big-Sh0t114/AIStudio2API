package bridge

import (
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestCredentialOwnership(t *testing.T) {
    path:=filepath.Join(t.TempDir(),"oauth.json")
    if err:=os.WriteFile(path,[]byte("{\"access_token\":\"external\"}"),0600);err!=nil{t.Fatal(err)}
    if err:=checkCredentialOwnership(path);err==nil {t.Fatal("external credential must never be overwritten")}
    managed:=authFile{ManagedBy:"aistudio2api",AccessToken:"first",RefreshToken:"refresh",ExpiryDate:time.Now().Add(time.Hour).UnixMilli()}
    owned,err:=json.Marshal(managed)
    if err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(path,owned,0600);err!=nil{t.Fatal(err)}
    if err:=checkCredentialOwnership(path);err!=nil{t.Fatal(err)}
    managed.AccessToken="second"
    if err:=saveOAuthCredential(path,managed);err!=nil{t.Fatal(err)}
    final,err:=os.ReadFile(path)
    if err!=nil{t.Fatal(err)}
    if !strings.Contains(string(final),"\"second\"") {t.Fatal("managed credential was not rotated")}
    if info,err:=os.Stat(path);err!=nil{t.Fatal(err)}else if info.Mode().Perm()&0077!=0{
        t.Fatalf("credential permissions too broad: %v",info.Mode().Perm())
    }
}

func TestOAuthMissingConfigurationDoesNotStartListener(t *testing.T) {
    p,err:=New(Config{})
    if err!=nil{t.Fatal(err)}
    _,err=p.StartOAuth()
    var api *APIError
    if !errors.As(err,&api)||api.Code!="oauth_not_configured" {t.Fatalf("expected missing config error, got %v",err)}
    if p.flowActive {t.Fatal("invalid configuration must not claim active OAuth state")}
}

func TestOAuthBadProxy(t *testing.T) {
    _,err:=New(Config{Proxy:"http://"})
    if err==nil {t.Fatal("incomplete proxy should be rejected")}
    _,err=New(Config{Proxy:"http://127.0.0.1:8080, http://127.0.0.1:8081"})
    if err!=nil {t.Fatalf("comma-separated gateway proxy pool not accepted: %v",err)}
}
