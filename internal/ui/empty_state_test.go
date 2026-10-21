package ui

import (
	"reflect"
	"strings"
	"testing"

	"nabd/internal/event"
	"nabd/internal/presentation"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestIdleWelcomeFitsPhoneMatrixAndStaysPure(t *testing.T) {
	for _, width := range []int{20, 32, 40, 60, 80} {
		for height := 1; height <= 40; height++ {
			f := NewFeed()
			_, _ = f.Update(tea.WindowSizeMsg{Width: width, Height: height})
			beforeLines := append([]string(nil), f.lines...)
			beforeItems := f.proj.Items()
			view := f.View()
			afterItems := f.proj.Items()
			if !reflect.DeepEqual(beforeLines, f.lines) && !(len(beforeLines) == 0 && len(f.lines) == 0) || !reflect.DeepEqual(beforeItems, afterItems) {
				t.Fatalf("View mutated feed at %dx%d", width, height)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("idle line exceeds width at %dx%d: %q", width, height, line)
				}
			}
			if height >= 8 && !strings.Contains(view, "Nabd is ready") {
				t.Fatalf("welcome missing at %dx%d:\n%s", width, height, view)
			}
		}
	}
}

func TestIdleWelcomeIgnoresOnlyRunBoundaryButNotContentOrBusyStates(t *testing.T) {
	f := NewFeed()
	_, _ = f.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	_, _ = f.Update(agentEventBatchMsg{Events: []event.Event{{Seq: 1, Type: event.RunStart, Text: "nabd dev · 0123456789012345678901234567890123456789 · unknown · openai · project"}}})
	if !strings.Contains(f.View(), "Nabd is ready") {
		t.Fatal("run boundary alone must not hide welcome")
	}
	_, _ = f.Update(agentEventBatchMsg{Events: []event.Event{{Seq: 2, Type: event.UserMsg, Text: "question"}}})
	if strings.Contains(f.View(), "Nabd is ready") {
		t.Fatal("welcome must disappear once content exists")
	}
	f.composer.clear()
	f.busy = true
	if strings.Contains(f.View(), "Nabd is ready") {
		t.Fatal("welcome must disappear while busy")
	}
}

func TestRunBoundaryIdentityCompactsWithoutInterpretingUnexpectedText(t *testing.T) {
	full := "nabd dev · 0123456789012345678901234567890123456789 · unknown · openai · project"
	got := ansi.Strip(renderRunBoundary(presentation.FeedItem{Type: presentation.ItemRunBoundary, Text: full}, 40)[0])
	if !strings.Contains(got, "openai") || !strings.Contains(got, "project") {
		t.Fatalf("compact identity lost provider/path: %q", got)
	}
	if strings.Contains(got, "0123456789") {
		t.Fatalf("compact identity retained long commit at narrow width: %q", got)
	}
	unexpected := "nabd custom · text · that · is · unexpected"
	untouched := ansi.Strip(renderRunBoundary(presentation.FeedItem{Type: presentation.ItemRunBoundary, Text: unexpected}, 80)[0])
	if !strings.Contains(untouched, unexpected) {
		t.Fatalf("unexpected boundary was reinterpreted: %q", untouched)
	}
}

func TestIdleWelcomeHiddenByEachInteractiveState(t *testing.T) {
	cases := map[string]func(*Feed){
		"running":        func(f *Feed) { f.running = true },
		"busy":           func(f *Feed) { f.busy = true },
		"permission":     func(f *Feed) { f.modalVisible = true },
		"decision":       func(f *Feed) { f.decisionPending = true },
		"secret":         func(f *Feed) { f.secretPrompt = true },
		"navigation":     func(f *Feed) { f.navigationMode = true },
		"draft":          func(f *Feed) { f.composer.setValue("draft") },
		"notice":         func(f *Feed) { f.addNotice(presentation.ItemNotice, "notice") },
		"manual content": func(f *Feed) { f.lines = []string{"existing content"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := NewFeed()
			if !f.emptyStateActive() {
				t.Fatal("invalid idle fixture")
			}
			mutate(f)
			if f.emptyStateActive() {
				t.Fatal("welcome remained active")
			}
			if strings.Contains(f.View(), "Nabd is ready") {
				t.Fatal("welcome remained visible")
			}
		})
	}
}

func TestRunIdentityWidthLadderAndASCII(t *testing.T) {
	text := "nabd 2.1.3 · 0123456789abcdef0123456789abcdef01234567 · 2026-10-21T20:00:00Z · openai · project"
	item := presentation.FeedItem{Type: presentation.ItemRunBoundary, Text: text}
	for _, tc := range []struct {
		width        int
		want, absent string
	}{
		{160, "0123456789abcdef0123456789abcdef01234567", ""},
		{75, "0123456", "0123456789"},
		{40, "openai · project", "0123456"},
	} {
		got := ansi.Strip(renderRunBoundary(item, tc.width)[0])
		if !strings.Contains(got, tc.want) || (tc.absent != "" && strings.Contains(got, tc.absent)) {
			t.Fatalf("identity width=%d: %q", tc.width, got)
		}
		if ansi.StringWidth(got) > tc.width {
			t.Fatalf("width exceeded: %q", got)
		}
	}
	if _, ok := parseRunIdentity("nabd custom · text · that · is · unexpected"); ok {
		t.Fatal("unexpected identity parsed")
	}
	t.Setenv("NABD_ASCII_ONLY", "1")
	got := ansi.Strip(renderRunBoundary(item, 40)[0])
	for _, r := range got {
		if r > 127 {
			t.Fatalf("non-ASCII chrome: %q", got)
		}
	}
	if !strings.Contains(got, "openai - project") {
		t.Fatalf("ASCII compact: %q", got)
	}
}

func TestIdleBoundaryWelcomeHeightAndPointerPadding(t *testing.T) {
	for _, width := range []int{20, 32, 40, 60, 80} {
		for height := 1; height <= 40; height++ {
			f := NewFeed()
			f.Update(tea.WindowSizeMsg{Width: width, Height: height})
			f.Update(agentEventBatchMsg{Events: []event.Event{{Seq: 1, Type: event.RunStart, Text: "nabd 2.1.3 · none · unknown · openai · project"}}})
			beforePadding := f.viewportTopPadding(f.computeLayout())
			view := f.View()
			if len(strings.Split(view, "\n")) > height {
				t.Fatalf("height exceeded %dx%d", width, height)
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > width {
					t.Fatalf("width exceeded %dx%d", width, height)
				}
			}
			if f.viewportTopPadding(f.computeLayout()) != beforePadding {
				t.Fatal("welcome changed pointer padding")
			}
			if view != f.View() {
				t.Fatal("View not deterministic")
			}
		}
	}
}
