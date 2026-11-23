package aistudio

import (
    "context"
    "crypto/sha1"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strings"
    "time"
)

const geminiWebHost = "gemini.google.com"

var ErrGeminiWebAuthentication = errors.New("Gemini Web requires an authenticated Google session")

// GeminiWebHTTP borrows the SpiderMonkey Playground worker's exact HTTP client,
// cookie synchronization and configured proxy. No new Chrome process, cookie
// export, alternate credential jar or independent browser profile is created.
func (worker *NativeWorker) GeminiWebHTTP(ctx context.Context, method, rawURL string, headers http.Header, body []byte) (int, []byte, error) {
    runtime, ok := worker.current().(*goWAARuntime)
    if !ok {return 0,nil,errors.New("Gemini Web shared HTTP requires SpiderMonkey")}
    target,err:=url.Parse(rawURL)
    if err!=nil{return 0,nil,err}
    if target.Scheme!="https"||target.Host!=geminiWebHost||target.User!=nil||target.Fragment!="" {
        return 0,nil,errors.New("Gemini Web HTTP permits only https://gemini.google.com")
    }
    if method==http.MethodGet {
        if target.Path!="/app" {return 0,nil,errors.New("Gemini Web GET path is restricted to /app")}
    } else if method==http.MethodPost {
        if target.Path!="/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate" {
            return 0,nil,errors.New("Gemini Web POST path is restricted to StreamGenerate")
        }
    } else {return 0,nil,errors.New("Gemini Web supports only GET and POST")}

    reqHeaders:=headers.Clone()
    if reqHeaders==nil {reqHeaders=make(http.Header)}
    reqHeaders.Set("Accept-Encoding","identity")
    if method==http.MethodPost {
        reqHeaders.Set("Origin","https://gemini.google.com")
        reqHeaders.Set("Referer","https://gemini.google.com/app")
        reqHeaders.Set("Sec-Fetch-Site","same-origin")
        reqHeaders.Set("Sec-Fetch-Mode","cors")
    }
    // The Google web RPC expects SAPISIDHASH in addition to the browser cookies.
    // Derive it in memory from the shared jar; never log or persist cookie values.
    cookieHeader, _ := runtime.cookies().CookieHeader(rawURL,time.Now())
    sapisid:=""
    for _,part:=range strings.Split(cookieHeader,";") {
        key,value,found:=strings.Cut(strings.TrimSpace(part),"=")
        if found && (key=="SAPISID"||key=="__Secure-3PAPISID"||key=="__Secure-1PAPISID") {
            sapisid=value
            if key=="SAPISID" {break}
        }
    }
    if sapisid!="" {
        now:=time.Now().Unix()
        digest:=sha1.Sum([]byte(fmt.Sprintf("%d %s https://gemini.google.com",now,sapisid)))
        reqHeaders.Set("Authorization",fmt.Sprintf("SAPISIDHASH %d_%x",now,digest))
    }
    response,err:=runtime.do(ctx,method,rawURL,body,reqHeaders)
    if err!=nil {return 0,nil,fmt.Errorf("Gemini Web shared request failed: %w",err)}
    defer response.Body.Close()
    if response.Request==nil||response.Request.URL==nil||response.Request.URL.Scheme!="https"||response.Request.URL.Host!=geminiWebHost {
        return 0,nil,ErrGeminiWebAuthentication
    }
    switch response.StatusCode {
    case http.StatusMovedPermanently,http.StatusFound,http.StatusSeeOther,http.StatusTemporaryRedirect,http.StatusPermanentRedirect:
        return 0,nil,ErrGeminiWebAuthentication
    }
    const maxBytes = 32 << 20
    content,err:=io.ReadAll(io.LimitReader(response.Body,maxBytes+1))
    if err!=nil{return 0,nil,err}
    if len(content)>maxBytes{return 0,nil,errors.New("Gemini Web response exceeds 32 MiB")}
    return response.StatusCode,content,nil
}
