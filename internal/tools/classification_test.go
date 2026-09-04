package tools

import (
	"testing"
)

func TestAllToolsClassified(t *testing.T) {
	r := NewRegistry(nil, nil)
	for _, spec := range r.Specs() {
		_, ok := r.Class(spec.Name)
		if !ok {
			t.Errorf("Tool %q is not classified", spec.Name)
		}
	}
}

func TestUnknownToolClass(t *testing.T) {
	r := NewRegistry(nil, nil)
	if _, ok := r.Class("nonexistent"); ok {
		t.Errorf("Expected nonexistent tool to be unclassified")
	}
}
