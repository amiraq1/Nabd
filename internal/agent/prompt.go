package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"nabd/internal/provider"
)

// Section is one independently replaceable prompt fragment. An empty Name is
// the raw preamble; all other names are rendered as stable tagged sections.
type Section struct{ Name, Body string }

var sectionName = regexp.MustCompile(`^[a-z]*$`)

// Prompter builds the model-facing system prompt from the stable base and the
// tools active in this session. It is deliberately outside tools: prompt
// construction must not discover or activate capabilities.
type Prompter struct {
	Base  string
	Extra string
}

// Render renders independently tagged sections. Empty Name is the raw
// preamble; all other names must be lowercase ASCII letters.
func Render(secs []Section) (string, error) {
	var b strings.Builder
	for _, s := range secs {
		if s.Name == "" {
			b.WriteString(s.Body)
			continue
		}
		if !sectionName.MatchString(s.Name) {
			return "", fmt.Errorf("invalid prompt section name %q", s.Name)
		}
		fmt.Fprintf(&b, "\n<<<PROMPT_SECTION[%s]>>>\n%s\n<<<END_PROMPT_SECTION[%s]>>>", s.Name, s.Body, s.Name)
	}
	return b.String(), nil
}

// Diff returns only changed sections. A nil value means the section was deleted.
func Diff(prev, cur []Section) map[string]*string {
	old, now := sectionMap(prev), sectionMap(cur)
	out := map[string]*string{}
	for name, body := range now {
		if prior, ok := old[name]; !ok || prior != body {
			v := body
			out[name] = &v
		}
	}
	for name := range old {
		if _, ok := now[name]; !ok {
			out[name] = nil
		}
	}
	return out
}

func sectionMap(secs []Section) map[string]string {
	m := make(map[string]string, len(secs))
	for _, s := range secs {
		m[s.Name] = s.Body
	}
	return m
}

// Fingerprint identifies the exact ordered sections seen by the model.
func Fingerprint(secs []Section) string {
	h := sha256.New()
	for _, s := range secs {
		fmt.Fprintf(h, "%d:%s%d:%s;", len(s.Name), s.Name, len(s.Body), s.Body)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BuildToolSections retains the legacy text API while keeping deterministic
// ordering. The richer ToolPrompter path is used by tools.Registry.
func BuildToolSections(specs []provider.ToolSpec) string {
	cp := append([]provider.ToolSpec(nil), specs...)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].Name < cp[j].Name })
	var b strings.Builder
	for _, spec := range cp {
		b.WriteString("\n## tool ")
		b.WriteString(spec.Name)
	}
	return b.String()
}

// Build appends session-specific prompt data after the fixed base.
func (p Prompter) Build(specs []provider.ToolSpec) string {
	sections := []Section{{Name: "", Body: p.Base}}
	if p.Extra != "" {
		sections = append(sections, Section{Name: "extra", Body: p.Extra})
	}
	if text, err := Render(sections); err == nil {
		return text + BuildToolSections(specs)
	}
	return p.Base
}
