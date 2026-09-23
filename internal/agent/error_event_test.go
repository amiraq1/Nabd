package agent

import (
	"errors"
	"testing"

	"nabd/internal/endpoint"
)

func TestRunErrorEventCarriesTypedCode(t *testing.T) {
	if got := RunErrorEvent(ErrSpendBudget); got.ErrorCode != string(ErrCodeBudget) {
		t.Fatalf("code = %q, want %q", got.ErrorCode, ErrCodeBudget)
	}
	if got := RunErrorEvent(ErrMaxTurns); got.ErrorCode != string(ErrCodeMaxTurns) {
		t.Fatalf("code = %q, want %q", got.ErrorCode, ErrCodeMaxTurns)
	}
}

func TestPersistErrorCarriesJournalPath(t *testing.T) {
	err := NewPersistError(errors.New("disk full"), "/tmp/session.jsonl")
	if ErrorCodeOf(err) != ErrCodePersist || JournalPathOf(err) != "/tmp/session.jsonl" {
		t.Fatalf("classification = %q path=%q", ErrorCodeOf(err), JournalPathOf(err))
	}
}

func TestEndpointRefusedTwoLayerWrapClassified(t *testing.T) {
	// Layer 1: CheckBaseURL wraps ErrEndpointRefused
	layer1 := errors.Join(errors.New("baseURL http://127.0.0.1:8118 uses scheme http"), endpoint.ErrEndpointRefused)
	// Layer 2: proxyFromEnv / transport wraps layer 1
	layer2 := errors.Join(errors.New("Post https://api.groq.com/openai/v1/chat/completions: proxy endpoint refused"), layer1)

	if got := ErrorCodeOf(layer2); got != ErrCodeEndpointRefused {
		t.Fatalf("ErrorCodeOf(layer2) = %q, want %q", got, ErrCodeEndpointRefused)
	}
	if got := RunErrorEvent(layer2).ErrorCode; got != string(ErrCodeEndpointRefused) {
		t.Fatalf("RunErrorEvent(layer2).ErrorCode = %q, want %q", got, ErrCodeEndpointRefused)
	}
}

func TestGenericUnrelatedErrorRemainsUnknown(t *testing.T) {
	generic := errors.New("connection reset by peer")
	if got := ErrorCodeOf(generic); got != ErrCodeUnknown {
		t.Fatalf("ErrorCodeOf(generic) = %q, want %q", got, ErrCodeUnknown)
	}

	wrapped := errors.Join(errors.New("outer wrap"), errors.New("inner failure"))
	if got := ErrorCodeOf(wrapped); got != ErrCodeUnknown {
		t.Fatalf("ErrorCodeOf(wrapped) = %q, want %q", got, ErrCodeUnknown)
	}
}
