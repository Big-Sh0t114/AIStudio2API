package camoufoxnative

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "net/url"
 "strings"
 "sync"
 "time"
)

// NotebookPage borrows a temporary background tab without navigating the
// AI Studio page. Caller must hold its worker lifecycle lock until Close.
type NotebookPage struct {
 worker *Worker
 contextID string
 mu sync.Mutex
 closed bool
}
func(worker *Worker)OpenNotebookPage(ctx context.Context)(*NotebookPage,error){
 worker.mu.Lock()
 if worker.closed||worker.client==nil{
  worker.mu.Unlock()
  return nil,errors.New("Playground Camoufox worker is not running")
 }
 worker.mu.Unlock()
 id,err:=worker.createTab(ctx)
 if err!=nil{return nil,err}
 page:=&NotebookPage{worker:worker,contextID:id}
 if err=page.Navigate(ctx,"https://notebook.google.com/");err!=nil{
  _=page.Close()
  return nil,err
 }
 return page,nil
}

// Evaluate serializes nested Notebook RPC responses as JSON, since BiDi
// remote values may not otherwise preserve arbitrarily nested objects.
func(page *NotebookPage)Evaluate(ctx context.Context,expression string)(json.RawMessage,error){
 page.mu.Lock()
 defer page.mu.Unlock()
 if page.closed{return nil,errors.New("Notebook tab is closed")}
 wrapped:="(async () => JSON.stringify(await ("+expression+")))()"
 remote,err:=page.worker.client.evaluate(ctx,page.contextID,wrapped)
 if err!=nil{return nil,fmt.Errorf("evaluate Notebook tab: %w",err)}
 value,ok:=remote["value"].(string)
 if !ok||value==""||!json.Valid([]byte(value)){return nil,errors.New("Notebook tab returned invalid JSON")}
 return json.RawMessage(value),nil
}
func(page *NotebookPage)Navigate(ctx context.Context,target string)error{
 u,err:=url.Parse(target)
 if err!=nil{return err}
 if u.Scheme!="https"||!strings.EqualFold(u.Hostname(),"notebook.google.com"){
  return errors.New("Notebook tab navigation is restricted to notebook.google.com")
 }
 page.mu.Lock()
 defer page.mu.Unlock()
 if page.closed{return errors.New("Notebook tab is closed")}
 _,err=page.worker.client.command(ctx,"browsingContext.navigate",map[string]any{
  "context":page.contextID,"url":target,"wait":"complete",
 })
 if err!=nil{return fmt.Errorf("navigate Notebook tab: %w",err)}
 return nil
}
func(page *NotebookPage)Close()error{
 page.mu.Lock()
 defer page.mu.Unlock()
 if page.closed{return nil}
 page.closed=true
 ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
 defer cancel()
 _,err:=page.worker.client.command(ctx,"browsingContext.close",map[string]any{"context":page.contextID})
 return err
}
