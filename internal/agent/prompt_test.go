package agent

import (
	"strings"
	"testing"

	"nabd/internal/provider"
)

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
	if got != "base\nskills" {
		t.Fatalf("prompt = %q", got)
	}
}
