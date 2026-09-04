package ui

import (
	"strings"

	"nabd/internal/agent"
)

// maxHistoryEntries caps the in-memory UI history of user messages. When
// the cap is crossed the oldest entry is dropped from the UI history only:
// the journal and its user_msg events are never touched.
const maxHistoryEntries = 200

// userHistory is the in-memory list of successfully submitted user
// messages, newest last, plus the interactive Up/Down browsing state.
// It is a UI convenience: the journal remains the source of truth, and
// replay/rewind never edits history entries.
type userHistory struct {
	entries []string

	// Browsing state. idx is the 1-based distance from the newest entry
	// while recalling; 0 means the composer draft is current (not
	// browsing). draft holds the composer text saved when the user first
	// moved from the composer into history, so Down past the newest entry
	// restores it exactly.
	idx   int
	draft string
}

func newUserHistory() *userHistory { return &userHistory{} }

// add appends one submitted message. Empty, whitespace-only, and
// consecutive duplicates are skipped. Identical non-consecutive messages
// are kept. The oldest entry is dropped when the cap is crossed.
func (h *userHistory) add(s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == s {
		return
	}
	h.entries = append(h.entries, s)
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
	}
}

// buildFromEvents reconstructs the UI history from live user_msg events.
// Feed it agent.Live(...) so rewind-cancelled messages (which are not on
// the live branch) never enter history.
func (h *userHistory) buildFromEvents(events []agent.Event) {
	h.entries = nil
	for _, e := range events {
		if e.Type == agent.UserMsg {
			h.add(e.Text)
		}
	}
}

// len returns the number of stored entries.
func (h *userHistory) len() int { return len(h.entries) }

// at returns the entry idx positions back from the newest (at(1) is the
// newest). ok is false when idx is out of range.
func (h *userHistory) at(idx int) (string, bool) {
	i := len(h.entries) - idx
	if i < 0 {
		return "", false
	}
	return h.entries[i], true
}

// browsing reports whether history recall is active.
func (h *userHistory) browsing() bool { return h.idx > 0 }

// up recalls the next-older entry. The first Up from the composer saves
// the current text as the draft. ok is false when already at the oldest
// entry (the caller keeps the current composer text unchanged).
func (h *userHistory) up() (string, bool) {
	if h.idx == 0 {
		// First transition into history: browsing starts at the newest
		// entry. The caller must have saved the draft.
		h.idx = 1
	} else if h.idx >= h.len() {
		return "", false // already at the oldest entry
	} else {
		h.idx++
	}
	s, ok := h.at(h.idx)
	if !ok {
		// No entries at all: stay put, do not enter browsing.
		h.idx = 0
		return "", false
	}
	return s, true
}

// down recalls the next-newer entry. After the newest entry it returns to
// the draft and leaves browsing. When not browsing (idx == 0) it is a
// no-op: Down only walks history while recall is active.
func (h *userHistory) down() (string, bool) {
	if h.idx == 0 {
		return "", false
	}
	if h.idx <= 1 {
		d := h.draft
		h.idx = 0
		return d, true
	}
	h.idx--
	s, ok := h.at(h.idx)
	if !ok {
		h.idx = 0
		return "", false
	}
	return s, true
}

// edited marks that the user modified a recalled message: browsing ends and
// the modified text becomes the new draft.
func (h *userHistory) edited() {
	h.idx = 0
	h.draft = ""
}

// saveDraft stores the composer text to restore after browsing past the
// newest entry. Only the first transition into browsing may overwrite it.
func (h *userHistory) saveDraft(s string) {
	if h.idx == 0 {
		h.draft = s
	}
}

// setDraft replaces the draft (used when the current text becomes the new
// draft after editing a recalled message).
func (h *userHistory) setDraft(s string) {
	h.draft = s
}

// reset clears browsing state and the draft without touching entries.
func (h *userHistory) resetBrowsing() {
	h.idx = 0
	h.draft = ""
}
