package agent

import (
	"sort"
	"strings"

	"nabd/internal/provider"
)

// Prompter builds the model-facing system prompt from the stable base and the
// tools active in this session. It is deliberately outside tools: prompt
// construction must not discover or activate capabilities.
type Prompter struct {
	Base  string
	Extra string
}

// BuildToolSections returns deterministic, bounded-by-input tool sections.
// Tool specs are sorted by name so ReadDir/order or registry insertion order
// cannot make the fixed prompt change between runs.
func BuildToolSections(specs []provider.ToolSpec) string {
	cp := append([]provider.ToolSpec(nil), specs...)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].Name < cp[j].Name })
	var b strings.Builder
	for _, spec := range cp {
		b.WriteString("\n## tool ")
		b.WriteString(spec.Name)
		if spec.Description != "" {
			b.WriteString("\n")
			b.WriteString(spec.Description)
		}
	}
	return b.String()
}

// Build appends session-specific prompt data after the fixed base. Empty
// sections are omitted, preserving the historical prompt byte-for-byte.
func (p Prompter) Build(specs []provider.ToolSpec) string {
	base := p.Base
	if base == "" {
		return p.Extra
	}
	var b strings.Builder
	b.WriteString(base)
	if tools := BuildToolSections(specs); tools != "" {
		b.WriteString(tools)
	}
	if p.Extra != "" {
		b.WriteString("\n")
		b.WriteString(p.Extra)
	}
	return b.String()
}
