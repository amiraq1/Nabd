package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/perm"
	"nabd/internal/skill"
)

func writeTestSkill(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name+".md")
	content := "---\nname: " + name + "\ndescription: test skill\n---\n" + body
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A body edited after the session loaded it is refused. This is the test that
// catches a skill tool that trusts its index: without the re-hash it would
// happily serve instructions the prompt was never built from, and the journal
// would not show the substitution.
func TestSkillToolRefusesBodyChangedSinceLoad(t *testing.T) {
	dir := t.TempDir()
	path := writeTestSkill(t, dir, "greet", "original body\n")

	loaded, diag := skill.LoadUser(dir)
	if len(loaded) != 1 {
		t.Fatalf("setup: want 1 skill, got %d (%+v)", len(loaded), diag)
	}

	reg := &Registry{byName: map[string]Tool{}, skillsFn: func() []skill.Skill { return loaded }}
	tool := skillTool{reg}

	out, ok, err := tool.Run(context.Background(), json.RawMessage(`{"name":"greet"}`))
	if err != nil || !ok {
		t.Fatalf("first load must succeed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "original body") {
		t.Fatalf("body not returned: %q", out)
	}

	// The file changes under the session's feet.
	if err := os.WriteFile(path, []byte("---\nname: greet\ndescription: test skill\n---\nEVIL body\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, ok, err = tool.Run(context.Background(), json.RawMessage(`{"name":"greet"}`))
	if err == nil {
		t.Fatal("a mutated body must be refused")
	}
	if ok {
		t.Error("a refused load must not report success")
	}
	if !strings.Contains(err.Error(), "changed since load") {
		t.Errorf("the refusal must say why, got %q", err)
	}
}

func TestSkillToolArgumentBoundary(t *testing.T) {
	reg := &Registry{byName: map[string]Tool{}, skillsFn: func() []skill.Skill { return nil }}
	tool := skillTool{reg}

	for _, tc := range []struct {
		name string
		args string
	}{
		{"unknown field", `{"name":"greet","extra":1}`},
		{"duplicate key", `{"name":"greet","name":"other"}`},
		{"empty name", `{"name":"  "}`},
		{"unknown skill", `{"name":"nope"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := tool.Run(context.Background(), json.RawMessage(tc.args)); err == nil {
				t.Fatalf("%s must be rejected", tc.name)
			}
		})
	}
}

// The skill body is untrusted content: reading it must stay ReadOnly so it
// cannot be routed through a mutating approval path.
func TestSkillToolIsReadOnly(t *testing.T) {
	if got := (skillTool{}).Class(); got != perm.ReadOnly {
		t.Fatalf("skill tool class = %v, want ReadOnly", got)
	}
}
