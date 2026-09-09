package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// keyMsgFor builds the exact tea.KeyMsg a terminal delivers for an advertised
// key name (as written in the textarea's DefaultKeyMap). Alt-modified arrows
// arrive as the plain key with Alt set; alt+</alt+> and alt+letters arrive
// as alt-modified runes.
func keyMsgFor(s string) tea.KeyMsg {
	switch s {
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "alt+right":
		return tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	case "alt+left":
		return tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	case "alt+backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}
	case "alt+delete":
		return tea.KeyMsg{Type: tea.KeyDelete, Alt: true}
	case "ctrl+home":
		return tea.KeyMsg{Type: tea.KeyCtrlHome}
	case "ctrl+end":
		return tea.KeyMsg{Type: tea.KeyCtrlEnd}
	}
	if strings.HasPrefix(s, "ctrl+") && len(s) == 6 {
		return tea.KeyMsg{Type: tea.KeyCtrlA + tea.KeyType(s[5]-'a')}
	}
	if strings.HasPrefix(s, "alt+") && len(s) == 5 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(s[4])}, Alt: true}
	}
	panic("unhandled advertised key: " + s)
}

// TestTextareaKeymapContract pins the vendored textarea's documented keymap:
// every binding in DefaultKeyMap matches the exact tea.KeyMsg a terminal
// delivers for each advertised key, and performs the advertised edit on a
// real textarea model. This guards the third-party dependency so a future
// upgrade cannot silently drop or rebind editing keys.
func TestTextareaKeymapContract(t *testing.T) {
	km := textarea.DefaultKeyMap

	type step struct {
		keys  []string // advertised keys for the binding
		init  string   // initial value (cursor starts at the end)
		setup string   // whitespace-separated keys applied before the target key
		probe bool     // type "X" after the target key to verify cursor position
		want  string   // final value
	}
	cases := []struct {
		name string
		bind key.Binding
		step step
	}{
		{name: "CharacterForward", bind: km.CharacterForward, step: step{keys: []string{"right", "ctrl+f"}, init: "ab", setup: "ctrl+a", probe: true, want: "aXb"}},
		{name: "CharacterBackward", bind: km.CharacterBackward, step: step{keys: []string{"left", "ctrl+b"}, init: "ab", probe: true, want: "aXb"}},
		{name: "WordForward", bind: km.WordForward, step: step{keys: []string{"alt+right", "alt+f"}, init: "a b", setup: "ctrl+a", probe: true, want: "aX b"}},
		{name: "WordBackward", bind: km.WordBackward, step: step{keys: []string{"alt+left", "alt+b"}, init: "a b c", probe: true, want: "a b Xc"}},
		{name: "DeleteWordBackward", bind: km.DeleteWordBackward, step: step{keys: []string{"alt+backspace", "ctrl+w"}, init: "hello world", want: "hello "}},
		{name: "DeleteWordForward", bind: km.DeleteWordForward, step: step{keys: []string{"alt+delete", "alt+d"}, init: "hello world", setup: "ctrl+a", want: " world"}},
		{name: "DeleteAfterCursor", bind: km.DeleteAfterCursor, step: step{keys: []string{"ctrl+k"}, init: "abc def", setup: "left left left", want: "abc "}},
		{name: "DeleteBeforeCursor", bind: km.DeleteBeforeCursor, step: step{keys: []string{"ctrl+u"}, init: "abc def", want: ""}},
		{name: "InsertNewline", bind: km.InsertNewline, step: step{keys: []string{"enter", "ctrl+m"}, init: "ab", want: "ab\n"}},
		{name: "DeleteCharacterBackward", bind: km.DeleteCharacterBackward, step: step{keys: []string{"backspace", "ctrl+h"}, init: "abc", want: "ab"}},
		{name: "DeleteCharacterForward", bind: km.DeleteCharacterForward, step: step{keys: []string{"delete", "ctrl+d"}, init: "abc", setup: "ctrl+a", want: "bc"}},
		{name: "LineStart", bind: km.LineStart, step: step{keys: []string{"home", "ctrl+a"}, init: "ab", probe: true, want: "Xab"}},
		{name: "LineEnd", bind: km.LineEnd, step: step{keys: []string{"end", "ctrl+e"}, init: "ab", setup: "ctrl+a", probe: true, want: "abX"}},
		{name: "InputBegin", bind: km.InputBegin, step: step{keys: []string{"alt+<", "ctrl+home"}, init: "aa\nbb", probe: true, want: "Xaa\nbb"}},
		{name: "InputEnd", bind: km.InputEnd, step: step{keys: []string{"alt+>", "ctrl+end"}, init: "aa\nbb", setup: "ctrl+home", probe: true, want: "aa\nbbX"}},
		{name: "TransposeCharacterBackward", bind: km.TransposeCharacterBackward, step: step{keys: []string{"ctrl+t"}, init: "ab", want: "ba"}},
		{name: "UppercaseWordForward", bind: km.UppercaseWordForward, step: step{keys: []string{"alt+u"}, init: "word", setup: "ctrl+a", want: "WORD"}},
		{name: "LowercaseWordForward", bind: km.LowercaseWordForward, step: step{keys: []string{"alt+l"}, init: "WORD", setup: "ctrl+a", want: "word"}},
		{name: "CapitalizeWordForward", bind: km.CapitalizeWordForward, step: step{keys: []string{"alt+c"}, init: "word", setup: "ctrl+a", want: "Word"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Every advertised key must match this binding (exact String()).
			for _, ks := range tc.step.keys {
				if !key.Matches(keyMsgFor(ks), tc.bind) {
					t.Errorf("%q does not match binding %s", ks, tc.name)
				}
			}
			// The binding must perform the advertised edit.
			for _, ks := range tc.step.keys {
				ta := textarea.New()
				ta.SetWidth(40)
				ta.SetValue(tc.step.init)
				ta.Focus()
				ta.CursorEnd()
				for _, s := range strings.Fields(tc.step.setup) {
					ta, _ = ta.Update(keyMsgFor(s))
				}
				ta, _ = ta.Update(keyMsgFor(ks))
				if tc.step.probe {
					ta, _ = ta.Update(keyRunes("X"))
				}
				if got := ta.Value(); got != tc.step.want {
					t.Errorf("key %q: value = %q, want %q", ks, got, tc.step.want)
				}
			}
		})
	}
}

// TestComposerEditingKeysPassThrough verifies editing keys are handed
// through the feed's router to the textarea untouched (the router only
// intercepts send/newline/history/scroll/safety keys). Each case types text
// via the full Update path, moves the cursor with the keys under test, and
// probes the cursor by typing a marker rune.
func TestComposerEditingKeysPassThrough(t *testing.T) {
	cases := []struct {
		name string
		init string
		keys []tea.KeyMsg
		want string
	}{
		{name: "left arrow", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyLeft}}, want: "aXb"},
		{name: "right arrow", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyCtrlA}, {Type: tea.KeyRight}}, want: "aXb"},
		{name: "ctrl+b", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyCtrlB}}, want: "aXb"},
		{name: "ctrl+f", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyCtrlA}, {Type: tea.KeyCtrlF}}, want: "aXb"},
		{name: "home", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyHome}}, want: "Xab"},
		{name: "end", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyHome}, {Type: tea.KeyEnd}}, want: "abX"},
		{name: "ctrl+a line start", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyCtrlA}}, want: "Xab"},
		{name: "ctrl+e line end", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyCtrlA}, {Type: tea.KeyCtrlE}}, want: "abX"},
		{name: "ctrl+home", init: "aa\nbb", keys: []tea.KeyMsg{{Type: tea.KeyCtrlHome}}, want: "Xaa\nbb"},
		{name: "ctrl+end", init: "aa\nbb", keys: []tea.KeyMsg{{Type: tea.KeyCtrlHome}, {Type: tea.KeyCtrlEnd}}, want: "aa\nbbX"},
		{name: "ctrl+k delete after", init: "abc def", keys: []tea.KeyMsg{{Type: tea.KeyLeft}, {Type: tea.KeyLeft}, {Type: tea.KeyLeft}, {Type: tea.KeyCtrlK}}, want: "abc X"},
		{name: "ctrl+u delete before", init: "abc def", keys: []tea.KeyMsg{{Type: tea.KeyCtrlU}}, want: "X"},
		{name: "ctrl+w delete word", init: "hello world", keys: []tea.KeyMsg{{Type: tea.KeyCtrlW}}, want: "hello X"},
		{name: "alt+backspace delete word", init: "hello world", keys: []tea.KeyMsg{{Type: tea.KeyBackspace, Alt: true}}, want: "hello X"},
		{name: "backspace", init: "abc", keys: []tea.KeyMsg{{Type: tea.KeyBackspace}}, want: "abX"},
		{name: "delete forward", init: "abc", keys: []tea.KeyMsg{{Type: tea.KeyCtrlA}, {Type: tea.KeyDelete}}, want: "Xbc"},
		{name: "ctrl+d forward", init: "abc", keys: []tea.KeyMsg{{Type: tea.KeyCtrlA}, {Type: tea.KeyCtrlD}}, want: "Xbc"},
		{name: "ctrl+t transpose", init: "ab", keys: []tea.KeyMsg{{Type: tea.KeyCtrlT}}, want: "baX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFeed()
			typeIntoFeed(t, f, tc.init)
			for _, k := range tc.keys {
				_, _ = f.Update(k)
			}
			typeIntoFeed(t, f, "X")
			if got := f.composer.value(); got != tc.want {
				t.Fatalf("composer = %q, want %q", got, tc.want)
			}
			// Pass-through must never send or start a run.
			if f.busy {
				t.Fatal("editing key must not start a run")
			}
		})
	}
}

// TestComposerUpDownPassThrough verifies Up/Down move the cursor inside a
// multi-line composer (rather than recalling history) when the cursor is not
// on the first/last logical line.
func TestComposerUpDownPassThrough(t *testing.T) {
	f := NewFeed()
	typeIntoFeed(t, f, "first")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "second")
	// Cursor is on line 1 (last). Up moves to line 0, no history recall.
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	typeIntoFeed(t, f, "X")
	if got := f.composer.value(); got != "firstX\nsecond" {
		t.Fatalf("composer after Up = %q, want %q", got, "firstX\nsecond")
	}
	if f.HistoryBrowsing() {
		t.Fatal("Up inside the text must be a cursor move, not history recall")
	}
	// Down from line 0 returns to line 1 (still not history).
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	typeIntoFeed(t, f, "Y")
	if got := f.composer.value(); got != "firstX\nsecondY" {
		t.Fatalf("composer after Down = %q, want %q", got, "firstX\nsecondY")
	}
	if f.HistoryBrowsing() {
		t.Fatal("Down inside the text must be a cursor move, not history recall")
	}
}

// TestComposerRouterBoundaries verifies keys the feed's router owns never
// leak into the textarea as edits: Enter sends, Ctrl+J/Alt+Enter insert a
// newline, PgUp/PgDn scroll the viewport, and the composer is untouched.
func TestComposerRouterBoundaries(t *testing.T) {
	f := NewFeed()
	f.SetRunner(runnerFunc(func(string) error { return nil }))
	typeIntoFeed(t, f, "abc")

	// Enter must send, not insert a newline (the textarea would otherwise
	// split the line).
	_, cmd := f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must produce a send command")
	}
	if v := f.composer.value(); v != "" {
		t.Fatalf("composer after Enter = %q, want empty (sent)", v)
	}
	// Execute the send command to start the run and process the doneMsg
	// so that f.busy returns to false (the mock runner returns nil).
	if cmd != nil {
		msg := cmd()
		if done, ok := msg.(doneMsg); ok {
			_, _ = f.Update(done)
		}
	}

	// Ctrl+J and Alt+Enter must add a line, never send.
	typeIntoFeed(t, f, "l1")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	typeIntoFeed(t, f, "l2")
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	typeIntoFeed(t, f, "l3")
	if got := f.composer.value(); got != "l1\nl2\nl3" {
		t.Fatalf("composer after newline shortcuts = %q, want %q", got, "l1\nl2\nl3")
	}
	if f.busy {
		t.Fatal("newline shortcuts must not send")
	}

	// PgUp/PgDn must scroll the viewport, leaving the composer untouched.
	before := f.composer.value()
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	_, _ = f.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if got := f.composer.value(); got != before {
		t.Fatalf("composer changed across PgUp/PgDn: %q -> %q", before, got)
	}
}
