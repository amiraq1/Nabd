package textarea

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode"

	"github.com/MakeNowJust/heredoc"
	"github.com/aymanbagabas/go-udiff"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestVerticalScrolling(t *testing.T) {
	textarea := newTextArea()
	textarea.Prompt = ""
	textarea.ShowLineNumbers = false
	textarea.SetHeight(1)
	textarea.SetWidth(20)
	textarea.CharLimit = 100

	textarea, _ = textarea.Update(nil)

	input := "This is a really long line that should wrap around the text area."

	for _, k := range input {
		textarea, _ = textarea.Update(keyPress(k))
	}

	view := textarea.View()

	// The view should contain the first "line" of the input.
	if !strings.Contains(view, "This is a really") {
		t.Log(view)
		t.Error("Text area did not render the input")
	}

	// But we should be able to scroll to see the next line.
	// Let's scroll down for each line to view the full input.
	lines := []string{
		"long line that",
		"should wrap around",
		"the text area.",
	}
	for _, line := range lines {
		textarea.viewport.ScrollDown(1)
		view = textarea.View()
		if !strings.Contains(view, line) {
			t.Log(view)
			t.Error("Text area did not render the correct scrolled input")
		}
	}
}

func TestWordWrapOverflowing(t *testing.T) {
	// An interesting edge case is when the user enters many words that fill up
	// the text area and then goes back up and inserts a few words which causes
	// a cascading wrap and causes an overflow of the last line.
	//
	// In this case, we should not let the user insert more words if, after the
	// entire wrap is complete, the last line is overflowing.
	textarea := newTextArea()

	textarea.SetHeight(3)
	textarea.SetWidth(20)
	textarea.CharLimit = 500

	textarea, _ = textarea.Update(nil)

	input := "Testing Testing Testing Testing Testing Testing Testing Testing"

	for _, k := range input {
		textarea, _ = textarea.Update(keyPress(k))
		textarea.View()
	}

	// We have essentially filled the text area with input.
	// Let's see if we can cause wrapping to overflow the last line.
	textarea.row = 0
	textarea.col = 0

	input = "Testing"

	for _, k := range input {
		textarea, _ = textarea.Update(keyPress(k))
		textarea.View()
	}

	lastLineWidth := textarea.LineInfo().Width
	if lastLineWidth > 20 {
		t.Log(lastLineWidth)
		t.Log(textarea.View())
		t.Fail()
	}
}

func TestValueSoftWrap(t *testing.T) {
	textarea := newTextArea()
	textarea.SetWidth(16)
	textarea.SetHeight(10)
	textarea.CharLimit = 500

	textarea, _ = textarea.Update(nil)

	input := "Testing Testing Testing Testing Testing Testing Testing Testing"

	for _, k := range []rune(input) {
		textarea, _ = textarea.Update(keyPress(k))
		textarea.View()
	}

	value := textarea.Value()
	if value != input {
		t.Log(value)
		t.Log(input)
		t.Fatal("The text area does not have the correct value")
	}
}

func TestSetValue(t *testing.T) {
	textarea := newTextArea()
	textarea.SetValue(strings.Join([]string{"Foo", "Bar", "Baz"}, "\n"))

	if textarea.row != 2 && textarea.col != 3 {
		t.Log(textarea.row, textarea.col)
		t.Fatal("Cursor Should be on row 2 column 3 after inserting 2 new lines")
	}

	value := textarea.Value()
	if value != "Foo\nBar\nBaz" {
		t.Fatal("Value should be Foo\nBar\nBaz")
	}

	// SetValue should reset text area
	textarea.SetValue("Test")
	value = textarea.Value()
	if value != "Test" {
		t.Log(value)
		t.Fatal("Text area was not reset when SetValue() was called")
	}
}

func TestInsertString(t *testing.T) {
	textarea := newTextArea()

	// Insert some text
	input := "foo baz"

	for _, k := range []rune(input) {
		textarea, _ = textarea.Update(keyPress(k))
	}

	// Put cursor in the middle of the text
	textarea.col = 4

	textarea.InsertString("bar ")

	value := textarea.Value()
	if value != "foo bar baz" {
		t.Log(value)
		t.Fatal("Expected insert string to insert bar between foo and baz")
	}
}

func TestCanHandleEmoji(t *testing.T) {
	textarea := newTextArea()
	input := "🧋"

	for _, k := range []rune(input) {
		textarea, _ = textarea.Update(keyPress(k))
	}

	value := textarea.Value()
	if value != input {
		t.Log(value)
		t.Fatal("Expected emoji to be inserted")
	}

	input = "🧋🧋🧋"

	textarea.SetValue(input)

	value = textarea.Value()
	if value != input {
		t.Log(value)
		t.Fatal("Expected emoji to be inserted")
	}

	if textarea.col != 3 {
		t.Log(textarea.col)
		t.Fatal("Expected cursor to be on the third character")
	}

	if charOffset := textarea.LineInfo().CharOffset; charOffset != 6 {
		t.Log(charOffset)
		t.Fatal("Expected cursor to be on the sixth character")
	}
}

func TestVerticalNavigationKeepsCursorHorizontalPosition(t *testing.T) {
	textarea := newTextArea()
	textarea.SetWidth(20)

	textarea.SetValue(strings.Join([]string{"你好你好", "Hello"}, "\n"))

	textarea.row = 0
	textarea.col = 2

	// 你好|你好
	// Hell|o
	// 1234|

	// Let's imagine our cursor is on the first line where the pipe is.
	// We press the down arrow to get to the next line.
	// The issue is that if we keep the cursor on the same column, the cursor will jump to after the `e`.
	//
	// 你好|你好
	// He|llo
	//
	// But this is wrong because visually we were at the 4th character due to
	// the first line containing double-width runes.
	// We want to keep the cursor on the same visual column.
	//
	// 你好|你好
	// Hell|o
	//
	// This test ensures that the cursor is kept on the same visual column by
	// ensuring that the column offset goes from 2 -> 4.

	lineInfo := textarea.LineInfo()
	if lineInfo.CharOffset != 4 || lineInfo.ColumnOffset != 2 {
		t.Log(lineInfo.CharOffset)
		t.Log(lineInfo.ColumnOffset)
		t.Fatal("Expected cursor to be on the fourth character because there are two double width runes on the first line.")
	}

	downMsg := tea.KeyMsg{Type: tea.KeyDown, Alt: false, Runes: []rune{}}
	textarea, _ = textarea.Update(downMsg)

	lineInfo = textarea.LineInfo()
	if lineInfo.CharOffset != 4 || lineInfo.ColumnOffset != 4 {
		t.Log(lineInfo.CharOffset)
		t.Log(lineInfo.ColumnOffset)
		t.Fatal("Expected cursor to be on the fourth character because we came down from the first line.")
	}
}

func TestVerticalNavigationShouldRememberPositionWhileTraversing(t *testing.T) {
	textarea := newTextArea()
	textarea.SetWidth(40)

	// Let's imagine we have a text area with the following content:
	//
	// Hello
	// World
	// This is a long line.
	//
	// If we are at the end of the last line and go up, we should be at the end
	// of the second line.
	// And, if we go up again we should be at the end of the first line.
	// But, if we go back down twice, we should be at the end of the last line
	// again and not the fifth (length of second line) character of the last line.
	//
	// In other words, we should remember the last horizontal position while
	// traversing vertically.

	textarea.SetValue(strings.Join([]string{"Hello", "World", "This is a long line."}, "\n"))

	// We are at the end of the last line.
	if textarea.col != 20 || textarea.row != 2 {
		t.Log(textarea.col)
		t.Fatal("Expected cursor to be on the 20th character of the last line")
	}

	// Let's go up.
	upMsg := tea.KeyMsg{Type: tea.KeyUp, Alt: false, Runes: []rune{}}
	textarea, _ = textarea.Update(upMsg)

	// We should be at the end of the second line.
	if textarea.col != 5 || textarea.row != 1 {
		t.Log(textarea.col)
		t.Fatal("Expected cursor to be on the 5th character of the second line")
	}

	// And, again.
	textarea, _ = textarea.Update(upMsg)

	// We should be at the end of the first line.
	if textarea.col != 5 || textarea.row != 0 {
		t.Log(textarea.col)
		t.Fatal("Expected cursor to be on the 5th character of the first line")
	}

	// Let's go down, twice.
	downMsg := tea.KeyMsg{Type: tea.KeyDown, Alt: false, Runes: []rune{}}
	textarea, _ = textarea.Update(downMsg)
	textarea, _ = textarea.Update(downMsg)

	// We should be at the end of the last line.
	if textarea.col != 20 || textarea.row != 2 {
		t.Log(textarea.col)
		t.Fatal("Expected cursor to be on the 20th character of the last line")
	}

	// Now, for correct behavior, if we move right or left, we should forget
	// (reset) the saved horizontal position. Since we assume the user wants to
	// keep the cursor where it is horizontally. This is how most text areas
	// work.

	textarea, _ = textarea.Update(upMsg)
	leftMsg := tea.KeyMsg{Type: tea.KeyLeft, Alt: false, Runes: []rune{}}
	textarea, _ = textarea.Update(leftMsg)

	if textarea.col != 4 || textarea.row != 1 {
		t.Log(textarea.col)
		t.Fatal("Expected cursor to be on the 5th character of the second line")
	}

	// Going down now should keep us at the 4th column since we moved left and
	// reset the horizontal position saved state.
	textarea, _ = textarea.Update(downMsg)
	if textarea.col != 4 || textarea.row != 2 {
		t.Log(textarea.col)
		t.Fatal("Expected cursor to be on the 4th character of the last line")
	}
}

func TestView(t *testing.T) {
	t.Parallel()

	type want struct {
		view      string
		cursorRow int
		cursorCol int
	}

	tests := []struct {
		name      string
		modelFunc func(Model) Model
		want      want
	}{
		{
			name: "placeholder",
			want: want{
				view: heredoc.Doc(`
					>   1 Hello, World!
					>
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "single line",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line")

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 the first line
					>
					>
					>
					>
					>
				`),
				cursorRow: 0,
				cursorCol: 14,
			},
		},
		{
			name: "multiple lines",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line\nthe second line\nthe third line")

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 the first line
					>   2 the second line
					>   3 the third line
					>
					>
					>
				`),
				cursorRow: 2,
				cursorCol: 14,
			},
		},
		{
			name: "single line without line numbers",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line")
				m.ShowLineNumbers = false

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> the first line
					>
					>
					>
					>
					>
				`),
				cursorRow: 0,
				cursorCol: 14,
			},
		},
		{
			name: "multipline lines without line numbers",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line\nthe second line\nthe third line")
				m.ShowLineNumbers = false

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> the first line
					> the second line
					> the third line
					>
					>
					>
				`),
				cursorRow: 2,
				cursorCol: 14,
			},
		},
		{
			name: "single line and custom end of buffer character",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line")
				m.EndOfBufferCharacter = '*'

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 the first line
					> *
					> *
					> *
					> *
					> *
				`),
				cursorRow: 0,
				cursorCol: 14,
			},
		},
		{
			name: "multiple lines and custom end of buffer character",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line\nthe second line\nthe third line")
				m.EndOfBufferCharacter = '*'

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 the first line
					>   2 the second line
					>   3 the third line
					> *
					> *
					> *
				`),
				cursorRow: 2,
				cursorCol: 14,
			},
		},
		{
			name: "single line without line numbers and custom end of buffer character",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line")
				m.ShowLineNumbers = false
				m.EndOfBufferCharacter = '*'

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> the first line
					> *
					> *
					> *
					> *
					> *
				`),
				cursorRow: 0,
				cursorCol: 14,
			},
		},
		{
			name: "multiple lines without line numbers and custom end of buffer character",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line\nthe second line\nthe third line")
				m.ShowLineNumbers = false
				m.EndOfBufferCharacter = '*'

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> the first line
					> the second line
					> the third line
					> *
					> *
					> *
				`),
				cursorRow: 2,
				cursorCol: 14,
			},
		},
		{
			name: "single line and custom prompt",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line")
				m.Prompt = "* "

				return m
			},
			want: want{
				view: heredoc.Doc(`
					*   1 the first line
					*
					*
					*
					*
					*
				`),
				cursorRow: 0,
				cursorCol: 14,
			},
		},
		{
			name: "multiple lines and custom prompt",
			modelFunc: func(m Model) Model {
				m.SetValue("the first line\nthe second line\nthe third line")
				m.Prompt = "* "

				return m
			},
			want: want{
				view: heredoc.Doc(`
					*   1 the first line
					*   2 the second line
					*   3 the third line
					*
					*
					*
				`),
				cursorRow: 2,
				cursorCol: 14,
			},
		},
		{
			name: "type single line",
			modelFunc: func(m Model) Model {
				input := "foo"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 foo
					>
					>
					>
					>
					>
				`),
				cursorRow: 0,
				cursorCol: 3,
			},
		},
		{
			name: "type multiple lines",
			modelFunc: func(m Model) Model {
				input := "foo\nbar\nbaz"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 foo
					>   2 bar
					>   3 baz
					>
					>
					>
				`),
				cursorRow: 2,
				cursorCol: 3,
			},
		},
		{
			name: "softwrap",
			modelFunc: func(m Model) Model {
				m.ShowLineNumbers = false
				m.Prompt = ""
				m.SetWidth(5)

				input := "foo bar baz"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					foo
					bar
					baz



				`),
				cursorRow: 2,
				cursorCol: 3,
			},
		},
		{
			name: "single line character limit",
			modelFunc: func(m Model) Model {
				m.CharLimit = 7

				input := "foo bar baz"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 foo bar
					>
					>
					>
					>
					>
				`),
				cursorRow: 0,
				cursorCol: 7,
			},
		},
		{
			name: "multiple lines character limit",
			modelFunc: func(m Model) Model {
				m.CharLimit = 19

				input := "foo bar baz\nfoo bar baz"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 foo bar baz
					>   2 foo bar
					>
					>
					>
					>
				`),
				cursorRow: 1,
				cursorCol: 7,
			},
		},
		{
			name: "set width",
			modelFunc: func(m Model) Model {
				m.SetWidth(10)

				input := "12"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 12
					>
					>
					>
					>
					>
				`),
				cursorRow: 0,
				cursorCol: 2,
			},
		},
		{
			name: "set width max length text minus one",
			modelFunc: func(m Model) Model {
				m.SetWidth(10)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 123
					>
					>
					>
					>
					>
				`),
				cursorRow: 0,
				cursorCol: 3,
			},
		},
		{
			name: "set width max length text",
			modelFunc: func(m Model) Model {
				m.SetWidth(10)

				input := "1234"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 1234
					>
					>
					>
					>
					>
				`),
				cursorRow: 1,
				cursorCol: 0,
			},
		},
		{
			name: "set width max length text plus one",
			modelFunc: func(m Model) Model {
				m.SetWidth(10)

				input := "12345"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 1234
					>     5
					>
					>
					>
					>
				`),
				cursorRow: 1,
				cursorCol: 1,
			},
		},
		{
			name: "set width set max width minus one",
			modelFunc: func(m Model) Model {
				m.MaxWidth = 10
				m.SetWidth(11)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 123
					>
					>
					>
					>
					>
				`),
				cursorRow: 0,
				cursorCol: 3,
			},
		},
		{
			name: "set width set max width",
			modelFunc: func(m Model) Model {
				m.MaxWidth = 10
				m.SetWidth(11)

				input := "1234"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 1234
					>
					>
					>
					>
					>
				`),
				cursorRow: 1,
				cursorCol: 0,
			},
		},
		{
			name: "set width set max width plus one",
			modelFunc: func(m Model) Model {
				m.MaxWidth = 10
				m.SetWidth(11)

				input := "12345"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 1234
					>     5
					>
					>
					>
					>
				`),
				cursorRow: 1,
				cursorCol: 1,
			},
		},
		{
			name: "set width min width minus one",
			modelFunc: func(m Model) Model {
				m.SetWidth(6)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 1
					>     2
					>     3
					>
					>
					>
				`),
				cursorRow: 3,
				cursorCol: 0,
			},
		},
		{
			name: "set width min width",
			modelFunc: func(m Model) Model {
				m.SetWidth(7)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 1
					>     2
					>     3
					>
					>
					>
				`),
				cursorRow: 3,
				cursorCol: 0,
			},
		},
		{
			name: "set width min width no line numbers",
			modelFunc: func(m Model) Model {
				m.ShowLineNumbers = false
				m.SetWidth(0)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> 1
					> 2
					> 3
					>
					>
					>
				`),
				cursorRow: 3,
				cursorCol: 0,
			},
		},
		{
			name: "set width min width no line numbers no prompt",
			modelFunc: func(m Model) Model {
				m.ShowLineNumbers = false
				m.Prompt = ""
				m.SetWidth(0)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					1
					2
					3



				`),
				cursorRow: 3,
				cursorCol: 0,
			},
		},
		{
			name: "set width min width plus one",
			modelFunc: func(m Model) Model {
				m.SetWidth(8)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 12
					>     3
					>
					>
					>
					>
				`),
				cursorRow: 1,
				cursorCol: 1,
			},
		},
		{
			name: "set width without line numbers max length text minus one",
			modelFunc: func(m Model) Model {
				m.ShowLineNumbers = false
				m.SetWidth(6)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> 123
					>
					>
					>
					>
					>
				`),
				cursorRow: 0,
				cursorCol: 3,
			},
		},
		{
			name: "set width without line numbers max length text",
			modelFunc: func(m Model) Model {
				m.ShowLineNumbers = false
				m.SetWidth(6)

				input := "1234"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> 1234
					>
					>
					>
					>
					>
				`),
				cursorRow: 1,
				cursorCol: 0,
			},
		},
		{
			name: "set width without line numbers max length text plus one",
			modelFunc: func(m Model) Model {
				m.ShowLineNumbers = false
				m.SetWidth(6)

				input := "12345"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> 1234
					> 5
					>
					>
					>
					>
				`),
				cursorRow: 1,
				cursorCol: 1,
			},
		},
		{
			name: "set width with style",
			modelFunc: func(m Model) Model {
				m.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
				m.Focus()

				m.SetWidth(12)

				input := "1"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					┌──────────┐
					│>   1 1   │
					│>         │
					│>         │
					│>         │
					│>         │
					│>         │
					└──────────┘
				`),
				cursorRow: 0,
				cursorCol: 1,
			},
		},
		{
			name: "set width with style max width minus one",
			modelFunc: func(m Model) Model {
				m.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
				m.Focus()

				m.SetWidth(12)

				input := "123"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					┌──────────┐
					│>   1 123 │
					│>         │
					│>         │
					│>         │
					│>         │
					│>         │
					└──────────┘
				`),
				cursorRow: 0,
				cursorCol: 3,
			},
		},
		{
			name: "set width with style max width",
			modelFunc: func(m Model) Model {
				m.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
				m.Focus()

				m.SetWidth(12)

				input := "1234"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					┌──────────┐
					│>   1 1234│
					│>         │
					│>         │
					│>         │
					│>         │
					│>         │
					└──────────┘
				`),
				cursorRow: 1,
				cursorCol: 0,
			},
		},
		{
			name: "set width with style max width plus one",
			modelFunc: func(m Model) Model {
				m.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
				m.Focus()

				m.SetWidth(12)

				input := "12345"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					┌──────────┐
					│>   1 1234│
					│>     5   │
					│>         │
					│>         │
					│>         │
					│>         │
					└──────────┘
				`),
				cursorRow: 1,
				cursorCol: 1,
			},
		},
		{
			name: "set width without line numbers with style",
			modelFunc: func(m Model) Model {
				m.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
				m.Focus()

				m.ShowLineNumbers = false
				m.SetWidth(12)

				input := "123456"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					┌──────────┐
					│> 123456  │
					│>         │
					│>         │
					│>         │
					│>         │
					│>         │
					└──────────┘
				`),
				cursorRow: 0,
				cursorCol: 6,
			},
		},
		{
			name: "set width without line numbers with style max width minus one",
			modelFunc: func(m Model) Model {
				m.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
				m.Focus()

				m.ShowLineNumbers = false
				m.SetWidth(12)

				input := "1234567"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					┌──────────┐
					│> 1234567 │
					│>         │
					│>         │
					│>         │
					│>         │
					│>         │
					└──────────┘
				`),
				cursorRow: 0,
				cursorCol: 7,
			},
		},
		{
			name: "set width without line numbers with style max width",
			modelFunc: func(m Model) Model {
				m.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
				m.Focus()

				m.ShowLineNumbers = false
				m.SetWidth(12)

				input := "12345678"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					┌──────────┐
					│> 12345678│
					│>         │
					│>         │
					│>         │
					│>         │
					│>         │
					└──────────┘
				`),
				cursorRow: 1,
				cursorCol: 0,
			},
		},
		{
			name: "set width without line numbers with style max width plus one",
			modelFunc: func(m Model) Model {
				m.FocusedStyle.Base = lipgloss.NewStyle().Border(lipgloss.NormalBorder())
				m.Focus()

				m.ShowLineNumbers = false
				m.SetWidth(12)

				input := "123456789"
				m = sendString(m, input)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					┌──────────┐
					│> 12345678│
					│> 9       │
					│>         │
					│>         │
					│>         │
					│>         │
					└──────────┘
				`),
				cursorRow: 1,
				cursorCol: 1,
			},
		},
		{
			name: "placeholder min width",
			modelFunc: func(m Model) Model {
				m.SetWidth(0)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 H
					>     e
					>     l
					>     l
					>     o
					>     ,
				`),
			},
		},
		{
			name: "placeholder single line",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line"
				m.ShowLineNumbers = false

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> placeholder the first line
					>
					>
					>
					>
					>
					`),
			},
		},
		{
			name: "placeholder multiple lines",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line\nplaceholder the second line\nplaceholder the third line"
				m.ShowLineNumbers = false

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> placeholder the first line
					> placeholder the second line
					> placeholder the third line
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder single line with line numbers",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line"
				m.ShowLineNumbers = true

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 placeholder the first line
					>
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder multiple lines with line numbers",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line\nplaceholder the second line\nplaceholder the third line"
				m.ShowLineNumbers = true

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 placeholder the first line
					>     placeholder the second line
					>     placeholder the third line
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder single line with end of buffer character",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line"
				m.ShowLineNumbers = false
				m.EndOfBufferCharacter = '*'

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> placeholder the first line
					> *
					> *
					> *
					> *
					> *
				`),
			},
		},
		{
			name: "placeholder multiple lines with with end of buffer character",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line\nplaceholder the second line\nplaceholder the third line"
				m.ShowLineNumbers = false
				m.EndOfBufferCharacter = '*'

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> placeholder the first line
					> placeholder the second line
					> placeholder the third line
					> *
					> *
					> *
				`),
			},
		},
		{
			name: "placeholder single line with line numbers and end of buffer character",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line"
				m.ShowLineNumbers = true
				m.EndOfBufferCharacter = '*'

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 placeholder the first line
					> *
					> *
					> *
					> *
					> *
				`),
			},
		},
		{
			name: "placeholder multiple lines with line numbers and end of buffer character",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line\nplaceholder the second line\nplaceholder the third line"
				m.ShowLineNumbers = true
				m.EndOfBufferCharacter = '*'

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 placeholder the first line
					>     placeholder the second line
					>     placeholder the third line
					> *
					> *
					> *
				`),
			},
		},
		{
			name: "placeholder single line that is longer than max width",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line that is longer than the max width"
				m.SetWidth(40)
				m.ShowLineNumbers = false

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> placeholder the first line that is
					> longer than the max width
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder multiple lines that are longer than max width",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line that is longer than the max width\nplaceholder the second line that is longer than the max width"
				m.ShowLineNumbers = false
				m.SetWidth(40)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> placeholder the first line that is
					> longer than the max width
					> placeholder the second line that is
					> longer than the max width
					>
					>
				`),
			},
		},
		{
			name: "placeholder single line that is longer than max width with line numbers",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line that is longer than the max width"
				m.ShowLineNumbers = true
				m.SetWidth(40)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 placeholder the first line that is
					>     longer than the max width
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder multiple lines that are longer than max width with line numbers",
			modelFunc: func(m Model) Model {
				m.Placeholder = "placeholder the first line that is longer than the max width\nplaceholder the second line that is longer than the max width"
				m.ShowLineNumbers = true
				m.SetWidth(40)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 placeholder the first line that is
					>     longer than the max width
					>     placeholder the second line that
					>     is longer than the max width
					>
					>
				`),
			},
		},
		{
			name: "placeholder single line that is longer than max width at limit",
			modelFunc: func(m Model) Model {
				m.Placeholder = "123456789012345678"
				m.ShowLineNumbers = false
				m.SetWidth(20)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> 123456789012345678
					>
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder single line that is longer than max width at limit plus one",
			modelFunc: func(m Model) Model {
				m.Placeholder = "1234567890123456789"
				m.ShowLineNumbers = false
				m.SetWidth(20)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> 123456789012345678
					> 9
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder single line that is longer than max width with line numbers at limit",
			modelFunc: func(m Model) Model {
				m.Placeholder = "12345678901234"
				m.ShowLineNumbers = true
				m.SetWidth(20)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 12345678901234
					>
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder single line that is longer than max width with line numbers at limit plus one",
			modelFunc: func(m Model) Model {
				m.Placeholder = "123456789012345"
				m.ShowLineNumbers = true
				m.SetWidth(20)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 12345678901234
					>     5
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder multiple lines that are longer than max width at limit",
			modelFunc: func(m Model) Model {
				m.Placeholder = "123456789012345678\n123456789012345678"
				m.ShowLineNumbers = false
				m.SetWidth(20)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> 123456789012345678
					> 123456789012345678
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder multiple lines that are longer than max width at limit plus one",
			modelFunc: func(m Model) Model {
				m.Placeholder = "1234567890123456789\n1234567890123456789"
				m.ShowLineNumbers = false
				m.SetWidth(20)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					> 123456789012345678
					> 9
					> 123456789012345678
					> 9
					>
					>
				`),
			},
		},
		{
			name: "placeholder multiple lines that are longer than max width with line numbers at limit",
			modelFunc: func(m Model) Model {
				m.Placeholder = "12345678901234\n12345678901234"
				m.ShowLineNumbers = true
				m.SetWidth(20)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 12345678901234
					>     12345678901234
					>
					>
					>
					>
				`),
			},
		},
		{
			name: "placeholder multiple lines that are longer than max width with line numbers at limit plus one",
			modelFunc: func(m Model) Model {
				m.Placeholder = "123456789012345\n123456789012345"
				m.ShowLineNumbers = true
				m.SetWidth(20)

				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 12345678901234
					>     5
					>     12345678901234
					>     5
					>
					>
				`),
			},
		},
		{
			name: "placeholder chinese character",
			modelFunc: func(m Model) Model {
				m.Placeholder = "输入消息..."
				m.ShowLineNumbers = true
				m.SetWidth(20)
				return m
			},
			want: want{
				view: heredoc.Doc(`
					>   1 输入消息...
					>
					>
					>
					>
					>

				`),
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			textarea := newTextArea()

			if tt.modelFunc != nil {
				textarea = tt.modelFunc(textarea)
			}

			view := stripString(textarea.View())
			wantView := stripString(tt.want.view)

			if view != wantView {
				t.Log(udiff.Unified("expected", "got", wantView, view))
				t.Fatalf("Want:\n%v\nGot:\n%v\n", wantView, view)
			}

			cursorRow := textarea.cursorLineNumber()
			cursorCol := textarea.LineInfo().ColumnOffset
			if tt.want.cursorRow != cursorRow || tt.want.cursorCol != cursorCol {
				format := "Want cursor at row: %v, col: %v Got: row: %v col: %v\n"
				t.Fatalf(format, tt.want.cursorRow, tt.want.cursorCol, cursorRow, cursorCol)
			}
		})
	}
}

func newTextArea() Model {
	textarea := New()

	textarea.Prompt = "> "
	textarea.Placeholder = "Hello, World!"

	textarea.Focus()

	textarea, _ = textarea.Update(nil)

	return textarea
}

func keyPress(key rune) tea.Msg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}, Alt: false}
}

func sendString(m Model, str string) Model {
	for _, k := range []rune(str) {
		m, _ = m.Update(keyPress(k))
	}

	return m
}

func stripString(str string) string {
	s := ansi.Strip(str)
	ss := strings.Split(s, "\n")

	var lines []string
	for _, l := range ss {
		trim := strings.TrimRightFunc(l, unicode.IsSpace)
		if trim != "" {
			lines = append(lines, trim)
		}
	}

	return strings.Join(lines, "\n")
}

func TestIncrementalWrapEquivalence(t *testing.T) {
	testCases := []struct {
		name  string
		text  string
		width int
	}{
		{"empty", "", 10},
		{"short", "hello world", 10},
		{"unbroken 100", strings.Repeat("x", 100), 10},
		{"unbroken 8100", strings.Repeat("a", 8100), 76},
		{"arabic unbroken", strings.Repeat("ع", 8100), 76},
		{"mixed words", "the quick brown fox jumps over the lazy dog and keeps going on and on and on", 15},
		{"cjk text", strings.Repeat("你好世界", 50), 20},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runes := []rune(tc.text)
			fullWrapped := wrap(runes, tc.width)

			offsets := make([]int, len(fullWrapped))
			for i := 1; i < len(fullWrapped); i++ {
				offsets[i] = offsets[i-1] + len(fullWrapped[i-1])
			}

			// Verify that for any intermediate row k, wrapping runes[offsets[k]:] matches fullWrapped[k:]
			for k := 1; k < len(fullWrapped); k++ {
				suffixRunes := runes[offsets[k]:]
				suffixWrapped := wrap(suffixRunes, tc.width)

				if len(suffixWrapped) != len(fullWrapped)-k {
					t.Fatalf("row %d: len(suffixWrapped)=%d, want %d", k, len(suffixWrapped), len(fullWrapped)-k)
				}
				for rowIdx := range suffixWrapped {
					if string(suffixWrapped[rowIdx]) != string(fullWrapped[k+rowIdx]) {
						t.Fatalf("row %d+%d mismatch:\n got: %q\nwant: %q", k, rowIdx, string(suffixWrapped[rowIdx]), string(fullWrapped[k+rowIdx]))
					}
				}
			}
		})
	}
}

// TestIncrementalAlgorithmLoopSimulation is a supplementary simulation verifying
// the core row-slicing logic on a test-local copy before exercising the real Model cache.
func TestIncrementalAlgorithmLoopSimulation(t *testing.T) {
	// Helper to simulate the incremental wrap algorithm
	incrementalWrap := func(oldRunes []rune, oldWrapped [][]rune, oldOffsets []int, newRunes []rune, width int) [][]rune {
		if len(oldWrapped) == 0 || len(oldOffsets) == 0 {
			return wrap(newRunes, width)
		}

		prefixLen := commonRunesPrefix(oldRunes, newRunes)
		if prefixLen == 0 {
			return wrap(newRunes, width)
		}

		// Find editRow: the row containing prefixLen
		editRow := len(oldWrapped) - 1
		for i := 0; i < len(oldOffsets)-1; i++ {
			if oldOffsets[i+1] > prefixLen {
				editRow = i
				break
			}
		}

		reuseRows := editRow - 1
		if reuseRows <= 0 {
			return wrap(newRunes, width)
		}

		reuseOffset := oldOffsets[reuseRows]
		remainderRunes := newRunes[reuseOffset:]
		remainderWrapped := wrap(remainderRunes, width)

		result := make([][]rune, 0, reuseRows+len(remainderWrapped))
		for i := 0; i < reuseRows; i++ {
			cp := make([]rune, len(oldWrapped[i]))
			copy(cp, oldWrapped[i])
			result = append(result, cp)
		}
		result = append(result, remainderWrapped...)
		return result
	}

	texts := []string{
		"The quick brown fox jumps over the lazy dog repeatedly and keeps jumping until everyone is tired.",
		strings.Repeat("abcdefghij ", 20),
		strings.Repeat("word123 ", 40),
		strings.Repeat("x", 500),
		strings.Repeat("مرحبا بالعالم كيف الحال ", 30),
		strings.Repeat("日本語テスト文章です ", 30),
	}

	widths := []int{10, 20, 50, 80}

	for _, text := range texts {
		for _, w := range widths {
			oldRunes := []rune(text)
			oldWrapped := wrap(oldRunes, w)
			oldOffsets := make([]int, len(oldWrapped))
			for i := 1; i < len(oldWrapped); i++ {
				oldOffsets[i] = oldOffsets[i-1] + len(oldWrapped[i-1])
			}

			// Test deletions from end (like backspace)
			for del := 1; del <= min(50, len(oldRunes)); del += 3 {
				newRunes := oldRunes[:len(oldRunes)-del]
				got := incrementalWrap(oldRunes, oldWrapped, oldOffsets, newRunes, w)
				want := wrap(newRunes, w)

				if len(got) != len(want) {
					t.Fatalf("text len %d w %d del %d: len got=%d, want=%d", len(text), w, del, len(got), len(want))
				}
				for row := range got {
					if string(got[row]) != string(want[row]) {
						t.Fatalf("text w %d del %d row %d:\n got: %q\nwant: %q", w, del, row, string(got[row]), string(want[row]))
					}
				}
			}

			// Test edits in the middle
			for pos := len(oldRunes) / 2; pos < len(oldRunes)-5; pos += 10 {
				newRunes := append([]rune{}, oldRunes[:pos]...)
				newRunes = append(newRunes, []rune("EDIT")...)
				newRunes = append(newRunes, oldRunes[pos+2:]...)

				got := incrementalWrap(oldRunes, oldWrapped, oldOffsets, newRunes, w)
				want := wrap(newRunes, w)

				if len(got) != len(want) {
					t.Fatalf("text len %d w %d mid-edit at %d: len got=%d, want=%d", len(text), w, pos, len(got), len(want))
				}
				for row := range got {
					if string(got[row]) != string(want[row]) {
						t.Fatalf("text w %d mid-edit at %d row %d:\n got: %q\nwant: %q", w, pos, row, string(got[row]), string(want[row]))
					}
				}
			}
		}
	}
}

func equalGrid(a, b [][]rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	return true
}

func assertGridMatchesOracle(t *testing.T, label string, got [][]rune, runes []rune, width int) {
	t.Helper()
	want := wrap(runes, width)
	if !equalGrid(got, want) {
		t.Fatalf("%s mismatch:\ngot  (%d rows):\n%s\nwant (%d rows):\n%s",
			label, len(got), formatGrid(got), len(want), formatGrid(want))
	}
}

func formatGrid(g [][]rune) string {
	var sb strings.Builder
	for i, row := range g {
		sb.WriteString(fmt.Sprintf("  [%d]: %q\n", i, string(row)))
	}
	return sb.String()
}

// TestActualCacheDifferential exercises the actual production Model.memoizedWrap
// across all edit topologies, wrap boundaries, and width changes against the
// independent oracle wrap(runes, width).
func TestActualCacheDifferential(t *testing.T) {
	t.Run("UnchangedRepeatedCalls", func(t *testing.T) {
		m := New()
		m.SetWidth(20)
		runes := []rune("The quick brown fox jumps over the lazy dog repeatedly.")
		got1 := m.memoizedWrap(runes, m.width, 0)
		assertGridMatchesOracle(t, "first wrap", got1, runes, m.width)

		got2 := m.memoizedWrap(runes, m.width, 0)
		assertGridMatchesOracle(t, "repeated call", got2, runes, m.width)

		// Exact match returns cached slice pointer directly
		if len(got1) > 0 && len(got2) > 0 && &got1[0] != &got2[0] {
			t.Fatal("expected exact cache hit to return the same slice pointer")
		}
	})

	t.Run("TailEdits", func(t *testing.T) {
		m := New()
		m.SetWidth(15)
		base := []rune(strings.Repeat("abcdefghij ", 10))
		_ = m.memoizedWrap(base, m.width, 0)

		// Insertions at tail
		for add := 1; add <= 20; add += 3 {
			curr := append(base, []rune(strings.Repeat("z", add))...)
			got := m.memoizedWrap(curr, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("tail add %d", add), got, curr, m.width)
		}

		// Deletions from tail
		for del := 1; del <= 30; del += 3 {
			curr := base[:len(base)-del]
			got := m.memoizedWrap(curr, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("tail del %d", del), got, curr, m.width)
		}
	})

	t.Run("BeginningEdits", func(t *testing.T) {
		m := New()
		m.SetWidth(15)
		base := []rune(strings.Repeat("abcdefghij ", 10))
		_ = m.memoizedWrap(base, m.width, 0)

		// Prepend
		for add := 1; add <= 15; add += 3 {
			curr := append([]rune(strings.Repeat("A", add)), base...)
			got := m.memoizedWrap(curr, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("prepend %d", add), got, curr, m.width)
		}

		// Delete from start
		for del := 1; del <= 25; del += 4 {
			curr := base[del:]
			got := m.memoizedWrap(curr, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("del from start %d", del), got, curr, m.width)
		}
	})

	t.Run("MiddleEditsAndReplacements", func(t *testing.T) {
		m := New()
		m.SetWidth(20)
		base := []rune(strings.Repeat("0123456789 ", 15))
		_ = m.memoizedWrap(base, m.width, 0)

		mid := len(base) / 2
		// Insert in middle
		ins := append(append([]rune{}, base[:mid]...), []rune("NEW_INSERTED_WORD")...)
		ins = append(ins, base[mid:]...)
		gotIns := m.memoizedWrap(ins, m.width, 0)
		assertGridMatchesOracle(t, "mid insertion", gotIns, ins, m.width)

		// Delete in middle
		del := append(append([]rune{}, base[:mid]...), base[mid+15:]...)
		gotDel := m.memoizedWrap(del, m.width, 0)
		assertGridMatchesOracle(t, "mid deletion", gotDel, del, m.width)

		// Same-length replacement in middle
		repl := append(append([]rune{}, base[:mid]...), []rune("XYZWVUTSRQPONMLK")...)
		repl = append(repl, base[mid+16:]...)
		gotRepl := m.memoizedWrap(repl, m.width, 0)
		assertGridMatchesOracle(t, "same-length replacement", gotRepl, repl, m.width)
	})

	t.Run("WrapBoundaryEdits", func(t *testing.T) {
		m := New()
		m.SetWidth(20)
		base := []rune("first_long_word_that_fills_line second_long_word_on_next third_long_word_here")
		w0 := wrap(base, m.width)
		if len(w0) < 3 {
			t.Fatalf("expected at least 3 rows, got %d", len(w0))
		}
		_ = m.memoizedWrap(base, m.width, 0)

		// Find wrap boundary between row 0 and row 1
		boundary := len(w0[0])
		for offset := -2; offset <= 2; offset++ {
			pos := boundary + offset
			if pos < 0 || pos >= len(base) {
				continue
			}
			mutated := make([]rune, len(base))
			copy(mutated, base)
			mutated[pos] = '#'
			got := m.memoizedWrap(mutated, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("edit at boundary offset %d", offset), got, mutated, m.width)
		}
	})

	t.Run("ConsecutiveSingleRuneEdits", func(t *testing.T) {
		m := New()
		m.SetWidth(25)
		curr := []rune(strings.Repeat("a", 300))
		_ = m.memoizedWrap(curr, m.width, 0)

		// 30 continuous backspaces reusing previous real cache state
		for i := 0; i < 30; i++ {
			curr = curr[:len(curr)-1]
			got := m.memoizedWrap(curr, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("backspace step %d", i), got, curr, m.width)
		}

		// 30 continuous insertions
		for i := 0; i < 30; i++ {
			curr = append(curr, 'b')
			got := m.memoizedWrap(curr, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("insert step %d", i), got, curr, m.width)
		}
	})

	t.Run("WidthChangesFollowedByEditing", func(t *testing.T) {
		m := New()
		curr := []rune("Sample text with several words that wraps across varying terminal column widths.")
		widths := []int{40, 20, 10, 60, 15, 80}

		for _, w := range widths {
			m.SetWidth(w)
			got := m.memoizedWrap(curr, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("width %d initial", w), got, curr, m.width)

			// Edit after width change
			curr = append(curr, []rune(" more text")...)
			gotAfter := m.memoizedWrap(curr, m.width, 0)
			assertGridMatchesOracle(t, fmt.Sprintf("width %d after edit", w), gotAfter, curr, m.width)
		}
	})

	t.Run("EmptyTransitions", func(t *testing.T) {
		m := New()
		m.SetWidth(20)

		// Empty -> non-empty
		empty := []rune("")
		gotEmpty1 := m.memoizedWrap(empty, m.width, 0)
		assertGridMatchesOracle(t, "empty initial", gotEmpty1, empty, m.width)

		nonEmpty := []rune("hello world")
		gotNonEmpty := m.memoizedWrap(nonEmpty, m.width, 0)
		assertGridMatchesOracle(t, "empty to non-empty", gotNonEmpty, nonEmpty, m.width)

		// Non-empty -> empty
		gotEmpty2 := m.memoizedWrap(empty, m.width, 0)
		assertGridMatchesOracle(t, "non-empty to empty", gotEmpty2, empty, m.width)
	})

	t.Run("AdjacentLogicalLinesIsolation", func(t *testing.T) {
		m := New()
		m.SetWidth(15)

		line0 := []rune(strings.Repeat("A", 45))
		line1 := []rune(strings.Repeat("B", 45))
		line2 := []rune(strings.Repeat("C", 45))

		got0 := m.memoizedWrap(line0, m.width, 0)
		got1 := m.memoizedWrap(line1, m.width, 1)
		got2 := m.memoizedWrap(line2, m.width, 2)

		assertGridMatchesOracle(t, "line 0", got0, line0, m.width)
		assertGridMatchesOracle(t, "line 1", got1, line1, m.width)
		assertGridMatchesOracle(t, "line 2", got2, line2, m.width)

		// Mutate line 1 only; lines 0 and 2 must remain unaffected
		line1Mut := append(line1, []rune("BBBB")...)
		got1Mut := m.memoizedWrap(line1Mut, m.width, 1)
		assertGridMatchesOracle(t, "line 1 mutated", got1Mut, line1Mut, m.width)

		got0Check := m.memoizedWrap(line0, m.width, 0)
		got2Check := m.memoizedWrap(line2, m.width, 2)
		assertGridMatchesOracle(t, "line 0 after line 1 mutation", got0Check, line0, m.width)
		assertGridMatchesOracle(t, "line 2 after line 1 mutation", got2Check, line2, m.width)
	})
}

// TestActualModelUpdateIntegration drives the full Model.Update path with real tea.KeyMsg
// events, comparing rendered output, line info, cursor, and value against an independent
// cold-wrap reference model at every step. The reference model explicitly bypasses the layout
// cache (layoutCache == nil), forcing cold wrap(runes, width) on every single operation.
func TestActualModelUpdateIntegration(t *testing.T) {
	newPair := func(w, h int) (Model, Model) {
		m := New()
		m.SetWidth(w)
		m.SetHeight(h)
		m.Cursor.Blink = false
		m.Focus()

		ref := New()
		ref.SetWidth(w)
		ref.SetHeight(h)
		ref.Cursor.Blink = false
		ref.Focus()
		// ref is an independent cold-wrap reference: with layoutCache = nil,
		// memoizedWrap unconditionally executes wrap(runes, width) on every call,
		// never storing or consulting cached row layouts.
		ref.layoutCache = nil

		return m, ref
	}

	assertEquiv := func(t *testing.T, step string, m, ref Model) {
		t.Helper()
		if ref.layoutCache != nil {
			t.Fatalf("[%s] reference model must maintain layoutCache == nil (strict cold wrap)", step)
		}
		if m.layoutCache == nil {
			t.Fatalf("[%s] active model must have layoutCache enabled", step)
		}
		if m.Value() != ref.Value() {
			t.Fatalf("[%s] Value mismatch:\ngot : %q\nwant: %q", step, m.Value(), ref.Value())
		}
		if m.Line() != ref.Line() {
			t.Fatalf("[%s] Line mismatch: got %d, want %d", step, m.Line(), ref.Line())
		}
		liM := m.LineInfo()
		liRef := ref.LineInfo()
		if liM != liRef {
			t.Fatalf("[%s] LineInfo mismatch: got %+v, want %+v", step, liM, liRef)
		}
		viewM := m.View()
		viewRef := ref.View()
		if viewM != viewRef {
			t.Fatalf("[%s] View mismatch:\ngot :\n%s\nwant:\n%s", step, viewM, viewRef)
		}
	}

	m, ref := newPair(30, 10)

	// Step 1: Type unbroken ASCII text that soft-wraps across multiple rows
	for i, ch := range "TheQuickBrownFoxJumpsOverTheLazyDogAndKeepsRunningWithoutStoppingAtAll" {
		m, _ = m.Update(keyPress(ch))
		ref, _ = ref.Update(keyPress(ch))
		if i%10 == 0 {
			assertEquiv(t, fmt.Sprintf("typing char %d", i), m, ref)
		}
	}
	assertEquiv(t, "after initial typing", m, ref)

	// Step 2: Backspace 15 times
	for i := 0; i < 15; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		ref, _ = ref.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		assertEquiv(t, fmt.Sprintf("backspace %d", i), m, ref)
	}

	// Step 3: Move cursor left and forward delete
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		ref, _ = ref.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	for i := 0; i < 5; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDelete})
		ref, _ = ref.Update(tea.KeyMsg{Type: tea.KeyDelete})
		assertEquiv(t, fmt.Sprintf("forward delete %d", i), m, ref)
	}

	// Step 4: Insert newlines (splitLine)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	ref, _ = ref.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assertEquiv(t, "after newline split", m, ref)

	for _, ch := range "Second line content that is also reasonably long" {
		m, _ = m.Update(keyPress(ch))
		ref, _ = ref.Update(keyPress(ch))
	}
	assertEquiv(t, "after typing second line", m, ref)

	// Step 5: Backspace at line start to merge with line above (mergeLineAbove)
	m.CursorStart()
	ref.CursorStart()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	ref, _ = ref.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	assertEquiv(t, "after mergeLineAbove", m, ref)

	// Step 6: Move to end and split line again, then mergeLineBelow via forward delete
	m.CursorEnd()
	ref.CursorEnd()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	ref, _ = ref.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, ch := range "Third line" {
		m, _ = m.Update(keyPress(ch))
		ref, _ = ref.Update(keyPress(ch))
	}
	m.CursorUp()
	ref.CursorUp()
	m.CursorEnd()
	ref.CursorEnd()
	// Delete at line end merges line below
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	ref, _ = ref.Update(tea.KeyMsg{Type: tea.KeyDelete})
	assertEquiv(t, "after mergeLineBelow", m, ref)

	// Step 7: Paste (both bracketed paste KeyMsg and clipboard pasteMsg)
	pasteKey := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune(" [bracketed paste text with words] "),
		Paste: true,
	}
	m, _ = m.Update(pasteKey)
	ref, _ = ref.Update(pasteKey)
	assertEquiv(t, "after bracketed paste KeyMsg", m, ref)

	m, _ = m.Update(pasteMsg("multi-line\npasted\ncontent"))
	ref, _ = ref.Update(pasteMsg("multi-line\npasted\ncontent"))
	assertEquiv(t, "after pasteMsg", m, ref)

	// Backspace 10 times to delete into pasted content
	for i := 0; i < 10; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		ref, _ = ref.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	assertEquiv(t, "after deleting into pasted content", m, ref)

	// Step 8: Resize terminal width
	m.SetWidth(45)
	ref.SetWidth(45)
	assertEquiv(t, "after resize to 45", m, ref)

	m.SetWidth(20)
	ref.SetWidth(20)
	assertEquiv(t, "after resize to 20", m, ref)

	// Step 9: Reset and SetValue
	m.Reset()
	ref.Reset()
	assertEquiv(t, "after Reset", m, ref)

	m.SetValue("Fresh content after reset that wraps nicely")
	ref.SetValue("Fresh content after reset that wraps nicely")
	assertEquiv(t, "after SetValue", m, ref)
}

// TestActualCacheUnicodeAdversarial exercises the actual cache across complex Unicode
// classes, combining marks, CJK wide characters, ZWJ sequences, flags, and narrow widths.
func TestActualCacheUnicodeAdversarial(t *testing.T) {
	fixtures := []struct {
		name string
		text string
	}{
		{"ASCII_Unbroken", strings.Repeat("x", 400)},
		{"Arabic_Letters", "السلام عليكم ورحمة الله وبركاته يا أخي العزيز أهلاً بك"},
		{"Arabic_CombiningHarakat", "الْعَرَبِيَّةُ لُغَةٌ جَمِيلَةٌ مَعَ التَّشْكِيلِ وَالْحَرَكَاتِ الْمُتَعَدِّدَةِ"},
		{"Arabic_CombiningRuns", strings.Repeat("بًٌٍَُِّْ", 20)},
		{"Latin_CombiningAccents", "e\u0301c\u0327a\u0300o\u0302u\u0308n\u0303e\u0301c\u0327a\u0300o\u0302u\u0308n\u0303"},
		{"CJK_WideRunes", "漢字テスト、日本語の文字列、全角文字の折り返し処理の検証です。美しい日本語の文章。"},
		{"Emoji_SingleCodePoint", "🚀🎉🔥⭐✨🎈💡🏆🎯🎨🎭🎪🎰🎲🎸🎺🎻🎹🎼"},
		{"Emoji_Modifiers", "👍🏻👍🏼👍🏽👍🏾👍🏿👋🏻👋🏼👋🏽👋🏾👋🏿🙌🏻🙌🏼🙌🏽🙌🏾🙌🏿"},
		{"Emoji_ZWJSequences", "👨‍👩‍👧‍👦👩‍💻🧑‍🔬🏃‍♀️🏳️‍🌈👨‍👩‍👧‍👦👩‍💻🧑‍🔬🏃‍♀️🏳️‍🌈"},
		{"RegionalFlags", "🇸🇦🇺🇸🇬🇧🇩🇪🇯🇵🇫🇷🇨🇦🇧🇷🇮🇹🇪🇸🇰🇷🇨🇳"},
		{"Mixed_SpacesWordsWide", "Hello 世界! 🌍 Here is some Arabic: مرحبا بكم and emoji 👍🏽👨‍👩‍👧."},
		{"TrailingSpaces", "WordWithTrailingSpaces    and   more   spaces   "},
	}

	widths := []int{1, 2, 5, 10, 18, 25, 40, 70}

	for _, tc := range fixtures {
		for _, w := range widths {
			t.Run(fmt.Sprintf("%s_w%d", tc.name, w), func(t *testing.T) {
				m := New()
				m.SetWidth(w)
				runes := []rune(tc.text)
				if len(runes) == 0 {
					return
				}

				// Initial wrap
				gotInit := m.memoizedWrap(runes, m.width, 0)
				assertGridMatchesOracle(t, "initial wrap", gotInit, runes, m.width)

				// Test 1: Deletion from tail (if length > 5)
				if len(runes) > 5 {
					del := runes[:len(runes)-3]
					gotDel := m.memoizedWrap(del, m.width, 0)
					assertGridMatchesOracle(t, "tail deletion", gotDel, del, m.width)
				}

				// Test 2: Appending to tail
				appended := append(runes, []rune(" END")...)
				gotApp := m.memoizedWrap(appended, m.width, 0)
				assertGridMatchesOracle(t, "tail append", gotApp, appended, m.width)

				// Test 3: Mid-point edit (if length > 10)
				if len(runes) > 10 {
					mid := len(runes) / 2
					midEdit := append(append([]rune{}, runes[:mid]...), []rune("★")...)
					midEdit = append(midEdit, runes[mid+1:]...)
					gotMid := m.memoizedWrap(midEdit, m.width, 0)
					assertGridMatchesOracle(t, "mid edit", gotMid, midEdit, m.width)
				}
			})
		}
	}
}

// TestActualCacheSequentialRandomEdits runs a deterministic pseudo-random sequence of
// 500 edit operations on a single Model with real cache state, comparing every step
// against full re-wrap.
func TestActualCacheSequentialRandomEdits(t *testing.T) {
	const seed = 42
	rnd := rand.New(rand.NewSource(seed))

	runePool := []rune(
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 " +
			"مرحباالعالم " +
			"日本語文字 " +
			"e\u0301c\u0327 " +
			"👍🏽🚀🎉",
	)

	m := New()
	m.SetWidth(25)

	// Start with 50 initial runes
	runes := make([]rune, 50)
	for i := range runes {
		runes[i] = runePool[rnd.Intn(len(runePool))]
	}

	_ = m.memoizedWrap(runes, m.width, 0)

	const numOps = 500
	for opIdx := 0; opIdx < numOps; opIdx++ {
		opType := rnd.Intn(4)
		switch opType {
		case 0: // Insert 1..4 runes at random position
			k := rnd.Intn(4) + 1
			ins := make([]rune, k)
			for i := range ins {
				ins[i] = runePool[rnd.Intn(len(runePool))]
			}
			pos := 0
			if len(runes) > 0 {
				pos = rnd.Intn(len(runes) + 1)
			}
			newRunes := append([]rune{}, runes[:pos]...)
			newRunes = append(newRunes, ins...)
			newRunes = append(newRunes, runes[pos:]...)
			runes = newRunes

		case 1: // Delete 1..4 runes at random position
			if len(runes) > 2 {
				k := min(rnd.Intn(4)+1, len(runes)-1)
				pos := rnd.Intn(len(runes) - k + 1)
				newRunes := append([]rune{}, runes[:pos]...)
				newRunes = append(newRunes, runes[pos+k:]...)
				runes = newRunes
			}

		case 2: // Replace 1..3 runes at random position
			if len(runes) > 2 {
				k := min(rnd.Intn(3)+1, len(runes))
				pos := rnd.Intn(len(runes) - k + 1)
				for i := 0; i < k; i++ {
					runes[pos+i] = runePool[rnd.Intn(len(runePool))]
				}
			}

		case 3: // Change width
			newWidth := rnd.Intn(60) + 10 // 10..69
			m.SetWidth(newWidth)
		}

		got := m.memoizedWrap(runes, m.width, 0)
		want := wrap(runes, m.width)

		if !equalGrid(got, want) {
			t.Fatalf("op %d (type %d, seed %d) mismatch at width %d with %d runes:\ngot  (%d rows):\n%s\nwant (%d rows):\n%s",
				opIdx, opType, seed, m.width, len(runes), len(got), formatGrid(got), len(want), formatGrid(want))
		}
	}
}

// TestCacheRetainedEntriesOnLineShiftAndShrink validates cache cleanup and ownership
// when line counts change or lines shift via splitLine, mergeLineAbove, and mergeLineBelow.
func TestCacheRetainedEntriesOnLineShiftAndShrink(t *testing.T) {
	m := New()
	m.SetWidth(25)

	// Create 5 lines and populate cache
	m.SetValue("line0_content_here\nline1_content_here\nline2_content_here\nline3_content_here\nline4_content_here")
	_ = m.View()

	if len(m.layoutCache.lines) != 5 {
		t.Fatalf("expected 5 cached line layouts, got %d", len(m.layoutCache.lines))
	}

	// 1. mergeLineAbove at line 4 (line count shrinks to 4)
	m.row = 4
	m.col = 0
	m.mergeLineAbove(4)
	_ = m.View()

	if len(m.layoutCache.lines) > len(m.value) {
		t.Fatalf("cache retained dead entries after mergeLineAbove: len(cache)=%d, len(value)=%d",
			len(m.layoutCache.lines), len(m.value))
	}

	// 2. mergeLineBelow at line 0 (line count shrinks to 3)
	m.row = 0
	m.col = len(m.value[0])
	m.mergeLineBelow(0)
	_ = m.View()

	if len(m.layoutCache.lines) > len(m.value) {
		t.Fatalf("cache retained dead entries after mergeLineBelow: len(cache)=%d, len(value)=%d",
			len(m.layoutCache.lines), len(m.value))
	}

	// 3. splitLine at line 1 (line count grows to 4)
	m.row = 1
	m.col = len(m.value[1]) / 2
	m.splitLine(1, m.col)
	_ = m.View()

	if len(m.layoutCache.lines) > len(m.value) {
		t.Fatalf("cache exceeded line count after splitLine: len(cache)=%d, len(value)=%d",
			len(m.layoutCache.lines), len(m.value))
	}
}

// FuzzActualCacheWrap fuzzes the real Model cache against the independent wrap oracle.
func FuzzActualCacheWrap(f *testing.F) {
	f.Add("hello world this is a test line for wrapping", 15)
	f.Add(strings.Repeat("a", 200), 20)
	f.Add("مرحبا بالعالم كيف الحال اليوم في كل مكان", 15)
	f.Add("日本語テスト文章です折り返し確認", 12)
	f.Add("👍🏽👨‍👩‍👧‍👦🚀🎉", 6)
	f.Add("e\u0301c\u0327a\u0300o\u0302", 4)
	f.Add("trailing spaces    and words", 10)

	f.Fuzz(func(t *testing.T, text string, width int) {
		if width < 1 || width > 100 {
			return
		}
		runes := []rune(text)
		if len(runes) > 400 {
			return
		}

		m := New()
		m.SetWidth(width)

		// Pass 1: Initial wrap
		got1 := m.memoizedWrap(runes, m.width, 0)
		want1 := wrap(runes, m.width)
		if !equalGrid(got1, want1) {
			t.Fatalf("fuzz initial wrap mismatch for text %q at width %d", text, width)
		}

		// Pass 2: Incremental mutation
		if len(runes) > 2 {
			mutated := runes[:len(runes)-1]
			got2 := m.memoizedWrap(mutated, m.width, 0)
			want2 := wrap(mutated, m.width)
			if !equalGrid(got2, want2) {
				t.Fatalf("fuzz incremental wrap mismatch for %q -> %q at width %d", string(runes), string(mutated), width)
			}
		}
	})
}
