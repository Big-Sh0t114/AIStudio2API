package notebook

import (
	"context"
	"testing"
	"time"
)

func TestNotebookReadySourceCacheAvoidsRedundantRPC(t *testing.T) {
	p := New(t.TempDir())
	ids := []any{[]any{[]any{"source-id"}}}
	titles := map[string]string{"source-id": "Document"}
	a := &account{sourceMetadata: map[string]notebookSourceSnapshot{
		"notebook-1": {ids: ids, titles: titles, expires: time.Now().Add(time.Minute)},
	}}
	// The account deliberately has no browser. A cache hit must not try an RPC.
	cachedIDs, cachedTitles, err := p.readySources(context.Background(), a, "notebook-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cachedIDs) != 1 || cachedTitles["source-id"] != "Document" {
		t.Fatalf("cached metadata not returned: ids=%#v titles=%#v", cachedIDs, cachedTitles)
	}
}
func TestPrepareConversationIsolatesUnrelatedRequests(t *testing.T) {
	_, first, err := prepareConversation([]Message{
		{Role: "user", Content: "Question A"},
		{Role: "assistant", Content: "Answer A"},
		{Role: "user", Content: "Follow up"},
	})
	if err != nil || len(first) != 2 {
		t.Fatalf("first request: %#v %v", first, err)
	}
	question, second, err := prepareConversation([]Message{{Role: "user", Content: "Question B"}})
	if err != nil || question != "Question B" || len(second) != 0 {
		t.Fatalf("history leaked across requests: %q %#v %v", question, second, err)
	}
}
