package ui

import (
	"context"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSlashCommandParityBetweenFeedAndChat verifies that all registered
// commands are handled by both Feed and Legacy Chat, with identical unknown-command
// handling.
func TestSlashCommandParityBetweenFeedAndChat(t *testing.T) {
	cmds := AllSlashCommands()

	// Track Feed callbacks
	feedCalls := make(map[string]bool)
	f := NewFeed()
	f.SetCallbacks(&SessionCallbacks{
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
		OnProvider: func() string {
			feedCalls["/provider"] = true
			return "providers"
		},
		OnModels: func(ctx context.Context, providerID string) ([]string, string, error) {
			feedCalls["/models"] = true
			return []string{"model-1"}, "note", nil
		},
		OnConnect: func(providerID, key string) (string, error) {
			feedCalls["/connect"] = true
			return "connected", nil
		},
	})

	// Track Chat callbacks
	chatCalls := make(map[string]bool)
	c := NewChat(runnerStub{}, make(chan agent.Event, 1))
	c.SetCallbacks(&SessionCallbacks{
		OnUndo: func(n int) string {
			chatCalls["/undo"] = true
			return ""
		},
		OnRewind: func(n int) (string, string) {
			chatCalls["/rewind"] = true
			return "", "rewound"
		},
		OnCtx: func() string {
			chatCalls["/ctx"] = true
			return "ctx"
		},
		OnCompact: func() string {
			chatCalls["/compact"] = true
			return "compact"
		},
		OnEdits: func() string {
			chatCalls["/edits"] = true
			return "edits"
		},
		OnProvider: func() string {
			chatCalls["/provider"] = true
			return "providers"
		},
		OnModels: func(ctx context.Context, providerID string) ([]string, string, error) {
			chatCalls["/models"] = true
			return []string{"model-1"}, "note", nil
		},
		OnConnect: func(providerID, key string) (string, error) {
			chatCalls["/connect"] = true
			return "connected", nil
		},
	})

	for _, cmd := range cmds {
		input := cmd.Name
		if cmd.Name == "/connect" || cmd.Name == "/models" {
			input += " mock-provider"
		}

		// Test Feed
		_, cmdFn := f.runCommand(input)
		if cmdFn != nil {
			cmdFn()
		}
		if cmd.Name == "/connect" {
			f.secretKey = "mock-key"
			f.Update(tea.KeyMsg{Type: tea.KeyEnter})
		}
		if cmd.Name != "/help" && !feedCalls[cmd.Name] {
			t.Errorf("Feed failed to dispatch command: %s", cmd.Name)
		}

		// Test Chat
		res := c.command(input)
		if strings.HasPrefix(res, "unknown command") {
			t.Errorf("Chat reported unknown for command: %s", cmd.Name)
		}
		if cmd.Name == "/connect" {
			c.secretKey = "mock-key"
			c.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
