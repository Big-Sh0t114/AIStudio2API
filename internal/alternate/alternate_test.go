package alternate

import (
 "bytes"
 "context"
 "encoding/json"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestWebCatalogDynamicModelsAndVariants(t *testing.T) {
 first:=make([]any,18);first[0]="hash-default";first[1]="Hydrogen";first[11]="4 Hydrogen";first[17]=float64(2)
 second:=make([]any,18);second[0]="hash-argon";second[1]="Argon";second[11]="4 Argon";second[17]=float64(5)
 header:=make([]any,15);header[1]=float64(2);header[3]="variant-carbon";header[4]="Carbon";header[12]="4 Carbon"
 row:=make([]any,4);row[0]=header;row[3]=[]any{float64(1)}
 payload:=make([]any,25)
 payload[0]=float64(2);payload[15]=[]any{first,second}
 policy:=make([]any,2);policy[1]=[]any{row};payload[24]=policy
 models,err:=parseWebCatalog(payload)
 if err!=nil{t.Fatal(err)}
 got:=map[string]webModel{}
 for _,m:=range models {got[m.ID]=m}
 if len(got)!=3 {t.Fatalf("models=%v",models)}
 if !got["gemini-4-hydrogen"].Default {t.Error("expected default model")}
 if got["gemini-4-carbon"].Variant!="variant-carbon" {t.Error("variant model not parsed")}
}
func TestFindRPCTagDoesNotPanicOnArrays(t *testing.T){
 data:=[]any{[]any{[]any{"wrb.fr","otAQ7b","[1]"},0},[]any{"noop"}}
 if value,ok:=findRPC(data,"otAQ7b");!ok||value!="[1]"{t.Fatalf("%v %v",value,ok)}
}
func TestWebModelSourceAcceptsEmailButRejectsPath(t *testing.T){
 for _,id:=range []string{"foo@gmail.com:gemini-4-argon","abc-123:default"} {
  if _,_,err:=splitWebID(id);err!=nil{t.Error(err)}
 }
 for _,id:=range []string{"../admin:default","https://host:default","foo:bar:baz"}{
  if _,_,err:=splitWebID(id);err==nil{t.Errorf("accepted %s",id)}
 }
}
func TestJSPBHeaderIncludesModelHash(t *testing.T) {
 b:=webBootstrap{Language:"en",BL:"boq_test"}
 encoded:=webModelHeader(webModel{Hash:"hash-custom",Mode:5},b,2)
 if !bytes.Contains(encoded,[]byte("hash-custom")) {t.Fatal("model hash missing")}
}
func TestImportedCredentialsCannotBeOverwritten(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"token.json")
 initial:=agCredentials{AccessToken:"access",RefreshToken:"refresh",ManagedBy:"external-app"}
 raw,_:=json.Marshal(initial)
 if err:=os.WriteFile(path,raw,0600);err!=nil{t.Fatal(err)}
 provider:=NewAntigravity(Config{OAuthFile:path})
 if err:=provider.save(agCredentials{AccessToken:"hijack",ManagedBy:"external-app"});err!=nil{t.Fatal(err)}
 c,err:=provider.read();if err!=nil{t.Fatal(err)}
 if c.AccessToken!="access"{t.Fatalf("overwrote external credentials: %#v",c)}
}
func TestStartOAuthRequiresExplicitCredentials(t *testing.T) {
 provider:=NewAntigravity(Config{Lifecycle:context.Background()})
 _,err:=provider.StartOAuth(context.Background())
 if err==nil||!strings.Contains(err.Error(),"ANTIGRAVITY"){t.Fatalf("expected configuration error, got %v",err)}
}
func TestPromptRoleFlattening(t *testing.T){
 text,err:=safeText([]Message{{Role:"system",Content:"Instruction"},{Role:"user",Content:"Question"}})
 if err!=nil||!strings.Contains(text,"[USER]\nQuestion"){t.Fatalf("prompt=%q err=%v",text,err)}
}

func TestGeminiWebThinkingHeaderMatchesOfficialEnum(t *testing.T) {
 b:=webBootstrap{Language:"en",BL:"boq_test",ClientID:"client-1"}
 header:=webModelHeader(webModel{Hash:"model-hash",Mode:5},b,4)
 if !bytes.Contains(header,[]byte("model-hash")) {t.Fatal("missing model hash")}
 payload:=webGeneratePayload(webModel{Mode:5},b,"rid","cid",4)
 if got:=payload[80];got!=4{t.Fatalf("thinking level=%v",got)}
}
