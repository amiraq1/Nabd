package agent

import (
	"strings"
	"testing"
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

func TestRenderSectionNameRules(t *testing.T) {
	for _, name := range []string{"_skills", "1", "__"} {
		if _, err := Render([]Section{{Name: name, Body: "x"}}); err == nil {
			t.Fatalf("invalid section %q accepted", name)
		}
	}
	if _, err := Render([]Section{{Name: "skill-index", Body: "x"}}); err != nil {
		t.Fatalf("hyphenated section rejected: %v", err)
	}
}

func TestDiffAndFingerprint(t *testing.T) {
	prev := []Section{{Name: "", Body: "p"}, {Name: "tools", Body: "a"}, {Name: "old", Body: "x"}}
	cur := []Section{{Name: "", Body: "p"}, {Name: "tools", Body: "b"}, {Name: "new", Body: "n"}}
	d := Diff(prev, cur)
	if d[""] != nil || *d["tools"] != "b" || d["old"] != nil || *d["new"] != "n" {
		t.Fatalf("unexpected diff: %#v", d)
	}
	if Fingerprint(prev) == Fingerprint(cur) || Fingerprint(prev) == Fingerprint([]Section{{Name: "", Body: "p"}, {Name: "tools", Body: "a"}, {Name: "old", Body: "y"}}) || Fingerprint([]Section{{Name: "a", Body: "x"}, {Name: "b", Body: "y"}}) == Fingerprint([]Section{{Name: "b", Body: "y"}, {Name: "a", Body: "x"}}) {
		t.Fatal("fingerprint missed a change")
	}
}

func TestPrompterSectionsUseActiveRichTools(t *testing.T) {
	got, err := (Prompter{Base: "base"}).BuildSections([]Section{{Name: "a", Body: "snippet"}})
	if err != nil || !strings.Contains(got, "snippet") || strings.Contains(got, "Description") {
		t.Fatalf("prompt=%q err=%v", got, err)
	}
}

func TestPrompterBuildKeepsBaseAndExtra(t *testing.T) {
	got, err := (Prompter{Base: "base", Extra: "skills"}).Build(nil)
	if err != nil || !strings.HasPrefix(got, "base") || !strings.Contains(got, "PROMPT_SECTION[extra]") || !strings.Contains(got, "skills") {
		t.Fatalf("prompt = %q err=%v", got, err)
	}
}

func TestRenderRejectsDuplicateSections(t *testing.T) {
	if _, err := Render([]Section{{Name: "tools", Body: "a"}, {Name: "tools", Body: "b"}}); err == nil {
		t.Fatal("duplicate section accepted")
	}
}

func TestDiffIdenticalReturnsNil(t *testing.T) {
	if got := Diff([]Section{{Name: "tools", Body: "a"}}, []Section{{Name: "tools", Body: "a"}}); got != nil {
		t.Fatalf("identical diff = %#v, want nil", got)
	}
}
