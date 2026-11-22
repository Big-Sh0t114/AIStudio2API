package api
import("io";"net/http/httptest";"strings";"testing")
func TestNotebookModelBodyPeekPreservesRequest(t *testing.T){
 body:=`{"messages":[{"role":"user","content":"hello"}],"model":"notebook:abc:def","stream":false}`
 req:=httptest.NewRequest("POST","/v1/chat/completions",strings.NewReader(body))
 if !notebookModelInBody(req){t.Fatal("notebook request not detected")}
 after,err:=io.ReadAll(req.Body);if err!=nil||string(after)!=body{t.Fatalf("peek altered body: %s %v",after,err)}
 req2:=httptest.NewRequest("POST","/v1/chat/completions",strings.NewReader(`{"model":"gemini-3.8-flash"}`))
 if notebookModelInBody(req2){t.Fatal("non-notebook model misclassified")}
}
func TestNotebookTextContent(t *testing.T){
 s,err:=notebookTextContent([]byte(`"hello"`));if err!=nil||s!="hello"{t.Fatalf("string %q %v",s,err)}
 s,err=notebookTextContent([]byte(`[{"type":"text","text":"A"},{"type":"text","text":"B"}]`));if err!=nil||s!="A\nB"{t.Fatalf("parts %q %v",s,err)}
 if _,err=notebookTextContent([]byte(`[{"type":"image_url","image_url":{}}]`));err==nil{t.Fatal("nontext part accepted")}
}
