package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUnifiedDiffSharedBudgetPreventsConcurrentCeilingAllocations(t *testing.T) {
	budget := newDiffBudget(1)
	if err := budget.acquire(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := unifiedDiffWithBudget(ctx, budget, []byte("a\n"), []byte("b\n"), "x")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline while aggregate budget is reserved", err)
	}
	budget.release(1)
	if _, err := unifiedDiffWithBudget(context.Background(), budget, []byte("a\n"), []byte("b\n"), "x"); err != nil {
		t.Fatalf("budget was not released: %v", err)
	}
}
