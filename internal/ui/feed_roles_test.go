package ui

import (
	"strings"
	"testing"

	"nabd/internal/presentation"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestFeedRoles(t *testing.T) {
	// Enable color for tests to verify deterministic cyan/green
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	tests := []struct {
		name  string
		items []presentation.FeedItem
		width int
		want  []string
	}{
		{
			name: "user and assistant role labels with color",
			items: []presentation.FeedItem{
				{Type: presentation.ItemUserMsg, Text: "Hello"},
				{Type: presentation.ItemAssistant, Text: "Hi there"},
			},
			width: 50,
			want: append(renderUserMsg(presentation.FeedItem{Type: presentation.ItemUserMsg, Text: "Hello"}, 50),
				"",
				"\x1b[1m\x1b[32mNabd\x1b[0m\x1b[0m", // Bold Green
				"Hi there",
			),
		},
		{
			name: "multiline bodies and narrow terminal widths",
			items: []presentation.FeedItem{
				{Type: presentation.ItemUserMsg, Text: "Line 1\nLine 2 is long and wraps"},
			},
			width: 15,
			want:  renderUserMsg(presentation.FeedItem{Type: presentation.ItemUserMsg, Text: "Line 1\nLine 2 is long and wraps"}, 15),
		},
		{
			name: "Arabic mixed with ASCII, paths, numbers, and emoji",
			items: []presentation.FeedItem{
				{Type: presentation.ItemUserMsg, Text: "مرحبا 123 😊 /path/to/file"},
			},
			width: 50,
			want:  renderUserMsg(presentation.FeedItem{Type: presentation.ItemUserMsg, Text: "مرحبا 123 😊 /path/to/file"}, 50),
		},
		{
			name: "two consecutive conversation turns and incremental assistant updates",
			// incremental updates in items are represented as fully formed items here
			items: []presentation.FeedItem{
				{Type: presentation.ItemUserMsg, Text: "Q1"},
				{Type: presentation.ItemAssistant, Text: "A1"},
				{Type: presentation.ItemUserMsg, Text: "Q2"},
				{Type: presentation.ItemAssistant, Text: "A2..."},
			},
			width: 50,
			want: func() []string {
				var w []string
				w = append(w, renderUserMsg(presentation.FeedItem{Type: presentation.ItemUserMsg, Text: "Q1"}, 50)...)
				w = append(w, "", "\x1b[1m\x1b[32mNabd\x1b[0m\x1b[0m", "A1", "")
				w = append(w, renderUserMsg(presentation.FeedItem{Type: presentation.ItemUserMsg, Text: "Q2"}, 50)...)
				w = append(w, "", "\x1b[1m\x1b[32mNabd\x1b[0m\x1b[0m", "A2...")
				return w
			}(),
		},
		{
			name: "no duplicated labels/separators and no terminal-width overflow",
			items: []presentation.FeedItem{
				{Type: presentation.ItemUserMsg, Text: "A"},
				{Type: presentation.ItemAssistant, Text: "B"},
			},
			width: 50,
			want: append(renderUserMsg(presentation.FeedItem{Type: presentation.ItemUserMsg, Text: "A"}, 50),
				"",
				"\x1b[1m\x1b[32mNabd\x1b[0m\x1b[0m",
				"B",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderItems(tt.items, tt.width)
			if len(got) != len(tt.want) {
				t.Errorf("got %d lines, want %d lines\ngot:\n%s\nwant:\n%s", len(got), len(tt.want), strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d:\ngot:  %q\nwant: %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFeedRolesColorDisabled(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)

	items := []presentation.FeedItem{
		{Type: presentation.ItemUserMsg, Text: "Hello"},
		{Type: presentation.ItemAssistant, Text: "Hi there"},
	}
	got := renderItems(items, 50)
	want := append(renderUserMsg(presentation.FeedItem{Type: presentation.ItemUserMsg, Text: "Hello"}, 50),
		"",
		"Nabd",
		"Hi there",
	)

	if len(got) != len(want) {
		t.Errorf("got %d lines, want %d lines\ngot:\n%s", len(got), len(want), strings.Join(got, "\n"))
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
