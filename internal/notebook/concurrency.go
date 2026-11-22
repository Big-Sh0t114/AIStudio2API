package notebook

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Shared Notebook calls are bounded per Google Playground source, not per
// Notebook document or local alias. This prevents duplicated account records
// from multiplying the permitted concurrency against the same Google quota.
const (
	DefaultConcurrency        = 4
	MinConcurrency            = 1
	MaxConcurrency            = 16
	notebookMaxQueuedRequests = 10
	notebookQueueWait         = 30 * time.Second
)

type notebookGate struct {
	mu       sync.Mutex
	active   int
	limit    int
	waiting  int
	changed  chan struct{}
	cooldown time.Time
}

func newNotebookGate(limits ...int) *notebookGate {
	limit := DefaultConcurrency
	if len(limits) > 0 && limits[0] >= MinConcurrency && limits[0] <= MaxConcurrency {
		limit = limits[0]
	}
	return &notebookGate{limit: limit, changed: make(chan struct{})}
}

func cooldownError(until time.Time) error {
	return &APIError{Status: 429, Code: "rate_limit_exceeded",
		Message: "Notebook source is cooling down until " + until.Format(time.RFC3339), RetryAt: until}
}

func (g *notebookGate) cooldownUntil() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cooldown
}

func (g *notebookGate) notifyLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *notebookGate) setLimit(limit int) {
	g.mu.Lock()
	if g.limit != limit {
		g.limit = limit
		g.notifyLocked()
	}
	g.mu.Unlock()
}

func (g *notebookGate) markCooldown(until time.Time) {
	g.mu.Lock()
	if until.After(g.cooldown) {
		g.cooldown = until
		g.notifyLocked()
	}
	g.mu.Unlock()
}

// acquire uses a context-aware, bounded queue. A lower configured limit
// never cancels active generations; it takes effect as those calls finish.
func (g *notebookGate) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if time.Now().Before(g.cooldown) {
		until := g.cooldown
		g.mu.Unlock()
		return nil, cooldownError(until)
	}
	if g.waiting == 0 && g.active < g.limit {
		g.active++
		g.mu.Unlock()
		return g.release, nil
	}
	if g.waiting >= notebookMaxQueuedRequests {
		g.mu.Unlock()
		return nil, &APIError{Status: 429, Code: "notebook_queue_full",
			Message: fmt.Sprintf("Notebook source has %d queued requests", notebookMaxQueuedRequests)}
	}
	g.waiting++
	g.mu.Unlock()

	timer := time.NewTimer(notebookQueueWait)
	defer timer.Stop()
	for {
		g.mu.Lock()
		if err := ctx.Err(); err != nil {
			g.waiting--
			g.mu.Unlock()
			return nil, err
		}
		if time.Now().Before(g.cooldown) {
			until := g.cooldown
			g.waiting--
			g.mu.Unlock()
			return nil, cooldownError(until)
		}
		if g.active < g.limit {
			g.active++
			g.waiting--
			g.mu.Unlock()
			return g.release, nil
		}
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			g.mu.Lock()
			g.waiting--
			g.mu.Unlock()
			return nil, ctx.Err()
		case <-timer.C:
			g.mu.Lock()
			g.waiting--
			g.mu.Unlock()
			return nil, &APIError{Status: 504, Code: "notebook_queue_timeout",
				Message: "Notebook request exceeded the 30-second queue wait limit"}
		}
	}
}

func (g *notebookGate) release() {
	g.mu.Lock()
	g.active--
	g.notifyLocked()
	g.mu.Unlock()
}

func (p *Provider) gateForSource(sourceID string) *notebookGate {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gates == nil {
		p.gates = make(map[string]*notebookGate)
	}
	if gate := p.gates[sourceID]; gate != nil {
		return gate
	}
	gate := newNotebookGate(p.concurrentLimit)
	// Preserve any persisted 429 cooldowns from legacy Notebook bindings.
	for _, a := range p.accounts {
		a.mu.Lock()
		if a.status.SourceAccountID == sourceID {
			gate.markCooldown(a.status.CooldownUntil)
		}
		a.mu.Unlock()
	}
	p.gates[sourceID] = gate
	return gate
}

func (p *Provider) markSourceCooldown(sourceID string, until time.Time) {
	if sourceID == "" || until.IsZero() {
		return
	}
	gate := p.gateForSource(sourceID)
	gate.markCooldown(until)
	// Legacy duplicate Notebook IDs share the upstream quota and must not
	// independently bypass a 429 cooldown by selecting another local record.
	for _, a := range p.list() {
		a.mu.Lock()
		if a.status.SourceAccountID == sourceID && until.After(a.status.CooldownUntil) {
			a.status.CooldownUntil = until
			a.status.State = "cooldown"
		}
		a.mu.Unlock()
	}
}

// Chat routes existing standalone Chrome accounts through the established
// serialized CDP flow, and linked Playground accounts through bounded
// concurrent, request-local Notebook sessions.
func (p *Provider) Chat(ctx context.Context, model string, messages []Message) (Answer, error) {
	accountID, notebookID, err := splitModel(model)
	if err != nil {
		return Answer{}, err
	}
	a, err := p.get(accountID)
	if err != nil {
		return Answer{}, err
	}
	a.mu.Lock()
	sourceID := a.status.SourceAccountID
	a.mu.Unlock()
	if sourceID == "" {
		return p.chatSerial(ctx, model, messages)
	}
	question, history, err := prepareConversation(messages)
	if err != nil {
		return Answer{}, err
	}
	gate := p.gateForSource(sourceID)
	done, err := gate.acquire(ctx)
	if err != nil {
		return Answer{}, err
	}
	defer done()
	return p.chatLinked(ctx, a, sourceID, notebookID, question, history)
}

// snapshotSourceMetadata creates an independent map for the request. Snapshots
// themselves are immutable; the caller never modifies their nested titles/IDs.
func snapshotSourceMetadata(source map[string]notebookSourceSnapshot) map[string]notebookSourceSnapshot {
	snapshot := make(map[string]notebookSourceSnapshot, len(source))
	for id, item := range source {
		snapshot[id] = item
	}
	return snapshot
}

func (p *Provider) chatLinked(ctx context.Context, a *account, sourceID, notebookID, question string, history []any) (Answer, error) {
	if err := ctx.Err(); err != nil {
		return Answer{}, err
	}
	a.mu.Lock()
	if a.status.CooldownUntil.After(time.Now()) {
		until := a.status.CooldownUntil
		a.mu.Unlock()
		return Answer{}, cooldownError(until)
	}
	local := &account{
		status:         AccountStatus{SourceAccountID: sourceID},
		sourceMetadata: snapshotSourceMetadata(a.sourceMetadata),
		httpBootstrap:  a.httpBootstrap,
	}
	a.status.RequestCount++
	a.status.LastRequest = time.Now()
	a.mu.Unlock()

	// Each call owns its own borrowed tab/HTTP adapter. Never keep the account
	// state mutex across network I/O or use a shared browser field here.
	err := p.ensureBrowser(ctx, local)
	var ans Answer
	if err == nil {
		ans, err = p.chatLocked(ctx, local, notebookID, question, history)
	}
	// Closing the request-local session also returns the Worker read lease and
	// copies its transient HTTP bootstrap into local.httpBootstrap.
	p.closeSharedPage(local)

	var cooldown time.Time
	a.mu.Lock()
	if err != nil {
		delete(a.sourceMetadata, notebookID)
	} else if source, ok := local.sourceMetadata[notebookID]; ok {
		if a.sourceMetadata == nil {
			a.sourceMetadata = make(map[string]notebookSourceSnapshot)
		}
		a.sourceMetadata[notebookID] = source
	}
	if local.httpBootstrap != nil {
		a.httpBootstrap = local.httpBootstrap
	}
	if err == nil {
		a.status.State = "ready"
		a.status.Error = ""
		a.status.SuccessCount++
	} else {
		a.status.Error = err.Error()
		var api *APIError
		if errors.As(err, &api) {
			switch api.Status {
			case 429:
				cooldown = api.RetryAt
				if cooldown.IsZero() {
					cooldown = time.Now().Add(10 * time.Minute)
				}
				if cooldown.After(a.status.CooldownUntil) {
					a.status.CooldownUntil = cooldown
				}
				a.status.State = "cooldown"
			case 401:
				a.status.State = "auth_required"
			}
		}
	}
	a.mu.Unlock()
	if !cooldown.IsZero() {
		p.markSourceCooldown(sourceID, cooldown)
	}
	_ = p.save()
	return ans, err
}
