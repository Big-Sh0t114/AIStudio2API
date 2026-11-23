package bridge

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"
    "regexp"
    "strings"
    "time"

    "github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

var (
    webBuildLabel = regexp.MustCompile("\\\"cfb2h\\\":\\\"(boq_[^\\\"]+)\\\"")
    webCSRF = regexp.MustCompile("\\\"SNlM0e\\\":\\\"([^\\\"]+)\\\"")
)

// geminiWeb uses the active Playground account's SpiderMonkey HTTP runtime:
// same cookie jar, synchronized Google authentication, user agent and proxy.
// The default model is intentionally the only advertised one until dynamic
// model selection is implemented; pretending to select a model is unsafe.
func (p *Provider) geminiWeb(ctx context.Context, suffix string, messages []Message) (string,error) {
    accountID, selection, ok := strings.Cut(suffix,":")
    if !ok || accountID=="" || selection!="default" {
        return "", &APIError{400, "invalid_model", "Gemini Web model must be gemini-web:<playground-account-id>:default"}
    }
    if p.cfg.Sources==nil {return "",&APIError{503,"not_configured","Shared Playground sessions are not configured"}}
    page,err:=p.cfg.Sources.Open(ctx,accountID)
    if err!=nil{return "",&APIError{503,"account_unavailable",err.Error()}}
    defer page.Close()
    transport,ok:=page.(notebook.HTTPPage)
    if !ok {
        return "",&APIError{501,"backend_unsupported","Gemini Web requires WAA_BACKEND=spidermonkey to reuse Playground HTTP cookies"}
    }
    headers:=http.Header{}
    headers.Set("Accept","text/html")
    status,html,err:=webRequest(ctx,transport,http.MethodGet,"https://gemini.google.com/app",headers,nil)
    if err!=nil{return "",err}
    if status!=http.StatusOK {
        if status==http.StatusUnauthorized||status==http.StatusForbidden {
            return "",&APIError{401,"auth_required","Gemini Web is not authenticated for the linked Playground account"}
        }
        return "",&APIError{502,"upstream_error",fmt.Sprintf("Gemini Web app returned HTTP %d",status)}
    }
    build:=webBuildLabel.FindSubmatch(html)
    token:=webCSRF.FindSubmatch(html)
    if len(build)<2||len(token)<2 {
        return "",&APIError{401,"auth_required","Gemini Web cookies are missing or expired; reauthenticate the linked Playground account"}
    }

    var prompt strings.Builder
    for _,m:=range messages {
        switch strings.ToLower(m.Role) {
        case "system","developer","user","assistant":
            if strings.TrimSpace(m.Content)!="" {
                prompt.WriteString("[");prompt.WriteString(strings.ToUpper(m.Role));prompt.WriteString("]\n")
                prompt.WriteString(m.Content);prompt.WriteString("\n\n")
            }
        default:
            return "",&APIError{400,"unsupported_role","Gemini Web accepts text-only system, developer, user and assistant roles"}
        }
    }
    if prompt.Len()==0 {return "",&APIError{400,"invalid_request","No text messages supplied"}}
    uuid:=strings.ToUpper(randomID())
    inner:=make([]any,69)
    inner[0]=[]any{prompt.String(),0,nil,[]any{},nil,nil,0}
    inner[1]=[]any{"en"}
    inner[2]=[]any{"","","",nil,nil,nil,nil,nil,nil,""}
    inner[6]=[]any{1}
    inner[7]=1
    inner[10]=1
    inner[11]=0
    inner[17]=[]any{[]any{0}}
    inner[18]=0
    inner[27]=1
    inner[30]=[]any{4}
    inner[41]=[]any{1}
    inner[53]=0
    inner[59]=uuid
    inner[61]=[]any{}
    inner[68]=2
    payload,err:=json.Marshal(inner)
    if err!=nil{return "",err}
    outer,err:=json.Marshal([]any{nil,string(payload)})
    if err!=nil{return "",err}
    form:=url.Values{}
    form.Set("f.req",string(outer))
    form.Set("at",string(token[1]))
    query:=url.Values{}
    query.Set("bl",string(build[1]))
    query.Set("hl","en")
    query.Set("_reqid",fmt.Sprint(time.Now().Unix()%1000000))
    query.Set("rt","c")
    endpoint:="https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate?"+query.Encode()
    h:=http.Header{}
    h.Set("Content-Type","application/x-www-form-urlencoded")
    h.Set("Origin","https://gemini.google.com")
    h.Set("Referer","https://gemini.google.com/app")
    h.Set("X-Same-Domain","1")
    h.Set("Accept","*/*")
    // No model override: without an authenticated listModels RPC the web UI
    // only guarantees its default model, independent of account tier.
    status,body,err:=webRequest(ctx,transport,http.MethodPost,endpoint,h,[]byte(form.Encode()))
    if err!=nil{return "",err}
    if status==http.StatusTooManyRequests {
        return "",&APIError{429,"rate_limit_exceeded","Gemini Web quota or request rate limit exceeded"}
    }
    if status!=http.StatusOK {
        return "",&APIError{502,"upstream_error",fmt.Sprintf("Gemini Web StreamGenerate returned HTTP %d",status)}
    }
    answer:=extractWebText(string(body))
    if answer=="" {
        return "",&APIError{502,"empty_response","Gemini Web returned no text; its private RPC format may have changed"}
    }
    return answer,nil
}

func webRequest(ctx context.Context, transport notebook.HTTPPage, method,target string, headers http.Header, body []byte) (int,[]byte,error) {
    result,err:=transport.Fetch(ctx,method,target,headers,body)
    if err!=nil{return 0,nil,err}
    return result.Status,result.Body,nil
}

// Gemini's StreamGenerate sends cumulative text snapshots inside length-prefixed
// wrb.fr JSON frames. Parse the validated JSON structure rather than matching
// arbitrary response substrings, so errors cannot be mistaken for assistant text.
func extractWebText(body string) string {
    longest:=""
    for _,line:=range strings.Split(body,"\n") {
        if !strings.Contains(line,"\"wrb.fr\"") {continue}
        var frames any
        if err:=json.Unmarshal([]byte(line),&frames);err!=nil {continue}
        for _,v:=range webFindTexts(frames) {
            if len(v)>len(longest) {longest=v}
        }
    }
    return strings.TrimSpace(longest)
}
func webFindTexts(raw any) []string {
    arr,ok:=raw.([]any)
    if !ok {return nil}
    if len(arr)>=3 && arr[0]=="wrb.fr" {
        nested,ok:=arr[2].(string)
        if ok {
            var data any
            if json.Unmarshal([]byte(nested),&data)==nil {
                if parts,ok:=webAt(data,4).([]any);ok {
                    var result []string
                    for _,part:=range parts {
                        texts,ok:=webAt(part,1).([]any)
                        if !ok {continue}
                        for _,t:=range texts {if s,ok:=t.(string);ok && s!="" {result=append(result,s)}}
                    }
                    return result
                }
            }
        }
        return nil
    }
    var result []string
    for _,child:=range arr {result=append(result,webFindTexts(child)...)}
    return result
}
func webAt(v any, index int) any {
    arr,ok:=v.([]any)
    if !ok||index<0||index>=len(arr){return nil}
    return arr[index]
}
