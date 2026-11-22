// Package pagepool provides origin-agnostic page selection and lease-based
// lifecycle management used by the Playground and Notebook browser adapters.
// The protocol-specific page bootstrap stays with each adapter.
package pagepool

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrClosed = errors.New("browser page pool is closed")

// Status represents a browser page without depending on its origin or the
// calling account's lease type.
type Status struct {
	Busy    bool
	Retired bool
	Freed   time.Time
}

// PickOldestFree matches Playground's long-standing policy: a non-retired,
// unused page freed earliest is reused first.
func PickOldestFree(pages []Status) int {
	choice := -1
	for i, page := range pages {
		if page.Busy || page.Retired {
			continue
		}
		if choice < 0 || page.Freed.Before(pages[choice].Freed) {
			choice = i
		}
	}
	return choice
}

// NeedsSpare retains Playground's speculative-page rule without binding the
// decision to WAA, Notebook, or a particular browser implementation.
func NeedsSpare(pages, busy, max int) bool {
	return pages > 0 && busy >= 2 && busy >= pages && pages <= max
}

// Pool reuses browser pages with request-scoped leases and an optional idle
// timeout. New/close implement the service-specific browser protocol.
type Pool[T comparable] struct {
	mu        sync.Mutex
	slots     []*slot[T]
	opening   int
	max       int
	idle      time.Duration
	newPage   func(context.Context) (T, error)
	closePage func(T) error
	changed   chan struct{}
	closed    bool
}

type slot[T comparable] struct {
	page   T
	busy   bool
	freed  time.Time
	epoch  uint64
	timer  *time.Timer
}

// Lease owns one page until Release or Discard. A lease must never be reused
// after release. Its callbacks are idempotent, including across goroutines.
type Lease[T comparable] struct {
	pool    *Pool[T]
	slot    *slot[T]
	once    sync.Once
	lastErr error
}

func New[T comparable](max int, idle time.Duration, open func(context.Context) (T, error), close func(T) error) *Pool[T] {
	if max < 1 {
		max = 1
	}
	return &Pool[T]{max: max, idle: idle, newPage: open, closePage: close, changed: make(chan struct{})}
}

func (p *Pool[T]) notifyLocked() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *Pool[T]) Acquire(ctx context.Context) (*Lease[T], error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, ErrClosed
		}
		states := make([]Status, len(p.slots))
		for i, s := range p.slots {
			states[i] = Status{Busy: s.busy, Freed: s.freed}
		}
		if idx := PickOldestFree(states); idx >= 0 {
			item := p.slots[idx]
			item.busy = true
			item.epoch++
			if item.timer != nil {
				item.timer.Stop()
				item.timer = nil
			}
			p.mu.Unlock()
			return &Lease[T]{pool: p, slot: item}, nil
		}
		if len(p.slots)+p.opening < p.max {
			p.opening++
			p.mu.Unlock()
			page, err := p.newPage(ctx)
			p.mu.Lock()
			p.opening--
			p.notifyLocked()
			if err != nil {
				p.mu.Unlock()
				return nil, err
			}
			if p.closed {
				p.mu.Unlock()
				_ = p.closePage(page)
				return nil, ErrClosed
			}
			item := &slot[T]{page: page, busy: true}
			p.slots = append(p.slots, item)
			p.mu.Unlock()
			return &Lease[T]{pool: p, slot: item}, nil
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (l *Lease[T]) Page() T { return l.slot.page }

func (p *Pool[T]) forgetLocked(target *slot[T]) {
	for i, item := range p.slots {
		if item == target {
			p.slots = append(p.slots[:i], p.slots[i+1:]...)
			break
		}
	}
}

func (l *Lease[T]) release(discard bool) error {
	l.once.Do(func() {
		p, item := l.pool, l.slot
		p.mu.Lock()
		item.busy = false
		item.epoch++
		if discard || p.closed || p.idle <= 0 {
			if item.timer != nil {
				item.timer.Stop()
				item.timer = nil
			}
			p.forgetLocked(item)
			p.notifyLocked()
			p.mu.Unlock()
			l.lastErr = p.closePage(item.page)
			return
		}
		item.freed = time.Now()
		epoch := item.epoch
		item.timer = time.AfterFunc(p.idle, func() { p.expire(item, epoch) })
		p.notifyLocked()
		p.mu.Unlock()
	})
	return l.lastErr
}

func (l *Lease[T]) Release() error { return l.release(false) }
func (l *Lease[T]) Discard() error { return l.release(true) }

func (p *Pool[T]) expire(target *slot[T], epoch uint64) {
	p.mu.Lock()
	if target.busy || target.epoch != epoch {
		p.mu.Unlock()
		return
	}
	found := false
	for _, item := range p.slots {
		if item == target {
			found = true
			break
		}
	}
	if !found {
		p.mu.Unlock()
		return
	}
	p.forgetLocked(target)
	target.timer = nil
	p.notifyLocked()
	p.mu.Unlock()
	_ = p.closePage(target.page)
}

// Stop invalidates idle pages immediately and marks busy leases for disposal
// on return. This never waits for an active request or holds another worker's
// lifecycle mutex while its idle timer runs.
func (p *Pool[T]) Stop() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	var idle []T
	for _, item := range p.slots {
		item.epoch++
		if item.timer != nil {
			item.timer.Stop()
			item.timer = nil
		}
		if !item.busy {
			idle = append(idle, item.page)
		}
	}
	p.slots = nil
	p.notifyLocked()
	p.mu.Unlock()
	for _, item := range idle {
		_ = p.closePage(item)
	}
}
