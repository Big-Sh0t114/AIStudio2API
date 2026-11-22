package camoufoxnative

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/pagepool"
)

// A NotebookPage lease must return to the same origin-agnostic pool used by
// the Worker. No browser is required for this lifecycle regression test.
func TestNotebookPageCloseReusesSharedLease(t *testing.T) {
	var opened atomic.Int32
	pool := pagepool.New[string](1, time.Minute,
		func(context.Context) (string, error) {
			opened.Add(1)
			return "notebook-context-1", nil
		},
		func(string) error { return nil },
	)
	defer pool.Stop()
	lease, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	page := &NotebookPage{lease: lease, contextID: lease.Page()}
	if err := page.Close(); err != nil {
		t.Fatal(err)
	}
	if err := page.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if next.Page() != "notebook-context-1" || opened.Load() != 1 {
		t.Fatalf("Notebook did not reuse page lease: %q, opens=%d", next.Page(), opened.Load())
	}
	if err := next.Discard(); err != nil {
		t.Fatal(err)
	}
}
