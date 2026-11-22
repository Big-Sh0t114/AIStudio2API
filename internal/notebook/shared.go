package notebook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// SharedPage executes Notebook's origin-local RPC in a borrowed browser tab.
type SharedPage interface {
	Evaluate(context.Context, string) (json.RawMessage, error)
	Navigate(context.Context, string) error
	Close() error
}

// HTTPPage is an optional direct transport for a linked SpiderMonkey worker.
// Camoufox and standalone Chrome continue to use browser-side JavaScript.
// It reuses the source account's HTTP client, proxy and authenticated cookies.
type HTTPPage interface {
	Fetch(ctx context.Context, method, target string, headers http.Header, body []byte) (HTTPResponse, error)
}
type HTTPResponse struct {
	Status int
	Body   []byte
}
type SourceAccount struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"`
}
type SharedSessions interface {
	Sources(context.Context) ([]SourceAccount, error)
	Open(context.Context, string) (SharedPage, error)
}

func NewWithShared(root string, sessions SharedSessions) *Provider {
	p := New(root)
	p.shared = sessions
	return p
}
func (p *Provider) Sources(ctx context.Context) ([]SourceAccount, error) {
	if p.shared == nil {
		return []SourceAccount{}, nil
	}
	return p.shared.Sources(ctx)
}
func (p *Provider) CreateWithSource(ctx context.Context, label, sourceID string) (AccountStatus, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return p.Create(label)
	}
	if p.shared == nil {
		return AccountStatus{}, &APIError{Status: 503, Code: "shared_browser_unavailable", Message: "Shared Playground browser unavailable"}
	}
	accounts, err := p.shared.Sources(ctx)
	if err != nil {
		return AccountStatus{}, err
	}
	found := false
	for _, v := range accounts {
		if v.ID == sourceID {
			found = true
			break
		}
	}
	if !found {
		return AccountStatus{}, &APIError{Status: 400, Code: "unknown_source_account", Message: fmt.Sprintf("Playground account %q is not enabled", sourceID)}
	}
	label = strings.TrimSpace(label)
	if len(label) > 120 {
		return AccountStatus{}, &APIError{Status: 400, Code: "invalid_request", Message: "Label too long"}
	}
	if label == "" {
		label = "Shared Playground Notebook"
	}
	st := AccountStatus{ID: newID(), Label: label, SourceAccountID: sourceID, State: "auth_required", Notebooks: []Notebook{}}
	p.mu.Lock()
	p.accounts[st.ID] = &account{status: st}
	p.mu.Unlock()
	if err := p.save(); err != nil {
		return st, fmt.Errorf("save linked notebook: %w", err)
	}
	return st, nil
}

// Caller holds the Notebook account mutex. Closing this object closes only
// the borrowed Notebook tab; it cannot close the Playground worker.
func (p *Provider) closeSharedPage(a *account) {
	if a.status.SourceAccountID != "" && a.browser != nil {
		a.httpBootstrap = a.browser.httpBootstrap
		_ = a.browser.close()
		a.browser = nil
	}
}
