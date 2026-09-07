package ui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"nabd/internal/agent"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	downRawSample = []byte{0x1b, 0x5b, 0x3c, 0xd9, 0xa6, 0xd9, 0xa5, 0x3b, 0xd9, 0xa4, 0xd9, 0xa2, 0x3b, 0xd9, 0xa1, 0xd9, 0xa8, 0x4d}
	upRawSample   = []byte{0x1b, 0x5b, 0x3c, 0xd9, 0xa6, 0xd9, 0xa4, 0x3b, 0xd9, 0xa4, 0xd9, 0xa3, 0x3b, 0xd9, 0xa1, 0xd9, 0xa0, 0x4d}
)

type msgCollectorModel struct {
	msgs []tea.Msg
	mu   sync.Mutex
}

func (m *msgCollectorModel) Init() tea.Cmd { return nil }
func (m *msgCollectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.mu.Lock()
	m.msgs = append(m.msgs, msg)
	m.mu.Unlock()
	return m, nil
}
func (m *msgCollectorModel) View() string { return "" }

func (m *msgCollectorModel) getMsgs() []tea.Msg {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]tea.Msg, len(m.msgs))
	copy(res, m.msgs)
	return res
}

type oneByteStreamReader struct {
	data []byte
	pos  int
}

func (r *oneByteStreamReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

// TestSGRNormalizer_ExactCapturedReports tests normalization of the exact captured Termux samples.
func TestSGRNormalizer_ExactCapturedReports(t *testing.T) {
	t.Run("down sample to ASCII", func(t *testing.T) {
		norm := NewSGRNormalizer(bytes.NewReader(downRawSample))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		expected := "\x1b[<65;42;18M"
		if string(out) != expected {
			t.Fatalf("expected %q, got %q", expected, string(out))
		}
	})

	t.Run("up sample to ASCII", func(t *testing.T) {
		norm := NewSGRNormalizer(bytes.NewReader(upRawSample))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		expected := "\x1b[<64;43;10M"
		if string(out) != expected {
			t.Fatalf("expected %q, got %q", expected, string(out))
		}
	})

	t.Run("release event lower m", func(t *testing.T) {
		releaseRaw := bytes.Clone(downRawSample)
		releaseRaw[len(releaseRaw)-1] = 'm'
		norm := NewSGRNormalizer(bytes.NewReader(releaseRaw))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected read error: %v", err)
		}
		expected := "\x1b[<65;42;18m"
		if string(out) != expected {
			t.Fatalf("expected %q, got %q", expected, string(out))
		}
	})

	t.Run("real decoding down sample through Bubble Tea", func(t *testing.T) {
		model := &msgCollectorModel{}
		norm := NewSGRNormalizer(bytes.NewReader(downRawSample))
		p := tea.NewProgram(model, tea.WithInput(norm), tea.WithOutput(io.Discard))

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		go func() {
			<-ctx.Done()
			p.Quit()
		}()
		_, _ = p.Run()

		msgs := model.getMsgs()
		var mouseMsgs []tea.MouseMsg
		for _, m := range msgs {
			if mm, ok := m.(tea.MouseMsg); ok {
				mouseMsgs = append(mouseMsgs, mm)
			}
		}
		if len(mouseMsgs) != 1 {
			t.Fatalf("expected 1 MouseMsg, got %d (all msgs: %v)", len(mouseMsgs), msgs)
		}
		mm := mouseMsgs[0]
		if mm.Button != tea.MouseButtonWheelDown {
			t.Errorf("expected MouseButtonWheelDown, got %v", mm.Button)
		}
		if mm.X != 41 || mm.Y != 17 {
			t.Errorf("expected 0-indexed coords (41, 17), got (%d, %d)", mm.X, mm.Y)
		}
	})

	t.Run("real decoding up sample through Bubble Tea", func(t *testing.T) {
		model := &msgCollectorModel{}
		norm := NewSGRNormalizer(bytes.NewReader(upRawSample))
		p := tea.NewProgram(model, tea.WithInput(norm), tea.WithOutput(io.Discard))

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		go func() {
			<-ctx.Done()
			p.Quit()
		}()
		_, _ = p.Run()

		msgs := model.getMsgs()
		var mouseMsgs []tea.MouseMsg
		for _, m := range msgs {
			if mm, ok := m.(tea.MouseMsg); ok {
				mouseMsgs = append(mouseMsgs, mm)
			}
		}
		if len(mouseMsgs) != 1 {
			t.Fatalf("expected 1 MouseMsg, got %d (all msgs: %v)", len(mouseMsgs), msgs)
		}
		mm := mouseMsgs[0]
		if mm.Button != tea.MouseButtonWheelUp {
			t.Errorf("expected MouseButtonWheelUp, got %v", mm.Button)
		}
		if mm.X != 42 || mm.Y != 9 {
			t.Errorf("expected 0-indexed coords (42, 9), got (%d, %d)", mm.X, mm.Y)
		}
	})
}

// TestSGRNormalizer_ASCIIReports verifies that existing ASCII reports are preserved unchanged.
func TestSGRNormalizer_ASCIIReports(t *testing.T) {
	downAscii := []byte("\x1b[<65;42;18M")
	upAscii := []byte("\x1b[<64;43;10M")

	normDown := NewSGRNormalizer(bytes.NewReader(downAscii))
	outDown, err := io.ReadAll(normDown)
	if err != nil || string(outDown) != string(downAscii) {
		t.Fatalf("expected %q, got %q (err: %v)", string(downAscii), string(outDown), err)
	}

	normUp := NewSGRNormalizer(bytes.NewReader(upAscii))
	outUp, err := io.ReadAll(normUp)
	if err != nil || string(outUp) != string(upAscii) {
		t.Fatalf("expected %q, got %q (err: %v)", string(upAscii), string(outUp), err)
	}
}

// TestSGRNormalizer_Fragmentation tests fragmentation across chunk and byte boundaries.
func TestSGRNormalizer_Fragmentation(t *testing.T) {
	t.Run("every 2-chunk cut boundary", func(t *testing.T) {
		for cut := 0; cut <= len(downRawSample); cut++ {
			c1 := downRawSample[:cut]
			c2 := downRawSample[cut:]
			norm := NewSGRNormalizer(io.MultiReader(bytes.NewReader(c1), bytes.NewReader(c2)))
			out, err := io.ReadAll(norm)
			if err != nil {
				t.Fatalf("cut %d returned error: %v", cut, err)
			}
			if string(out) != "\x1b[<65;42;18M" {
				t.Fatalf("cut %d produced %q, expected %q", cut, string(out), "\x1b[<65;42;18M")
			}
		}
	})

	t.Run("every byte as 1-byte read", func(t *testing.T) {
		norm := NewSGRNormalizer(&oneByteStreamReader{data: downRawSample})
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("1-byte read error: %v", err)
		}
		if string(out) != "\x1b[<65;42;18M" {
			t.Fatalf("expected %q, got %q", "\x1b[<65;42;18M", string(out))
		}
	})

	t.Run("split UTF-8 multibyte boundary", func(t *testing.T) {
		// Cut right between 0xd9 and 0xa6 of first digit ٦
		idx := bytes.Index(downRawSample, []byte{0xd9, 0xa6})
		if idx == -1 {
			t.Fatal("0xd9 0xa6 not found in sample")
		}
		c1 := downRawSample[:idx+1] // ends with 0xd9
		c2 := downRawSample[idx+1:] // starts with 0xa6
		norm := NewSGRNormalizer(io.MultiReader(bytes.NewReader(c1), bytes.NewReader(c2)))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(out) != "\x1b[<65;42;18M" {
			t.Fatalf("expected %q, got %q", "\x1b[<65;42;18M", string(out))
		}
	})
}

// TestSGRNormalizer_ConcatenatedAndSurrounded verifies multiple reports and text surrounding them.
func TestSGRNormalizer_ConcatenatedAndSurrounded(t *testing.T) {
	t.Run("concatenated reports", func(t *testing.T) {
		both := append(append([]byte{}, downRawSample...), upRawSample...)
		norm := NewSGRNormalizer(bytes.NewReader(both))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "\x1b[<65;42;18M\x1b[<64;43;10M"
		if string(out) != expected {
			t.Fatalf("expected %q, got %q", expected, string(out))
		}
	})

	t.Run("surrounded by text", func(t *testing.T) {
		combined := []byte("prefix-")
		combined = append(combined, downRawSample...)
		combined = append(combined, []byte("-middle-")...)
		combined = append(combined, upRawSample...)
		combined = append(combined, []byte("-suffix")...)

		norm := NewSGRNormalizer(bytes.NewReader(combined))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "prefix-\x1b[<65;42;18M-middle-\x1b[<64;43;10M-suffix"
		if string(out) != expected {
			t.Fatalf("expected %q, got %q", expected, string(out))
		}
	})
}

// TestSGRNormalizer_PreservesOrdinaryInput ensures ordinary Arabic text and digits are not transliterated.
func TestSGRNormalizer_PreservesOrdinaryInput(t *testing.T) {
	t.Run("ordinary Arabic text with Arabic digits", func(t *testing.T) {
		arabic := []byte("مرحبا بكم في نبض: التكلفة ١٠٠ دولار والعدد ٥٠ قطعة")
		norm := NewSGRNormalizer(bytes.NewReader(arabic))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(out) != string(arabic) {
			t.Fatalf("Arabic text was modified! Expected %q, got %q", string(arabic), string(out))
		}
	})

	t.Run("bracketed paste with Arabic text and digits", func(t *testing.T) {
		paste := []byte("\x1b[200~مرحبا ١٢٣ و 456\x1b[201~")
		norm := NewSGRNormalizer(bytes.NewReader(paste))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(out) != string(paste) {
			t.Fatalf("bracketed paste was modified! Expected %q, got %q", string(paste), string(out))
		}
	})

	t.Run("keyboard shortcuts and control characters", func(t *testing.T) {
		shortcuts := []byte("\x1b[A\x1b[B\x1b[5~\x1b[6~\x1b\r\x03\x04\n\t")
		norm := NewSGRNormalizer(bytes.NewReader(shortcuts))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(out) != string(shortcuts) {
			t.Fatalf("shortcuts were modified! Expected %q, got %q", string(shortcuts), string(out))
		}
	})
}

// TestSGRNormalizer_MalformedAndBounds checks safety against malformed input and buffer limits.
func TestSGRNormalizer_MalformedAndBounds(t *testing.T) {
	t.Run("non-numeric parameters flushed as raw bytes", func(t *testing.T) {
		malformed := []byte("\x1b[<invalid;text;hereM")
		norm := NewSGRNormalizer(bytes.NewReader(malformed))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(out) != string(malformed) {
			t.Fatalf("expected raw pass-through %q, got %q", string(malformed), string(out))
		}
	})

	t.Run("buffer limit bounding prevents unbounded growth", func(t *testing.T) {
		long := []byte("\x1b[<" + strings.Repeat("1", maxSGRBuffer+20) + "M")
		norm := NewSGRNormalizer(bytes.NewReader(long))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(out) != string(long) {
			t.Fatalf("expected raw pass-through for oversized sequence")
		}
	})

	t.Run("incomplete sequence flushed at EOF", func(t *testing.T) {
		incomplete := []byte("\x1b[<٦٥;")
		norm := NewSGRNormalizer(bytes.NewReader(incomplete))
		out, err := io.ReadAll(norm)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(out) != string(incomplete) {
			t.Fatalf("expected incomplete sequence flushed as raw bytes %q, got %q", string(incomplete), string(out))
		}
	})
}

// TestFeed_TouchScrollingLogic verifies Feed scrolling, follow/unseen toggles, and bounds.
func TestFeed_TouchScrollingLogic(t *testing.T) {
	f := NewFeed()
	f.SetTouch(true)
	f.width = 80
	f.height = 24

	// Populate feed with 40 rendered lines.
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("Line %02d", i))
	}
	f.lines = lines

	lm := f.computeLayout()
	if lm.ViewportRows <= 0 {
		t.Fatalf("expected positive ViewportRows, got %d", lm.ViewportRows)
	}
	bs := f.bottomStart(lm.ViewportRows)
	f.scrollTop = bs
	f.follow = true
	f.unseen = 0

	vpMidY := lm.HeaderRows + lm.ViewportRows/2

	// 1. Wheel Up scrolls upward by 3 lines and disables follow.
	wheelUpMsg := tea.MouseMsg{
		X:      10,
		Y:      vpMidY,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}
	_, _ = f.Update(wheelUpMsg)
	if f.follow {
		t.Errorf("expected follow to be false after scrolling up")
	}
	expectedScroll := bs - 3
	if f.scrollTop != expectedScroll {
		t.Errorf("expected scrollTop %d, got %d", expectedScroll, f.scrollTop)
	}

	// 2. Further Wheel Ups decrement by 3 until 0.
	for i := 0; i < 20; i++ {
		_, _ = f.Update(wheelUpMsg)
	}
	if f.scrollTop != 0 {
		t.Errorf("expected scrollTop clamped to 0, got %d", f.scrollTop)
	}

	// 3. Wheel Down scrolls downward by 3 lines.
	wheelDownMsg := tea.MouseMsg{
		X:      10,
		Y:      vpMidY,
		Button: tea.MouseButtonWheelDown,
		Action: tea.MouseActionPress,
	}
	f.unseen = 5
	_, _ = f.Update(wheelDownMsg)
	if f.scrollTop != 3 {
		t.Errorf("expected scrollTop 3 after scrolling down from 0, got %d", f.scrollTop)
	}

	// 4. Scrolling all the way to the bottom restores follow and clears unseen.
	for i := 0; i < 20; i++ {
		_, _ = f.Update(wheelDownMsg)
	}
	if f.scrollTop != bs {
		t.Errorf("expected scrollTop %d (bottomStart), got %d", bs, f.scrollTop)
	}
	if !f.follow {
		t.Errorf("expected follow to be true after reaching bottom")
	}
	if f.unseen != 0 {
		t.Errorf("expected unseen to be 0 after reaching bottom, got %d", f.unseen)
	}
}

// TestFeed_TouchHitTestingAndModalSafety verifies gesture boundaries and modal isolation.
func TestFeed_TouchHitTestingAndModalSafety(t *testing.T) {
	f := NewFeed()
	f.SetTouch(true)
	f.width = 80
	f.height = 24

	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("Line %02d", i))
	}
	f.lines = lines

	lm := f.computeLayout()
	bs := f.bottomStart(lm.ViewportRows)
	f.scrollTop = bs
	f.follow = true

	// 1. Gesture over composer row (below viewport) is ignored.
	composerY := lm.HeaderRows + lm.ViewportRows + 2
	compMsg := tea.MouseMsg{
		X:      10,
		Y:      composerY,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}
	_, _ = f.Update(compMsg)
	if f.scrollTop != bs || !f.follow {
		t.Errorf("gesture over composer modified scroll state: scrollTop=%d, follow=%v", f.scrollTop, f.follow)
	}

	// 2. Gesture over footer row is ignored.
	footerY := f.height - 1
	footerMsg := tea.MouseMsg{
		X:      10,
		Y:      footerY,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}
	_, _ = f.Update(footerMsg)
	if f.scrollTop != bs || !f.follow {
		t.Errorf("gesture over footer modified scroll state: scrollTop=%d, follow=%v", f.scrollTop, f.follow)
	}

	// 3. Gesture while permission modal is visible is ignored.
	f.modalVisible = true
	vpMidY := lm.HeaderRows + lm.ViewportRows/2
	modalMsg := tea.MouseMsg{
		X:      10,
		Y:      vpMidY,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	}
	_, _ = f.Update(modalMsg)
	if f.scrollTop != bs {
		t.Errorf("gesture while modal visible modified scrollTop: got %d", f.scrollTop)
	}
	f.modalVisible = false

	// 4. Gesture while decision is pending is ignored.
	f.decisionPending = true
	_, _ = f.Update(modalMsg)
	if f.scrollTop != bs {
		t.Errorf("gesture while decision pending modified scrollTop: got %d", f.scrollTop)
	}
	f.decisionPending = false

	// 5. When touch is disabled, mouse events are ignored.
	f.SetTouch(false)
	_, _ = f.Update(modalMsg)
	if f.scrollTop != bs {
		t.Errorf("gesture while touch disabled modified scrollTop: got %d", f.scrollTop)
	}
	f.SetTouch(true)

	// 6. Non-wheel mouse clicks or motion are ignored.
	clickMsg := tea.MouseMsg{
		X:      10,
		Y:      vpMidY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	}
	_, _ = f.Update(clickMsg)
	if f.scrollTop != bs || !f.follow {
		t.Errorf("click modified scroll state: scrollTop=%d, follow=%v", f.scrollTop, f.follow)
	}
}

// TestPTYTouchScrolling verifies real live terminal behavior with raw SGR byte injection.
func TestPTYTouchScrolling(t *testing.T) {
	sess := StartPTYSessionWithTouch(t, 80, 24)

	// Inject 60 lines of synthetic content to require scrolling.
	var events []agent.Event
	events = append(events, agent.Event{Seq: 1, Type: agent.RunStart})
	for i := 1; i <= 60; i++ {
		events = append(events, agent.Event{
			Seq:  i + 1,
			Type: agent.UserMsg,
			Text: fmt.Sprintf("Item %02d: touch scrolling regression test", i),
		})
	}
	sess.InjectBatch(events)

	err := sess.WaitForText("Item 60", 3*time.Second)
	if err != nil {
		t.Fatalf("initial feed lines did not render: %v", err)
	}

	snapBefore := sess.Snapshot()
	assertComposerNearBottom(t, snapBefore, 3)

	// Inject raw up-swipe SGR report (ESC[<٦٤;٤٣;١٠M).
	// On Terminal, wheel up moves view upward (reveals older lines, scrollTop decreases).
	_, err = sess.Master.Write(upRawSample)
	if err != nil {
		t.Fatalf("failed to write raw up-wheel report to master: %v", err)
	}

	// Verify that the view scrolled up and visible frame changed without data race.
	err = sess.WaitForCondition("scrolled upward after swipe", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("updates available") || !snap.Contains("Item 60")
	})
	if err != nil {
		t.Fatalf("expected follow to be false after swipe up: %v", err)
	}

	// Crucial assertion: Verify no escape sequence fragments leaked into the composer!
	snapScrolled := sess.Snapshot()
	if compRow, ok := snapScrolled.ComposerRow(); ok {
		rowText := snapScrolled.PlainRows()[compRow]
		if strings.Contains(rowText, "٦") || strings.Contains(rowText, string(upRawSample)) {
			t.Fatalf("escape sequence leaked into composer: %q", rowText)
		}
	}

	// Inject raw down-swipe SGR report (ESC[<٦٥;٤٢;١٨M) multiple times to scroll back to bottom.
	for i := 0; i < 5; i++ {
		_, _ = sess.Master.Write(downRawSample)
		time.Sleep(20 * time.Millisecond)
	}

	err = sess.WaitForCondition("restored follow at bottom", 3*time.Second, func(snap ScreenSnapshot) bool {
		return snap.Contains("Item 60") && !snap.Contains("updates available")
	})
	if err != nil {
		t.Fatalf("expected follow restored at bottom: %v", err)
	}

	// Composer must still be clean.
	snapBottom := sess.Snapshot()
	if compRow, ok := snapBottom.ComposerRow(); ok {
		rowText := snapBottom.PlainRows()[compRow]
		if strings.Contains(rowText, "٦") || strings.Contains(rowText, string(downRawSample)) {
			t.Fatalf("escape sequence leaked into composer after down swipe: %q", rowText)
		}
	}

	// Verify that mouse mode sequences were sent to terminal:
	rawBytes := string(sess.RawBytes())
	if !strings.Contains(rawBytes, "\x1b[?1006h") {
		t.Errorf("expected SGR mouse mode enable \\x1b[?1006h in raw terminal stream")
	}

	// Quit program and verify clean exit and teardown.
	sess.Program.Quit()
	select {
	case err := <-sess.Done():
		if err != nil {
			t.Errorf("unexpected error on exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("timed out waiting for session done channel on clean exit")
	}

	// Verify that mouse mode disable sequence was emitted during teardown.
	rawAfter := string(sess.RawBytes())
	if !strings.Contains(rawAfter, "\x1b[?1006l") && !strings.Contains(rawAfter, "\x1b[?1002l") {
		t.Errorf("expected mouse mode disable sequence in raw terminal stream")
	}

	sess.Close()
}
