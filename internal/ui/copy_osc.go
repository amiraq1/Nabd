package ui

import (
	"errors"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// clipboardModeEnv selects the Termux clipboard transport:
//
//	unset / "auto" -> OSC 52 first: no termux-api dependency, no subprocess
//	"exec"         -> termux-clipboard-set only: slower, but exit-code verifiable
//	"osc"          -> OSC 52 only, never spawn a process
const clipboardModeEnv = "NABD_CLIPBOARD"

func clipboardMode() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv(clipboardModeEnv)))
}

// errEmptyClipboardPayload keeps the failure path typed instead of reporting
// a nil error alongside a failure notice.
var errEmptyClipboardPayload = errors.New("empty clipboard payload")

// osc52Cmd writes the OSC 52 sequence off the Update path and reports through
// clipboardResultMsg, like every other transport.
//
// What err == nil means here: the bytes left the process. It does NOT mean the
// terminal accepted them. OSC 52 is write-only and unacknowledged, so this
// transport trades the exec path's verifiable exit code for speed and for
// independence from the termux-api package.
//
// Residual N4 race: the program is built without tea.WithOutput, so the
// renderer owns os.Stdout as well. The payload is emitted in a single
// io.WriteString call so it cannot be split across a frame boundary by this
// code; bubbletea v1 exposes no renderer-owned clipboard channel, so the race
// is narrowed, not eliminated. Closing it fully requires bubbletea v2.
func osc52Cmd(w io.Writer, body redactedText, notice string) tea.Cmd {
	return func() tea.Msg {
		res := clipboardResultMsg{notice: notice, body: body}
		seq, err := encodeOSC52(string(body), defaultMaxCopyBytes)
		if err != nil || seq == "" {
			res.err = err
			if res.err == nil {
				res.err = errEmptyClipboardPayload
			}
			res.detail = copyUnavailableNotice
			return res
		}
		if _, err := io.WriteString(w, seq); err != nil {
			res.err = err
			res.detail = copyUnavailableNotice
		}
		return res
	}
}

// termuxClipboardCmd picks the transport for a Termux session. An explicitly
// injected clipboardCommand always wins: tests depend on it, and an operator
// who set it meant it.
func (m *Feed) termuxClipboardCmd(name string, body redactedText, notice string) tea.Cmd {
	if m.clipboardCommand != "" || clipboardMode() == "exec" {
		return copyCmd(name, body, notice)
	}
	w := m.clipboardWriter
	if w == nil {
		w = os.Stdout
	}
	return osc52Cmd(w, body, notice)
}
