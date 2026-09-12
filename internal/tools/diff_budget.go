package tools

import (
	"context"
	"fmt"
	"sync"
)

type diffBudget struct {
	mu      sync.Mutex
	max     int
	used    int
	changed chan struct{}
}

func newDiffBudget(max int) *diffBudget {
	return &diffBudget{max: max, changed: make(chan struct{})}
}

func (b *diffBudget) acquire(ctx context.Context, cells int) error {
	if b == nil || cells <= 0 {
		return nil
	}
	if cells > b.max {
		return fmt.Errorf("diff work budget exceeded: %d cells exceeds aggregate limit %d", cells, b.max)
	}
	for {
		b.mu.Lock()
		if cells <= b.max-b.used {
			b.used += cells
			b.mu.Unlock()
			return nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (b *diffBudget) release(cells int) {
	if b == nil || cells <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= cells
	if b.used < 0 {
		b.used = 0
	}
	close(b.changed)
	b.changed = make(chan struct{})
	b.mu.Unlock()
}
