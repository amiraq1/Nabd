package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
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
	root      *Root
	sh        *snap.Shadow
	edits     *editLog
	list      []Tool
	byName    map[string]Tool
	linesRead int // set by read_file, consumed by the next commit()
}

func NewRegistry(root *Root, sh *snap.Shadow) *Registry {
	log := &editLog{}
	r := &Registry{root: root, sh: sh, edits: log, byName: map[string]Tool{}}
	r.add(readFile{root, r}, globFiles{root}, grepFiles{root})
	r.add(writeFile{root, sh, log, r}, editFile{root, sh, log, r})
	r.add(bashTool{root})
	return r
}

// SetLinesRead records how many lines read_file just showed the model. The
// next commit() stamps that number on the EditRecord: a blind write (no
// read before it) carries ReadLines=0.
func (r *Registry) SetLinesRead(n int) { r.linesRead = n }

// LastEdit returns the persisted record of the newest mutation, or nil if
// nothing has been written yet.
func (r *Registry) LastEdit() *agent.EditRecord {
	es := r.edits.all()
	if len(es) == 0 {
		return nil
	}
	return es[len(es)-1].Record
}

func (r *Registry) Edits() []Edit {
	return r.edits.all()
}

func (r *Registry) Class(tool string) (perm.Class, bool) {
	switch tool {
	case "write_file", "edit_file":
		return perm.Mutating, true
	case "read_file", "glob", "grep":
		return perm.ReadOnly, true
	default:
		return 0, false
	}
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

type Detailed interface {
	RunDetailed(context.Context, json.RawMessage) (agent.Outcome, error)
}

func (r *Registry) RunDetailed(ctx context.Context, name string, raw json.RawMessage) (agent.Outcome, error) {
	t, ok := r.byName[name]
	if !ok {
		return agent.Outcome{}, fmt.Errorf("أداة غير معروفة: %s", name)
	}
	if d, ok := t.(Detailed); ok {
		return d.RunDetailed(ctx, raw)
	}
	txt, good, err := t.Run(ctx, raw)
	return agent.Outcome{Text: txt, OK: good}, err
}
