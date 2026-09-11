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
