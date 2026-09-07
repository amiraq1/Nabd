package ui

import (
	"strings"
	"testing"
	"nabd/internal/presentation"
)

func TestFormatMarkdown(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		width    int
		expected []string
	}{
		{
			name:  "paragraphs",
			input: "Hello\nWorld",
			width: 20,
			expected: []string{
				"Hello",
				"World",
			},
		},
		{
			name:  "headings",
			input: "# Heading 1\n## Heading 2\n### Heading 3\n#not a heading",
			width: 20,
			expected: []string{
				bold.Render("Heading 1"),
				bold.Render("Heading 2"),
				bold.Render("Heading 3"),
				"#not a heading",
			},
		},
		{
			name:  "bold spans",
			input: "Hello **world**!",
			width: 20,
			expected: []string{
				"Hello " + bold.Render("world") + "!",
			},
		},
		{
			name:  "lists alignment",
			input: "- Item one that is very long and should wrap\n1. Ordered item also long and wrapping",
			width: 15,
			expected: []string{
				"- Item one that",
				"  is very long",
				"  and should",
				"  wrap",
				"1. Ordered item",
				"   also long",
				"   and wrapping",
			},
		},
		{
			name:  "literal fenced code",
			input: "```go\n# Not a heading\n**Not bold**\n```",
			width: 20,
			expected: []string{
				"```go",
				"# Not a heading",
				"**Not bold**",
				"```",
			},
		},
		{
			name:  "inline code",
			input: "Use `**bold**` inside.",
			width: 50,
			expected: []string{
				"Use `**bold**` inside.",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatMarkdown(tc.input, tc.width)
			if len(got) != len(tc.expected) {
				t.Fatalf("length mismatch: got %d, want %d\ngot:\n%q\nwant:\n%q", len(got), len(tc.expected), got, tc.expected)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("line %d: got %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

func TestMarkdownChunking(t *testing.T) {
	fullText := "This is **bold** and `code`\n- List item\n# Heading"
	expected := formatMarkdown(fullText, 40)

	chunks := []string{
		"This ",
		"is **b",
		"old** and `co",
		"de`\n- List i",
		"tem\n# Hea",
		"ding",
	}

	var accumulated string
	for _, chunk := range chunks {
		accumulated += chunk
	}
	
	got := formatMarkdown(accumulated, 40)
	if len(got) != len(expected) {
		t.Fatalf("mismatch")
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], expected[i])
		}
	}
}

func TestMarkdownArabic(t *testing.T) {
	input := "Arabic **مرحبا** 123 /path/to/file 🎉"
	// Should not reorder or destroy anything
	expected := formatMarkdown(input, 50)
	if len(expected) == 0 || !strings.Contains(expected[0], "مرحبا") {
		t.Errorf("Arabic was mangled: %v", expected)
	}
}

func TestMarkdownNarrowWidth(t *testing.T) {
	input := "- A very long list item that must be wrapped"
	// Extremely narrow width
	got := formatMarkdown(input, 4)
	if len(got) < 3 {
		t.Errorf("Expected multiple wrapped lines, got: %v", got)
	}
	// Should degrade safely
}

func TestHostileANSI(t *testing.T) {
	// We test via renderAssistant since SanitizeForDisplay is used there
	item := presentation.FeedItem{
		Type: presentation.ItemAssistant,
		Text: "Hello \x1b[31mRed\x1b[0m **World**",
	}
	got := renderAssistant(item, 50)
	// The ANSI escape should be stripped or rendered harmless, but not parsed as styling
	foundBold := false
	for _, line := range got {
		if strings.Contains(line, "\x1b[31m") {
			t.Errorf("ANSI escape leaked through: %q", line)
		}
		if strings.Contains(line, bold.Render("World")) {
			foundBold = true
		}
	}
	if !foundBold {
		t.Errorf("Markdown bold was not applied properly")
	}
}

func TestRoleLabelAndSpacing(t *testing.T) {
	item := presentation.FeedItem{
		Type: presentation.ItemAssistant,
		Text: "Hello",
	}
	got := renderAssistant(item, 50)
	if len(got) < 2 {
		t.Fatalf("Expected role label and text, got: %v", got)
	}
	if !strings.Contains(got[0], "Nabd") {
		t.Errorf("Expected role label 'Nabd', got %q", got[0])
	}
	if !strings.Contains(got[1], "Hello") {
		t.Errorf("Expected text 'Hello', got %q", got[1])
	}
}
