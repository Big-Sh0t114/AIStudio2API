package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

type sourceFreeSessions struct {
	mu             sync.Mutex
	sourceRows     []any
	rejectChat     bool
	chatHTTPStatus int
	chatCalls      int
	sourceCalls    int
	sourceArg      any
}

func (s *sourceFreeSessions) Sources(context.Context) ([]SourceAccount, error) {
	return []SourceAccount{{ID: "playground-1", Label: "Pro account", State: "ready"}}, nil
}
func (s *sourceFreeSessions) Open(context.Context, string) (SharedPage, error) {
	return sourceFreePage{s}, nil
}

type sourceFreePage struct{ owner *sourceFreeSessions }

func (p sourceFreePage) Navigate(context.Context, string) error { return nil }
func (p sourceFreePage) Close() error                           { return nil }

func (p sourceFreePage) Evaluate(_ context.Context, expression string) (json.RawMessage, error) {
	if !strings.HasPrefix(expression, requestJS+"(") || !strings.HasSuffix(expression, ")") {
		return nil, errors.New("not a Notebook RPC evaluation")
	}
	var req struct {
		RPCID  string `json:"rpcid"`
		Params []any  `json:"params"`
	}
	arg := strings.TrimSuffix(strings.TrimPrefix(expression, requestJS+"("), ")")
	if err := json.Unmarshal([]byte(arg), &req); err != nil {
		return nil, err
	}
	var frame []any
	p.owner.mu.Lock()
	if req.RPCID == "rLM1Ne" {
		p.owner.sourceCalls++
		payload, _ := json.Marshal([]any{[]any{"nb-empty", p.owner.sourceRows}})
		frame = []any{"wrb.fr", "rLM1Ne", string(payload), nil, nil, nil}
	} else if req.RPCID == "" {
		p.owner.chatCalls++
		if len(req.Params) < 2 {
			p.owner.mu.Unlock()
			return nil, errors.New("chat params too short")
		}
		p.owner.sourceArg = req.Params[0]
		if p.owner.chatHTTPStatus != 0 {
			status := p.owner.chatHTTPStatus
			p.owner.mu.Unlock()
			result, _ := json.Marshal(browserResponse{Status: status, Text: "upstream rejected"})
			return result, nil
		}
		if p.owner.rejectChat {
			frame = []any{"wrb.fr", "", nil, nil, nil, []any{3, "missing sources"}}
		} else {
			chunk, _ := json.Marshal([]any{[]any{"answer from Notebook"}, nil, nil, nil, true})
			frame = []any{"wrb.fr", "", string(chunk), nil, nil, nil}
		}
	} else {
		p.owner.mu.Unlock()
		return nil, errors.New("unexpected Notebook RPC ID: " + req.RPCID)
	}
	p.owner.mu.Unlock()
	wire, _ := json.Marshal([]any{frame})
	value, _ := json.Marshal(browserResponse{Status: 200, Text: ")]}'\n" + string(wire)})
	return value, nil
}

func newSourceFreeProvider(t *testing.T, s *sourceFreeSessions) (*Provider, AccountStatus) {
	t.Helper()
	p := NewWithShared(t.TempDir(), s)
	account, err := p.CreateWithSource(context.Background(), "Notebook Pro", "playground-1")
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.get(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.status.State = "ready"
	a.mu.Unlock()
	return p, account
}

func TestEmptyNotebookChatSendsExplicitEmptySourceArray(t *testing.T) {
	s := &sourceFreeSessions{sourceRows: []any{}}
	p, a := newSourceFreeProvider(t, s)
	model := "notebook:" + a.ID + ":nb-empty"
	for i := 0; i < 2; i++ {
		reply, err := p.Chat(context.Background(), model, []Message{{Role: "user", Content: "Hello"}})
		if err != nil {
			t.Fatal(err)
		}
		if reply.Text != "answer from Notebook" || len(reply.Citations) != 0 {
			t.Fatalf("unexpected source-free response: %+v", reply)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.chatCalls != 2 || s.sourceCalls != 1 {
		t.Fatalf("source-free catalog not reused: chat=%d source=%d", s.chatCalls, s.sourceCalls)
	}
	ids, ok := s.sourceArg.([]any)
	if !ok || ids == nil || len(ids) != 0 {
		t.Fatalf("source-free request must contain JSON [] (not null): %#v", s.sourceArg)
	}
	rows := p.Accounts()
	if len(rows) != 1 || rows[0].RequestCount != 2 || rows[0].SuccessCount != 2 {
		t.Fatalf("usage counters lost: %+v", rows)
	}
}

func TestNonemptyNotebookUnreadySourcesRemainBlocked(t *testing.T) {
	s := &sourceFreeSessions{sourceRows: []any{
		[]any{[]any{"pending-source"}, "Not processed", nil, []any{nil, float64(1)}},
	}}
	p, a := newSourceFreeProvider(t, s)
	_, err := p.Chat(context.Background(), "notebook:"+a.ID+":nb-empty", []Message{{Role: "user", Content: "Hello"}})
	var api *APIError
	if !errors.As(err, &api) || api.Code != "sources_not_ready" || api.Status != 400 {
		t.Fatalf("unready content was treated as empty Notebook: %v", err)
	}
	if s.chatCalls != 0 {
		t.Fatalf("sent invalid source-free chat despite pending source: %d", s.chatCalls)
	}
}

func TestEmptyNotebookUpstreamRejectionReportsAgentModeGap(t *testing.T) {
	s := &sourceFreeSessions{sourceRows: []any{}, rejectChat: true}
	p, a := newSourceFreeProvider(t, s)
	_, err := p.Chat(context.Background(), "notebook:"+a.ID+":nb-empty", []Message{{Role: "user", Content: "Hello"}})
	var api *APIError
	if !errors.As(err, &api) || api.Code != "source_free_chat_unavailable" || api.Status != 502 {
		t.Fatalf("source-free upstream denial not reported accurately: %v", err)
	}
	if s.chatCalls != 1 {
		t.Fatalf("upstream was not tried exactly once: %d", s.chatCalls)
	}
}

func TestGroundedNotebookStillForwardsReadySourceIDs(t *testing.T) {
	s := &sourceFreeSessions{sourceRows: []any{
		[]any{[]any{"valid-id"}, "Document", nil, []any{nil, float64(2)}},
	}}
	p, a := newSourceFreeProvider(t, s)
	reply, err := p.Chat(context.Background(), "notebook:"+a.ID+":nb-empty", []Message{{Role: "user", Content: "Question"}})
	if err != nil || reply.Text != "answer from Notebook" {
		t.Fatalf("grounded chat regression: %+v %v", reply, err)
	}
	ids, ok := s.sourceArg.([]any)
	if !ok || len(ids) != 1 || str(at(ids[0], 0, 0)) != "valid-id" {
		t.Fatalf("grounded source ID changed: %#v", s.sourceArg)
	}
}

func TestEmptyNotebookHTTP400ReportsAgentProtocolGap(t *testing.T) {
	s := &sourceFreeSessions{sourceRows: []any{}, chatHTTPStatus: 400}
	p, a := newSourceFreeProvider(t, s)
	_, err := p.Chat(context.Background(), "notebook:"+a.ID+":nb-empty", []Message{{Role: "user", Content: "Hello"}})
	var api *APIError
	if !errors.As(err, &api) || api.Code != "source_free_chat_unavailable" || api.Status != 502 {
		t.Fatalf("HTTP 400 must be classified as unsupported no-source protocol: %v", err)
	}
	if s.chatCalls != 1 {
		t.Fatalf("unexpected source-free request count: %d", s.chatCalls)
	}
}
