package agent

import (
	"errors"
	"testing"
)

func TestRunErrorEventCarriesTypedCode(t *testing.T) {
	if got := RunErrorEvent(ErrSpendBudget); got.ErrorCode != string(ErrBudget) {
		t.Fatalf("code = %q, want %q", got.ErrorCode, ErrBudget)
	}
	if got := RunErrorEvent(ErrMaxTurns); got.ErrorCode != string(ErrMaxTurnsCode) {
		t.Fatalf("code = %q, want %q", got.ErrorCode, ErrMaxTurnsCode)
	}
}

func TestPersistErrorCarriesJournalPath(t *testing.T) {
	err := NewPersistError(errors.New("disk full"), "/tmp/session.jsonl")
	if ErrorCodeOf(err) != ErrPersist || JournalPathOf(err) != "/tmp/session.jsonl" {
		t.Fatalf("classification = %q path=%q", ErrorCodeOf(err), JournalPathOf(err))
	}
}
