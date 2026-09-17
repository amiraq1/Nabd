package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/ignorefile"
)

// writeSkill lays down one skill file. fm is written verbatim after the opening
// fence, so a test can produce a malformed header on purpose.
func writeSkill(t *testing.T, dir, rel, fm, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\n"+fm+"---\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const goodFM = "name: greet\ndescription: say hello\n"

// A project skill whose path escapes the root through a symlink is refused and
// reported. Without the symlink check the loader would happily read a file
// outside the scope the user opted into.
func TestProjectSkillSymlinkEscapeIsRefused(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("---\nname: secret\ndescription: stolen\n---\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	dir := ProjectDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "evil.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	skills, diag := LoadProject(root, ignorefile.Matcher{}, true)
	for _, s := range skills {
		if s.Name == "secret" {
			t.Fatalf("a symlinked skill escaped the root: %+v", s)
		}
	}
	if len(diag) == 0 {
		t.Fatal("the refusal must be reported, not silent")
	}
	joined := ""
	for _, d := range diag {
		joined += d.String() + "\n"
	}
	if !strings.Contains(joined, "symlink") {
		t.Errorf("diagnostic must say why: %s", joined)
	}
}

// A 100 MiB body is refused. The read is bounded by construction, so the file
// never becomes a 100 MiB allocation; this test also pins the exact boundary
// one byte either side of the limit.
func TestHugeSkillFileIsRefusedWithoutBuffering(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "huge.md")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("---\n" + goodFM + "---\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(100 << 20); err != nil { // sparse: no 100 MiB written
		t.Fatal(err)
	}
	f.Close()

	skills, diag := LoadUser(dir)
	if len(skills) != 0 {
		t.Fatalf("a 100 MiB skill was accepted: %+v", skills)
	}
	if len(diag) != 1 || diag[0].Kind != DiagUnreadable {
		t.Fatalf("want one unreadable diagnostic, got %+v", diag)
	}
	if !strings.Contains(diag[0].Msg, "exceeds") {
		t.Errorf("diagnostic must name the limit, got %q", diag[0].Msg)
	}

	// The boundary: a body at the ceiling loads, one byte over does not.
	body := strings.Repeat("x", maxBodyBytes)
	if _, err := parseSkill(dir, "boundary.md", ScopeUser,
		[]byte("---\n"+goodFM+"---\n"+body)); err != nil {
		t.Errorf("a body at the limit must load: %v", err)
	}
	if _, err := parseSkill(dir, "boundary.md", ScopeUser,
		[]byte("---\n"+goodFM+"---\n"+body+"x")); err == nil {
		t.Error("a body one byte over the limit must be refused")
	}
}

// A repeated frontmatter key is refused: "last one wins" would let a second
// name silently replace the first.
func TestDuplicateFrontmatterKeyIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "dup.md", "name: first\nname: second\ndescription: d\n", "body\n")

	skills, diag := LoadUser(dir)
	if len(skills) != 0 {
		t.Fatalf("a duplicate key was accepted: %+v", skills)
	}
	if len(diag) != 1 || !strings.Contains(diag[0].Msg, "duplicate") {
		t.Fatalf("want a duplicate-key diagnostic, got %+v", diag)
	}
}

// Two files claiming one name yield exactly one usable skill and one collision
// diagnostic naming the file that won, so the user can see which one is live.
func TestNameCollisionKeepsOneAndReportsTheLoser(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "a-first.md", "name: dup\ndescription: first\n", "A\n")
	writeSkill(t, dir, "z-last.md", "name: dup\ndescription: second\n", "Z\n")

	skills, diag := LoadUser(dir)
	if len(skills) != 1 {
		t.Fatalf("want exactly one skill, got %d: %+v", len(skills), skills)
	}
	if skills[0].Desc != "first" {
		t.Errorf("the first loaded file must win, got %q", skills[0].Desc)
	}
	if len(diag) != 1 || diag[0].Kind != DiagCollision {
		t.Fatalf("want one collision diagnostic, got %+v", diag)
	}
	if !strings.Contains(diag[0].Msg, "a-first.md") {
		t.Errorf("the diagnostic must name the winner, got %q", diag[0].Msg)
	}
}

// Project skills are absent unless the operator opted in. This is the whole
// point of the default stance: a repository cannot enable its own instructions.
func TestProjectSkillsAreAbsentWhenOptInIsOff(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, ProjectDir(root), "in.md", goodFM, "body\n")

	skills, diag := LoadProject(root, ignorefile.Matcher{}, false)
	if len(skills) != 0 || len(diag) != 0 {
		t.Fatalf("opt-out must load nothing at all, got %+v %+v", skills, diag)
	}

	text, _ := FormatForPrompt(skills)
	if strings.Contains(text, "greet") {
		t.Errorf("an opted-out project skill reached the prompt: %q", text)
	}

	// And with the opt-in the same file does load, so the test would catch a
	// loader that simply never reads anything.
	on, _ := LoadProject(root, ignorefile.Matcher{}, true)
	if len(on) != 1 || on[0].Scope != ScopeProject {
		t.Fatalf("opt-in must load the project skill, got %+v", on)
	}
}

// The recorded hash is over the body bytes actually loaded, so a replay can
// prove which instructions the session ran with.
func TestJournalRecordsHashMatchesLoadedBytes(t *testing.T) {
	dir := t.TempDir()
	body := "steps:\n1. do the thing\n"
	writeSkill(t, dir, "greet.md", goodFM, body)

	skills, diag := LoadUser(dir)
	if len(skills) != 1 {
		t.Fatalf("want one skill, got %+v (diag %+v)", skills, diag)
	}
	sum := sha256.Sum256([]byte(body))
	want := hex.EncodeToString(sum[:])
	if skills[0].Hash != want {
		t.Fatalf("hash = %s, want sha256 of the body %s", skills[0].Hash, want)
	}

	recs := JournalRecords(skills)
	if len(recs) != 1 || recs[0].Hash != want || recs[0].Name != "greet" {
		t.Fatalf("journal record must carry the loaded hash, got %+v", recs)
	}
}

// The prompt index is bounded, deterministic, and never silently short.
func TestFormatForPromptIsBoundedSortedAndLoud(t *testing.T) {
	mk := func(name string, descLen int) Skill {
		return Skill{Name: name, Desc: strings.Repeat("d", descLen), Rel: name + ".md", Scope: ScopeUser}
	}
	a, b := mk("alpha", 10), mk("bravo", 10)
	one, diag := FormatForPrompt([]Skill{b, a})
	two, _ := FormatForPrompt([]Skill{a, b})
	if one != two {
		t.Fatalf("order leaked into the prompt:\n%q\n%q", one, two)
	}
	if !strings.Contains(one, "alpha") || strings.Contains(one, "body") {
		t.Errorf("index must list names without bodies: %q", one)
	}
	if len(diag) != 0 {
		t.Errorf("small index must not report diagnostics: %+v", diag)
	}

	// Auto-invocation can be disabled for a skill; it then never reaches the
	// prompt, because the model is not allowed to call it on its own.
	hidden := Skill{Name: "hidden", Desc: "d", Rel: "hidden.md", NoAuto: true}
	text, _ := FormatForPrompt([]Skill{hidden})
	if strings.Contains(text, "hidden") {
		t.Errorf("a disable-model-invocation skill reached the prompt: %q", text)
	}

	// Over budget: the excess is dropped AND reported.
	var many []Skill
	for i := 0; i < 40; i++ {
		many = append(many, Skill{
			Name:  "s" + strings.Repeat("x", i%5) + string(rune('a'+i)),
			Desc:  strings.Repeat("d", maxDescBytes),
			Rel:   "s.md",
			Scope: ScopeUser,
		})
	}
	text, diag = FormatForPrompt(many)
	if len(text) > maxPromptBytes+len(promptHeader)+1 {
		t.Errorf("prompt block is %d bytes, over the %d limit", len(text), maxPromptBytes)
	}
	if len(diag) == 0 {
		t.Error("dropping skills must be reported, never silent")
	}
}
