package aistudio

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNotebookBrowserOperationCountsAsPlaygroundActivity(t *testing.T) {
	pool := NewAccountPool([]*Account{{ID: "shared-google-account"}}, 2)
	busy, lastUsed := pool.Activity("shared-google-account")
	if busy || !lastUsed.IsZero() {
		t.Fatalf("new account unexpectedly active: busy=%v used=%v", busy, lastUsed)
	}
	done, err := pool.BeginBrowserOperation("shared-google-account")
	if err != nil {
		t.Fatal(err)
	}
	busy, lastUsed = pool.Activity("shared-google-account")
	if !busy || time.Since(lastUsed) > time.Minute {
		t.Fatalf("Notebook operation not counted: busy=%v used=%v", busy, lastUsed)
	}
	if _, err := pool.Remove("shared-google-account", nil); !errors.Is(err, ErrAccountLeased) {
		t.Fatalf("removed account during Notebook activity: %v", err)
	}
	// Exclusive Playground operations and worker recycling must not take over
	// the same account while a Notebook page lease remains active.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := pool.AcquireAccount(ctx, "shared-google-account"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquired exclusive account while Notebook active: %v", err)
	}
	done()
	done() // duplicate Close/release must not underflow activity count
	busy, after := pool.Activity("shared-google-account")
	if busy || after.Before(lastUsed) {
		t.Fatalf("Notebook activity not released: busy=%v before=%v after=%v", busy, lastUsed, after)
	}
	if _, err := pool.BeginBrowserOperation("missing"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account was accepted: %v", err)
	}
}
