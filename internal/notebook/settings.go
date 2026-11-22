package notebook

import "fmt"

// Concurrency is a global limit applied separately to each linked Playground
// Google source. Standalone Chrome Notebook accounts remain single-flight.
func (p *Provider) Concurrency() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.concurrentLimit < MinConcurrency || p.concurrentLimit > MaxConcurrency {
		return DefaultConcurrency
	}
	return p.concurrentLimit
}

// SetConcurrency changes the active request limit immediately without
// resetting in-flight sessions, model IDs, quota or cooldowns. It persists to
// the existing Notebook accounts.json and configures borrowed Camoufox workers
// when their next Notebook page is acquired.
func (p *Provider) SetConcurrency(limit int) error {
	p.settingsMu.Lock()
	defer p.settingsMu.Unlock()
	if limit < MinConcurrency || limit > MaxConcurrency {
		return &APIError{
			Status: 400, Code: "invalid_concurrency",
			Message: fmt.Sprintf("Notebook concurrency must be between %d and %d", MinConcurrency, MaxConcurrency),
		}
	}
	p.mu.Lock()
	old := p.concurrentLimit
	p.concurrentLimit = limit
	for _, gate := range p.gates {
		gate.setLimit(limit)
	}
	p.mu.Unlock()
	if err := p.save(); err != nil {
		p.mu.Lock()
		// Keep the previous in-memory value if persistence failed.
		if p.concurrentLimit == limit {
			p.concurrentLimit = old
			for _, gate := range p.gates {
				gate.setLimit(old)
			}
		}
		p.mu.Unlock()
		return fmt.Errorf("save Notebook concurrency: %w", err)
	}
	if sessions, ok := p.shared.(interface{ SetNotebookConcurrency(int) }); ok {
		sessions.SetNotebookConcurrency(limit)
	}
	return nil
}
