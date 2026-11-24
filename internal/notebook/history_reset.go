package notebook

import (
	"context"
	"fmt"
	"strings"
)

const (
	listChatSessionsRPC = "hPTbtc"
	deleteChatTurnsRPC  = "J7Gthc"
	listChatTurnsRPC    = "khqZz"
)

// A document gate is deliberately keyed by the upstream Notebook ID rather
// than a local account alias. Its reference count includes waiting calls.
type notebookDocumentGate struct {
	gate *notebookGate
	refs int
}

func (p *Provider) acquireDocument(ctx context.Context, id string) (func(), error) {
	p.mu.Lock()
	if p.documentGates == nil {
		p.documentGates = make(map[string]*notebookDocumentGate)
	}
	entry := p.documentGates[id]
	if entry == nil {
		entry = &notebookDocumentGate{gate: newNotebookGate(1)}
		p.documentGates[id] = entry
	}
	entry.refs++
	p.mu.Unlock()

	unref := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(p.documentGates, id)
		}
	}
	release, err := entry.gate.acquire(ctx)
	if err != nil {
		unref()
		return nil, err
	}
	return func() {
		release()
		unref()
	}, nil
}

func resetProtocolError(message string) error {
	return &APIError{Status: 502, Code: "notebook_history_reset_failed", Message: message}
}

// The observed ListChatSessions response groups sessions as [[[id]]]. Treat
// genuinely empty responses as empty, but never assume a changed schema is
// empty: generation must stop rather than inherit an unverified history.
func currentConversationID(raw any) (string, error) {
	if raw == nil {
		return "", nil
	}
	groups, ok := raw.([]any)
	if !ok {
		return "", resetProtocolError("Unrecognized Notebook conversation-list response")
	}
	for _, group := range groups {
		if group == nil {
			continue
		}
		rows, ok := group.([]any)
		if !ok {
			return "", resetProtocolError("Unrecognized Notebook conversation group")
		}
		for _, row := range rows {
			cols, ok := row.([]any)
			if !ok || len(cols) == 0 {
				return "", resetProtocolError("Unrecognized Notebook conversation row")
			}
			id, ok := cols[0].(string)
			if !ok || strings.TrimSpace(id) == "" {
				return "", resetProtocolError("Notebook conversation has no valid ID")
			}
			return id, nil
		}
	}
	return "", nil
}

func (p *Provider) getCurrentConversation(ctx context.Context, a *account, notebookID string) (string, error) {
	raw, err := p.rpc(ctx, a, listChatSessionsRPC, []any{[]any{}, nil, notebookID, 1}, "/notebook/"+notebookID)
	if err != nil {
		return "", fmt.Errorf("query Notebook conversation before generation: %w", err)
	}
	return currentConversationID(raw)
}

func emptyConversationTurns(raw any) (bool, error) {
	if raw == nil {
		return true, nil
	}
	rows, ok := raw.([]any)
	if !ok {
		return false, resetProtocolError("Unrecognized Notebook chat-turn response")
	}
	if len(rows) == 0 || rows[0] == nil {
		return true, nil
	}
	turns, ok := rows[0].([]any)
	if !ok {
		return false, resetProtocolError("Unrecognized Notebook chat-turn list")
	}
	return len(turns) == 0, nil
}

// resetConversation mirrors the NotebookLM UI's Delete history operation.
// This is intentionally destructive to the selected Notebook's chat history,
// not its sources or notes. It must run while the document gate is held.
func (p *Provider) resetConversation(ctx context.Context, a *account, notebookID string) error {
	id, err := p.getCurrentConversation(ctx, a, notebookID)
	if err != nil || id == "" {
		return err
	}
	path := "/notebook/" + notebookID
	if _, err := p.rpc(ctx, a, deleteChatTurnsRPC, []any{[]any{}, id, nil, 1}, path); err != nil {
		return fmt.Errorf("delete Notebook chat history: %w", err)
	}

	// Some cohorts retain an empty session after deleting its turns. Others
	// remove it. Check the currently selected session, not merely the old ID.
	current, err := p.getCurrentConversation(ctx, a, notebookID)
	if err != nil || current == "" {
		return err
	}
	raw, err := p.rpc(ctx, a, listChatTurnsRPC, []any{[]any{}, nil, nil, current, 1}, path)
	if err != nil {
		return fmt.Errorf("verify Notebook chat history deletion: %w", err)
	}
	empty, err := emptyConversationTurns(raw)
	if err != nil {
		return err
	}
	if !empty {
		return resetProtocolError("Notebook chat history is still present after deletion; generation was not sent")
	}
	return nil
}
