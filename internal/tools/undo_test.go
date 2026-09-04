package tools

import (
	"encoding/json"
	"testing"

	"nabd/internal/provider"
	"nabd/internal/snap"
)

func newReg(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	return NewRegistry(root, sh), root.Dir()
}


// providerToolCall converts our internal args into a provider.ToolCall
func providerToolCall(name string, input json.RawMessage) provider.ToolCall {
	return provider.ToolCall{
		ID:    "test-id",
		Name:  name,
		Input: input,
	}
}
