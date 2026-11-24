package notebook

import (
	"context"
	"testing"
	"time"
)

func TestDocumentGateSameNotebookSerializesAcrossAliases(t *testing.T) {
	p := New(t.TempDir())
	release, err := p.acquireDocument(context.Background(), "notebook-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if done, err := p.acquireDocument(ctx, "notebook-1"); err == nil {
		done()
		t.Fatal("same Notebook acquired concurrently")
	}
	other, err := p.acquireDocument(context.Background(), "notebook-2")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	again, err := p.acquireDocument(context.Background(), "notebook-1")
	if err != nil {
		t.Fatal(err)
	}
	again()
	if len(p.documentGates) != 0 {
		t.Fatal("unused document gates were not removed")
	}
}

func TestCurrentConversationIDStrictDecoding(t *testing.T) {
	for _, raw := range []any{nil, []any{}, []any{[]any{}}, []any{nil}} {
		id, err := currentConversationID(raw)
		if id != "" || err != nil {
			t.Fatalf("empty session list: %q, %v", id, err)
		}
	}
	id, err := currentConversationID([]any{[]any{[]any{"session-1"}}})
	if id != "session-1" || err != nil {
		t.Fatalf("session list: %q, %v", id, err)
	}
	for _, raw := range []any{"unexpected", []any{"unexpected"}, []any{[]any{[]any{nil}}}} {
		if _, err := currentConversationID(raw); err == nil {
			t.Fatal("malformed session list treated as empty")
		}
	}
}

func TestEmptyConversationTurnsStrictDecoding(t *testing.T) {
	for _, raw := range []any{nil, []any{}, []any{[]any{}}, []any{nil}} {
		empty, err := emptyConversationTurns(raw)
		if !empty || err != nil {
			t.Fatalf("expected empty turns: %v, %v", empty, err)
		}
	}
	empty, err := emptyConversationTurns([]any{[]any{[]any{"old turn"}}})
	if empty || err != nil {
		t.Fatalf("old turns not detected: %v, %v", empty, err)
	}
	if _, err := emptyConversationTurns([]any{"unexpected"}); err == nil {
		t.Fatal("invalid turn list treated as empty")
	}
}
