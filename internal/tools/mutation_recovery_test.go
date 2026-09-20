//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

// A failed durable intent must stop the mutation before writeFromRoot can
// publish any bytes. The caller can safely retry without an ambiguous file
// state or an orphaned committed edit.
func TestMutationIntentFailureDoesNotPublish(t *testing.T) {
	r, dir := newReg(t)
	sentinel := errors.New("journal unavailable")
	var prepared *agent.EditRecord
	r.OnMutationPrepared = func(rec *agent.EditRecord) error {
		prepared = rec
		return sentinel
	}

	raw, err := json.Marshal(map[string]string{
		"path":    "out.txt",
		"content": "new\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	out, ok, runErr := r.Run(context.Background(), providerToolCall("write_file", raw))
	if ok {
		t.Fatalf("write unexpectedly succeeded: out=%q", out)
	}
	if !errors.Is(runErr, sentinel) {
		t.Fatalf("err=%v, want journal error", runErr)
	}
	if prepared == nil || prepared.MutationID == "" {
		t.Fatal("mutation intent was not prepared before the write")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "out.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("target exists after intent failure: err=%v", statErr)
	}
	if edits := r.Edits(); len(edits) != 0 {
		t.Fatalf("edits=%d, want no committed edit", len(edits))
	}
}