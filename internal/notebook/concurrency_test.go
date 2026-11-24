package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotebookGateBoundedCapacityAndQueueCancellation(t *testing.T) {
	g := newNotebookGate(2)
	first, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	defer second()

	var wg sync.WaitGroup
	cancel := make([]context.CancelFunc, 0, notebookMaxQueuedRequests)
	for i := 0; i < notebookMaxQueuedRequests; i++ {
		ctx, stop := context.WithCancel(context.Background())
		cancel = append(cancel, stop)
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := g.acquire(ctx)
			if release != nil {
				release()
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("queued operation did not honor cancellation: %v", err)
			}
		}()
	}
	deadline := time.After(time.Second * 2)
	for {
		g.mu.Lock()
		waiting := g.waiting
		g.mu.Unlock()
		if waiting == notebookMaxQueuedRequests {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d requests queued", waiting)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	_, err = g.acquire(context.Background())
	var api *APIError
	if !errors.As(err, &api) || api.Code != "notebook_queue_full" || api.Status != 429 {
		t.Fatalf("unbounded Notebook queue: %v", err)
	}
	for _, stop := range cancel {
		stop()
	}
	wg.Wait()
	g.mu.Lock()
	waiting := g.waiting
	g.mu.Unlock()
	if waiting != 0 {
		t.Fatalf("canceled queue leaked %d entries", waiting)
	}
}

func TestNotebookGateAllowsTwoConcurrentAndWakesWaiter(t *testing.T) {
	g := newNotebookGate(2)
	releaseA, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	releaseB, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseB()
	got := make(chan func(), 1)
	errs := make(chan error, 1)
	go func() {
		release, err := g.acquire(context.Background())
		if err != nil {
			errs <- err
			return
		}
		got <- release
	}()
	select {
	case release := <-got:
		release()
		t.Fatal("third request started before a slot was freed")
	case err := <-errs:
		t.Fatalf("third request rejected rather than queued: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	releaseA()
	select {
	case release := <-got:
		release()
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("waiting request did not wake")
	}
}

func TestNotebookGateSharesCooldownAcrossAliases(t *testing.T) {
	g := newNotebookGate()
	now := time.Now().Add(2 * time.Minute)
	g.markCooldown(now)
	if _, err := g.acquire(context.Background()); err == nil {
		t.Fatal("accepted request while same Google source was cooling down")
	} else {
		var api *APIError
		if !errors.As(err, &api) || api.Status != 429 || api.RetryAt.Before(now) {
			t.Fatalf("unexpected cooldown: %v", err)
		}
	}
}

type concurrentNotebookSessions struct {
	openCount atomic.Int32
	active    atomic.Int32
	peak      atomic.Int32
	started   chan struct{}
	allow     chan struct{}
	closed    atomic.Int32
}

func (s *concurrentNotebookSessions) Sources(context.Context) ([]SourceAccount, error) {
	return []SourceAccount{{ID: "playground-1", State: "ready"}}, nil
}

func (s *concurrentNotebookSessions) Open(context.Context, string) (SharedPage, error) {
	s.openCount.Add(1)
	return &concurrentNotebookPage{owner: s}, nil
}

type concurrentNotebookPage struct{ owner *concurrentNotebookSessions }

func (p *concurrentNotebookPage) Navigate(context.Context, string) error { return nil }
func (p *concurrentNotebookPage) Close() error                           { p.owner.closed.Add(1); return nil }
func (p *concurrentNotebookPage) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	if !strings.Contains(expression, "sourcePath") {
		return nil, errors.New("unexpected Notebook RPC")
	}
	if strings.Contains(expression, `"rpcid":"hPTbtc"`) {
		frame, _ := json.Marshal([]any{[]any{"wrb.fr", listChatSessionsRPC, "[]", nil, nil, nil}})
		return json.Marshal(browserResponse{Status: 200, Text: ")]}'\n" + string(frame)})
	}
	n := p.owner.active.Add(1)
	for {
		highest := p.owner.peak.Load()
		if n <= highest || p.owner.peak.CompareAndSwap(highest, n) {
			break
		}
	}
	defer p.owner.active.Add(-1)
	select {
	case p.owner.started <- struct{}{}:
	default:
	}
	select {
	case <-p.owner.allow:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// A chat RPC (rpcid="") is returned in a frame with no rpc ID.
	inner, _ := json.Marshal([]any{[]any{"generated answer"}, nil, nil, nil, true})
	frame, _ := json.Marshal([]any{[]any{"wrb.fr", "", string(inner), nil, nil, nil}})
	response, _ := json.Marshal(browserResponse{Status: 200, Text: ")]}'\n" + string(frame)})
	return response, nil
}

func TestLinkedNotebookGenerationsRunInParallelWithIndependentPages(t *testing.T) {
	sessions := &concurrentNotebookSessions{started: make(chan struct{}, 4), allow: make(chan struct{}, 4)}
	p := NewWithShared(t.TempDir(), sessions)
	if err := p.SetConcurrency(2); err != nil {
		t.Fatal(err)
	}
	st, err := p.CreateWithSource(context.Background(), "Concurrent", "playground-1")
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.get(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.status.State = "ready"
	a.sourceMetadata = map[string]notebookSourceSnapshot{
		"nb-1": {ids: []any{[]any{[]any{"source-id"}}}, titles: map[string]string{"source-id": "doc"}, expires: time.Now().Add(time.Minute)},
		"nb-2": {ids: []any{[]any{[]any{"source-id"}}}, titles: map[string]string{"source-id": "doc"}, expires: time.Now().Add(time.Minute)},
		"nb-3": {ids: []any{[]any{[]any{"source-id"}}}, titles: map[string]string{"source-id": "doc"}, expires: time.Now().Add(time.Minute)},
	}
	a.mu.Unlock()
	models := []string{"notebook:"+st.ID+":nb-1", "notebook:"+st.ID+":nb-2", "notebook:"+st.ID+":nb-3"}
	results := make(chan error, 3)
	for _, model := range models {
		go func(model string) {
			ans, err := p.Chat(context.Background(), model, []Message{{Role: "user", Content: "question"}})
			if err == nil && ans.Text != "generated answer" {
				err = errors.New("unexpected reply: " + ans.Text)
			}
			results <- err
		}(model)
	}
	// Two simultaneous Notebook calls must reach distinct page objects.
	for i := 0; i < 2; i++ {
		select {
		case <-sessions.started:
		case <-time.After(2 * time.Second):
			t.Fatal("second concurrent Notebook request never started")
		}
	}
	if sessions.peak.Load() != 2 || sessions.openCount.Load() != 2 {
		t.Fatalf("expected 2 independent concurrent sessions: peak=%d opened=%d", sessions.peak.Load(), sessions.openCount.Load())
	}
	select {
	case <-sessions.started:
		t.Fatal("third request bypassed the concurrency gate")
	case <-time.After(40 * time.Millisecond):
	}
	sessions.allow <- struct{}{}
	select {
	case <-sessions.started:
	case <-time.After(time.Second):
		t.Fatal("third queued Notebook request did not start after release")
	}
	sessions.allow <- struct{}{}
	sessions.allow <- struct{}{}
	for i := 0; i < 3; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("Notebook request failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Notebook request deadlocked")
		}
	}
	if sessions.peak.Load() > 2 || sessions.openCount.Load() != 3 || sessions.closed.Load() != 3 {
		t.Fatalf("request leases not isolated or released: peak=%d open=%d close=%d", sessions.peak.Load(), sessions.openCount.Load(), sessions.closed.Load())
	}
	sts := p.Accounts()
	if len(sts) != 1 || sts[0].RequestCount != 3 || sts[0].SuccessCount != 3 {
		t.Fatalf("lost concurrent usage counts: %+v", sts)
	}
}

func TestNotebookAliasSharesGateAndCooldown(t *testing.T) {
	p := NewWithShared(t.TempDir(), fakeShared{})
	a, err := p.CreateWithSource(context.Background(), "Original", "playground-1")
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.accounts["legacy-alias"] = &account{status: AccountStatus{ID: "legacy-alias", SourceAccountID: "playground-1", State: "ready"}}
	p.mu.Unlock()
	gateA := p.gateForSource("playground-1")
	gateB := p.gateForSource("playground-1")
	if gateA != gateB {
		t.Fatal("source acquired separate concurrency gates")
	}
	until := time.Now().Add(time.Minute)
	p.markSourceCooldown("playground-1", until)
	for _, st := range p.Accounts() {
		if st.ID == a.ID || st.ID == "legacy-alias" {
			if st.CooldownUntil.Before(until) {
				t.Fatalf("cooldown not shared with %s", st.ID)
			}
		}
	}
}

// Tests use deterministic channels; the generated page never stores history
// between calls, and the account mutex is not held during Evaluate.
var _ sync.Locker = (*sync.Mutex)(nil)
