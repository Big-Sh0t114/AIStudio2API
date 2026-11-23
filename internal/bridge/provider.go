// Package bridge adds optional OAuth and Gemini Web backends to the existing
// OpenAI-compatible gateway. Credentials are never returned to API callers.
package bridge

import (
    "context"
    "fmt"
    "net/http"
    "net/url"
    "os"
    "strings"
    "sync"
    "time"

    "github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

type Message struct {
    Role string
    Content string
}

type Model struct {
    ID string
    Name string
    Owner string
}

type Config struct {
    OAuthFile string
    OAuthClientID string
    OAuthClientSecret string
    ProjectID string
    AntigravityModels []string
    Sources notebook.SharedSessions
    Proxy string
}

// Provider is immutable except for its protected, in-memory OAuth token cache.
// Both backends are opt-in; the Playground worker is never created by the OAuth path.
type Provider struct {
    cfg Config
    http *http.Client
    oauth tokenCache
    flowMu sync.Mutex
    flowActive bool
}

func New(cfg Config) (*Provider, error) {
    transport := http.DefaultTransport.(*http.Transport).Clone()
    if strings.TrimSpace(cfg.Proxy) != "" {
        parsed, err := url.Parse(cfg.Proxy)
        if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5") {
            return nil, fmt.Errorf("bridge: invalid proxy URL")
        }
        transport.Proxy = http.ProxyURL(parsed)
    }
    return &Provider{
        cfg: cfg,
        http: &http.Client{Transport: transport, Timeout: 10 * time.Minute,
            CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
    }, nil
}

// Handles is deliberately prefix-based, so a missing credential cannot cause
// the request to fall back to the unrelated AI Studio model catalog.
func Handles(model string) bool {
    return strings.HasPrefix(model, "antigravity:") || strings.HasPrefix(model, "gemini-web:")
}

func (p *Provider) Models(ctx context.Context) []Model {
    models := make([]Model, 0)
    if p.cfg.OAuthFile != "" {
        if info, err := os.Stat(p.cfg.OAuthFile); err == nil && !info.IsDir() {
            for _, name := range p.cfg.AntigravityModels {
                name = strings.TrimSpace(name)
                if validModelName(name) {
                    models = append(models, Model{ID: "antigravity:" + name, Name: "Antigravity · " + name, Owner: "antigravity"})
                }
            }
        }
    }
    if p.cfg.Sources != nil {
        sources, err := p.cfg.Sources.Sources(ctx)
        if err == nil {
            for _, src := range sources {
                if src.ID == "" || src.State == "disabled" {
                    continue
                }
                models = append(models, Model{
                    ID: "gemini-web:" + src.ID + ":default",
                    Name: "Gemini Web · " + src.Label + " (account default)",
                    Owner: "gemini-web",
                })
            }
        }
    }
    return models
}

func (p *Provider) Chat(ctx context.Context, model string, messages []Message) (string, error) {
    switch {
    case strings.HasPrefix(model, "antigravity:"):
        return p.antigravity(ctx, strings.TrimPrefix(model, "antigravity:"), messages)
    case strings.HasPrefix(model, "gemini-web:"):
        return p.geminiWeb(ctx, strings.TrimPrefix(model, "gemini-web:"), messages)
    default:
        return "", &APIError{Status: 404, Code: "model_not_found", Message: "not a bridge model"}
    }
}

func validModelName(model string) bool {
    if model == "" || len(model) > 128 {
        return false
    }
    for _, r := range model {
        if r < 'a' || r > 'z' {
            if r < 'A' || r > 'Z' {
                if r < '0' || r > '9' {
                    if r != '.' && r != '_' && r != '-' {
                        return false
                    }
                }
            }
        }
    }
    return true
}

// APIError reports only stable, credential-free diagnostic details.
type APIError struct {
    Status int
    Code string
    Message string
}
func (e *APIError) Error() string { return e.Message }
