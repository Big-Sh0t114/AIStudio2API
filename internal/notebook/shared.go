package notebook

import (
	"context"
	"encoding/json"
	"net/http"
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
// HTTPStreamPage delivers successful response body chunks during the read.
type HTTPStreamPage interface {
	FetchStream(ctx context.Context, method, target string, headers http.Header, body []byte, onChunk func([]byte) error) (int, error)
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
	if c, ok := sessions.(interface{ SetNotebookConcurrency(int) }); ok {
		c.SetNotebookConcurrency(p.Concurrency())
	}
	return p
}
func (p *Provider) Sources(ctx context.Context) ([]SourceAccount, error) {
	if p.shared == nil {
		return []SourceAccount{}, nil
	}
	return p.shared.Sources(ctx)
}

// CreateWithSource binds a Playground account idempotently. It remains a
// two-return wrapper for existing callers; the HTTP handler uses the variant
// that also reports whether a record was actually created.
func (p *Provider) CreateWithSource(ctx context.Context, label, sourceID string) (AccountStatus, error) {
	account, _, err := p.CreateWithSourceResult(ctx, label, sourceID)
	return account, err
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
