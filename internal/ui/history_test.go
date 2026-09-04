package ui

import (
	"strconv"
	"testing"

	"nabd/internal/agent"
)

func TestHistoryAddSkipsEmptyAndWhitespace(t *testing.T) {
	h := newUserHistory()
	h.add("")
	h.add("   ")
	h.add("\n\t")
	if h.len() != 0 {
		t.Fatalf("empty/whitespace adds must be skipped, len=%d", h.len())
	}
}

func TestHistoryAddSkipsConsecutiveDuplicates(t *testing.T) {
	h := newUserHistory()
	h.add("hello")
	h.add("hello")
	if h.len() != 1 {
		t.Fatalf("consecutive duplicate must be skipped, len=%d", h.len())
	}
}

func TestHistoryKeepsNonConsecutiveDuplicates(t *testing.T) {
	h := newUserHistory()
	h.add("same")
	h.add("other")
	h.add("same") // non-consecutive duplicate is allowed
	if h.len() != 3 {
		t.Fatalf("non-consecutive duplicate must be kept, len=%d", h.len())
	}
}

func TestHistoryAddCapsAtMaxEntries(t *testing.T) {
	h := newUserHistory()
	for i := 0; i < maxHistoryEntries+50; i++ {
		h.add("msg " + strconv.Itoa(i))
	}
	if h.len() != maxHistoryEntries {
		t.Fatalf("history must cap at %d, got %d", maxHistoryEntries, h.len())
	}
	// The oldest entry was dropped; the newest remain.
	s, ok := h.at(1)
	if !ok || s != "msg "+strconv.Itoa(maxHistoryEntries+49) {
		t.Fatalf("newest entry after cap = %q,%v", s, ok)
	}
	oldest, ok := h.at(maxHistoryEntries)
	if !ok || oldest != "msg "+strconv.Itoa(50) {
		t.Fatalf("oldest entry after cap = %q,%v want msg 50", oldest, ok)
	}
}

func TestHistoryUpDown(t *testing.T) {
	h := newUserHistory()
	h.add("first")
	h.add("second")
	h.add("third")

	// Up from the composer: newest entry.
	s, ok := h.up()
	if !ok || s != "third" {
		t.Fatalf("first up = %q,%v want third,true", s, ok)
	}
	// Second up: older.
	s, ok = h.up()
	if !ok || s != "second" {
		t.Fatalf("second up = %q,%v want second,true", s, ok)
	}
	// Third up: oldest.
	s, ok = h.up()
	if !ok || s != "first" {
		t.Fatalf("third up = %q,%v want first,true", s, ok)
	}
	// Up past the oldest: stays put, not ok.
	if _, ok := h.up(); ok {
		t.Fatal("up past the oldest entry must report not-ok")
	}
	// Down back toward newest.
	s, ok = h.down()
	if !ok || s != "second" {
		t.Fatalf("down = %q,%v want second,true", s, ok)
	}
	s, ok = h.down()
	if !ok || s != "third" {
		t.Fatalf("down = %q,%v want third,true", s, ok)
	}
	// Down past the newest: back to the draft.
	s, ok = h.down()
	if !ok || s != "" {
		t.Fatalf("down past newest = %q,%v want draft '',true", s, ok)
	}
	if h.browsing() {
		t.Fatal("down past newest must end browsing")
	}
}

func TestHistoryDraftPreserved(t *testing.T) {
	h := newUserHistory()
	h.add("stored")
	h.saveDraft("my draft text")

	if _, ok := h.up(); !ok {
		t.Fatal("up should recall newest entry")
	}
	// Down to the draft: the saved text comes back.
	s, ok := h.down()
	if !ok || s != "my draft text" {
		t.Fatalf("down to draft = %q,%v want 'my draft text',true", s, ok)
	}
}

func TestHistoryNoEntries(t *testing.T) {
	h := newUserHistory()
	if _, ok := h.up(); ok {
		t.Fatal("up with no entries must not enter browsing")
	}
	if h.browsing() {
		t.Fatal("up with no entries must not set browsing")
	}
}

func TestHistoryEditedEndsBrowsing(t *testing.T) {
	h := newUserHistory()
	h.add("stored")
	h.saveDraft("draft")
	if _, ok := h.up(); !ok {
		t.Fatal("up should recall an entry")
	}
	if !h.browsing() {
		t.Fatal("should be browsing after up")
	}
	h.edited()
	if h.browsing() {
		t.Fatal("edited() must end browsing")
	}
}

func TestHistoryResetBrowsing(t *testing.T) {
	h := newUserHistory()
	h.add("stored")
	h.saveDraft("draft")
	_, _ = h.up()
	h.resetBrowsing()
	if h.browsing() {
		t.Fatal("resetBrowsing must clear browsing")
	}
	if _, ok := h.down(); ok {
		t.Fatal("after reset, down must not produce an entry")
	}
}

func TestHistoryBuildFromEvents(t *testing.T) {
	// Live events including user messages; Interrupted/RunEnd are not
	// user messages and must not enter history.
	events := []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "one"},
		{Seq: 2, Type: agent.TextDelta, Text: "delta"},
		{Seq: 3, Type: agent.UserMsg, Text: "two"},
		{Seq: 4, Type: agent.Interrupted},
		{Seq: 5, Type: agent.RunEnd},
	}
	h := newUserHistory()
	h.buildFromEvents(events)
	if h.len() != 2 {
		t.Fatalf("history from events = %d entries, want 2", h.len())
	}
	s, _ := h.at(1)
	if s != "two" {
		t.Fatalf("newest entry = %q, want 'two'", s)
	}
}

func TestHistoryBuildFromEventsSkipsWhitespace(t *testing.T) {
	events := []agent.Event{
		{Seq: 1, Type: agent.UserMsg, Text: "   "},
		{Seq: 2, Type: agent.UserMsg, Text: "real"},
	}
	h := newUserHistory()
	h.buildFromEvents(events)
	if h.len() != 1 {
		t.Fatalf("whitespace user_msg must not enter history, len=%d", h.len())
	}
}
