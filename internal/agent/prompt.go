package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"nabd/internal/provider"
)

// Section is one independently replaceable prompt fragment. An empty Name is
// the raw preamble; all other names are rendered as stable tagged sections.
type Section struct{ Name, Body string }

var sectionName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Prompter builds the model-facing system prompt from the stable base and the
// tools active in this session. It is deliberately outside tools: prompt
// construction must not discover or activate capabilities.
type Prompter struct {
	Base  string
	Extra string
}

// Render renders independently tagged sections. Empty Name is the raw
// preamble; all other names must be lowercase ASCII letters, digits, or underscores.
func Render(secs []Section) (string, error) {
	var b strings.Builder
	seen := make(map[string]bool, len(secs))
	for _, s := range secs {
		if s.Name == "" {
			if seen[s.Name] {
				return "", fmt.Errorf("duplicate prompt section %q", s.Name)
			}
			seen[s.Name] = true
			b.WriteString(s.Body)
			continue
		}
		if !sectionName.MatchString(s.Name) {
			return "", fmt.Errorf("invalid prompt section name %q", s.Name)
		}
		if seen[s.Name] {
			return "", fmt.Errorf("duplicate prompt section %q", s.Name)
		}
		seen[s.Name] = true
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
	if len(out) == 0 {
		return nil
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
	for i, s := range secs {
		fmt.Fprintf(h, "%d:%d:%s%d:%s;", i, len(s.Name), s.Name, len(s.Body), s.Body)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// PromptSections is an optional session-level source for rich tool sections.
type PromptSections interface{ PromptSections() []Section }

// BuildSections renders the base, optional extra, and active rich sections.
func (p Prompter) BuildSections(rich []Section) (string, error) {
	sections := []Section{{Name: "", Body: p.Base}}
	if p.Extra != "" {
		sections = append(sections, Section{Name: "extra", Body: p.Extra})
	}
	sections = append(sections, rich...)
	return Render(sections)
}

// Build preserves the old call shape while now returning errors instead of
// silently falling back to a prompt that lost its tools.
func (p Prompter) Build(specs []provider.ToolSpec) (string, error) {
	// specs is intentionally retained for source compatibility; rich sections
	// are the sole prompt tool path now.
	return p.BuildSections(nil)
}
