package ui

import (
	"testing"

	"nabd/internal/agent"
)

// TestUnifiedCallbacksContract pins the contract that the unification is
// meant to protect: Chat and Feed now read the same slash-command hooks from
// a single struct, so the /rewind hook must be the same arity on both. Any
// regression that re-introduces two callback shapes fails this test.
func TestUnifiedCallbacksContract(t *testing.T) {
	cb := &SessionCallbacks{
		OnUndo:    func(n int) string { return "" },
		OnCompact: func() string { return "" },
		OnRewind:  func(n int) (string, string) { return "", "rewound" },
		OnCtx:     func() string { return "" },
		OnEdits:   func() string { return "" },
	}

	f := NewFeed()
	f.SetCallbacks(cb)
	if _, status := f.callbacks.OnRewind(1); status != "rewound" {
		t.Fatalf("Feed /rewind contract broken: %q", status)
	}

	c := NewChat(runnerStub{}, make(chan agent.Event, 1))
	c.SetCallbacks(cb)
	if _, status := c.callbacks.OnRewind(1); status != "rewound" {
		t.Fatalf("Chat /rewind contract broken: %q", status)
	}
}

// TestChatRewindSetsInput verifies the Chat-specific side-effect of /rewind:
// the restored text is pushed into the composer, matching the Feed path.
func TestChatRewindSetsInput(t *testing.T) {
	c := NewChat(runnerStub{}, make(chan agent.Event, 1))
	c.SetCallbacks(&SessionCallbacks{
		OnRewind: func(n int) (string, string) { return "restored draft", "rewound" },
	})
	if got, _ := c.command("/rewind 2"); got != "rewound" {
		t.Fatalf("Chat /rewind status = %q, want rewound", got)
	}
	if c.input != "restored draft" {
		t.Fatalf("Chat /rewind did not restore input: %q", c.input)
	}
}

// TestChatRewindEmptyStatusFallsBack ensures an empty status still yields the
// rewound fallback string rather than leaving the user on a blank line.
func TestChatRewindEmptyStatusFallsBack(t *testing.T) {
	c := NewChat(runnerStub{}, make(chan agent.Event, 1))
	c.SetCallbacks(&SessionCallbacks{
		OnRewind: func(n int) (string, string) { return "", "" },
	})
	if got, _ := c.command("/rewind 1"); got != "rewound" {
		t.Fatalf("Chat /rewind empty-status fallback = %q, want rewound", got)
	}
}
