package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"nabd/internal/redact"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	copySuccessNotice     = "copied selected card"
	copyUnavailableNotice = "copy unavailable"
	copyTooLargeNotice    = "copy blocked: content too large"

	copyFullReportNotice = "copied full report"
	copyFullReportSaved  = "report saved: "
	copyFullReportFailed = "report export failed: "

	// copyCommandTimeoutNotice and copyCommandMissingNotice replace the raw
	// exec error ("signal: killed", "executable file not found") with a message
	// that names the dependency the user has to fix. ASCII only, like every
	// other visible UI string.
	copyCommandTimeoutNotice = "termux-clipboard-set timed out; install and open the Termux:API app"
	copyCommandMissingNotice = "termux-clipboard-set not found; install the termux-api package"
	// copyCommandFailedNotice covers every remaining failure (non-zero exit,
	// permission denied, I/O): err.Error() names neither the tool nor the fix.
	copyCommandFailedNotice = "termux-clipboard-set failed; check the termux-api install"

	// copyRescueFailedNotice is appended when a failed clipboard delivery could
	// not be rescued to a file either. The text is lost, and the status line
	// must say so rather than staying silent about it.
	copyRescueFailedNotice = "export failed; text not saved"
)

// defaultClipboardCommand is the external clipboard-set binary used on Termux.
// Feed.clipboardCommand overrides it so tests never run the real binary.
const defaultClipboardCommand = "termux-clipboard-set"

// redactedText is text that has already passed credential redaction and
// display sanitization. It can only be produced by redactForExport, so a raw
// string cannot reach an exported file by accident: handing a bare string to
// writePrivateReport is a compile error, not something a reviewer has to catch.
type redactedText string

// redactForExport applies the two mandatory transformations, in order:
// credential redaction first, then display sanitization. Every path that hands
// text to the clipboard or persists it as a report must obtain its input here.
func redactForExport(s string) redactedText {
	redacted := redact.Redact(s)
	return redactedText(SanitizeForDisplay(redacted, DisplayPolicy{AllowNewline: true, Redact: true}))
}

type clipboardResultMsg struct {
	err    error
	notice string
	// detail, when non-empty, replaces err.Error() in the status line with a
	// message that names the broken dependency instead of the raw exec error.
	detail string
	// body is the already-redacted payload. It travels with the result so that
	// a failed clipboard delivery can still be rescued to a file instead of
	// losing the user's text. It is empty for the synchronous paths, which
	// report their own failures inline.
	body redactedText
}

func copyCmd(name string, body redactedText, notice string) tea.Cmd {
	return func() tea.Msg {
		// 10s, not 5s: a cold Termux:API start has been measured to exceed 5s on a
		// real device. The call is asynchronous, so the UI never blocks on it.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, name)
		c.Env = clipboardChildEnv(os.Environ())
		c.Stdin = strings.NewReader(string(body))
		err := c.Run()
		res := clipboardResultMsg{err: err, notice: notice, body: body}
		switch {
		case err == nil:
		case ctx.Err() == context.DeadlineExceeded:
			// The deadline killed the process, so err is "signal: killed" —
			// which names neither the tool nor the fix. Check ctx, not err.
			res.detail = copyCommandTimeoutNotice
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
			// ErrNotFound is what LookPath returns for a bare name; an absolute
			// path that does not exist surfaces as ENOENT instead. Both mean
			// the same thing to the user: the binary is not there.
			res.detail = copyCommandMissingNotice
		default:
			// Any other failure (non-zero exit, permission denied, I/O) would
			// otherwise surface err.Error() verbatim, which names neither the
			// tool nor the fix.
			res.detail = copyCommandFailedNotice
		}
		return res
	}
}

// clipboardChildEnv returns a filtered environment for clipboard command execution.
// It forwards only the variables needed by clipboard utilities (e.g. termux-clipboard-set)
// and mock scripts (PATH, TMPDIR, PREFIX, TERMUX_VERSION), dropping session credentials.
func clipboardChildEnv(parent []string) []string {
	out := make([]string, 0, 8)
	for _, kv := range parent {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		switch k {
		case "PATH", "PREFIX", "TMPDIR", "TEMP", "TMP", "TERMUX_VERSION", "ANDROID_ROOT", "ANDROID_DATA", "CLIP_CAPTURE":
			out = append(out, kv)
		}
	}
	return out
}

// isTermux reports whether this process runs inside Termux. TERMUX_VERSION is the
// canonical marker, but it is not guaranteed to be inherited by every child
// process; PREFIX points at the Termux filesystem root and is checked as a
// fallback so a real device is not reported as "copy unavailable".
func isTermux() bool {
	return os.Getenv("TERMUX_VERSION") != "" ||
		strings.Contains(os.Getenv("PREFIX"), "com.termux")
}

// writeOSC52 encodes the sanitized payload and writes the escape sequence to w.
// It is the single implementation shared by the explicit-writer and stdout
// transports, so the two cannot drift apart.
func (m *Feed) writeOSC52(w io.Writer, sanitized, notice string) (tea.Model, tea.Cmd) {
	seq, err := encodeOSC52(sanitized, defaultMaxCopyBytes)
	if err != nil || seq == "" {
		m.setStatus(copyUnavailableNotice, rankResult)
		return m, nil
	}
	if _, err := io.WriteString(w, seq); err != nil {
		m.setStatus(copyUnavailableNotice, rankResult)
		return m, nil
	}
	m.setStatus(notice, rankResult)
	return m, nil
}

// dispatchCopy routes clipboard content according to the environment:
//  1. Custom test clipboard writer (m.clipboardWriter != nil) -> synchronous OSC 52
//  2. Remote SSH connection (SSH_CONNECTION != "") -> synchronous OSC 52 to stdout
//  3. Termux environment (isTermux) -> asynchronous termux-clipboard-set command
//  4. Unavailable otherwise
func (m *Feed) dispatchCopy(body redactedText, notice string) (tea.Model, tea.Cmd) {
	sanitized := string(body)
	if m.clipboardWriter != nil {
		return m.writeOSC52(m.clipboardWriter, sanitized, notice)
	}

	// Remote sessions receive OSC 52 on stdout. This single condition is the
	// environment gate documented in CHANGELOG and ui-parity §8.
	if os.Getenv("SSH_CONNECTION") != "" {
		return m.writeOSC52(os.Stdout, sanitized, notice)
	}

	if isTermux() {
		name := m.clipboardCommand
		if name == "" {
			name = defaultClipboardCommand
		}
		return m, copyCmd(name, body, notice)
	}

	m.setStatus(copyUnavailableNotice, rankResult)
	return m, nil
}

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

	// 2. Credential redaction, then display sanitization (in that order).
	body := redactForExport(rawText)

	// 3. Size limit
	if len(body) > defaultMaxCopyBytes {
		m.setStatus(copyTooLargeNotice, rankResult)
		return m, nil
	}

	return m.dispatchCopy(body, copySuccessNotice)
}

// fullReportText renders every card in the feed with its tool output expanded
// and returns the plain text of the whole report. It renders through the pure
// renderBlocks path, never renderItemsCached, so the visible viewport —
// m.lines, m.offsets, m.toolsExpanded and the line cache — is left untouched.
func (m *Feed) fullReportText() string {
	items := m.navigationItems()
	if len(items) == 0 {
		return ""
	}
	width := m.width
	if width < minViewportWidth {
		width = minViewportWidth
	}
	lines := renderItems(items, width, true)
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = stripCardGutter(ansi.Strip(l))
	}
	return strings.Join(clean, "\n")
}

// copyFullReport copies the entire report (all cards, tools expanded) through
// the same pipeline as copySelectedCard:
//
//	projected cards (expanded) -> credential redaction -> display sanitization -> OSC 52
//
// When the report exceeds the clipboard ceiling it is written, already
// redacted and sanitized, to a private file under ~/.ag/exports instead. It
// reads only the projected feed — never a raw session journal — and returns no
// tool execution commands.
func (m *Feed) copyFullReport() (tea.Model, tea.Cmd) {
	if m.modalVisible || m.decisionPending {
		return m, nil
	}

	rawText := m.fullReportText()
	if strings.TrimSpace(rawText) == "" {
		m.setStatus(copyUnavailableNotice, rankResult)
		return m, nil
	}

	// Redaction runs before sanitization; both run before the text can reach
	// the clipboard or a file. redactForExport is the only producer of a
	// redactedText, so writePrivateReport can never receive raw text.
	body := redactForExport(rawText)

	// The clipboard has a hard ceiling; an oversized report is exported to a
	// private file rather than dropped.
	if len(body) > defaultMaxCopyBytes {
		path, err := saveReport(body)
		if err != nil {
			m.setStatus(copyFullReportFailed+err.Error(), rankResult)
			return m, nil
		}
		m.setStatus(copyFullReportSaved+path, rankResult)
		return m, nil
	}

	return m.dispatchCopy(body, copyFullReportNotice)
}
