package app

import (
 "context"
 "errors"
 "net/http"

 "github.com/Mag1cFall/AIStudio2API/internal/alternate"
 "github.com/Mag1cFall/AIStudio2API/internal/aistudio"
 "github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

// alternateSources borrows the same resident Playground worker and lifecycle
// lease as NotebookLM; it creates no Chrome, Camoufox page or new cookie jar.
type alternateSources struct{shared *notebookSharedSessions}
func (s alternateSources) Sources(ctx context.Context) ([]alternate.Source,error) {
 accounts,err:=s.shared.Sources(ctx)
 if err!=nil{return nil,err}
 out:=make([]alternate.Source,0,len(accounts))
 for _,a:=range accounts{out=append(out,alternate.Source{ID:a.ID,Label:a.Label,State:a.State})}
 return out,nil
}
func (s alternateSources) Open(ctx context.Context,accountID string) (alternate.WebTransport,error) {
 page,err:=s.shared.Open(ctx,accountID)
 if err!=nil{return nil,err}
 borrowed,ok:=page.(*notebookSharedHTTPPage)
 if !ok{_ =page.Close();return nil,errors.New("Gemini Web needs an existing SpiderMonkey Playground worker")}
 spider,ok:=borrowed.SharedPage.(*spiderNotebookPage)
 if !ok{_ =page.Close();return nil,errors.New("Gemini Web shared page has no native network adapter")}
 return &alternateTransport{page:page,worker:spider.worker},nil
}
type alternateTransport struct {
 page notebook.SharedPage
 worker *aistudio.NativeWorker
}
func (p *alternateTransport) Close() error{return p.page.Close()}
func (p *alternateTransport) Authorization() (string,int,error) {return p.worker.GeminiWebAuthorization()}
func (p *alternateTransport) Fetch(ctx context.Context,method,target string,headers http.Header,body []byte) (alternate.HTTPResponse,error) {
 status,responseHeaders,result,err:=p.worker.GeminiWebHTTP(ctx,method,target,headers,body)
 if err!=nil{return alternate.HTTPResponse{},err}
 return alternate.HTTPResponse{Status:status,Headers:responseHeaders,Body:result},nil
}
