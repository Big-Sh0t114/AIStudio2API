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
)

// NotebookPage borrows a temporary background tab without navigating the
// AI Studio page. Caller must hold its worker lifecycle lock until Close.
type NotebookPage struct {
	worker    *Worker
	contextID string
	mu        sync.Mutex
	closed    bool
}

// notebookTabIdleTimeout keeps an idle Notebook page warm for frequent calls.
// Each Playground worker owns at most one idle Notebook tab.
const notebookTabIdleTimeout = 120 * time.Second

// notebookTabCache is an idle-tab pool scoped to a single Camoufox worker.
// A generation counter makes stale timer callbacks harmless after a Take.
type notebookTabCache struct {
	mu         sync.Mutex
	idleID     string
	timer      *time.Timer
	generation uint64
	closed     bool
	ttl        time.Duration
	discard    func(string)
}

func newNotebookTabCache(ttl time.Duration, discard func(string)) *notebookTabCache {
	return &notebookTabCache{ttl: ttl, discard: discard}
}
func (cache *notebookTabCache) Take() (string, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.closed {
		return "", false
	}
	cache.generation++
	if cache.timer != nil {
		cache.timer.Stop()
		cache.timer = nil
	}
	id := cache.idleID
	cache.idleID = ""
	return id, true
}
func (cache *notebookTabCache) Put(id string) {
	if id == "" {
		return
	}
	cache.mu.Lock()
	cache.generation++
	generation := cache.generation
	if cache.timer != nil {
		cache.timer.Stop()
		cache.timer = nil
	}
	obsolete := cache.idleID
	cache.idleID = ""
	if cache.closed || cache.ttl <= 0 {
		cache.mu.Unlock()
		if obsolete != "" {
			cache.discard(obsolete)
		}
		cache.discard(id)
		return
	}
	cache.idleID = id
	cache.timer = time.AfterFunc(cache.ttl, func() { cache.expire(generation) })
	cache.mu.Unlock()
	if obsolete != "" {
		cache.discard(obsolete)
	}
}
func (cache *notebookTabCache) expire(generation uint64) {
	cache.mu.Lock()
	if cache.closed || cache.generation != generation {
		cache.mu.Unlock()
		return
	}
	id := cache.idleID
	cache.idleID = ""
	cache.timer = nil
	cache.mu.Unlock()
	if id != "" {
		cache.discard(id)
	}
}
func (cache *notebookTabCache) Stop() {
	cache.mu.Lock()
	cache.closed = true
	cache.generation++
	if cache.timer != nil {
		cache.timer.Stop()
		cache.timer = nil
	}
	id := cache.idleID
	cache.idleID = ""
	cache.mu.Unlock()
	if id != "" {
		cache.discard(id)
	}
}

// OpenNotebookPage reuses the warm tab when possible and otherwise opens one
// in the background. It never selects or navigates the Playground chat tab.
func (worker *Worker) OpenNotebookPage(ctx context.Context) (*NotebookPage, error) {
	worker.mu.Lock()
	ready := !worker.closed && worker.client != nil
	worker.mu.Unlock()
	if !ready {
		return nil, errors.New("Playground Camoufox worker is not running")
	}
	if worker.notebookCache != nil {
		id, alive := worker.notebookCache.Take()
		if !alive {
			return nil, errors.New("Playground Camoufox worker is shutting down")
		}
		if id != "" {
			onOrigin, err := worker.client.evaluateBool(ctx, id, "location.origin === 'https://notebook.google.com'")
			if err == nil && onOrigin {
				return &NotebookPage{worker: worker, contextID: id}, nil
			}
			_ = worker.closeTab(id)
		}
	}
	id, err := worker.createTab(ctx)
	if err != nil {
		return nil, err
	}
	page := &NotebookPage{worker: worker, contextID: id}
	if err = page.Navigate(ctx, "https://notebook.google.com/"); err != nil {
		// A failed navigation must not become a cached tab.
		_ = worker.closeTab(id)
		return nil, err
	}
	return page, nil
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

// Close releases the request's lease. A valid page stays warm for 120 seconds.
func (page *NotebookPage) Close() error {
	page.mu.Lock()
	if page.closed {
		page.mu.Unlock()
		return nil
	}
	page.closed = true
	page.mu.Unlock()
	if page.worker.notebookCache != nil {
		page.worker.notebookCache.Put(page.contextID)
		return nil
	}
	return page.worker.closeTab(page.contextID)
}
