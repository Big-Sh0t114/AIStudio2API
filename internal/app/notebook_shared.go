package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/notebook"
)

// notebookSharedSessions uses a dedicated background tab of the existing
// Playground Camoufox worker. No separate Chrome browser is launched.
type notebookSharedSessions struct{ manager *runtimeManager }
type notebookSharedPage struct {
	notebook.SharedPage
	once    sync.Once
	release func()
}

func (p *notebookSharedPage) Close() error {
	var err error
	p.once.Do(func() {
		err = p.SharedPage.Close()
		if p.release != nil {
			p.release()
		}
	})
	return err
}
func (s *notebookSharedSessions) Sources(ctx context.Context) ([]notebook.SourceAccount, error) {
	accounts, err := s.manager.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]notebook.SourceAccount, 0, len(accounts))
	for _, a := range accounts {
		if !a.Enabled {
			continue
		}
		out = append(out, notebook.SourceAccount{ID: a.ID, Label: a.Label, State: a.State})
	}
	return out, nil
}
func (s *notebookSharedSessions) Open(ctx context.Context, accountID string) (notebook.SharedPage, error) {
	generation, done := s.manager.generation()
	keep := false
	defer func() {
		if !keep {
			done()
		}
	}()
	tracked, ok := generation.service.(*trackedService)
	if !ok || !tracked.Running() {
		return nil, errors.New("Start Playground service before using a shared Notebook browser")
	}
	workers := tracked.workers
	workers.mu.RLock()
	acc := workers.accounts[accountID]
	workers.mu.RUnlock()
	if acc == nil {
		return nil, fmt.Errorf("Playground account %q not available", accountID)
	}
	if workers.residentWorker(accountID) == nil {
		if _, err := workers.Worker(ctx, accountID, ""); err != nil {
			return nil, fmt.Errorf("start Playground worker for shared Notebook: %w", err)
		}
	}
	// Block resets and idle eviction while the borrowed tab is active.
	// AI Studio requests may still use the existing worker concurrently.
	acc.startupMu.Lock()
	acc.mu.Lock()
	runtime := acc.worker
	acc.mu.Unlock()
	if runtime == nil {
		acc.startupMu.Unlock()
		return nil, errors.New("Playground worker stopped during Notebook setup")
	}
	phase := runtime.State().Phase
	if phase != aistudio.WorkerReady && phase != aistudio.WorkerBusy {
		acc.startupMu.Unlock()
		return nil, errors.New("Playground browser worker is not ready")
	}
	page, err := runtime.OpenNotebookPage(ctx)
	if err != nil {
		acc.startupMu.Unlock()
		return nil, err
	}
	keep = true
	return &notebookSharedPage{SharedPage: page, release: func() { acc.startupMu.Unlock(); done() }}, nil
}
