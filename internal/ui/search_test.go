package ui

import (
	"fmt"
	"testing"
	"unicode/utf8"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

func feedWithCustomTexts(t *testing.T, texts []string, width int) *Feed {
	t.Helper()
	m := NewFeed()
	m.width = width
	m.height = 20
	events := make([]agent.Event, 0, len(texts)*2)
	for i, txt := range texts {
		events = append(events,
			agent.Event{
				Seq:  i*2 + 1,
				Type: agent.ToolStart,
				Call: &agent.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "bash"},
			},
			agent.Event{
				Seq:  i*2 + 2,
				Type: agent.ToolEnd,
				Call: &agent.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "bash", Output: txt, OK: true},
			},
		)
	}
	m.applyBatch(events)
	m.running = false
	m.busy = false
	m.runningTool = ""
	m.toolsExpanded = true
	m.refresh()
	return m
}

func TestSearchFindsArabicAndLatinText(t *testing.T) {
	texts := []string{
		"building system status monitor",
		"تحديث النظام وتجهيز الحزم",
	}
	m := feedWithCustomTexts(t, texts, 80)
	m.enterNavigation()

	// Search for Arabic
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.search.active {
		t.Fatal("expected search to be active after '/'")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("نظام")})
	if len(m.search.matches) == 0 {
		t.Fatal("search failed to find Arabic text 'نظام'")
	}
	if m.search.matches[0].itemIndex != 1 {
		t.Fatalf("expected Arabic match on card 1, got card %d", m.search.matches[0].itemIndex)
	}

	// Cancel and search for Latin
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.search.active {
		t.Fatal("expected search to be inactive after Esc")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("status")})
	if len(m.search.matches) == 0 {
		t.Fatal("search failed to find Latin text 'status'")
	}
	if m.search.matches[0].itemIndex != 0 {
		t.Fatalf("expected Latin match on card 0, got card %d", m.search.matches[0].itemIndex)
	}
}

func TestSearchNextAndPreviousWrapDeterministically(t *testing.T) {
	texts := []string{
		"target card one",
		"target card two",
		"target card three",
	}
	m := feedWithCustomTexts(t, texts, 80)
	m.enterNavigation()

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("target")})

	if len(m.search.matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(m.search.matches))
	}
	if m.search.matchIndex != 0 {
		t.Fatalf("initial matchIndex = %d, want 0", m.search.matchIndex)
	}

	// Enter advances
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.search.matchIndex != 1 {
		t.Fatalf("after Enter: matchIndex = %d, want 1", m.search.matchIndex)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.search.matchIndex != 2 {
		t.Fatalf("after Enter: matchIndex = %d, want 2", m.search.matchIndex)
	}

	// Enter wraps back to 0
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.search.matchIndex != 0 {
		t.Fatalf("after Enter wrap: matchIndex = %d, want 0", m.search.matchIndex)
	}

	// Shift+Tab retreats and wraps to 2
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.search.matchIndex != 2 {
		t.Fatalf("after Shift+Tab wrap: matchIndex = %d, want 2", m.search.matchIndex)
	}

	// KeyUp retreats to 1
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.search.matchIndex != 1 {
		t.Fatalf("after KeyUp: matchIndex = %d, want 1", m.search.matchIndex)
	}
}

func TestSearchDoesNotMutateProjection(t *testing.T) {
	texts := []string{
		"alpha target",
		"beta target",
	}
	m := feedWithCustomTexts(t, texts, 80)
	before := m.proj.Items()
	fps := make([]uint64, len(before))
	for i, it := range before {
		fps[i] = it.Fingerprint()
	}

	m.enterNavigation()
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("target")})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	after := m.proj.Items()
	if len(after) != len(before) {
		t.Fatalf("projector item count changed: %d -> %d", len(before), len(after))
	}
	for i, it := range after {
		if it.Fingerprint() != fps[i] {
			t.Fatalf("projector item %d mutated during search", i)
		}
	}
}

func TestSearchEscapeRestoresViewport(t *testing.T) {
	m := feedWithTools(t, 10, 80)
	m.toolsExpanded = true
	m.enterNavigation()
	m.selectItem(1)
	m.scrollTop = 2
	m.refresh()

	origSelected := m.selectedItem
	origScrollTop := m.scrollTop

	// Open search and search for "tool 8" which is on card 8
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("tool 8")})

	if len(m.search.matches) == 0 {
		t.Fatal("search did not find 'tool 8'")
	}
	if m.selectedItem == origSelected {
		t.Fatal("search jump did not change selection away from initial card")
	}

	// Press Esc to dismiss search
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if m.search.active {
		t.Fatal("search remained active after Esc")
	}
	if m.selectedItem != origSelected {
		t.Fatalf("Esc restored selectedItem = %d, want %d", m.selectedItem, origSelected)
	}
	if m.scrollTop != origScrollTop {
		t.Fatalf("Esc restored scrollTop = %d, want %d", m.scrollTop, origScrollTop)
	}
}

func TestSearchIsBlockedByPermissionModal(t *testing.T) {
	m := feedWithPendingPermission(t, 80)

	// Attempt to trigger search
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.search.active {
		t.Fatal("search activated while permission modal was visible")
	}

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("query")})
	if m.search.active || m.search.query != "" {
		t.Fatal("search query accepted while permission modal was visible")
	}

	if !m.modalVisible || m.decisionPending {
		t.Fatal("modal state was disturbed by search keystrokes")
	}
}

func TestSearchUsesRuneSafeOffsets(t *testing.T) {
	// Arabic text: each Arabic letter is 2 bytes in UTF-8.
	// "مرحبا بالعالم - target"
	prefix := "مرحبا بالعالم - "
	text := prefix + "target"
	prefixRunes := utf8.RuneCountInString(prefix)
	prefixBytes := len(prefix)

	if prefixBytes == prefixRunes {
		t.Fatalf("test precondition failed: prefix bytes (%d) must differ from rune count (%d)", prefixBytes, prefixRunes)
	}

	m := feedWithCustomTexts(t, []string{text}, 80)
	m.enterNavigation()

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("target")})

	if len(m.search.matches) == 0 {
		t.Fatal("failed to find 'target'")
	}

	match := m.search.matches[0]
	// In the clean line without gutter prefix "> ", the rune offset must be prefixRunes + offset.
	// Clean line stripped 2 runes ("> "), so rune offset is 2 + prefixRunes.
	expectedRuneStart := 2 + prefixRunes
	if match.runeStart != expectedRuneStart {
		t.Fatalf("runeStart = %d, want %d (byte offset would be %d)", match.runeStart, expectedRuneStart, 2+prefixBytes)
	}
	if match.runeEnd != expectedRuneStart+len("target") {
		t.Fatalf("runeEnd = %d, want %d", match.runeEnd, expectedRuneStart+len("target"))
	}
}
