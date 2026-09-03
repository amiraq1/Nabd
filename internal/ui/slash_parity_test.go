package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"
)

// TestSlashCommandParityBetweenFeedAndChat verifies that all registered
// commands are handled by both Feed and Legacy Chat, with identical unknown-command
// handling.
func TestSlashCommandParityBetweenFeedAndChat(t *testing.T) {
	cmds := AllSlashCommands()

	// Track Feed callbacks
	feedCalls := make(map[string]bool)
	f := NewFeed()
	f.SetCallbacks(&FeedCallbacks{
		OnUndo: func(n int) string {
			feedCalls["/undo"] = true
			return ""
		},
		OnRewind: func(n int) (string, string) {
			feedCalls["/rewind"] = true
			return "", "rewound"
		},
		OnCtx: func() string {
			feedCalls["/ctx"] = true
			return "ctx"
		},
		OnCompact: func() string {
			feedCalls["/compact"] = true
			return "compact"
		},
		OnEdits: func() string {
			feedCalls["/edits"] = true
			return "edits"
		},
	})

	// Track Chat callbacks
	chatCalls := make(map[string]bool)
	c := NewChat(runnerStub{}, make(chan agent.Event, 1))
	c.OnUndo = func(n int) string {
		chatCalls["/undo"] = true
		return ""
	}
	c.OnRewind = func(n int) string {
		chatCalls["/rewind"] = true
		return "rewound"
	}
	c.OnCtx = func() string {
		chatCalls["/ctx"] = true
		return "ctx"
	}
	c.OnCompact = func() string {
		chatCalls["/compact"] = true
		return "compact"
	}
	c.OnEdits = func() string {
		chatCalls["/edits"] = true
		return "edits"
	}

	for _, cmd := range cmds {
		// Test Feed
		_, _ = f.runCommand(cmd.Name)
		if cmd.Name != "/help" && !feedCalls[cmd.Name] {
			t.Errorf("Feed failed to dispatch command: %s", cmd.Name)
		}

		// Test Chat
		res := c.command(cmd.Name)
		if strings.HasPrefix(res, "unknown command") {
			t.Errorf("Chat reported unknown for command: %s", cmd.Name)
		}
		if cmd.Name != "/help" && !chatCalls[cmd.Name] {
			t.Errorf("Chat failed to dispatch command: %s", cmd.Name)
		}
	}

	// Verify unknown command behavior is consistent
	unknownInput := "/nonexistent_cmd"
	f.status = ""
	_, _ = f.runCommand(unknownInput)
	if f.status != "unknown command: "+unknownInput {
		t.Errorf("Feed unknown command status = %q, want %q", f.status, "unknown command: "+unknownInput)
	}

	chatRes := c.command(unknownInput)
	if chatRes != "unknown command: "+unknownInput {
		t.Errorf("Chat unknown command status = %q, want %q", chatRes, "unknown command: "+unknownInput)
	}
}
