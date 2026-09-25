package payload

import (
	"strings"
	"testing"
)

// TestDefaultSystemPromptMentionsUndo: the model must know the sanctioned
// recovery path for its own edits. Without it, the agent routes the user to
// git checkout / git revert for edits nabd can reverse itself — losing the
// journal-backed undo the tool exists to provide.
func TestDefaultSystemPromptMentionsUndo(t *testing.T) {
	p := DefaultSystemPrompt
	for _, want := range []string{"/undo", "write_file", "edit_file", "git checkout", "git revert"} {
		if !strings.Contains(p, want) {
			t.Errorf("DefaultSystemPrompt must mention %q", want)
		}
	}
}
