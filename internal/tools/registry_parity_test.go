package tools

import (
	"testing"

	"nabd/internal/snap"
)

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
