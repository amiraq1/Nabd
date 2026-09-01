package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"nabd/internal/provider"
)

// Tool is one capability. Args arrive as the model wrote them: unvalidated
// JSON. Every tool validates its own, and none of them trusts a path.
type Tool interface {
	Name() string
	Spec() provider.ToolSpec
	Run(ctx context.Context, args json.RawMessage) (out string, ok bool, err error)
}

// Registry is the agent.Tools implementation. Read-only at v0.4: nothing
// here can change a byte on disk, which is why no permission gate exists
// yet. That gate arrives with write.go, not before.
type Registry struct {
	root   *Root
	list   []Tool
	byName map[string]Tool
}

func NewRegistry(root *Root) *Registry {
	r := &Registry{root: root, byName: map[string]Tool{}}
	r.add(readFile{root}, globFiles{root}, grepFiles{root})
	return r
}

func (r *Registry) add(ts ...Tool) {
	for _, t := range ts {
		r.list = append(r.list, t)
		r.byName[t.Name()] = t
	}
}

func (r *Registry) Specs() []provider.ToolSpec {
	out := make([]provider.ToolSpec, 0, len(r.list))
	for _, t := range r.list {
		out = append(out, t.Spec())
	}
	return out
}

// Run dispatches by name. An unknown name is an error the model reads and
// recovers from, not a crash: models do invent tools.
func (r *Registry) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	t, found := r.byName[c.Name]
	if !found {
		return "", false, fmt.Errorf("أداة مجهولة: %s", c.Name)
	}
	args := c.Input
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	return t.Run(ctx, args)
}

// spec is sugar so each tool declares its schema in one line.
func spec(name, desc, schema string) provider.ToolSpec {
	return provider.ToolSpec{
		Name: name, Description: desc, Schema: json.RawMessage(schema),
	}
}

// skipDir keeps the walkers out of places that are large, generated, or
// none of the agent's business.
func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "__pycache__",
		"target", "dist", "build", ".next", ".cache", ".idea":
		return true
	}
	return false
}
