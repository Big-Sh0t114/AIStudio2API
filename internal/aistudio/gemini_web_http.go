package aistudio

import (
 "context"
 "crypto/sha1"
 "encoding/hex"
 "errors"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "strconv"
 "strings"
 "time"
)

// GeminiWebHTTP borrows the existing SpiderMonkey Playground worker's HTTP
// client, Google cookies, proxy and user agent. Only documented source-code
// protocol endpoints are allowed; arbitrary URLs and redirects are denied.
func (worker *NativeWorker) GeminiWebHTTP(ctx context.Context,method,target string,headers http.Header,body []byte) (int,http.Header,[]byte,error) {
 runtime,ok:=worker.current().(*goWAARuntime)
 if !ok {return 0,nil,nil,errors.New("Gemini Web requires WAA_BACKEND=spidermonkey")}
 parsed,err:=url.Parse(target);if err!=nil{return 0,nil,nil,err}
 if parsed.Scheme!="https"||parsed.User!=nil||parsed.Fragment!="" {return 0,nil,nil,errors.New("Invalid Gemini Web origin")}
 switch parsed.Host {
 case "gemini.google.com":
  if method=="GET"&&parsed.Path!="/app" ||method=="POST"&&parsed.Path!="/_/BardChatUi/data/batchexecute" {
   return 0,nil,nil,errors.New("Gemini Web URL not allowlisted")
  }
 case "geminiweb-pa.clients6.google.com":
  if parsed.Path!="/v1/processSession"||method!="POST"&&method!="GET" {
   return 0,nil,nil,errors.New("Gemini Web channel URL not allowlisted")
  }
 default:return 0,nil,nil,errors.New("Gemini Web destination not allowlisted")
 }
 if method!="GET"&&method!="POST"{return 0,nil,nil,errors.New("Unsupported method")}
 h:=headers.Clone();if h==nil{h=make(http.Header)}
 h.Set("Accept-Encoding","identity")
 if method=="GET"&&parsed.Host=="gemini.google.com" {
  h.Del("Origin")
  h.Set("Sec-Fetch-Mode","navigate")
  h.Set("Sec-Fetch-Site","none")
  h.Set("Sec-Fetch-Dest","document")
  h.Set("Sec-Fetch-User","?1")
 }else{
  h.Set("Origin","https://gemini.google.com")
  h.Set("Referer","https://gemini.google.com/")
  h.Set("Sec-Fetch-Mode","cors")
  h.Set("Sec-Fetch-Dest","empty")
  if parsed.Host=="gemini.google.com" {h.Set("Sec-Fetch-Site","same-origin")}else{h.Set("Sec-Fetch-Site","same-site")}
 }
 resp,err:=runtime.do(ctx,method,target,body,h)
 if err!=nil{return 0,nil,nil,fmt.Errorf("Gemini Web HTTP request: %w",err)}
 defer resp.Body.Close()
 if resp.Request==nil||resp.Request.URL==nil||resp.Request.URL.Host!=parsed.Host||resp.Request.URL.Scheme!="https" {
  return 0,nil,nil,errors.New("Gemini Web redirected outside the allowlisted origin")
 }
 if resp.StatusCode>=300&&resp.StatusCode<400 {
  return 0,nil,nil,errors.New("Gemini Web auth redirect; linked Playground login may have expired")
 }
 content,err:=io.ReadAll(io.LimitReader(resp.Body,(32<<20)+1))
 if err!=nil{return 0,nil,nil,err}
 if len(content)>32<<20{return 0,nil,nil,errors.New("Gemini Web HTTP body exceeds 32 MiB")}
 return resp.StatusCode,resp.Header.Clone(),content,nil
}

// GeminiWebAuthorization generates the Google web SAPISIDHASH header from
// the same authenticated Cookie jar; no token or Cookie is returned via APIs.
func (worker *NativeWorker) GeminiWebAuthorization() (string,int,error) {
 runtime,ok:=worker.current().(*goWAARuntime)
 if !ok{return "",0,errors.New("Gemini Web requires SpiderMonkey")}
 state:=runtime.cookies();now:=time.Now()
 stamp:=strconv.FormatInt(now.Unix(),10)
 values:=[]struct{name,prefix string}{
  {"SAPISID","SAPISIDHASH"},{"__Secure-1PAPISID","SAPISID1PHASH"},{"__Secure-3PAPISID","SAPISID3PHASH"},
 }
 var parts []string
 for _,v:=range values{
  cookie,ok:=state.CookieValue(v.name,"https://gemini.google.com/",now)
  if !ok||cookie==""{continue}
  hash:=sha1.Sum([]byte(stamp+" "+cookie+" https://gemini.google.com"))
  parts=append(parts,v.prefix+" "+stamp+"_"+hex.EncodeToString(hash[:]))
 }
 if len(parts)==0{return "",0,errors.New("Playground account has no SAPISID cookies for Gemini Web")}
 return strings.Join(parts," "),runtime.options.AuthUser,nil
}
