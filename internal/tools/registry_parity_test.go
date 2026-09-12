package tools

import (
	"sort"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/snap"
)

// wantRegisteredTools is the exact tool set this registry must serve, in
// sorted order. It is written out rather than derived so that adding,
// removing or renaming a tool is a deliberate edit here.
//
// internal/agent/loop.go's Tools comment names this same set; the comment and
// this list are the two halves of one claim, and both drift silently if a tool
// moves. (A previous version of that comment named a "list_dir" that was never
// registered: directory listing is served by glob.)
var wantRegisteredTools = []string{
	"bash", "edit_file", "glob", "grep", "read_file", "write_file",
}

// TestRegistryToolSet pins the registered tool names. A tool that exists
// without being listed, or is listed without existing, breaks the model's
// view of what it can call and the unknown-tool error's "available:" clause.
func TestRegistryToolSet(t *testing.T) {
	r := NewRegistry(nil, nil)

	got := make([]string, 0, len(r.byName))
	for name := range r.byName {
		got = append(got, name)
	}
	sort.Strings(got)

	if strings.Join(got, ",") != strings.Join(wantRegisteredTools, ",") {
		t.Fatalf("registered tools = %v, want %v", got, wantRegisteredTools)
	}
}

// TestRegistrySpecsParity pins the invariant the unknown-tool message
// depends on: the set of names registered via add() (byName) must equal the
// set Specs() hands the loop, both directions. If a tool is registered but
// absent from its own Spec(), the "available:" clause in the unknown-tool
// message would omit it — silently hiding a tool the model is allowed to
// call. This is an internal test so it can read the unexported byName/list.
func TestRegistrySpecsParity(t *testing.T) {
	dir := t.TempDir()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(root, sh)

	byName := make(map[string]bool, len(r.byName))
	for name := range r.byName {
		byName[name] = true
	}
	specs := r.Specs()

	if len(byName) != len(specs) {
		t.Fatalf("parity broken: byName=%d Specs()=%d", len(byName), len(specs))
	}
	for _, s := range specs {
		if !byName[s.Name] {
			t.Errorf("Specs() returns %q, not in registry byName", s.Name)
		}
	}
	// Every concrete tool must declare a name that the registry actually knows.
	for _, tt := range r.list {
		if s := tt.Spec(); !byName[s.Name] {
			t.Errorf("tool list declares %q not present in byName", s.Name)
		}
	}
}

// TestFenceToolNameAllowlistMatchesRegistry pins the fence's tool-name
// allowlist to the registry. The fence lives in internal/agent, which cannot
// import this package (tools imports agent), so the allowlist is declared
// there and verified here, in both directions: every registered tool must be
// fenceable by its real name, and the fence must not accept a name the
// registry does not register. Without this, adding a tool would silently
// fence it as "unknown" at the provider boundary, and a stale allowlist entry
// would advertise a tool the model cannot call.
func TestFenceToolNameAllowlistMatchesRegistry(t *testing.T) {
	r := NewRegistry(nil, nil)

	registered := make(map[string]bool, len(r.byName))
	for name := range r.byName {
		registered[name] = true
	}
	if len(registered) == 0 {
		t.Fatal("registry registered no tools; the comparison would be vacuous")
	}

	fenced := make(map[string]bool)
	for _, name := range agent.FenceToolNames() {
		fenced[name] = true
	}

	for name := range registered {
		if !fenced[name] {
			t.Errorf("tool %q is registered but missing from the fence allowlist; the model would read it as \"unknown\"", name)
		}
	}
	for name := range fenced {
		if !registered[name] {
			t.Errorf("fence allowlist accepts %q, which the registry does not register", name)
		}
	}
}
