package ui

import (
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"nabd/internal/redact"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	copySuccessNotice     = "copied selected card"
	copyUnavailableNotice = "copy unavailable"
	copyTooLargeNotice    = "copy blocked: content too large"
)

// SetCopyNotices configures localized user-facing status messages for clipboard actions.
func SetCopyNotices(success, unavailable, tooLarge string) {
	if success != "" {
		copySuccessNotice = success
	}
	if unavailable != "" {
		copyUnavailableNotice = unavailable
	}
	if tooLarge != "" {
		copyTooLargeNotice = tooLarge
	}
}

// cardTextForCopy extracts the display-rendered lines of card idx from m.lines.
// It reads exclusively from the projected view, never from the raw journal or raw error structs.
func (m *Feed) cardTextForCopy(idx int) string {
	items := m.navigationItems()
	if idx < 0 || idx >= len(items) || idx >= len(m.offsets) {
		return ""
	}
	start := m.offsets[idx]
	end := len(m.lines)
	if idx+1 < len(m.offsets) {
		end = m.offsets[idx+1]
	}
	if start >= len(m.lines) || start >= end {
		return ""
	}

	lines := m.lines[start:end]
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = stripCardGutter(ansi.Strip(l))
	}
	return strings.Join(clean, "\n")
}

// copySelectedCard copies the currently selected card using OSC 52.
// The data strictly traverses the pipeline:
//
//	projected card -> credential redaction -> display sanitization -> size limit -> base64 -> OSC 52
//
// It never touches raw journal content and returns no tool execution commands.
func (m *Feed) copySelectedCard() (tea.Model, tea.Cmd) {
	if m.modalVisible || m.decisionPending {
		return m, nil
	}
	if m.selectedItem < 0 {
		m.setStatus(copyUnavailableNotice, rankResult)
		return m, nil
	}

	// 1. Projected card text (from m.lines, not raw journal)
	rawText := m.cardTextForCopy(m.selectedItem)
	if strings.TrimSpace(rawText) == "" {
		m.setStatus(copyUnavailableNotice, rankResult)
		return m, nil
	}

	// 2. Credential redaction
	redacted := redact.Redact(rawText)

	// 3. Display sanitization
	sanitized := SanitizeForDisplay(redacted, DisplayPolicy{AllowNewline: true, Redact: true})

	// 4. Size limit
	if len(sanitized) > defaultMaxCopyBytes {
		m.setStatus(copyTooLargeNotice, rankResult)
		return m, nil
	}

	// 5. Base64 & OSC 52 encoding
	seq, err := encodeOSC52(sanitized, defaultMaxCopyBytes)
	if err != nil || seq == "" {
		m.setStatus(copyUnavailableNotice, rankResult)
		return m, nil
	}

	// 6. Write OSC 52 escape sequence to clipboard writer
	w := m.clipboardWriter
	if w == nil {
		w = os.Stdout
	}
	if _, err := io.WriteString(w, seq); err != nil {
		m.setStatus(copyUnavailableNotice, rankResult)
		return m, nil
	}

	m.setStatus(copySuccessNotice, rankResult)
	return m, nil
}
