package camoufoxnative

import (
 "context"
 "testing"
)
func TestNotebookPageNavigationRestriction(t *testing.T){
 page:=&NotebookPage{}
 if err:=page.Navigate(context.Background(),"http://notebook.google.com/");err==nil{t.Fatal("insecure URL accepted")}
 if err:=page.Navigate(context.Background(),"https://example.com/");err==nil{t.Fatal("foreign URL accepted")}
}
