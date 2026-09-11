package agent

import (
	"errors"
	"testing"
)

func TestSpendBudgetAccumulatesAcrossAttempts(t *testing.T) {
	b := &SpendBudget{Limit: 100}
	if err := b.Charge(30, 20, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Charge(40, 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Charge(1, 0, 1); !errors.Is(err, ErrSpendBudget) {
		t.Fatalf("Charge error = %v, want ErrSpendBudget", err)
	}
	if b.Used != 100 {
		t.Fatalf("Used = %d, want 100", b.Used)
	}
}
