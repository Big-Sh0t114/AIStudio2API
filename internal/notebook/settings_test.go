package notebook

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type adjustableSessions struct {
	fakeShared
	limit atomic.Int32
}

func (s *adjustableSessions) SetNotebookConcurrency(n int) {
	s.limit.Store(int32(n))
}

func TestNotebookConcurrencyPersistsAndNotifiesSharedBrowser(t *testing.T) {
	root := t.TempDir()
	shared := &adjustableSessions{}
	p := NewWithShared(root, shared)
	if p.Concurrency() != 4 || shared.limit.Load() != 4 {
		t.Fatalf("incorrect default concurrency: %d, browser=%d", p.Concurrency(), shared.limit.Load())
	}
	if err := p.SetConcurrency(12); err != nil {
		t.Fatal(err)
	}
	if p.Concurrency() != 12 || shared.limit.Load() != 12 {
		t.Fatalf("settings were not applied to provider/browser: %d/%d", p.Concurrency(), shared.limit.Load())
	}
	another := &adjustableSessions{}
	restarted := NewWithShared(root, another)
	if restarted.Concurrency() != 12 || another.limit.Load() != 12 {
		t.Fatalf("persisted setting was lost: %d/%d", restarted.Concurrency(), another.limit.Load())
	}
	for _, n := range []int{0, -1, 17, 100} {
		err := restarted.SetConcurrency(n)
		var api *APIError
		if !errors.As(err, &api) || api.Status != 400 || api.Code != "invalid_concurrency" {
			t.Fatalf("invalid concurrency %d was not rejected: %v", n, err)
		}
	}
	if restarted.Concurrency() != 12 {
		t.Fatalf("invalid save mutated setting: %d", restarted.Concurrency())
	}
	if err := restarted.SetConcurrency(1); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SetConcurrency(16); err != nil {
		t.Fatal(err)
	}
	if New(root).Concurrency() != 16 {
		t.Fatal("maximum was not persisted")
	}
}

func TestNotebookGateResizingRespectsRunningLeases(t *testing.T) {
	g := newNotebookGate(2)
	releaseA, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	releaseB, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	g.setLimit(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	acquired := make(chan func(), 1)
	done := make(chan error, 1)
	go func() {
		release, err := g.acquire(ctx)
		if err != nil {
			done <- err
			return
		}
		acquired <- release
	}()
	select {
	case release := <-acquired:
		release()
		t.Fatal("shrunk limit allowed third request")
	case <-time.After(30 * time.Millisecond):
	}
	releaseA()
	select {
	case release := <-acquired:
		release()
		t.Fatal("shrunk limit started queued request with one lease active")
	case <-time.After(30 * time.Millisecond):
	}
	releaseB()
	select {
	case release := <-acquired:
		release()
	case err := <-done:
		t.Fatalf("queued request failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("queued request never resumed")
	}
}

func TestNotebookGateIncreaseUnblocksWaitingRequestsImmediately(t *testing.T) {
	g := newNotebookGate(1)
	release, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	acquired := make(chan func(), 1)
	go func() {
		done, err := g.acquire(context.Background())
		if err == nil {
			acquired <- done
		}
	}()
	deadline := time.After(time.Second)
	for {
		g.mu.Lock()
		waiting := g.waiting
		g.mu.Unlock()
		if waiting == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("request never entered queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	g.setLimit(4)
	select {
	case done := <-acquired:
		done()
	case <-time.After(time.Second):
		t.Fatal("increased concurrency did not release queued request")
	}
}

func TestProviderConcurrencyChangeUpdatesExistingSourceGate(t *testing.T) {
	p := NewWithShared(t.TempDir(), fakeShared{})
	g := p.gateForSource("playground-1")
	if g.limit != 4 {
		t.Fatalf("wrong default gate %d", g.limit)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			_ = p.SetConcurrency((i % 16) + 1)
		}
	}()
	for i := 0; i < 8; i++ {
		release, err := g.acquire(context.Background())
		if err == nil {
			release()
		}
	}
	wg.Wait()
	if g.limit != p.Concurrency() {
		t.Fatalf("gate and provider limits diverged: %d/%d", g.limit, p.Concurrency())
	}
}
