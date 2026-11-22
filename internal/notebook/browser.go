package notebook

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "net"
 "net/http"
 "net/url"
 "os"
 "os/exec"
 "path/filepath"
 "runtime"
 "strconv"
 "strings"
 "time"

 "github.com/gorilla/websocket"
)
type browser struct {profile string;port int;cmd *exec.Cmd;shared SharedPage}
func chromePath()(string,error){
 for _,e:=range []string{"NOTEBOOK_CHROME_PATH","CHROME_PATH"}{if x:=os.Getenv(e);x!=""{if _,err:=os.Stat(x);err==nil{return x,nil}}}
 for _,n:=range []string{"google-chrome","google-chrome-stable","chromium","chromium-browser","chrome"}{if p,e:=exec.LookPath(n);e==nil{return p,nil}}
 var locations []string
 if runtime.GOOS=="windows"{for _,e:=range []string{"PROGRAMFILES","PROGRAMFILES(X86)","LOCALAPPDATA"}{if root:=os.Getenv(e);root!=""{locations=append(locations,filepath.Join(root,"Google","Chrome","Application","chrome.exe"))}}}
 if runtime.GOOS=="darwin"{locations=[]string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}}
 for _,p:=range locations{if _,e:=os.Stat(p);e==nil{return p,nil}}
 return "",errors.New("Chrome not found; configure NOTEBOOK_CHROME_PATH")
}
func(b *browser)activePort()int{
 data,err:=os.ReadFile(filepath.Join(b.profile,"DevToolsActivePort"));if err!=nil{return 0}
 first:=strings.SplitN(string(data),"\n",2)[0];port,_:=strconv.Atoi(strings.TrimSpace(first));return port
}
func(b *browser)ping(port int)bool{
 if port<1||port>65535{return false}
 c:=http.Client{Timeout:800*time.Millisecond};r,e:=c.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version",port));if e!=nil{return false};defer r.Body.Close();return r.StatusCode==200
}
// resolveProfile converts the profile directory to an absolute path before
// launching Chrome. Recent Chrome versions reject a relative --user-data-dir.
func (b *browser) resolveProfile() error {
 if strings.TrimSpace(b.profile)=="" {return errors.New("Notebook Chrome profile directory is empty")}
 absolute,err:=filepath.Abs(b.profile)
 if err!=nil{return fmt.Errorf("resolve Notebook Chrome profile directory: %w",err)}
 b.profile=filepath.Clean(absolute)
 return nil
}
func(b *browser)start(ctx context.Context,show bool)error{
 if b.shared!=nil{return nil}
 if err:=b.resolveProfile();err!=nil{return err}
 if b.ping(b.port){return nil}
 if port:=b.activePort();b.ping(port){b.port=port;return nil}
 path,err:=chromePath();if err!=nil{return err}
 if err=os.MkdirAll(b.profile,0700);err!=nil{return err}
 _=os.Remove(filepath.Join(b.profile,"DevToolsActivePort"))
 args:=[]string{"--user-data-dir="+b.profile,"--remote-debugging-port=0","--remote-debugging-address=127.0.0.1","--no-first-run","--no-default-browser-check","--window-size=1100,800"}
 if !show{args=append(args,"--headless=new")};args=append(args,"https://notebook.google.com/")
 b.cmd=exec.Command(path,args...)
 if err=b.cmd.Start();err!=nil{return fmt.Errorf("launch Chrome: %w",err)}
 process:=b.cmd;go func(){_=process.Wait()}()
 deadline:=time.Now().Add(20*time.Second)
 for time.Now().Before(deadline){
  if ctx.Err()!=nil{return ctx.Err()}
  if port:=b.activePort();b.ping(port){b.port=port;return nil}
  time.Sleep(180*time.Millisecond)
 }
 return errors.New("Chrome did not expose a local DevToolsActivePort within 20s")
}
type cdpTarget struct{Type string `json:"type"`;URL string `json:"url"`;WebSocketURL string `json:"webSocketDebuggerUrl"`}
func(b *browser)targets(ctx context.Context)([]cdpTarget,error){
 req,e:=http.NewRequestWithContext(ctx,"GET",fmt.Sprintf("http://127.0.0.1:%d/json/list",b.port),nil);if e!=nil{return nil,e}
 c:=http.Client{Timeout:5*time.Second};resp,e:=c.Do(req);if e!=nil{return nil,e};defer resp.Body.Close()
 var t []cdpTarget;e=json.NewDecoder(resp.Body).Decode(&t);return t,e
}
func(b *browser)page(ctx context.Context)(string,error){
 ts,e:=b.targets(ctx);if e!=nil{return "",e}
 for _,t:=range ts{if t.Type=="page"&&strings.HasPrefix(t.URL,"https://notebook.google.com/")&&t.WebSocketURL!=""{return t.WebSocketURL,nil}}
 for _,t:=range ts{if t.Type=="page"&&t.WebSocketURL!=""{return t.WebSocketURL,nil}}
 return "",errors.New("Chrome has no Notebook page; open notebook.google.com in login window")
}
func cdpCall(ctx context.Context,ws,method string,params any)(json.RawMessage,error){
 good,err:=localCDP(ws);if err!=nil||!good{return nil,errors.New("refusing non-local CDP address")}
 d:=websocket.Dialer{HandshakeTimeout:6*time.Second};conn,_,err:=d.DialContext(ctx,ws,nil);if err!=nil{return nil,err};defer conn.Close()
 deadline:=time.Now().Add(180*time.Second);if when,ok:=ctx.Deadline();ok&&when.Before(deadline){deadline=when};_=conn.SetReadDeadline(deadline)
 if err=conn.WriteJSON(map[string]any{"id":1,"method":method,"params":params});err!=nil{return nil,err}
 for {
  _,data,e:=conn.ReadMessage();if e!=nil{return nil,e}
  var env struct{ID int `json:"id"`;Result json.RawMessage `json:"result"`;Error *struct{Message string `json:"message"`} `json:"error"`}
  if json.Unmarshal(data,&env)!=nil||env.ID!=1{continue}
  if env.Error!=nil{return nil,errors.New(env.Error.Message)};return env.Result,nil
 }
}
func localCDP(ws string)(bool,error){
 u,e:=url.Parse(ws);if e!=nil{return false,e}
 h:=u.Hostname();ip:=net.ParseIP(h)
 return u.Scheme=="ws"&&(h=="localhost"||ip!=nil&&ip.IsLoopback()),nil
}
func(b *browser)evaluate(ctx context.Context,expression string)(json.RawMessage,error){
 if b.shared!=nil{return b.shared.Evaluate(ctx,expression)}
 endpoint,e:=b.page(ctx);if e!=nil{return nil,e}
 raw,e:=cdpCall(ctx,endpoint,"Runtime.evaluate",map[string]any{"expression":expression,"awaitPromise":true,"returnByValue":true});if e!=nil{return nil,e}
 var res struct {Result struct{Value json.RawMessage `json:"value"`;Description string `json:"description"`} `json:"result"`;ExceptionDetails json.RawMessage `json:"exceptionDetails"`}
 if e=json.Unmarshal(raw,&res);e!=nil{return nil,e}
 if len(res.ExceptionDetails)>0{return nil,fmt.Errorf("Notebook browser evaluation failed: %s",string(res.ExceptionDetails))}
 if len(res.Result.Value)==0{return nil,errors.New("Notebook browser evaluation returned no value")}
 return res.Result.Value,nil
}
func(b *browser)navigate(ctx context.Context,target string)error{
 if b.shared!=nil{return b.shared.Navigate(ctx,target)}
 endpoint,e:=b.page(ctx);if e!=nil{return e};_,e=cdpCall(ctx,endpoint,"Page.navigate",map[string]any{"url":target});return e
}
func(b *browser)close()error{
 if b.shared!=nil{err:=b.shared.Close();b.shared=nil;return err}
 if b.port>0{
  c:=http.Client{Timeout:1500*time.Millisecond}
  resp,e:=c.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version",b.port))
  if e==nil{
   var x struct{WebSocketURL string `json:"webSocketDebuggerUrl"`}
   _=json.NewDecoder(resp.Body).Decode(&x);resp.Body.Close()
   if x.WebSocketURL!=""{ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);_,_=cdpCall(ctx,x.WebSocketURL,"Browser.close",map[string]any{});cancel()}
  }
 }
 deadline:=time.Now().Add(5*time.Second)
 for b.port>0&&b.ping(b.port)&&time.Now().Before(deadline){time.Sleep(150*time.Millisecond)}
 if b.port>0&&b.ping(b.port){return errors.New("Chrome still closing; retry login")}
 b.port=0;b.cmd=nil;return nil
}
