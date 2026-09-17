package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/skill"
	"nabd/internal/snap"
)

// Tool is one capability. Args arrive as the model wrote them: unvalidated
// JSON. Every tool validates its own, and none of them trusts a path.
type Tool interface {
	Name() string
	Spec() provider.ToolSpec
	Run(ctx context.Context, args json.RawMessage) (out string, ok bool, err error)
}

// PathGate is the path-level half of the gate: the policy's answer for one
// root-relative path, as opposed to one tool name. The registry holds it so
// tools can enforce session .gitignore boundaries:
//   - read_file and grep consult CheckRead
//   - edit_file consults CheckEdit
//   - write_file and commit consult IsPathExcluded
type PathGate interface {
	CheckRead(rel string) (perm.Verdict, string)
	CheckEdit(rel string) (perm.Verdict, string)
	IsPathExcluded(rel string) bool
}

// SetPathGate installs the policy's path rule. Called once at startup by cmd/ag
// with the same *perm.Policy used as the gate, so the tool name and the path
// cannot be judged by two different policies.
func (r *Registry) SetPathGate(g PathGate) {
	r.pathGate = g
}

// pathRefused asks the path rule about rel for read_file/grep and reports
// whether the call must be refused. A nil gate means no rule was installed
// (tests, and any caller that builds a Registry without a policy), and then
// nothing is refused — the same behaviour this registry had before the rule
// existed.
func (r *Registry) pathRefused(rel string) (bool, string) {
	if r.pathGate == nil {
		return false, ""
	}
	v, why := r.pathGate.CheckRead(rel)
	return v == perm.Deny, why
}

// pathEditRefused asks the path rule about rel for edit_file and reports
// whether the edit must be refused. Unlike reading, editing an excluded file
// cannot be permitted in any mode because it requires reading and inspecting
// the content to find and replace text.
func (r *Registry) pathEditRefused(rel string) (bool, string) {
	if r.pathGate == nil {
		return false, ""
	}
	v, why := r.pathGate.CheckEdit(rel)
	return v == perm.Deny, why
}

// isPathExcluded reports whether rel matches the session .gitignore.
// Used by write_file and commit to suppress diffs and shadow storage.
func (r *Registry) isPathExcluded(rel string) bool {
	if r.pathGate == nil {
		return false
	}
	return r.pathGate.IsPathExcluded(rel)
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
	root       *Root
	sh         *snap.Shadow
	edits      *editLog
	list       []Tool
	byName     map[string]Tool
	meta       metadata
	diffBudget *diffBudget

	// OnRepair, when set, receives every fix the registry applies, before the
	// tool runs. cmd/ag wires it to the journal as a Notice; tests record it.
	// It must not call back into the registry.
	OnRepair func(Fix)

	// pathGate is the policy's path rule (see SetPathGate). Set once at startup,
	// before any tool runs, and never swapped afterwards.
	pathGate PathGate

	// repairOff disables pre-dispatch repair. Production leaves it false; the
	// NBD-420 measurement harness sets it to measure each rule's effect against
	// the same call with repair enabled (see repair_rounds_test.go).
	repairOff bool

	// skills holds the skill index the skill tool resolves names against. It is
	// set once before the first turn and read-only afterwards; the mutex exists
	// because a tool call and the setter can meet on the session-start path.
	skillsMu sync.Mutex
	skillsFn func() []skill.Skill
}

// SetSkillIndex installs the loaded skills for the skill tool. It is called at
// session start, before any turn, so a call can never observe a half-built
// index.
func (r *Registry) SetSkillIndex(fn func() []skill.Skill) {
	r.skillsMu.Lock()
	r.skillsFn = fn
	r.skillsMu.Unlock()
}

// skillList returns the current skill index, or nil when none was installed.
func (r *Registry) skillList() []skill.Skill {
	r.skillsMu.Lock()
	fn := r.skillsFn
	r.skillsMu.Unlock()
	if fn == nil {
		return nil
	}
	return fn()
}

func NewRegistry(root *Root, sh *snap.Shadow) *Registry {
	log := &editLog{}
	r := &Registry{root: root, sh: sh, edits: log, byName: map[string]Tool{}, diffBudget: newDiffBudget(maxDiffCells)}
	r.add(readFile{root, r}, globFiles{root}, grepFiles{root, r})
	r.add(writeFile{root, sh, log, r}, editFile{root, sh, log, r})
	r.add(bashTool{root})
	r.add(skillTool{r})
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
	fixed, _ := r.repairCall(c)
	name, args := fixed.Name, fixed.Input

	t, found := r.byName[name]
	if !found {
		return "", false, fmt.Errorf("unknown tool: %s", name)
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	return t.Run(ctx, args)
}

// repairCall is the single point both entry points pass through, so a malformed
// call is corrected on the plain and the rich path alike. The loop also calls
// RepairCall before it classifies, because the gate must see what will run
// rather than what the model wrote; repairing an already-corrected call is a
// no-op, since a repaired call presents no further fixes.
func (r *Registry) repairCall(c provider.ToolCall) (provider.ToolCall, []Fix) {
	if r.repairOff {
		return c, nil
	}
	name, args, fixes := repair(c.Name, c.Input, r.Specs(), r.mayDropUnknownKeys)
	for _, f := range fixes {
		if r.OnRepair != nil {
			r.OnRepair(f)
		}
	}
	c.Name, c.Input = name, args
	return c, fixes
}

// RepairCall corrects a malformed call, announcing every fix through OnRepair.
// It is the method the agent loop uses before classification, so the permission
// prompt names the call that will actually be executed.
func (r *Registry) RepairCall(c provider.ToolCall) provider.ToolCall {
	fixed, _ := r.repairCall(c)
	return fixed
}

// RepairCallWithFixes is RepairCall for callers that need the corrections
// themselves (tests, and anything that wants to report them).
func (r *Registry) RepairCallWithFixes(c provider.ToolCall) (provider.ToolCall, []Fix) {
	return r.repairCall(c)
}

// mayDropUnknownKeys reports whether an undeclared argument key may be removed
// for this tool. A key may be removed only where it is inert: where it cannot
// change the action the user approves. For bash the whole action is the `cmd`
// string the prompt displays, and for the ReadOnly tools an undeclared key cannot
// change what is read — neither can turn a stray key into a different action.
// write_file and edit_file are the other case: they share `old`, `new`, `all` and
// `content` as live fields, so an undeclared key there is how a model says it
// meant the other tool, and dropping it would replace a rejection with a
// different write (NBD-010). That pair is exactly the Mutating class, which is
// why the class answers the question today; a tool whose undeclared keys could be
// live outside that class needs this revisited, not widened by class.
func (r *Registry) mayDropUnknownKeys(tool string) bool {
	class, ok := r.Class(tool)
	return ok && class != perm.Mutating
}

// RepairCallWithDrops is RepairCall for the agent loop: it also returns the
// argument keys the layer removed as undeclared, so the loop can state the loss
// on the ToolStart event as data rather than only as a notice.
func (r *Registry) RepairCallWithDrops(c provider.ToolCall) (provider.ToolCall, []string) {
	fixed, fixes := r.repairCall(c)
	return fixed, DroppedKeys(fixes)
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
	case ".git", ".ag", "node_modules", "vendor", ".venv", "__pycache__",
		"target", "dist", "build", ".next", ".cache", ".idea":
		// .ag holds the shadow store: every historical version of every file.
		// A traversal tool that walks it would read deleted content back out.
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
	fixed, _ := r.repairCall(provider.ToolCall{Name: name, Input: raw})
	name, raw = fixed.Name, fixed.Input

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
