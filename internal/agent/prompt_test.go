package agent

import (
	"strings"
	"testing"

	"nabd/internal/provider"
)

func TestRenderStableAndSectioned(t *testing.T) {
	secs := []Section{{Name: "", Body: "base"}, {Name: "tools", Body: "read"}}
	want, err := Render(secs)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := Render(secs)
		if err != nil || got != want {
			t.Fatalf("render changed on run %d: %q", i, got)
		}
	}
	if !strings.Contains(want, "PROMPT_SECTION[tools]") {
		t.Fatalf("missing section tag: %q", want)
	}
}

func TestRenderRejectsInvalidSectionName(t *testing.T) {
	if _, err := Render([]Section{{Name: "Tool-1", Body: "x"}}); err == nil {
		t.Fatal("invalid section name accepted")
	}
}

func TestDiffAndFingerprint(t *testing.T) {
	prev := []Section{{Name: "", Body: "p"}, {Name: "tools", Body: "a"}, {Name: "old", Body: "x"}}
	cur := []Section{{Name: "", Body: "p"}, {Name: "tools", Body: "b"}, {Name: "new", Body: "n"}}
	d := Diff(prev, cur)
	if d[""] != nil || *d["tools"] != "b" || d["old"] != nil || *d["new"] != "n" {
		t.Fatalf("unexpected diff: %#v", d)
	}
	if Fingerprint(prev) == Fingerprint(cur) || Fingerprint(prev) == Fingerprint([]Section{{Name: "", Body: "p"}, {Name: "tools", Body: "a"}, {Name: "old", Body: "y"}}) {
		t.Fatal("fingerprint missed a change")
	}
}

func TestBuildToolSectionsIsDeterministic(t *testing.T) {
	in := []provider.ToolSpec{{Name: "z", Description: "Z"}, {Name: "a", Description: "A"}}
	got := BuildToolSections(in)
	if strings.Index(got, "## tool a") > strings.Index(got, "## tool z") {
		t.Fatalf("tool sections are not sorted: %q", got)
	}
	in[0].Name = "mutated"
	if strings.Contains(got, "mutated") {
		t.Fatal("section construction retained mutable input")
	}
}

func TestPrompterBuildKeepsBaseAndExtra(t *testing.T) {
	got := (Prompter{Base: "base", Extra: "skills"}).Build(nil)
	if !strings.HasPrefix(got, "base") || !strings.Contains(got, "PROMPT_SECTION[extra]") || !strings.Contains(got, "skills") {
		t.Fatalf("prompt = %q", got)
	}
}
