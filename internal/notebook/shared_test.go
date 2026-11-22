package notebook

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeShared struct{}
type fakePage struct{}

func (fakePage) Evaluate(_ context.Context, _ string) (json.RawMessage, error) {
	return json.RawMessage(`{"email":"test@example.com"}`), nil
}
func (fakePage) Navigate(context.Context, string) error { return nil }
func (fakePage) Close() error                           { return nil }
func (fakeShared) Sources(context.Context) ([]SourceAccount, error) {
	return []SourceAccount{{ID: "playground-1", Label: "Primary", State: "ready"}}, nil
}
func (fakeShared) Open(context.Context, string) (SharedPage, error) { return fakePage{}, nil }
func TestLinkedNotebookPersistence(t *testing.T) {
	p := NewWithShared(t.TempDir(), fakeShared{})
	st, err := p.CreateWithSource(context.Background(), "Linked", "playground-1")
	if err != nil {
		t.Fatal(err)
	}
	if st.SourceAccountID != "playground-1" {
		t.Fatalf("incorrect source: %+v", st)
	}
	p2 := NewWithShared(p.root, fakeShared{})
	loaded := p2.Accounts()
	if len(loaded) != 1 || loaded[0].SourceAccountID != "playground-1" {
		t.Fatalf("invalid persisted linked account: %+v", loaded)
	}
	if _, err := p.CreateWithSource(context.Background(), "Invalid", "unknown"); err == nil {
		t.Fatal("unknown account accepted")
	}
}
func TestSharedBrowserDoesNotStartChrome(t *testing.T) {
	p := NewWithShared(t.TempDir(), fakeShared{})
	st, err := p.CreateWithSource(context.Background(), "Linked", "playground-1")
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.get(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	if err := p.ensureBrowser(context.Background(), a); err != nil {
		a.mu.Unlock()
		t.Fatal(err)
	}
	if a.browser.shared == nil || a.browser.cmd != nil {
		a.mu.Unlock()
		t.Fatal("not using shared browser")
	}
	p.closeSharedPage(a)
	if a.browser != nil {
		a.mu.Unlock()
		t.Fatal("borrowed page not released")
	}
	a.mu.Unlock()
}
