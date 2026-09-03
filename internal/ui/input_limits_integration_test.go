package ui

import (
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// pasteMsg delivers a bracketed paste through the real key path.
func pasteInto(f *Feed, s string) {
	_, _ = f.Update(tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(s),
		Paste: true,
	})
}

// TestLimitAccepts7999And8000Runes: inputs at and just under the rune cap
// are accepted.
func TestLimitAccepts7999And8000Runes(t *testing.T) {
	f := NewFeed()
	at := arabicRunes(7999)
	typeIntoFeed(t, f, at[:100]) // seed a bit
	// Direct value set then edit path: simpler to paste the boundary text.
	f = NewFeed()
	pasteInto(f, arabicRunes(7999))
	if got := f.composer.valueLen(); got != 7999 {
		t.Fatalf("7999 rune paste: composer len = %d", got)
	}
	if f.status != "" {
		t.Fatalf("7999 runes must not raise a limit notice, got %q", f.status)
	}

	f2 := NewFeed()
	pasteInto(f2, arabicRunes(8000))
	if got := f2.composer.valueLen(); got != 8000 {
		t.Fatalf("8000 rune paste: composer len = %d", got)
	}
	if f2.status != "" {
		t.Fatalf("8000 runes must not raise a limit notice, got %q", f2.status)
	}
}

// TestLimitRejects8001Runes: an input over the rune cap is rejected whole,
// the previous text survives, and a notice appears.
func TestLimitRejects8001Runes(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "base text ")
	before := f.composer.value()
	pasteInto(f, arabicRunes(8001))
	if got := f.composer.value(); got != before {
		t.Fatalf("over-limit paste must be rejected whole; got len %d, want %q", len([]rune(got)), before)
	}
	if f.status == "" {
		t.Fatal("over-limit input must raise a notice")
	}
}

// TestLimitAccepts199And200Lines: line counts at and just under the cap
// are accepted.
func TestLimitAccepts199And200Lines(t *testing.T) {
	lines := func(n int) string { return strings.Repeat("x\n", n-1) + "x" }
	f := NewFeed()
	pasteInto(f, lines(199))
	if countInputLines(f.composer.value()) != 199 {
		t.Fatalf("199-line paste has %d lines", countInputLines(f.composer.value()))
	}
	f2 := NewFeed()
	pasteInto(f2, lines(200))
	if countInputLines(f2.composer.value()) != 200 {
		t.Fatalf("200-line paste has %d lines", countInputLines(f2.composer.value()))
	}
	if f2.status != "" {
		t.Fatal("200 lines must not raise a notice")
	}
}

// TestLimitRejects201Lines: a 201-line paste is rejected atomically.
func TestLimitRejects201Lines(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "keep ")
	before := f.composer.value()
	tooMany := strings.Repeat("y\n", 201)
	pasteInto(f, tooMany)
	if got := f.composer.value(); got != before {
		t.Fatalf("201-line paste must be rejected; got %q", got)
	}
	if f.status == "" {
		t.Fatal("201-line input must raise a notice")
	}
}

// TestLimitArabicRunes: the rune cap is enforced on Arabic text (2+ bytes
// per rune) — byte counting would reject far earlier.
func TestLimitArabicRunes(t *testing.T) {
	// 4000 Arabic runes = 8000 bytes; byte-counting would reject at 4000.
	f := NewFeed()
	pasteInto(f, arabicRunes(4000))
	if f.status != "" {
		t.Fatalf("4000 Arabic runes rejected: %q (byte-counting bug?)", f.status)
	}
	if got := f.composer.valueLen(); got != 4000 {
		t.Fatalf("composer len = %d, want 4000", got)
	}
}

// TestLimitEmojiRunes: emoji (4 bytes each) count as single runes.
func TestLimitEmojiRunes(t *testing.T) {
	f := NewFeed()
	pasteInto(f, strings.Repeat("😀", 7000))
	if f.status != "" {
		t.Fatalf("7000 emoji rejected: %q", f.status)
	}
	if got := f.composer.valueLen(); got != 7000 {
		t.Fatalf("composer len = %d, want 7000", got)
	}
	// 8001 emoji must be rejected.
	f2 := NewFeed()
	pasteInto(f2, strings.Repeat("😀", 8001))
	if f2.composer.valueLen() != 0 {
		t.Fatalf("8001 emoji must be rejected whole; len = %d", f2.composer.valueLen())
	}
}

// TestPasteOverLimitAtomicReject: a paste that would cross the limit is
// rejected whole — no partial text, previous content intact.
func TestPasteOverLimitAtomicReject(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "original")
	pasteInto(f, arabicRunes(200)+"\n"+strings.Repeat("z", 200))
	// The paste crosses no single limit by itself; build one that does.
	f = NewFeed()
	before := "keep this text"
	typeIntoFeed(t, f, before)
	pasteInto(f, strings.Repeat("pad-", 4000)) // 16000 runes
	if got := f.composer.value(); got != before {
		t.Fatalf("over-limit paste left %q, want %q", got, before)
	}
}

// TestNoticeNotRepeatedOnEveryKeypress: after a limit notice, further
// normal edits clear it; it does not accumulate or spam.
func TestNoticeNotRepeatedOnEveryKeypress(t *testing.T) {
	f := NewFeed()
	// Hit the line limit with a paste.
	pasteInto(f, strings.Repeat("a\n", 250))
	if f.status == "" {
		t.Fatal("limit notice expected")
	}
	// Typing a valid key clears the notice (the status is transient).
	typeIntoFeed(t, f, "x")
	if f.status == limitNotice {
		t.Fatal("stale limit notice must clear on the next valid edit")
	}
}

// TestLimitNoticeNotInJournal: the limit notice never becomes an agent
// event (there is no sink path from the status line to the journal).
func TestLimitNoticeNotInJournal(t *testing.T) {
	f := NewFeed()
	// Feed some events; count the projector items.
	_, _ = f.Update(agentEventBatchMsg{Events: []agent.Event{
		{Seq: 1, Type: agent.RunStart, Text: "start"},
	}})
	pasteInto(f, strings.Repeat("a\n", 250))
	if f.status == "" {
		t.Fatal("limit notice expected")
	}
	items := f.proj.Items()
	for _, it := range items {
		if it.Text == limitNotice {
			t.Fatal("limit notice leaked into the projected feed")
		}
	}
	// And the status is not an agent event by construction: nothing here
	// writes to any journal.
}

// TestHistoryUpInMiddleMovesCursorNotHistory: Up on a non-first logical
// line moves the cursor inside the textarea; it does not open history.
func TestHistoryUpInMiddleMovesCursorNotHistory(t *testing.T) {
	f := NewFeed()
	// Seed history.
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	sendAndRun(f, "stored")
	_, _ = f.Update(doneMsg{err: nil})

	// Multiline composer; put the cursor on line 2.
	typeIntoFeed(t, f, "line one")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "line two")
	// Cursor is at the end of line two. Up moves to line one (a cursor
	// move), it must NOT replace the text with history.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != "line one\nline two" {
		t.Fatalf("Up in the middle must not open history; text = %q", got)
	}
}

// TestHistoryDownInMiddleMovesCursor: Down on a non-last logical line while
// not browsing is a cursor move.
func TestHistoryDownInMiddleMovesCursor(t *testing.T) {
	f := NewFeed()
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	sendAndRun(f, "stored")
	_, _ = f.Update(doneMsg{err: nil})

	typeIntoFeed(t, f, "first")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "second")
	// Move to the first line, then Down: cursor move, not history.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := f.composer.value(); got != "first\nsecond" {
		t.Fatalf("Down must not change text when not browsing; got %q", got)
	}
}

// TestHistoryEditRecalledEndsBrowsing: editing a recalled message exits
// browsing and the edited text becomes the new draft.
func TestHistoryEditRecalledEndsBrowsing(t *testing.T) {
	f := NewFeed()
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	sendAndRun(f, "original message")
	_, _ = f.Update(doneMsg{err: nil})

	// Recall via Up.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := f.composer.value(); got != "original message" {
		t.Fatalf("Up recall = %q", got)
	}
	// Edit the recalled text.
	typeIntoFeed(t, f, "!")
	if got := f.composer.value(); got != "original message!" {
		t.Fatalf("edited recall = %q", got)
	}
	if f.HistoryBrowsing() {
		t.Fatal("editing a recalled message must end browsing")
	}
	// Down must not walk history anymore (browsing ended).
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := f.composer.value(); got != "original message!" {
		t.Fatalf("Down after editing must not change text; got %q", got)
	}
}

// TestUpAtOldestKeepsText: repeated Up at the oldest entry keeps the text
// stable.
func TestUpAtOldestKeepsText(t *testing.T) {
	f := NewFeed()
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	sendAndRun(f, "one")
	_, _ = f.Update(doneMsg{err: nil})
	sendAndRun(f, "two")
	_, _ = f.Update(doneMsg{err: nil})

	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp}) // two
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp}) // one
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp}) // stays at one
	if got := f.composer.value(); got != "one" {
		t.Fatalf("Up past oldest = %q, want 'one'", got)
	}
}
