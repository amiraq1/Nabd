package main

import (
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/config"
	"nabd/internal/skill"
	"nabd/internal/tools"
)

// isolateConfig points the user-scoped config at an empty HOME and clears the
// opt-in, so a test proves exactly one source of NABD_SKILLS_PROJECT.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NABD_CONFIG", "")
	t.Setenv("NABD_SKILLS_PROJECT", "")
	config.ResetForTest()
	t.Cleanup(config.ResetForTest)
}

func writeProjectSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, ".nabd", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: project skill\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The opt-in is the only control separating "untrusted repository" from "active
// capability". It must come from user-scoped config/environment; a config file
// inside the project root must never enable it. This test fails if config.Get
// ever grows a project-root resolver.
func TestProjectSkillsStayDisabledWhenOptInLivesInTheProjectRoot(t *testing.T) {
	isolateConfig(t)
	proj := t.TempDir()
	writeProjectSkill(t, proj, "evil", "project-authored instructions\n")

	// The repository asks for the capability in its own tree.
	if err := os.MkdirAll(filepath.Join(proj, ".ag"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".ag", "config"), []byte("NABD_SKILLS_PROJECT=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := config.Get("NABD_SKILLS_PROJECT"); got != "" {
		t.Fatalf("config.Get read a project-root config: NABD_SKILLS_PROJECT=%q, want empty", got)
	}

	root, err := tools.NewRoot(proj)
	if err != nil {
		t.Fatal(err)
	}
	skills, _, err := loadSessionSkills(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Scope == skill.ScopeProject || s.Name == "evil" {
			t.Fatalf("project skill loaded without a user-scoped opt-in: %+v", s)
		}
	}
}

// Positive control: the same project skill DOES load when the operator sets the
// opt-in through the environment. Without this, the test above could pass for
// the wrong reason (a loader that never loads anything).
func TestProjectSkillsLoadWhenOptInIsUserScoped(t *testing.T) {
	isolateConfig(t)
	t.Setenv("NABD_SKILLS_PROJECT", "1")
	config.ResetForTest()
	t.Cleanup(config.ResetForTest)

	proj := t.TempDir()
	writeProjectSkill(t, proj, "helper", "operator-approved instructions\n")

	root, err := tools.NewRoot(proj)
	if err != nil {
		t.Fatal(err)
	}
	skills, _, err := loadSessionSkills(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skills {
		if s.Name == "helper" && s.Scope == skill.ScopeProject {
			return
		}
	}
	t.Fatalf("with NABD_SKILLS_PROJECT=1 the project skill must load; got %+v", skills)
}
