package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

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

// Registry is the agent.Tools implementation: it owns the permission Class
// lookup (perm.Classifier), stages read-credit for the next mutation
// (NBD-034), and dispatches every tool including write_file, edit_file,
// and bash. The gate itself lives in perm.Policy; the registry is where a
// tool's Class is declared and found.
// metadata is the per-invocation read state with Consume ownership: exactly
// one consumer may take it, and it resets on take so no later unrelated call
// can inherit a stale value. Protected by mu so concurrent tool calls cannot
// tear or double-consume it.
type metadata struct {
	mu         sync.Mutex
	credit     agent.ReadCredit // composite key: path + content hash + range + linesRead (NBD-034)
	truncated  bool             // set by read_file, consumed by RunDetailed
	nextOffset int              // set by read_file on truncation, consumed by RunDetailed
}

type Registry struct {
	root   *Root
	sh     *snap.Shadow
	edits  *editLog
	list   []Tool
	byName map[string]Tool
	meta   metadata
}

func NewRegistry(root *Root, sh *snap.Shadow) *Registry {
	log := &editLog{}
	r := &Registry{root: root, sh: sh, edits: log, byName: map[string]Tool{}}
	r.add(readFile{root, r}, globFiles{root}, grepFiles{root})
	r.add(writeFile{root, sh, log, r}, editFile{root, sh, log, r})
	r.add(bashTool{root})
	return r
}

// SetReadCredit records the provenance and line count of a read_file call (NBD-034).
// The next commit() validates this credit against the target file's path
// and pre-mutation content hash.
func (r *Registry) SetReadCredit(c agent.ReadCredit) {
	r.meta.mu.Lock()
	r.meta.credit = c
	r.meta.mu.Unlock()
}

// ReadCredit returns a copy of the currently staged read credit without consuming it.
func (r *Registry) ReadCredit() agent.ReadCredit {
	r.meta.mu.Lock()
	defer r.meta.mu.Unlock()
	return r.meta.credit
}

// ConsumeLinesRead atomically validates and returns the pending line count,
// then clears the staged credit so it cannot leak into a later write.
//
// Both abs and hashBefore are required arguments. Omitting the hash is a
// compile error, not a silent bypass of NBD-034. An empty hashBefore is
// valid for a brand-new file (no pre-mutation content).
func (r *Registry) ConsumeLinesRead(abs, hashBefore string) int {
	r.meta.mu.Lock()
	defer r.meta.mu.Unlock()
	credit := r.meta.credit
	r.meta.credit = agent.ReadCredit{}

	if credit.Path != "" && credit.Path != abs {
		return 0
	}
	if credit.Hash != "" && credit.Hash != hashBefore {
		return 0
	}
	return credit.LinesRead
}

// Compile-time proof that ConsumeLinesRead takes path and hash. The old
// variadic ConsumeLinesRead() / ConsumeLinesRead(abs) forms do not compile.
var _ func(*Registry, string, string) int = (*Registry).ConsumeLinesRead

// SetTruncated records that the last read_file call hit the byte cap, with
// the exact line to continue from.
func (r *Registry) SetTruncated(next int) {
	r.meta.mu.Lock()
	r.meta.truncated = true
	r.meta.nextOffset = next
	r.meta.mu.Unlock()
}

// ConsumeTruncated returns and clears the truncation flag plus the offset.
func (r *Registry) ConsumeTruncated() (bool, int) {
	r.meta.mu.Lock()
	defer r.meta.mu.Unlock()
	t := r.meta.truncated
	n := r.meta.nextOffset
	r.meta.truncated = false
	r.meta.nextOffset = 0
	return t, n
}

// ClearReadState drops any pending read metadata. Called when an invocation
// failed or was cancelled so its partial state cannot contaminate the next
// call.
func (r *Registry) ClearReadState() {
	r.meta.mu.Lock()
	r.meta.credit = agent.ReadCredit{}
	r.meta.truncated = false
	r.meta.nextOffset = 0
	r.meta.mu.Unlock()
}

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

type Classified interface {
	Class() perm.Class
}

func (r *Registry) Class(tool string) (perm.Class, bool) {
	t, ok := r.byName[tool]
	if !ok {
		return 0, false
	}
	if c, ok := t.(Classified); ok {
		return c.Class(), true
	}
	return 0, false
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
		return "", false, fmt.Errorf("unknown tool: %s", c.Name)
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

// Compile-time assertions: these tools must keep implementing Detailed, or
// the loop's rich path silently falls back to the plain one. That silence
// is exactly the bug that hid Truncated from read_file for its whole life —
// the loop asserted on []byte while Registry used json.RawMessage, so the
// assertion failed quietly and the rich path never ran in production.
var (
	_ Detailed = (*readFile)(nil)
	_ Detailed = (*bashTool)(nil)
)

func (r *Registry) RunDetailed(ctx context.Context, name string, raw json.RawMessage) (agent.Outcome, error) {
	t, ok := r.byName[name]
	if !ok {
		return agent.Outcome{}, fmt.Errorf("unknown tool: %s", name)
	}
	if d, ok := t.(Detailed); ok {
		return d.RunDetailed(ctx, raw)
	}
	txt, good, err := t.Run(ctx, raw)
	return agent.Outcome{Text: txt, OK: good}, err
}
