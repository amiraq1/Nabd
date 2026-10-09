package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"nabd/internal/event"
	"nabd/internal/presentation"
	"nabd/internal/rtl"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestRTLDisplayModeContract(t *testing.T) {
	cases := []struct {
		value string
		want  rtl.Mode
	}{
		{"", rtl.Reorder},
		{"logical", rtl.Logical},
		{"off", rtl.Logical},
		{"reorder", rtl.Reorder},
		{"mirror", rtl.ReorderAndMirror},
		{"auto", rtl.ReorderAndMirror},
		{"reorder-and-mirror", rtl.ReorderAndMirror},
		{"unknown", rtl.Reorder},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("NABD_RTL", tc.value)
			if got := rtlDisplayMode(); got != tc.want {
				t.Fatalf("rtlDisplayMode()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestMarkdownProducesSemanticCodeSpan(t *testing.T) {
	logical, spans := parseInlineSemantic("\u0627\u0641\u062a\u062d `go test ./...` \u0627\u0644\u0622\u0646", styleNone)
	if logical != "\u0627\u0641\u062a\u062d `go test ./...` \u0627\u0644\u0622\u0646" {
		t.Fatalf("logical text changed: %q", logical)
	}
	if len(spans) != 1 {
		t.Fatalf("spans=%v, want one code span", spans)
	}
	span := spans[0]
	if span.Kind != rtl.Code || logical[span.Start:span.End] != "`go test ./...`" {
		t.Fatalf("code span=%+v text=%q", span, logical[span.Start:span.End])
	}
}

func TestSemanticANSIIsEmittedAfterRTLLayout(t *testing.T) {
	t.Setenv("NABD_RTL", "mirror")
	logical, spans := parseInlineSemantic("\u0646\u0635 **bold**", styleNone)
	if strings.Contains(logical, "\x1b") {
		t.Fatal("semantic input contains ANSI")
	}
	lines, err := layoutSemanticText(logical, spans, 40)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "bold") {
		t.Fatalf("styled source disappeared: %q", got)
	}
	if want := bold.Render("bold"); !strings.Contains(got, want) {
		t.Fatalf("bold style was not emitted after layout: %q", got)
	}
}

func TestRTLWidthsAndGraphemeSafety(t *testing.T) {
	t.Setenv("NABD_RTL", "mirror")
	text := "\u0645\u064e\u0631\u0652\u062d\u064e\u0628\u064b\u0627 internal/ui/feed.go \U0001f469\u200d\U0001f4bb"
	for _, width := range []int{20, 39, 40, 79, 80, 120} {
		lines := wrap(text, width)
		for i, line := range lines {
			if got := ansi.StringWidth(line); got > width {
				t.Fatalf("width=%d line=%d measured=%d text=%q", width, i, got, line)
			}
			if strings.Contains(line, "\u200d") && !strings.Contains(line, "\U0001f469\u200d\U0001f4bb") {
				t.Fatalf("width=%d split ZWJ cluster: %q", width, line)
			}
		}
	}
}

func TestInvalidSemanticSpansFallBackWithoutMutation(t *testing.T) {
	t.Setenv("NABD_RTL", "mirror")
	logical := "\u0646\u0635"
	bad := []rtl.Span{{Start: 1, End: len(logical), Kind: rtl.Code}}
	if _, err := layoutSemanticText(logical, bad, 20); err == nil {
		t.Fatal("invalid byte boundary unexpectedly accepted")
	}
	got := renderMarkdownSemantic(logical, bad, 20)
	if strings.Join(got, "") != logical {
		t.Fatalf("fallback changed logical source: %q", got)
	}
}

func TestRenderDoesNotMutateCanonicalFeedText(t *testing.T) {
	t.Setenv("NABD_RTL", "mirror")
	source := "\u0645\u0631\u062d\u0628\u0627 (go test ./...)"
	item := presentation.FeedItem{Type: presentation.ItemAssistant, Text: source}
	_ = renderItem(item, 39)
	if item.Text != source {
		t.Fatalf("render mutated canonical text: got %q want %q", item.Text, source)
	}
}

func TestCopyUsesLogicalSourceWhenDisplayIsReordered(t *testing.T) {
	t.Setenv("NABD_RTL", "mirror")
	source := "\u0645\u0631\u062d\u0628\u0627 (test)"
	m := feedWithCustomTexts(t, []string{source}, 39)
	m.enterNavigation()
	m.selectItem(0)
	var out bytes.Buffer
	m.SetClipboardWriter(&out)
	m.Update(teaKeyRunes('c'))
	got := decodeOSC52Payload(t, out.String())
	if !strings.Contains(got, source) {
		t.Fatalf("copy lost logical source: %q", got)
	}
}

func TestSearchUsesLogicalSourceWhenDisplayIsReordered(t *testing.T) {
	t.Setenv("NABD_RTL", "mirror")
	source := "\u0645\u0631\u062d\u0628\u0627 \u0628\u0627\u0644\u0639\u0627\u0644\u0645"
	m := feedWithCustomTexts(t, []string{source}, 39)
	m.enterNavigation()
	m.search.query = "\u0645\u0631\u062d\u0628\u0627"
	m.updateSearchMatches()
	if len(m.search.matches) == 0 {
		t.Fatal("logical Arabic query did not map to the reordered card")
	}
}

func TestLineCacheIncludesRTLMode(t *testing.T) {
	t.Setenv("NABD_RTL", "logical")
	f := NewFeed()
	f.width = 39
	f.BuildFromEvents([]event.Event{{
		Seq: 1, Type: event.UserMsg, Text: "\u0645\u0631\u062d\u0628\u0627 hello",
	}})
	f.refresh()
	firstCount := f.renderCount
	t.Setenv("NABD_RTL", "mirror")
	f.refresh()
	if f.renderCount <= firstCount {
		t.Fatal("changing RTL mode reused a stale line-cache entry")
	}
}

func TestPTYRTLFrameWidth39(t *testing.T) {
	t.Setenv("NABD_RTL", "mirror")
	sess := StartPTYSession(t, 39, 24)
	defer sess.Close()

	sess.InjectBatch([]event.Event{
		{Seq: 1, Type: event.UserMsg, Text: "\u0627\u0641\u062a\u062d internal/ui/feed.go"},
		{Seq: 2, Type: event.TextDelta, Text: "\u0627\u0644\u0646\u062a\u064a\u062c\u0629 123 (`go test ./...`)"},
		{Seq: 3, Type: event.TurnEnd},
	})
	if err := sess.WaitForText("internal/ui/feed.go", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := sess.WaitForText("go test ./...", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	snap := sess.Snapshot()
	assertScreenBounds(t, snap)
	for i, row := range snap.Rows {
		if got := ansi.StringWidth(row); got > 39 {
			t.Fatalf("PTY row %d width=%d exceeds 39: %q", i, got, row)
		}
	}
}

func teaKeyRunes(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}
