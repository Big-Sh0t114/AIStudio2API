package notebook

import (
	"context"
	"fmt"
	"strings"
)

// preferredLinkedAccount selects an existing record deterministically. Existing
// records may reference Notebook model IDs already used by external clients:
// none are merged, deleted, renumbered or reset by binding the same source.
func preferredLinkedAccount(candidate, current AccountStatus) bool {
	if candidate.SuccessCount != current.SuccessCount {
		return candidate.SuccessCount > current.SuccessCount
	}
	if candidate.RequestCount != current.RequestCount {
		return candidate.RequestCount > current.RequestCount
	}
	// Prefer an account already authenticated over a failed duplicate.
	ready := func(s AccountStatus) bool { return s.State == "ready" || s.State == "cooldown" }
	if ready(candidate) != ready(current) {
		return ready(candidate)
	}
	// IDs are persisted: the tie-breaker must not depend on map iteration.
	return candidate.ID < current.ID
}

// linkedSourceLocked requires p.mu to be held (read or write). The account
// mutexes protect snapshots while a refresh or Notebook API request runs.
func (p *Provider) linkedSourceLocked(sourceID string) (AccountStatus, bool) {
	var selected AccountStatus
	found := false
	for _, item := range p.accounts {
		item.mu.Lock()
		st := item.status
		item.mu.Unlock()
		if st.SourceAccountID != sourceID {
			continue
		}
		if !found || preferredLinkedAccount(st, selected) {
			selected = st
			found = true
		}
	}
	if found {
		selected.Notebooks = append([]Notebook{}, selected.Notebooks...)
	}
	return selected, found
}

// CreateWithSourceResult is idempotent for linked Playground accounts.
// created=false means an existing Notebook record was reused and its notebook
// model IDs, request counters, cooldown and login status were preserved.
// Standalone Chrome accounts have no SourceAccountID and remain independent.
//
// The first lookup allows a re-login even if the Playground source listing is
// temporarily unavailable. A second lookup under the write lock makes
// concurrent POST /api/notebook/accounts calls idempotent as well.
func (p *Provider) CreateWithSourceResult(ctx context.Context, label, sourceID string) (AccountStatus, bool, error) {
	label = strings.TrimSpace(label)
	sourceID = strings.TrimSpace(sourceID)
	if len(label) > 120 {
		return AccountStatus{}, false, &APIError{Status: 400, Code: "invalid_request", Message: "Label too long"}
	}
	if err := ctx.Err(); err != nil {
		return AccountStatus{}, false, err
	}
	if sourceID == "" {
		st, err := p.Create(label)
		return st, err == nil, err
	}

	p.mu.RLock()
	existing, found := p.linkedSourceLocked(sourceID)
	p.mu.RUnlock()
	if found {
		return existing, false, nil
	}

	if p.shared == nil {
		return AccountStatus{}, false, &APIError{
			Status: 503, Code: "shared_browser_unavailable", Message: "Shared Playground browser unavailable",
		}
	}
	sources, err := p.shared.Sources(ctx)
	if err != nil {
		return AccountStatus{}, false, err
	}
	valid := false
	for _, source := range sources {
		if source.ID == sourceID {
			valid = true
			break
		}
	}
	if !valid {
		return AccountStatus{}, false, &APIError{
			Status: 400, Code: "unknown_source_account",
			Message: fmt.Sprintf("Playground account %q is not enabled", sourceID),
		}
	}
	if label == "" {
		label = "Shared Playground Notebook"
	}

	p.mu.Lock()
	// The same source may have been registered by another goroutine while
	// Sources() was resolving. This check prevents simultaneous duplicates.
	existing, found = p.linkedSourceLocked(sourceID)
	if found {
		p.mu.Unlock()
		return existing, false, nil
	}
	st := AccountStatus{
		ID: newID(), Label: label, SourceAccountID: sourceID,
		State: "auth_required", Notebooks: []Notebook{},
	}
	p.accounts[st.ID] = &account{status: st}
	p.mu.Unlock()

	if err := p.save(); err != nil {
		return st, true, fmt.Errorf("save linked notebook: %w", err)
	}
	return st, true, nil
}
