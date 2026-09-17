package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"nabd/internal/provider"

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
	if err == nil || ok || out != "" || !strings.Contains(err.Error(), "guarded execution") {
		t.Fatalf("plain execution must be refused: out=%q ok=%v err=%v", out, ok, err)
	}
	if _, err := tool.GuardedResult(context.Background(), json.RawMessage(`{"name":"greet"}`)); err != nil {
		t.Fatalf("guarded load must succeed: %v", err)
	}

	// The file changes under the session's feet.
	if err := os.WriteFile(path, []byte("---\nname: greet\ndescription: test skill\n---\nEVIL body\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = tool.GuardedResult(context.Background(), json.RawMessage(`{"name":"greet"}`))
	if err == nil {
		t.Fatal("a mutated body must be refused")
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

// GuardedFor is the registry's side of the guarded seam: it must report a guard
// exactly while the skill tool is installed, and must withdraw it when the index
// is emptied. The agent loop fails closed on the false answer, so a stale true
// or a stale false are both wrong.
func TestSkillPlainExecutionIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "greet", "PLAIN-SKILL-BODY")
	loaded, _ := skill.LoadUser(dir)
	reg := &Registry{byName: map[string]Tool{}}
	reg.SetSkillIndex(func() []skill.Skill { return loaded })
	for _, tc := range []struct {
		name string
		run  func() (string, bool, error)
	}{
		{"Run", func() (string, bool, error) {
			return reg.Run(context.Background(), provider.ToolCall{Name: "skill", Input: json.RawMessage(`{"name":"greet"}`)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, ok, err := tc.run()
			if err == nil || ok || out != "" || strings.Contains(err.Error(), "PLAIN-SKILL-BODY") {
				t.Fatalf("plain output was not refused: out=%q ok=%v err=%v", out, ok, err)
			}
		})
	}
	outcome, err := reg.RunDetailed(context.Background(), "skill", json.RawMessage(`{"name":"greet"}`))
	if err == nil || outcome.OK || outcome.Text != "" || strings.Contains(err.Error(), "PLAIN-SKILL-BODY") {
		t.Fatalf("detailed plain output was not refused: outcome=%+v err=%v", outcome, err)
	}
	guard, ok := reg.GuardedFor("skill")
	if !ok {
		t.Fatal("guarded producer missing")
	}
	if _, err := guard.GuardedResult(context.Background(), json.RawMessage(`{"name":"greet"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrySkillLifecycleSupportsConcurrentReaders(t *testing.T) {
	reg := &Registry{byName: map[string]Tool{}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				reg.Specs()
				reg.GuardedFor("skill")
				reg.Class("skill")
			}
		}()
	}
	for i := 0; i < 100; i++ {
		if i%2 == 0 {
			reg.SetSkillIndex(func() []skill.Skill { return []skill.Skill{{Name: "greet"}} })
		} else {
			reg.SetSkillIndex(func() []skill.Skill { return nil })
		}
	}
	wg.Wait()
	reg.SetSkillIndex(func() []skill.Skill { return nil })
	if _, ok := reg.GuardedFor("skill"); ok {
		t.Fatal("empty final index still registered")
	}
}

func TestRegistryGuardedForTracksSkillInstallation(t *testing.T) {
	reg := &Registry{byName: map[string]Tool{}}

	if g, ok := reg.GuardedFor("skill"); ok || g != nil {
		t.Fatalf("an empty registry must not offer a guard: guard=%v ok=%v", g, ok)
	}

	reg.SetSkillIndex(func() []skill.Skill {
		return []skill.Skill{{Name: "greet", Rel: "greet.md", Scope: skill.ScopeUser}}
	})
	g, ok := reg.GuardedFor("skill")
	if !ok {
		t.Fatal("an installed skill index must offer the guarded outcome")
	}
	if _, ok := g.(skillTool); !ok {
		t.Fatalf("GuardedFor returned %T, want the skill tool's guard", g)
	}

	reg.SetSkillIndex(func() []skill.Skill { return nil })
	if g, ok := reg.GuardedFor("skill"); ok || g != nil {
		t.Fatalf("an emptied skill index must withdraw the guard: guard=%v ok=%v", g, ok)
	}
}
