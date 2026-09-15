package presentation

import (
	"testing"

	"nabd/internal/agent"
)

func TestLegacyRunErrorProjectsAsUnknown(t *testing.T) {
	items, err := NewProjector().Build([]agent.Event{{Seq: 1, Type: agent.RunError, Err: "old failure"}})
	if err != nil || len(items) != 1 || items[0].Error == nil {
		t.Fatalf("projection failed: items=%#v err=%v", items, err)
	}
	card := items[0].Error
	if card.Code != agent.ErrUnknown || card.Retryable || card.RetryScope != RetryNone {
		t.Fatalf("legacy card = %#v", card)
	}
}

func TestProviderTemporaryRetryScope(t *testing.T) {
	card := NewErrorCard(agent.ErrProviderTemporary, "temporary", "")
	if !card.Retryable || card.RetryScope != RetryProviderTurn {
		t.Fatalf("card = %#v", card)
	}
}

func TestProviderAuthDoesNotBlindRetry(t *testing.T) {
	card := NewErrorCard(agent.ErrProviderAuth, "unauthorized", "")
	if card.Retryable || card.RetryScope != RetryNone {
		t.Fatalf("card = %#v", card)
	}
}

// TestErrorCardCarriesWaitSeconds keeps the router's retry-after as a field on
// the card. The message text that also contains the number is truncated to the
// terminal width above 40 columns and hidden below it, so a card without the
// field loses the only actionable number on a phone terminal.
func TestErrorCardCarriesWaitSeconds(t *testing.T) {
	items, err := NewProjector().Build([]agent.Event{
		{Seq: 1, Type: agent.RunError, Err: "all 2 route(s) exhausted", ErrorCode: "provider_temporary", RetryAfter: 20},
	})
	if err != nil || len(items) != 1 || items[0].Error == nil {
		t.Fatalf("projection failed: items=%#v err=%v", items, err)
	}
	if got := items[0].Error.WaitSeconds; got != 20 {
		t.Fatalf("WaitSeconds = %v, want 20", got)
	}

	// A run that reported no retry-after states no wait: 0 means "none", not
	// "wait zero seconds".
	items, err = NewProjector().Build([]agent.Event{
		{Seq: 1, Type: agent.RunError, Err: "auth failed", ErrorCode: "provider_auth"},
	})
	if err != nil || len(items) != 1 || items[0].Error == nil {
		t.Fatalf("projection failed: items=%#v err=%v", items, err)
	}
	if got := items[0].Error.WaitSeconds; got != 0 {
		t.Fatalf("WaitSeconds = %v, want 0", got)
	}
}
