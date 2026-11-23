package bridge

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"
)

func TestModelNamespaces(t *testing.T) {
    for _, tc := range []struct{model string; want bool}{
        {"antigravity:gemini-3.8-flash-high",true},
        {"gemini-web:account:default",true},
        {"notebook:account:notebook",false},
        {"gemini-3.8-flash",false},
    } {
        if got:=Handles(tc.model);got!=tc.want{t.Errorf("%q: got %v want %v",tc.model,got,tc.want)}
    }
    if validModelName("../../../etc/passwd") {t.Fatal("model validation permitted path traversal")}
}

func TestAntigravityNestedToken(t *testing.T) {
    raw:=[]byte("{\"token\":{\"access_token\":\"foo\",\"refresh_token\":\"bar\",\"expiry_date\":1999999999999},\"project_id\":\"project-1\"}")
    var auth authFile
    if err:=json.Unmarshal(raw,&auth);err!=nil {t.Fatal(err)}
    flattened:=auth.flatten()
    if flattened.AccessToken!="foo"||flattened.ProjectID!="project-1"||!flattened.expiryTime().After(time.Now()) {
        t.Fatalf("unexpected flattened OAuth credential: access=%q project=%q",flattened.AccessToken,flattened.ProjectID)
    }
}

func TestAntigravityModelCatalogExplicit(t *testing.T) {
    file:=filepath.Join(t.TempDir(),"credentials.json")
    if err:=os.WriteFile(file,[]byte("{}"),0600);err!=nil {t.Fatal(err)}
    provider,err:=New(Config{OAuthFile:file,AntigravityModels:[]string{"gemini-3.8-flash","", "../oops"}})
    if err!=nil {t.Fatal(err)}
    list:=provider.Models(context.Background())
    if len(list)!=1||list[0].ID!="antigravity:gemini-3.8-flash" {t.Fatalf("invalid model catalog: %+v",list)}
}

func TestWebTextFrameParsing(t *testing.T) {
    inner:=make([]any,5)
    inner[4]=[]any{[]any{nil,[]any{"first answer","longer final answer"}}}
    nested,err:=json.Marshal(inner)
    if err!=nil {t.Fatal(err)}
    frame,err:=json.Marshal([]any{[]any{"wrb.fr",nil,string(nested),nil}})
    if err!=nil {t.Fatal(err)}
    answer:=extractWebText(")]}'\n"+strings.Repeat("5",1)+"\n"+string(frame)+"\n")
    if answer!="longer final answer" {t.Fatalf("unexpected Gemini Web text: %q",answer)}
}

func TestWebInvalidModelFailsBeforeAccountOpen(t *testing.T) {
    provider,err:=New(Config{})
    if err!=nil {t.Fatal(err)}
    _,err=provider.Chat(context.Background(),"gemini-web:any:pro",[]Message{{Role:"user",Content:"hello"}})
    var api *APIError
    if err==nil || !strings.Contains(err.Error(),"default") {t.Fatalf("expected explicit model rejection, got %v",err)}
    _=api
}
