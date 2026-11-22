package notebook
import("encoding/json";"errors";"path/filepath";"strings";"testing";"time")
func TestModelNamespace(t *testing.T){
 acc,nb,err:=splitModel("notebook:abcdef012345:source-id")
 if err!=nil||acc!="abcdef012345"||nb!="source-id"{t.Fatalf("unexpected: %q %q %v",acc,nb,err)}
 for _,id:=range []string{"notebook:missing","gemini-3.1-pro","notebook:../evil:abc","notebook:valid:"}{if _,_,err:=splitModel(id);err==nil{t.Errorf("accepted %q",id)}}
}
func TestPersistentMetadata(t *testing.T){
 root:=filepath.Join(t.TempDir(),"notebook");p:=New(root);s,err:=p.Create("Test");if err!=nil{t.Fatal(err)}
 if !validID(s.ID){t.Fatal("invalid account ID")}
 list:=New(root).Accounts();if len(list)!=1||list[0].ID!=s.ID||list[0].State!="auth_required"{t.Fatalf("bad persisted metadata: %+v",list)}
 if err:=p.Remove(s.ID);err!=nil{t.Fatal(err)}
 if len(New(root).Accounts())!=0{t.Fatal("account not deleted")}
}
func TestFramesAndQuotaError(t *testing.T){
 payload,_:=json.Marshal([]any{[]any{"wrb.fr","abc",`["answer"]`,nil,nil,nil}})
 result,err:=decodeRPC(")]}'\n"+string(payload),"abc")
 if err!=nil||str(at(result,0))!="answer"{t.Fatalf("decodeRPC: %v %v",result,err)}
 rejected,_:=json.Marshal([]any{[]any{"wrb.fr","abc",nil,nil,nil,[]any{8,"limit reached"}}})
 _,err=decodeRPC(string(rejected),"abc")
 var api *APIError;if !errors.As(err,&api)||api.Status!=429||api.RetryAt.Before(time.Now()){t.Fatalf("quota: %v",err)}
}
func TestStatelessHistory(t *testing.T){
 text,history,err:=prepareConversation([]Message{{Role:"system",Content:"Be concise"},{Role:"user",Content:"A"},{Role:"assistant",Content:"B"},{Role:"user",Content:"C"}})
 if err!=nil||!strings.Contains(text,"Be concise")||!strings.HasSuffix(text,"C")||len(history)!=2{t.Fatalf("history: %q %#v %v",text,history,err)}
 if at(history[1],2)!=2{t.Fatalf("assistant role lost: %#v",history)}
 if _,_,err:=prepareConversation([]Message{{Role:"tool",Content:"x"},{Role:"user",Content:"y"}});err==nil{t.Fatal("accepted tool role")}
}
