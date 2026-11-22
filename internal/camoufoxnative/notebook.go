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

	"github.com/Mag1cFall/AIStudio2API/internal/pagepool"
)

// NotebookPage borrows a temporary background tab without navigating the
// AI Studio page. Caller must hold its worker lifecycle lock until Close.
type NotebookPage struct {
	worker    *Worker
	contextID string
	mu        sync.Mutex
	closed    bool
	lease     *pagepool.Lease[string]
}

// notebookTabIdleTimeout is a service-specific retention policy. The page
// lease implementation is shared with Playground; Notebook owns only its URL
// and RPC adapter.
const notebookTabIdleTimeout = 120 * time.Second

// OpenNotebookPage borrows a page from the Worker-scoped generic page pool.
// Its lease is returned even if the caller's context is canceled, and an
// invalid or redirected page is never handed to the next request.
func (worker *Worker) OpenNotebookPage(ctx context.Context) (*NotebookPage, error) {
	if worker.notebookPages == nil {
		return nil, errors.New("Notebook page pool is unavailable")
	}
	for attempts := 0; attempts < 2; attempts++ {
		lease, err := worker.notebookPages.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		contextID := lease.Page()
		onOrigin, err := worker.client.evaluateBool(ctx, contextID, "location.origin === 'https://notebook.google.com'")
		if err == nil && onOrigin {
			return &NotebookPage{worker: worker, contextID: contextID, lease: lease}, nil
		}
		// Browser context was replaced, navigated away, or lost. Discard the
		// lease and replace the tab rather than leaking an unsafe page.
		_ = lease.Discard()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("Notebook page did not stay on notebook.google.com")
}

// Evaluate serializes nested Notebook RPC responses as JSON, since BiDi
// remote values may not otherwise preserve arbitrarily nested objects.
func (page *NotebookPage) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.closed {
		return nil, errors.New("Notebook tab is closed")
	}
	wrapped := "(async () => JSON.stringify(await (" + expression + ")))()"
	remote, err := page.worker.client.evaluate(ctx, page.contextID, wrapped)
	if err != nil {
		return nil, fmt.Errorf("evaluate Notebook tab: %w", err)
	}
	value, ok := remote["value"].(string)
	if !ok || value == "" || !json.Valid([]byte(value)) {
		return nil, errors.New("Notebook tab returned invalid JSON")
	}
	return json.RawMessage(value), nil
}
func (page *NotebookPage) Navigate(ctx context.Context, target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "notebook.google.com") {
		return errors.New("Notebook tab navigation is restricted to notebook.google.com")
	}
	page.mu.Lock()
	defer page.mu.Unlock()
	if page.closed {
		return errors.New("Notebook tab is closed")
	}
	_, err = page.worker.client.command(ctx, "browsingContext.navigate", map[string]any{
		"context": page.contextID, "url": target, "wait": "complete",
	})
	if err != nil {
		return fmt.Errorf("navigate Notebook tab: %w", err)
	}
	return nil
}

// Close returns the page lease to the shared page pool. An idle Notebook tab
// remains warm for up to 120 seconds; the Playground Worker remains running.
func (page *NotebookPage) Close() error {
	page.mu.Lock()
	if page.closed {
		page.mu.Unlock()
		return nil
	}
	page.closed = true
	page.mu.Unlock()
	if page.lease != nil {
		return page.lease.Release()
	}
	return page.worker.closeTab(page.contextID)
}
