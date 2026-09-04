package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// ScreenSnapshot represents a stable 2D terminal grid snapshot.
type ScreenSnapshot struct {
	Width  int
	Height int
	Rows   []string
	Cursor CursorPosition
}

// CursorPosition represents the cursor state on the screen grid.
type CursorPosition struct {
	Row     int
	Column  int
	Visible bool
}

// PlainRows returns the rows stripped of ANSI escape codes and right-trimmed of spaces.
func (s ScreenSnapshot) PlainRows() []string {
	out := make([]string, len(s.Rows))
	for i, r := range s.Rows {
		out[i] = strings.TrimRight(ansi.Strip(r), " ")
	}
	return out
}

// PlainText returns the full terminal screen as multi-line plain text.
func (s ScreenSnapshot) PlainText() string {
	return strings.Join(s.PlainRows(), "\n")
}

// Contains reports whether text appears in any row of the screen snapshot.
func (s ScreenSnapshot) Contains(text string) bool {
	return strings.Contains(s.PlainText(), text)
}

// RowContaining finds the first 0-indexed row containing text, or returns (-1, false).
func (s ScreenSnapshot) RowContaining(text string) (int, bool) {
	for i, r := range s.PlainRows() {
		if strings.Contains(r, text) {
			return i, true
		}
	}
	return -1, false
}

// ComposerRow returns the row containing composer visual indicators (› or > or paused indicator).
func (s ScreenSnapshot) ComposerRow() (int, bool) {
	for i, r := range s.PlainRows() {
		if strings.Contains(r, "›") || strings.Contains(r, ">") || strings.Contains(r, "type a message") || strings.Contains(r, "composer paused") || strings.Contains(r, "paused") {
			return i, true
		}
	}
	return -1, false
}

// FooterRow returns the row containing footer indicators (shortcuts or key hints).
func (s ScreenSnapshot) FooterRow() (int, bool) {
	for i, r := range s.PlainRows() {
		if strings.Contains(r, "Ctrl+") || strings.Contains(r, "send") || strings.Contains(r, "newline") || strings.Contains(r, "Enter") || strings.Contains(r, "^C") || strings.Contains(r, "y once") || strings.Contains(r, "y/a/n") {
			return i, true
		}
	}
	return -1, false
}

// ModalVisible reports whether the Permission Modal card is currently rendered.
func (s ScreenSnapshot) ModalVisible() bool {
	return s.Contains("Permission Required") || (s.Contains("Allow Once") && s.Contains("Deny"))
}

// PTYSession manages a real OS pseudo-terminal connected to an interactive Bubble Tea model,
// monitored via an in-memory VT10x terminal grid emulator.
type PTYSession struct {
	t        *testing.T
	Width    int
	Height   int
	Master   *os.File
	Slave    *os.File
	VT       vt10x.Terminal
	Feed     *Feed
	Program  *tea.Program
	Approver *TestApprover
	Runner   *TestPTYRunner

	mu              sync.Mutex
	closed          bool
	doneChan        chan error
	rawBytes        []byte
	primaryBytes    []byte
	altScreenEnters int
	altScreenExits  int
}

// TestApprover wraps Approver to record decisions and count.
type TestApprover struct {
	*Approver
	mu        sync.Mutex
	decisions []agent.Decision
	stopCh    chan struct{}
}

func newTestApprover() *TestApprover {
	ap := &TestApprover{
		Approver: NewApprover(),
		stopCh:   make(chan struct{}),
	}
	go func() {
		for {
			select {
			case d, ok := <-ap.Approver.reply:
				if !ok {
					return
				}
				ap.mu.Lock()
				ap.decisions = append(ap.decisions, d)
				ap.mu.Unlock()
			case <-ap.stopCh:
				return
			}
		}
	}()
	return ap
}

func (a *TestApprover) Count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.decisions)
}

func (a *TestApprover) Decisions() []agent.Decision {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make([]agent.Decision, len(a.decisions))
	copy(cp, a.decisions)
	return cp
}

func (a *TestApprover) LastDecision() (agent.Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.decisions) == 0 {
		return 0, fmt.Errorf("no decisions recorded")
	}
	return a.decisions[len(a.decisions)-1], nil
}

func (a *TestApprover) WaitCount(count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if a.Count() >= count {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %d decisions (got %d)", count, a.Count())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (a *TestApprover) Close() {
	select {
	case <-a.stopCh:
	default:
		close(a.stopCh)
	}
}

// TestPTYRunner is a deterministic runner stub for PTY tests.
type TestPTYRunner struct {
	mu             sync.Mutex
	executionCount int
	runs           []string
	err            error // returned by Run when set (failure injection)
}

func (r *TestPTYRunner) Run(ctx context.Context, text string) error {
	r.mu.Lock()
	r.executionCount++
	r.runs = append(r.runs, text)
	err := r.err
	r.mu.Unlock()
	return err
}

// FailWith makes every subsequent Run return err.
func (r *TestPTYRunner) FailWith(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func (r *TestPTYRunner) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.executionCount
}

// defaultFeedProgramOptions wires the Bubble Tea program options used by Feed.
// On the baseline before Gate F, this is empty (inline mode).
var defaultFeedProgramOptions []tea.ProgramOption

// StartPTYSession starts a real PTY session for UI testing with given dimensions.
func StartPTYSession(t *testing.T, width, height int, opts ...tea.ProgramOption) *PTYSession {
	return StartPTYSessionWithPrimaryMarker(t, width, height, "", opts...)
}

// StartPTYSessionWithPrimaryMarker starts a PTY session with pre-existing primary screen content.
func StartPTYSessionWithPrimaryMarker(t *testing.T, width, height int, marker string, opts ...tea.ProgramOption) *PTYSession {
	t.Helper()

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("failed to open pty: %v", err)
	}

	ws := &pty.Winsize{Rows: uint16(height), Cols: uint16(width)}
	if err := pty.Setsize(master, ws); err != nil {
		master.Close()
		slave.Close()
		t.Fatalf("failed to set pty size: %v", err)
	}

	vt := vt10x.New(vt10x.WithSize(width, height))
	if marker != "" {
		vt.Write([]byte(marker + "\r\n"))
	}

	feed := NewFeed()
	feed.width = width
	feed.height = height

	runner := &TestPTYRunner{}
	approver := newTestApprover()
	feed.SetRunner(runner)
	feed.SetApprover(approver.Approver)
	feed.Approve = approver.Approver

	allOpts := []tea.ProgramOption{
		tea.WithInput(slave),
		tea.WithOutput(slave),
	}
	if len(defaultFeedProgramOptions) > 0 {
		allOpts = append(allOpts, defaultFeedProgramOptions...)
	} else {
		allOpts = append(allOpts, feed.ProgramOptions()...)
	}
	allOpts = append(allOpts, opts...)

	prog := tea.NewProgram(
		feed,
		allOpts...,
	)
	feed.SetProgram(prog)

	sess := &PTYSession{
		t:        t,
		Width:    width,
		Height:   height,
		Master:   master,
		Slave:    slave,
		VT:       vt,
		Feed:     feed,
		Program:  prog,
		Approver: approver,
		Runner:   runner,
		doneChan: make(chan error, 1),
	}

	// Background reader: reads bytes emitted by Bubble Tea on Master and feeds VT emulator.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				sess.mu.Lock()
				sess.rawBytes = append(sess.rawBytes, chunk...)
				rawStr := string(sess.rawBytes)
				sess.altScreenEnters = strings.Count(rawStr, "\x1b[?1049h") +
					strings.Count(rawStr, "\x1b[?47h") +
					strings.Count(rawStr, "\x1b[?1047h")
				sess.altScreenExits = strings.Count(rawStr, "\x1b[?1049l") +
					strings.Count(rawStr, "\x1b[?47l") +
					strings.Count(rawStr, "\x1b[?1047l")

				wasAlt := sess.VT.Mode()&vt10x.ModeAltScreen != 0
				sess.VT.Write(chunk)
				isAlt := sess.VT.Mode()&vt10x.ModeAltScreen != 0

				if !wasAlt && !isAlt {
					sess.primaryBytes = append(sess.primaryBytes, chunk...)
				}
				sess.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// Run Bubble Tea program in background
	go func() {
		_, err := prog.Run()
		sess.doneChan <- err
	}()

	prog.Send(tea.WindowSizeMsg{Width: width, Height: height})

	t.Cleanup(func() {
		sess.Close()
	})

	// Wait for initial render
	err = sess.WaitForCondition("initial layout ready", 3*time.Second, func(snap ScreenSnapshot) bool {
		_, hasComp := snap.ComposerRow()
		return hasComp
	})
	if err != nil {
		t.Fatalf("failed waiting for initial render: %v", err)
	}

	return sess
}

// WriteString writes text into the PTY master.
func (s *PTYSession) WriteString(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	_, _ = s.Master.Write([]byte(text))
}

// SendKey sends byte slice as key input.
func (s *PTYSession) SendKey(key []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	_, _ = s.Master.Write(key)
}

// Resize changes the terminal size both at OS PTY level and inside Bubble Tea.
func (s *PTYSession) Resize(width, height int) {
	s.mu.Lock()
	s.Width = width
	s.Height = height
	_ = pty.Setsize(s.Master, &pty.Winsize{Rows: uint16(height), Cols: uint16(width)})
	s.VT.Resize(width, height)
	s.mu.Unlock()

	if s.Program != nil {
		s.Program.Send(tea.WindowSizeMsg{Width: width, Height: height})
	}
}

// InjectBatch delivers an event batch directly to the feed model through Bubble Tea's event loop.
func (s *PTYSession) InjectBatch(events []agent.Event) {
	s.Feed.SendBatch(events)
}

// Snapshot returns the current 2D screen state.
func (s *PTYSession) Snapshot() ScreenSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := s.VT.Cursor()
	screenStr := strings.TrimSuffix(s.VT.String(), "\n")
	rawRows := strings.Split(screenStr, "\n")

	// Guarantee len(Rows) == s.Height
	rows := make([]string, s.Height)
	for i := 0; i < s.Height; i++ {
		if i < len(rawRows) {
			rows[i] = rawRows[i]
		} else {
			rows[i] = strings.Repeat(" ", s.Width)
		}
	}

	return ScreenSnapshot{
		Width:  s.Width,
		Height: s.Height,
		Rows:   rows,
		Cursor: CursorPosition{
			Row:     cur.Y,
			Column:  cur.X,
			Visible: true,
		},
	}
}

// WaitForCondition polls the terminal snapshot until the condition is met or timeout expires.
func (s *PTYSession) WaitForCondition(
	description string,
	timeout time.Duration,
	condition func(ScreenSnapshot) bool,
) error {
	deadline := time.Now().Add(timeout)
	interval := 15 * time.Millisecond

	for {
		snap := s.Snapshot()
		if condition(snap) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %q after %v\nDimensions: %dx%d\nCursor: (row=%d, col=%d)\nPlain Screen:\n%s",
				description, timeout, snap.Width, snap.Height, snap.Cursor.Row, snap.Cursor.Column, snap.PlainText())
		}
		time.Sleep(interval)
	}
}

// WaitForText waits for specific text to appear anywhere on screen.
func (s *PTYSession) WaitForText(text string, timeout time.Duration) error {
	return s.WaitForCondition(fmt.Sprintf("text %q", text), timeout, func(snap ScreenSnapshot) bool {
		return snap.Contains(text)
	})
}

// IsAltScreenActive reports whether the terminal is currently in alternate screen mode.
func (s *PTYSession) IsAltScreenActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.VT.Mode()&vt10x.ModeAltScreen != 0
}

// AltScreenEnters returns the number of times alternate screen enter was requested.
func (s *PTYSession) AltScreenEnters() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.altScreenEnters
}

// AltScreenExits returns the number of times alternate screen exit was requested.
func (s *PTYSession) AltScreenExits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.altScreenExits
}

// RawBytes returns a copy of every byte the program wrote to the PTY so far,
// in order. This is the terminal's history as a byte stream (what a
// scrollback would have recorded), not the final rendered screen.
func (s *PTYSession) RawBytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(s.rawBytes))
	copy(cp, s.rawBytes)
	return cp
}

// PrimaryBytes returns a copy of raw bytes written while alternate screen was inactive.
func (s *PTYSession) PrimaryBytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]byte, len(s.primaryBytes))
	copy(cp, s.primaryBytes)
	return cp
}

// PrimaryHasFullFrame reports whether a full TUI frame was rendered while in primary screen mode.
func (s *PTYSession) PrimaryHasFullFrame() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	str := string(s.primaryBytes)
	return strings.Contains(str, "──") && strings.Contains(str, "Enter")
}

// Done returns a channel signaling program completion.
func (s *PTYSession) Done() <-chan error {
	return s.doneChan
}

// Close gracefully closes the session, terminating Bubble Tea and PTY descriptors.
func (s *PTYSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	if s.Approver != nil {
		s.Approver.Close()
	}
	if s.Program != nil {
		s.Program.Quit()
	}
	// Allow program loop to shut down cleanly
	select {
	case <-s.doneChan:
	case <-time.After(100 * time.Millisecond):
	}
	if s.Master != nil {
		_ = s.Master.Close()
	}
	if s.Slave != nil {
		_ = s.Slave.Close()
	}
}

// Assertions per specification 13.7 & 13.8

// assertComposerNearBottom verifies that composer is within maxDistanceFromBottom rows from terminal bottom.
func assertComposerNearBottom(
	t *testing.T,
	snapshot ScreenSnapshot,
	maxDistanceFromBottom int,
) {
	t.Helper()
	composerRow, ok := snapshot.ComposerRow()
	if !ok {
		t.Fatalf("composer not found in screen:\n%s", snapshot.PlainText())
	}

	dist := snapshot.Height - 1 - composerRow
	if dist > maxDistanceFromBottom {
		t.Fatalf("composer is too far from bottom: composerRow=%d, totalHeight=%d, distance=%d, max allowed=%d\nScreen:\n%s",
			composerRow, snapshot.Height, dist, maxDistanceFromBottom, snapshot.PlainText())
	}

	// Verify no feed message appears below composer
	plainRows := snapshot.PlainRows()
	for r := composerRow + 2; r < len(plainRows); r++ {
		row := plainRows[r]
		if row == "" {
			continue
		}
		if strings.Contains(row, "Feed line") || strings.Contains(row, "bash output") || strings.Contains(row, "Long output") {
			t.Fatalf("feed content found below composer at row %d: %q\nScreen:\n%s", r, row, snapshot.PlainText())
		}
	}
}

// assertScreenBounds verifies that the snapshot conforms to strict terminal grid invariants.
func assertScreenBounds(
	t *testing.T,
	snapshot ScreenSnapshot,
) {
	t.Helper()
	if len(snapshot.Rows) != snapshot.Height {
		t.Fatalf("row count mismatch: got %d rows, want %d", len(snapshot.Rows), snapshot.Height)
	}

	for i, r := range snapshot.Rows {
		w := ansi.StringWidth(ansi.Strip(r))
		if w > snapshot.Width {
			t.Fatalf("row %d width %d exceeds terminal width %d: %q", i, w, snapshot.Width, r)
		}
	}

	if snapshot.Cursor.Visible {
		if snapshot.Cursor.Row < 0 || snapshot.Cursor.Row >= snapshot.Height {
			t.Fatalf("cursor row %d out of bounds [0, %d)", snapshot.Cursor.Row, snapshot.Height)
		}
		if snapshot.Cursor.Column < 0 || snapshot.Cursor.Column > snapshot.Width {
			t.Fatalf("cursor col %d out of bounds [0, %d]", snapshot.Cursor.Column, snapshot.Width)
		}
	}
}

// waitForPermissionModal waits for permission modal card to become visible on the screen grid.
func waitForPermissionModal(
	session *PTYSession,
	timeout time.Duration,
) (ScreenSnapshot, error) {
	var lastSnap ScreenSnapshot
	err := session.WaitForCondition("permission modal", timeout, func(snap ScreenSnapshot) bool {
		lastSnap = snap
		return snap.ModalVisible()
	})
	return lastSnap, err
}
