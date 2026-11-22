package camoufoxnative

import (
	"sync"
	"testing"
	"time"
)

func TestNotebookTabCacheReusesPageUntilIdleTimeout(t *testing.T) {
	closed := make(chan string, 4)
	cache := newNotebookTabCache(90*time.Millisecond, func(id string) { closed <- id })
	defer cache.Stop()

	if id, ok := cache.Take(); !ok || id != "" {
		t.Fatalf("empty cache: id=%q ok=%v", id, ok)
	}
	cache.Put("tab-1")
	if id, ok := cache.Take(); !ok || id != "tab-1" {
		t.Fatalf("expected warmed tab, got id=%q ok=%v", id, ok)
	}

	// A timer canceled by Take must never close an active tab.
	time.Sleep(130 * time.Millisecond)
	select {
	case id := <-closed:
		t.Fatalf("active tab was discarded: %q", id)
	default:
	}

	cache.Put("tab-1")
	select {
	case id := <-closed:
		if id != "tab-1" {
			t.Fatalf("expired id=%q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("idle Notebook tab was not reaped")
	}
	if id, ok := cache.Take(); !ok || id != "" {
		t.Fatalf("expired cache returned %q,ok=%v", id, ok)
	}
}

func TestNotebookTabCacheStopInvalidatesIdleTab(t *testing.T) {
	var mu sync.Mutex
	var removed []string
	cache := newNotebookTabCache(time.Minute, func(id string) {
		mu.Lock()
		removed = append(removed, id)
		mu.Unlock()
	})
	cache.Put("old-tab")
	cache.Stop()
	cache.Stop()
	if id, ok := cache.Take(); ok || id != "" {
		t.Fatalf("stopped cache returned id=%q,ok=%v", id, ok)
	}
	cache.Put("new-tab")
	mu.Lock()
	defer mu.Unlock()
	if len(removed) != 2 || removed[0] != "old-tab" || removed[1] != "new-tab" {
		t.Fatalf("cleanup after shutdown: %#v", removed)
	}
}

func TestNotebookTabCacheReplacesDuplicateIdleTab(t *testing.T) {
	var removed []string
	cache := newNotebookTabCache(time.Minute, func(id string) { removed = append(removed, id) })
	cache.Put("old")
	cache.Put("new")
	if len(removed) != 1 || removed[0] != "old" {
		t.Fatalf("replacement did not close stale tab: %#v", removed)
	}
	id, ok := cache.Take()
	if !ok || id != "new" {
		t.Fatalf("replacement not reusable: %q, %v", id, ok)
	}
	cache.Stop()
}
