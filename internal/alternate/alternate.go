// Package alternate integrates account-scoped Antigravity and Gemini Web models.
// Both protocols are unofficial and intentionally isolated from AI Studio itself.
package alternate

import (
    "context"
    "errors"
    "fmt"
    "net/http"
    "strings"
    "sync"
    "time"
)

type Message struct { Role, Content string }
type Model struct { ID, Name, Owner string }
type HTTPResponse struct { Status int; Headers http.Header; Body []byte }
type WebTransport interface {
    Fetch(context.Context, string, string, http.Header, []byte) (HTTPResponse, error)
    Authorization() (string, int, error)
    Close() error
}
type Source struct { ID, Label, State string }
type SharedSources interface {
    Sources(context.Context) ([]Source, error)
    Open(context.Context, string) (WebTransport, error)
}
type Config struct {
    OAuthFile, ClientID, ClientSecret, ProjectID, ProxyURL string
    Sources SharedSources
    Lifecycle context.Context
}
type Provider struct {
    ag *Antigravity
    web *Web
}
func New(cfg Config) *Provider {
    return &Provider{ag: NewAntigravity(cfg), web: &Web{sources:cfg.Sources, catalogs:make(map[string]webCatalog)}}
}
func Handles(id string) bool { return strings.HasPrefix(id,"antigravity:") || strings.HasPrefix(id,"gemini-web:") }
func (p *Provider) Models(ctx context.Context) []Model {
    var models []Model
    if p.ag != nil {
        if found, err := p.ag.Models(ctx); err == nil { models = append(models, found...) }
    }
    if p.web != nil {
        if found, err := p.web.Models(ctx); err == nil { models = append(models, found...) }
    }
    return models
}
func (p *Provider) Chat(ctx context.Context, id string, messages []Message) (string,error) {
    switch {
    case strings.HasPrefix(id,"antigravity:"):
        return p.ag.Chat(ctx,strings.TrimPrefix(id,"antigravity:"),messages)
    case strings.HasPrefix(id,"gemini-web:"):
        return p.web.Chat(ctx,strings.TrimPrefix(id,"gemini-web:"),messages)
    }
    return "", &Error{Status:404,Code:"model_not_found",Message:"Unknown alternate provider model"}
}
func (p *Provider) StartOAuth(ctx context.Context) (string,error) { return p.ag.StartOAuth(ctx) }
type Error struct { Status int; Code, Message string }
func (e *Error) Error() string { return e.Message }
func fail(status int,code,msg string) error { return &Error{Status:status,Code:code,Message:msg} }
func validSourceID(value string) bool {
    if value=="" || len(value)>120 { return false }
    for _, c := range value {
        if !(c>='a'&&c<='z'||c>='A'&&c<='Z'||c>='0'&&c<='9'||c=='-'||c=='_') { return false }
    }
    return true
}
func splitWebID(id string) (string,string,error) {
    source, model, ok:=strings.Cut(id,":")
    if !ok || !validSourceID(source) || model=="" || strings.Contains(model,":") {
        return "","",fail(400,"invalid_model","Model must be gemini-web:<playground-account-id>:<web-model-id>")
    }
    return source,model,nil
}
type Web struct {
    sources SharedSources
    mu sync.Mutex
    catalogs map[string]webCatalog
}
type webCatalog struct { models []webModel; bootstrap webBootstrap; expires time.Time }
func (w *Web) source(ctx context.Context,id string) (WebTransport,error) {
    if w.sources==nil { return nil, fail(503,"provider_unavailable","Gemini Web requires a shared Playground source") }
    sources,err:=w.sources.Sources(ctx); if err!=nil {return nil,err}
    for _,source:=range sources {
        if source.ID==id {return w.sources.Open(ctx,id)}
    }
    return nil,fail(404,"account_not_found",fmt.Sprintf("Playground account %q was not found or is disabled",id))
}
func (w *Web) Models(ctx context.Context) ([]Model,error) {
    if w.sources==nil {return nil,nil}
    sources,err:=w.sources.Sources(ctx);if err!=nil{return nil,err}
    var all []Model
    for _,src:=range sources {
        if !validSourceID(src.ID) {continue}
        cctx,cancel:=context.WithTimeout(ctx,12*time.Second)
        catalog,err:=w.catalog(cctx,src.ID,false)
        cancel()
        if err!=nil {continue}
        for _,m:=range catalog.models {
            all=append(all,Model{ID:"gemini-web:"+src.ID+":"+m.ID,Name:"Gemini Web / "+src.Label+" / "+m.Name,Owner:"gemini-web"})
        }
        all=append(all,Model{ID:"gemini-web:"+src.ID+":default",Name:"Gemini Web / "+src.Label+" / Default",Owner:"gemini-web"})
    }
    return all,nil
}
func (w *Web) catalog(ctx context.Context,id string,force bool) (webCatalog,error) {
    w.mu.Lock()
    cached,ok:=w.catalogs[id]
    w.mu.Unlock()
    if !force && ok && time.Now().Before(cached.expires) {return cached,nil}
    page,err:=w.source(ctx,id);if err!=nil{return webCatalog{},err}
    defer page.Close()
    data,err:=webInit(ctx,page)
    if err!=nil{return webCatalog{},err}
    w.mu.Lock()
    w.catalogs[id]=data
    w.mu.Unlock()
    return data,nil
}
func checkStatus(res HTTPResponse, op string) error {
    if res.Status==200 {return nil}
    if res.Status==401||res.Status==403{return fail(401,"authentication_required",op+": Google session not authenticated")}
    return fail(502,"upstream_error",fmt.Sprintf("%s: upstream HTTP %d",op,res.Status))
}
func safeText(messages []Message) (string,error) {
    if len(messages)==0 {return "",fail(400,"invalid_request","Messages are required")}
    var parts []string
    for _,m:=range messages {
        if m.Role!="system"&&m.Role!="developer"&&m.Role!="user"&&m.Role!="assistant" {return "",errors.New("Unsupported chat role "+m.Role)}
        if strings.TrimSpace(m.Content)!="" {
            parts=append(parts,"["+strings.ToUpper(m.Role)+"]\n"+m.Content)
        }
    }
    if len(parts)==0{return "",fail(400,"invalid_request","Empty message content")}
    return strings.Join(parts,"\n\n"),nil
}
