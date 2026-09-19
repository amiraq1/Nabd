package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

// decodeOSC52Payload extracts and decodes the base64 payload from an OSC 52 sequence.
func decodeOSC52Payload(t *testing.T, osc52 string) string {
	t.Helper()
	const prefix = "\x1b]52;c;"
	const suffix = "\x1b\\"
	if !strings.HasPrefix(osc52, prefix) || !strings.HasSuffix(osc52, suffix) {
		t.Fatalf("malformed OSC 52 sequence: %q", osc52)
	}
	payload := osc52[len(prefix) : len(osc52)-len(suffix)]
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("failed to decode base64 payload %q: %v", payload, err)
	}
	return string(decoded)
}

func TestCopyRedactsRecognizedCredentials(t *testing.T) {
	testCases := []struct {
		name   string
		secret string
	}{
		{"Bearer token", "Bearer abcdefgh12345678"},
		{"authorization header", "authorization: abcdefgh12345678"},
		{"GitHub PAT", "github_pat_abcdefghijklmnop12345678"},
		{"GitHub token", "ghp_abcdefghijklmnop12345678"},
		{"Groq key", "gsk_abcdefgh12345678"},
		{"NVIDIA key", "nvapi-abcdefgh12345678"},
		{"OpenRouter key", "sk-or-abcdefgh12345678"},
		{"Slack token", "xoxb-abcdefghijklmnop12345678"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cardText := fmt.Sprintf("output with credential: %s and other text", tc.secret)
			m := feedWithCustomTexts(t, []string{cardText}, 100)
			m.enterNavigation()
			m.selectItem(0)

			var buf bytes.Buffer
			m.SetClipboardWriter(&buf)

			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			if cmd != nil {
				t.Fatal("copy returned non-nil cmd")
			}

			if buf.Len() == 0 {
				t.Fatal("no OSC 52 sequence written to clipboard")
			}

			decoded := decodeOSC52Payload(t, buf.String())

			// The raw secret MUST NOT be in the clipboard payload
			if strings.Contains(decoded, tc.secret) {
				t.Fatalf("clipboard leaked raw secret %q in %q", tc.secret, decoded)
			}
			// Redacted token MUST be in the clipboard payload
			if !strings.Contains(decoded, "[REDACTED]") {
				t.Fatalf("clipboard payload missing [REDACTED] in %q", decoded)
			}

			// Status line must NEVER display the copied text or secret
			if strings.Contains(m.status, tc.secret) || strings.Contains(m.status, "output with credential") {
				t.Fatalf("status line leaked copied content: %q", m.status)
			}
			if m.status != copySuccessNotice {
				t.Fatalf("status = %q, want %q", m.status, copySuccessNotice)
			}
		})
	}
}

func TestCopyNeverUsesRawJournalContent(t *testing.T) {
	// Raw unprojected string in event call ID or raw unprojected fields
	const unprojectedRaw = "RAW_UNPROJECTED_JOURNAL_CONTENT_XYZ_999"

	m := NewFeed()
	m.width = 80
	m.height = 20
	// Apply an event with a clean display output, but unprojected raw data
	m.applyBatch([]agent.Event{
		{
			Seq:  1,
			Type: agent.ToolStart,
			Call: &agent.ToolCall{ID: "call-1", Name: "bash"},
		},
		{
			Seq:  2,
			Type: agent.ToolEnd,
			Call: &agent.ToolCall{ID: "call-1", Name: "bash", Output: "projected display line", OK: true},
		},
	})
	m.toolsExpanded = true
	m.refresh()

	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	decoded := decodeOSC52Payload(t, buf.String())

	if strings.Contains(decoded, unprojectedRaw) {
		t.Fatalf("clipboard leaked unprojected raw content: %q", decoded)
	}
	if !strings.Contains(decoded, "projected display line") {
		t.Fatalf("clipboard missing projected display line: %q", decoded)
	}
}

func TestCopyRejectsRawErrorBodies(t *testing.T) {
	const rawStackOrBody = "RAW_INTERNAL_STACK_TRACE_HTTP_500_BODY_SECRET"

	m := NewFeed()
	m.width = 80
	m.height = 20
	// Add an error notice where the raw body contains unprojected details
	m.applyBatch([]agent.Event{
		{
			Seq:        1,
			Type:       agent.RunError,
			Err:        "sanitized error description: connection failed",
			RawMessage: rawStackOrBody,
		},
	})
	m.refresh()

	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	decoded := decodeOSC52Payload(t, buf.String())

	if strings.Contains(decoded, rawStackOrBody) {
		t.Fatalf("clipboard leaked raw error body: %q", decoded)
	}
	if !strings.Contains(decoded, "connection failed") {
		t.Fatalf("clipboard missing projected error text: %q", decoded)
	}
}

func TestCopyIsBlockedByPermissionModal(t *testing.T) {
	m := feedWithPendingPermission(t, 80)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	// Attempt to copy using 'y'
	_, cmd1 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	// In permission modal, 'y' means AllowOnce! It must answer the modal, NOT copy!
	if buf.Len() > 0 {
		t.Fatal("copy wrote to clipboard while permission modal was active")
	}

	// Attempt to copy using 'c'
	var buf2 bytes.Buffer
	m2 := feedWithPendingPermission(t, 80)
	m2.SetClipboardWriter(&buf2)

	_, cmd2 := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd2 != nil {
		t.Fatal("unexpected cmd from 'c' during modal")
	}
	if buf2.Len() > 0 {
		t.Fatal("copy wrote to clipboard during permission modal")
	}
	_ = cmd1
}

func TestCopyNeverExecutesACommand(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"executable content test"}, 80)
	m.enterNavigation()
	m.selectItem(0)

	m.running = false
	m.busy = false
	m.runningTool = ""

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if m.running || m.busy || m.runningTool != "" {
		t.Fatal("copy triggered command execution or set running/busy state")
	}
}

func TestCopyReturnsNoToolCommand(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"card output"}, 80)
	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd != nil {
		t.Fatal("copy returned a non-nil tea.Cmd")
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil {
		t.Fatal("copy returned a non-nil tea.Cmd")
	}
}

func TestCopyDoesNotMutateProjection(t *testing.T) {
	m := feedWithCustomTexts(t, []string{"alpha card", "beta card"}, 80)
	before := m.proj.Items()
	fps := make([]uint64, len(before))
	for i, it := range before {
		fps[i] = it.Fingerprint()
	}

	m.enterNavigation()
	m.selectItem(0)

	var buf bytes.Buffer
	m.SetClipboardWriter(&buf)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	after := m.proj.Items()
	if len(after) != len(before) {
		t.Fatalf("projector item count changed: %d -> %d", len(before), len(after))
	}
	for i, it := range after {
		if it.Fingerprint() != fps[i] {
			t.Fatalf("projector item %d mutated by copy", i)
		}
	}
}

func TestCopyTermuxAsynchronousCmd(t *testing.T) {
	t.Setenv("TERMUX_VERSION", "0.119.0")
	t.Setenv("SSH_CONNECTION", "")

	m := feedWithCustomTexts(t, []string{"termux card output"}, 80)
	m.enterNavigation()
	m.selectItem(0)

	// Inject a harmless command so the test never runs the real
	// termux-clipboard-set: it is absent outside Termux and can block for
	// seconds on a device whose Termux:API app is cold, making the test
	// depend on the host rather than on copySelectedCard.
	m.clipboardCommand = "cat"

	// In Termux without a mock writer, copySelectedCard returns an async cmd
	_, cmd := m.copySelectedCard()
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd in Termux environment")
	}

	// Executing the cmd yields a clipboardResultMsg
	msg := cmd()
	res, ok := msg.(clipboardResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want clipboardResultMsg", msg)
	}
	if res.notice != copySuccessNotice {
		t.Fatalf("notice = %q, want %q", res.notice, copySuccessNotice)
	}

	// Delivering the message to Update sets the status
	m.Update(res)
	if res.err != nil {
		if !strings.HasPrefix(m.status, "copy failed: ") {
			t.Fatalf("status = %q, want prefix 'copy failed: '", m.status)
		}
	} else {
		if m.status != copySuccessNotice {
			t.Fatalf("status = %q, want %q", m.status, copySuccessNotice)
		}
	}
}

func TestCopyUnavailableOutsideTermuxAndSSH(t *testing.T) {
	t.Setenv("TERMUX_VERSION", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("PREFIX", "")

	m := feedWithCustomTexts(t, []string{"card output"}, 80)
	m.enterNavigation()
	m.selectItem(0)

	_, cmd := m.copySelectedCard()
	if cmd != nil {
		t.Fatal("expected nil cmd when clipboard is unavailable")
	}
	if m.status != copyUnavailableNotice {
		t.Fatalf("status = %q, want %q", m.status, copyUnavailableNotice)
	}
}

// TestCopyTermuxDetectedByPrefixOnly pins the fallback added because
// TERMUX_VERSION is not inherited by every child process on a real device:
// without it a valid Termux install with an empty TERMUX_VERSION is mistaken
// for "copy unavailable" and never reaches termux-clipboard-set.
func TestCopyTermuxDetectedByPrefixOnly(t *testing.T) {
	t.Setenv("TERMUX_VERSION", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("PREFIX", "/data/data/com.termux/files/usr")

	m := feedWithCustomTexts(t, []string{"termux card output"}, 80)
	m.enterNavigation()
	m.selectItem(0)
	m.clipboardCommand = "cat"

	_, cmd := m.copySelectedCard()
	if cmd == nil {
		t.Fatal("expected non-nil cmd when only PREFIX identifies Termux")
	}
	if m.status == copyUnavailableNotice {
		t.Fatalf("status = %q, want a copy attempt, not unavailable", m.status)
	}
}

// TestCopyFailureRescuesTextToFile covers the failure path that used to lose
// the user's text outright: the clipboard command cannot run, so the
// already-redacted body is written to the export directory and the status line
// says where it went. The clipboard is best-effort; the text is not.
func TestCopyFailureRescuesTextToFile(t *testing.T) {
	t.Setenv("TERMUX_VERSION", "0.119.0")
	t.Setenv("SSH_CONNECTION", "")
	dir := t.TempDir()
	t.Setenv(ExportsDirEnv, dir)

	secret := "ghp_abcdefghijklmnop12345678"
	m := feedWithCustomTexts(t, []string{"output with credential: " + secret + " and other text"}, 100)
	m.enterNavigation()
	m.selectItem(0)
	// A command that cannot exist, so the failure path is exercised without
	// depending on whether the real binary is installed on this host.
	m.clipboardCommand = "/nonexistent-clipboard-binary"

	_, cmd := m.copySelectedCard()
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd in Termux environment")
	}
	res, ok := cmd().(clipboardResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want clipboardResultMsg", cmd())
	}
	if res.err == nil {
		t.Fatal("expected the injected command to fail")
	}
	if res.detail != copyCommandMissingNotice {
		t.Fatalf("detail = %q, want %q", res.detail, copyCommandMissingNotice)
	}

	if _, cmd2 := m.Update(res); cmd2 != nil {
		t.Fatal("clipboardResultMsg must not schedule further work")
	}

	if !strings.HasPrefix(m.status, "copy failed: ") {
		t.Fatalf("status = %q, want prefix 'copy failed: '", m.status)
	}
	if !strings.Contains(m.status, copyFullReportSaved) {
		t.Fatalf("status = %q, want it to name the saved report", m.status)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read exports dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("exported %d files, want exactly 1", len(entries))
	}
	path := filepath.Join(dir, entries[0].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exported report: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("exported report leaked the credential: %q", string(data))
	}
	if !strings.Contains(string(data), "output with credential:") {
		t.Fatalf("exported report lost the card text: %q", string(data))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatalf("stat exported report: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("exported report mode = %o, want 600", perm)
	}
}

// captureClipboardCommand returns an executable that copies its own stdin to
// the file named by CLIP_CAPTURE. It exists because the other Termux tests only
// prove the command *ran*: none of them inspects what reached its stdin, which
// is exactly how an empty payload can pass while the clipboard ends up empty.
func captureClipboardCommand(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH to host the capture command: %v", err)
	}
	script := filepath.Join(t.TempDir(), "capture-clipboard")
	// The shebang names the shell resolved on this host, so the test runs the
	// same way on Linux and under Termux ($PREFIX/bin/sh is not /bin/sh).
	body := "#!" + sh + "\ncat > \"$CLIP_CAPTURE\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

// TestCopyTermuxSendsNonEmptyPayloadToStdin pins the payload itself, not just
// the execution: the bytes handed to the clipboard command must be non-empty
// and must carry the card text. A copy that runs successfully while sending
// zero bytes is the failure this guards.
func TestCopyTermuxSendsNonEmptyPayloadToStdin(t *testing.T) {
	t.Setenv("TERMUX_VERSION", "0.119.0")
	t.Setenv("SSH_CONNECTION", "")
	out := filepath.Join(t.TempDir(), "stdin.bin")
	t.Setenv("CLIP_CAPTURE", out)

	script := captureClipboardCommand(t)

	card := "card line one\ncard line two with arabic: مرحبا بالعالم"
	m := feedWithCustomTexts(t, []string{card}, 100)
	m.enterNavigation()
	m.selectItem(0)
	m.clipboardCommand = script

	_, cmd := m.copySelectedCard()
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd in Termux environment")
	}
	res, ok := cmd().(clipboardResultMsg)
	if !ok {
		t.Fatal("cmd did not return clipboardResultMsg")
	}
	if res.err != nil {
		t.Fatalf("capture command failed: %v", res.err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("copy sent zero bytes to the clipboard command's stdin")
	}
	if !strings.Contains(string(data), "card line one") {
		t.Fatalf("captured stdin = %q, want it to contain the card text", string(data))
	}
}
