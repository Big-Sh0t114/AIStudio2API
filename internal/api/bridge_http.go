package api

import (
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "time"
    "unicode/utf8"

    "github.com/Mag1cFall/AIStudio2API/internal/aistudio"
    "github.com/Mag1cFall/AIStudio2API/internal/bridge"
    "github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

// modelPrefixInBody checks the bounded request prefix and restores the body so
// the existing Notebook/Playground decoding is unaffected by the router.
func bridgeModelInBody(r *http.Request) bool {
    if r.Body==nil {return false}
    original:=r.Body
    prefix,_:=io.ReadAll(io.LimitReader(original,64<<10))
    r.Body=struct{io.Reader;io.Closer}{io.MultiReader(bytes.NewReader(prefix),original),original}
    var meta struct{Model string `json:"model"`}
    if json.Unmarshal(prefix,&meta)==nil {return bridge.Handles(meta.Model)}
    compact:=bytes.ReplaceAll(prefix,[]byte(" "),nil)
    return bytes.Contains(compact,[]byte("\"model\":\"antigravity:"))||
        bytes.Contains(compact,[]byte("\"model\":\"gemini-web:"))
}

func (s *server) handleBridgeOrNotebookChat(w http.ResponseWriter,r *http.Request) {
    if bridgeModelInBody(r) {
        s.handleBridgeChat(w,r)
        return
    }
    s.handleNotebookOrChat(w,r)
}

func (s *server) bridgeAuthorized(r *http.Request, model string) bool {
    if s.config.BridgeAuthorized==nil || !strings.HasPrefix(model,"gemini-web:") {return true}
    suffix:=strings.TrimPrefix(model,"gemini-web:")
    id,_,_:=strings.Cut(suffix,":")
    for _,candidate:=range apiKeyCandidates(r) {
        for _,key:=range apiKeys(s.config.APIKey) {
            if candidate==key {return s.config.BridgeAuthorized(r.Context(),key,id)}
        }
    }
    return false
}

func (s *server) handleBridgeChat(w http.ResponseWriter,r *http.Request) {
    if s.config.Bridge==nil {
        writeOpenAIError(w,503,"not_configured","Bridge provider is not configured")
        return
    }
    var input struct {
        Model string `json:"model"`
        Stream bool `json:"stream"`
        Messages []struct {Role string `json:"role"`;Content json.RawMessage `json:"content"`} `json:"messages"`
        Tools []json.RawMessage `json:"tools"`
        N *int `json:"n"`
    }
    if err:=json.NewDecoder(io.LimitReader(r.Body,8<<20)).Decode(&input);err!=nil {
        writeOpenAIError(w,400,"invalid_request",err.Error());return
    }
    if !bridge.Handles(input.Model) {
        writeOpenAIError(w,400,"invalid_model","Unsupported bridge model");return
    }
    if !s.bridgeAuthorized(r,input.Model) {
        writeOpenAIError(w,403,"permission_denied","API key cannot access this Gemini Web source account");return
    }
    if len(input.Tools)>0 {
        writeOpenAIError(w,400,"unsupported_feature","Bridge models currently support text chat only, without function tools");return
    }
    if input.N!=nil && *input.N!=1 {
        writeOpenAIError(w,400,"unsupported_feature","Bridge models support n=1 only");return
    }
    messages:=make([]bridge.Message,0,len(input.Messages))
    var promptTokens int64
    var chars int
    for _,msg:=range input.Messages {
        content,err:=notebookTextContent(msg.Content)
        if err!=nil {writeOpenAIError(w,400,"unsupported_content",err.Error());return}
        messages=append(messages,bridge.Message{Role:msg.Role,Content:content})
        promptTokens+=notebookTextTokens(content)
        chars+=utf8.RuneCountInString(content)
    }
    channel:="antigravity"
    if strings.HasPrefix(input.Model,"gemini-web:") {channel="gemini-web"}
    SetAccessLogChannel(r.Context(),channel)
    SetAccessLogTarget(r.Context(),input.Model,input.Model)
    SetAccessLogTextInput(r.Context(),len(messages),chars)
    StartAccessLog(r.Context())
    answer,err:=s.config.Bridge.Chat(r.Context(),input.Model,messages)
    if err!=nil {
        SetAccessLogError(r.Context(),err)
        writeBridgeError(w,err)
        return
    }
    usage:=&aistudio.Usage{InputTokens:promptTokens,OutputTokens:notebookTextTokens(answer),
        TotalTokens:promptTokens+notebookTextTokens(answer),Estimated:true}
    SetAccessLogGenerationResult(r.Context(),usage,0)
    SetAccessLogFinishReason(r.Context(),"stop")
    w.Header().Set("X-Token-Usage-Estimated","true")
    id,created:=newID("chatcmpl"),time.Now().Unix()
    if input.Stream {
        w.Header().Set("Content-Type","text/event-stream; charset=utf-8")
        w.Header().Set("Cache-Control","no-cache")
        w.WriteHeader(http.StatusOK)
        for _,delta:=range []map[string]any{
            {"id":id,"object":"chat.completion.chunk","created":created,"model":input.Model,
             "choices":[]any{map[string]any{"index":0,"delta":map[string]string{"role":"assistant","content":answer},"finish_reason":nil}}},
            {"id":id,"object":"chat.completion.chunk","created":created,"model":input.Model,
             "choices":[]any{map[string]any{"index":0,"delta":map[string]any{},"finish_reason":"stop"}}},
        } {
            raw,_:=json.Marshal(delta)
            if _,err:=fmt.Fprintf(w,"data: %s\n\n",raw);err!=nil{return}
        }
        _,_=io.WriteString(w,"data: [DONE]\n\n")
        _=http.NewResponseController(w).Flush()
        return
    }
    writeJSON(w,200,map[string]any{
        "id":id,"object":"chat.completion","created":created,"model":input.Model,
        "choices":[]any{map[string]any{"index":0,"message":map[string]string{"role":"assistant","content":answer},"finish_reason":"stop"}},
        "usage":map[string]any{"prompt_tokens":usage.InputTokens,"completion_tokens":usage.OutputTokens,
            "total_tokens":usage.TotalTokens,"estimated":true,"estimation_method":"text_only_heuristic"},
    })
}

func writeBridgeError(w http.ResponseWriter,err error) {
    var apiErr *bridge.APIError
    if errors.As(err,&apiErr) {
        writeOpenAIError(w,apiErr.Status,apiErr.Code,apiErr.Message)
        return
    }
    var notebookErr *notebook.APIError
    if errors.As(err,&notebookErr) {
        writeNotebookError(w,notebookErr)
        return
    }
    writeOpenAIError(w,502,"upstream_error",err.Error())
}

// These wrappers preserve the existing Notebook public catalog while adding
// namespaced bridge models, even when Playground is not running yet.
func (s *server) handleBridgeOrModels(w http.ResponseWriter,r *http.Request) {
    if s.config.Bridge==nil {s.handleNotebookOrModels(w,r);return}
    rec:=httptest.NewRecorder()
    s.handleNotebookOrModels(rec,r)
    var result struct {Object string `json:"object"`;Data []map[string]any `json:"data"`}
    if rec.Code>=200&&rec.Code<300 {_=json.Unmarshal(rec.Body.Bytes(),&result)}
    if result.Data==nil {result.Data=[]map[string]any{}}
    for _,m:=range s.config.Bridge.Models(r.Context()) {
        if !s.bridgeAuthorized(r,m.ID) {continue}
        result.Data=append(result.Data,map[string]any{"id":m.ID,"name":m.Name,"object":"model","owned_by":m.Owner})
    }
    if len(result.Data)==0&&rec.Code>=400 {
        w.WriteHeader(rec.Code);_,_=w.Write(rec.Body.Bytes());return
    }
    writeJSON(w,200,map[string]any{"object":"list","data":result.Data})
}

func (s *server) handleBridgeOrModel(w http.ResponseWriter,r *http.Request) {
    model:=r.PathValue("model")
    if !bridge.Handles(model) {s.handleNotebookOrModel(w,r);return}
    if s.config.Bridge!=nil {
        for _,m:=range s.config.Bridge.Models(r.Context()) {
            if m.ID==model&&s.bridgeAuthorized(r,model) {
                writeJSON(w,200,map[string]any{"id":m.ID,"name":m.Name,"object":"model","owned_by":m.Owner})
                return
            }
        }
    }
    writeOpenAIError(w,404,"model_not_found","Bridge model is not registered or its account is unavailable")
}
