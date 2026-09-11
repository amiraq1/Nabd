package store

import (
	"errors"
	"os"
	"testing"
)

func TestNewJSONLExclusiveRejectsExistingPath(t *testing.T) {
	path := t.TempDir() + "/session.jsonl"
	first, err := NewJSONLExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := NewJSONLExclusive(path)
	if second != nil {
		_ = second.Close()
		t.Fatal("exclusive open returned a journal for an existing path")
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("error = %v, want os.ErrExist", err)
	}
}
