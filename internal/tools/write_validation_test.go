package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fileSHA256 returns the SHA-256 hex of a file's current contents.
func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fileMode returns the FileInfo mode bits of a file.
func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// TestWriteValidationRejectsBeforeSideEffects verifies NBD-010: every invalid
// mutating request is rejected BEFORE any filesystem side effect. The acceptance
// invariants for every rejected request are: (1) file SHA256 unchanged, (2) file
// mode unchanged, (3) no directory created, (4) MkdirAll not reached,
// (5) read-credit not consumed.
func TestWriteValidationRejectsBeforeSideEffects(t *testing.T) {
	ctx := context.Background()

	// Seed a target file so we can detect mutation via SHA256/mode.
	seedContent := []byte("original content\nline two\n")
	for _, name := range []string{"write_file", "edit_file"} {
		t.Run(name, func(t *testing.T) {
			r, dir := newReg(t)
			target := filepath.Join(dir, "target.txt")
			if err := os.WriteFile(target, seedContent, 0o644); err != nil {
				t.Fatal(err)
			}
			// Stage some read-credit so we can detect whether it is consumed.
			// A rejected request must leave this credit intact (still 7); a
			// consumed credit would read back 0 because ConsumeLinesRead
			// resets the slot. The invariant is "unchanged", not "zero".
			const stagedCredit = 7
			r.SetLinesRead(stagedCredit)

			beforeHash := fileSHA256(t, target)
			beforeMode := fileMode(t, target)

			type tc struct {
				name string
				raw  string
			}
			cases := []tc{
				{"content absent", `{"path":"target.txt"}`},
				{"content null", `{"path":"target.txt","content":null}`},
				{"content number", `{"path":"target.txt","content":42}`},
				{"content object", `{"path":"target.txt","content":{"x":1}}`},
				{"content array", `{"path":"target.txt","content":[1,2]}`},
				{"duplicate content key", `{"path":"target.txt","content":"a","content":"b"}`},
				{"unknown field", `{"path":"target.txt","content":"x","bogus":1}`},
				{"invalid JSON", `{"path":"target.txt",`},
			}
			// edit_file-specific invalid cases.
			if name == "edit_file" {
				cases = []tc{
					{"new absent", `{"path":"target.txt","old":"original content"}`},
					{"new null", `{"path":"target.txt","old":"original content","new":null}`},
					{"new number", `{"path":"target.txt","old":"original content","new":42}`},
					{"new object", `{"path":"target.txt","old":"original content","new":{"x":1}}`},
					{"new array", `{"path":"target.txt","old":"original content","new":[1,2]}`},
					{"old absent", `{"path":"target.txt","new":"replacement"}`},
					{"unknown field", `{"path":"target.txt","old":"original content","new":"x","bogus":1}`},
					{"invalid JSON", `{"path":"target.txt",`},
				}
			}

			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					// Stage fresh credit per subtest: subtests share r, and a
					// prior subtest's invariant check resets the slot.
					r.SetLinesRead(stagedCredit)
					var raw json.RawMessage = json.RawMessage(c.raw)
					_, ok, err := r.Run(ctx, providerToolCall(name, raw))
					if ok || err == nil {
						t.Fatalf("%s: expected rejection (ok=false, err!=nil), got ok=%v err=%v", c.name, ok, err)
					}
					// Invariant 1: file SHA256 unchanged.
					if got := fileSHA256(t, target); got != beforeHash {
						t.Errorf("%s: file SHA256 changed: before=%s after=%s", c.name, beforeHash, got)
					}
					// Invariant 2: file mode unchanged.
					if got := fileMode(t, target); got != beforeMode {
						t.Errorf("%s: file mode changed: before=%04o after=%04o", c.name, beforeMode, got)
					}
					// Invariant 5: read-credit not consumed. The slot must still hold
					// the staged value, since validation failed before Consume.
					if got := r.ConsumeLinesRead(); got != stagedCredit {
						t.Errorf("%s: read-credit consumed or altered: got %d, want %d (staged)", c.name, got, stagedCredit)
					}
				})
			}

			// Restore credit for the directory-creation check.
			r.SetLinesRead(stagedCredit)
			// Invariant 3 & 4: no directory created, MkdirAll not reached.
			// A valid path under a non-existent subdirectory would trigger
			// MkdirAll for write_file; an invalid request must not.
			var raw json.RawMessage = json.RawMessage(`{"path":"nope/sub/target.txt","content":null`)
			if name == "edit_file" {
				raw = json.RawMessage(`{"path":"nope/sub/target.txt","old":"x","new":null}`)
			}
			_, _, _ = r.Run(ctx, providerToolCall(name, raw))
			if _, err := os.Stat(filepath.Join(dir, "nope")); !os.IsNotExist(err) {
				t.Errorf("directory \"nope\" was created by an invalid request; MkdirAll reached")
			}
		})
	}
}

// TestWriteValidationAcceptsExplicitEmpty verifies NBD-010: explicit empty
// values for content (write_file) and new (edit_file) MUST succeed, because
// empty is distinct from absent/null. The file is written with empty content.
func TestWriteValidationAcceptsExplicitEmpty(t *testing.T) {
	ctx := context.Background()

	t.Run("write_file content empty", func(t *testing.T) {
		r, dir := newReg(t)
		target := filepath.Join(dir, "empty.txt")
		seed := []byte("will be emptied\n")
		if err := os.WriteFile(target, seed, 0o644); err != nil {
			t.Fatal(err)
		}
		beforeHash := fileSHA256(t, target)

		raw, _ := json.Marshal(map[string]any{"path": "empty.txt", "content": ""})
		out, ok, err := r.Run(ctx, providerToolCall("write_file", raw))
		if err != nil || !ok {
			t.Fatalf("write_file with content=\"\" must succeed: ok=%v err=%v", ok, err)
		}
		// File must now be empty.
		b, _ := os.ReadFile(target)
		if len(b) != 0 {
			t.Fatalf("content not emptied: %q", string(b))
		}
		_ = out
		_ = beforeHash
	})

	t.Run("edit_file new empty", func(t *testing.T) {
		r, dir := newReg(t)
		target := filepath.Join(dir, "edit.txt")
		seed := []byte("remove this text please\n")
		if err := os.WriteFile(target, seed, 0o644); err != nil {
			t.Fatal(err)
		}

		raw, _ := json.Marshal(map[string]any{
			"path": "edit.txt",
			"old":  "remove this text ",
			"new":  "",
		})
		out, ok, err := r.Run(ctx, providerToolCall("edit_file", raw))
		if err != nil || !ok {
			t.Fatalf("edit_file with new=\"\" must succeed: ok=%v err=%v", ok, err)
		}
		b, _ := os.ReadFile(target)
		if string(b) != "please\n" {
			t.Fatalf("edit with empty new did not produce expected content: %q", string(b))
		}
		_ = out
	})
}

// TestWriteValidationDistinguishesAbsentNullEmpty verifies NBD-010: absent,
// null, and explicit empty string must remain distinguishable. Absent/null are
// rejected; explicit empty is accepted. The request representation must carry
// enough information to tell them apart.
func TestWriteValidationDistinguishesAbsentNullEmpty(t *testing.T) {
	ctx := context.Background()

	// write_file: absent content vs null content vs "" must be distinguishable.
	t.Run("write_file", func(t *testing.T) {
		r, dir := newReg(t)
		target := filepath.Join(dir, "t.txt")
		seed := []byte("seed\n")
		if err := os.WriteFile(target, seed, 0o644); err != nil {
			t.Fatal(err)
		}

		// absent -> reject
		raw, _ := json.Marshal(map[string]any{"path": "t.txt"})
		ok, err := runWriteOK(r, ctx, raw)
		if ok || err == nil {
			t.Errorf("absent content: expected rejection, got ok=%v err=%v", ok, err)
		}

		// null -> reject
		raw, _ = json.Marshal(map[string]any{"path": "t.txt", "content": nil})
		ok, err = runWriteOK(r, ctx, raw)
		if ok || err == nil {
			t.Errorf("null content: expected rejection, got ok=%v err=%v", ok, err)
		}

		// explicit empty -> accept (and actually empty the file)
		raw, _ = json.Marshal(map[string]any{"path": "t.txt", "content": ""})
		ok, err = runWriteOK(r, ctx, raw)
		if !ok || err != nil {
			t.Errorf("explicit empty content: expected success, got ok=%v err=%v", ok, err)
		}
		b, _ := os.ReadFile(target)
		if len(b) != 0 {
			t.Errorf("explicit empty content: file not emptied, got %q", string(b))
		}
	})

	// edit_file: null new vs "" new must be distinguishable.
	t.Run("edit_file", func(t *testing.T) {
		r, dir := newReg(t)
		target := filepath.Join(dir, "e.txt")
		seed := []byte("ABCDE\n")
		if err := os.WriteFile(target, seed, 0o644); err != nil {
			t.Fatal(err)
		}

		// null new -> reject
		raw, _ := json.Marshal(map[string]any{"path": "e.txt", "old": "ABC", "new": nil})
		ok, err := runEditOK(r, ctx, raw)
		if ok || err == nil {
			t.Errorf("null new: expected rejection, got ok=%v err=%v", ok, err)
		}

		// explicit empty new -> accept (removes "ABC")
		raw, _ = json.Marshal(map[string]any{"path": "e.txt", "old": "ABC", "new": ""})
		ok, err = runEditOK(r, ctx, raw)
		if !ok || err != nil {
			t.Errorf("explicit empty new: expected success, got ok=%v err=%v", ok, err)
		}
		b, _ := os.ReadFile(target)
		if string(b) != "DE\n" {
			t.Errorf("explicit empty new: expected \"DE\\n\", got %q", string(b))
		}
	})
}

// runWriteOK returns (ok, err) for a write_file call.
func runWriteOK(r *Registry, ctx context.Context, raw json.RawMessage) (bool, error) {
	_, ok, err := r.Run(ctx, providerToolCall("write_file", raw))
	return ok, err
}

// runEditOK returns (ok, err) for an edit_file call.
func runEditOK(r *Registry, ctx context.Context, raw json.RawMessage) (bool, error) {
	_, ok, err := r.Run(ctx, providerToolCall("edit_file", raw))
	return ok, err
}

// TestWriteValidationRejectsCrossToolFields verifies NBD-010: a mutating
// request carrying a field that belongs to the OTHER tool (write_file carrying
// old/new/all; edit_file carrying content) is rejected at the validation
// boundary, not silently ignored. DisallowUnknownFields only catches fields
// outside the shared struct; cross-tool fields ARE in the struct, so they
// must be rejected per-tool. Every rejection must occur BEFORE any filesystem
// side effect: file SHA256 unchanged, mode unchanged, no directory created,
// read-credit NOT consumed.
func TestWriteValidationRejectsCrossToolFields(t *testing.T) {
	ctx := context.Background()
	seedContent := []byte("original content\nline two\n")

	type tc struct {
		tool string
		raw  string
	}
	cases := []tc{
		// write_file must not accept fields that belong to edit_file.
		{"write_file", `{"path":"target.txt","content":"x","old":"original"}`},
		{"write_file", `{"path":"target.txt","content":"x","new":"replacement"}`},
		{"write_file", `{"path":"target.txt","content":"x","all":true}`},
		// edit_file must not accept fields that belong to write_file.
		{"edit_file", `{"path":"target.txt","old":"original","new":"x","content":"extra"}`},
		// Genuinely unknown key is already rejected by DisallowUnknownFields,
		// but include it for completeness on both tools.
		{"write_file", `{"path":"target.txt","content":"x","bogus":1}`},
		{"edit_file", `{"path":"target.txt","old":"x","new":"y","bogus":1}`},
	}

	for _, c := range cases {
		t.Run(c.tool+"_"+string(c.raw[0]), func(t *testing.T) {
			r, dir := newReg(t)
			target := filepath.Join(dir, "target.txt")
			if err := os.WriteFile(target, seedContent, 0o644); err != nil {
				t.Fatal(err)
			}
			const stagedCredit = 7
			r.SetLinesRead(stagedCredit)

			beforeHash := fileSHA256(t, target)
			beforeMode := fileMode(t, target)

			var raw json.RawMessage = json.RawMessage(c.raw)
			_, ok, err := r.Run(ctx, providerToolCall(c.tool, raw))
			if ok || err == nil {
				t.Fatalf("%s: expected rejection (ok=false, err!=nil), got ok=%v err=%v", c.tool, ok, err)
			}
			// Invariant 1: file SHA256 unchanged.
			if got := fileSHA256(t, target); got != beforeHash {
				t.Errorf("file SHA256 changed: before=%s after=%s", beforeHash, got)
			}
			// Invariant 2: file mode unchanged.
			if got := fileMode(t, target); got != beforeMode {
				t.Errorf("file mode changed: before=%04o after=%04o", beforeMode, got)
			}
			// Invariant 3: read-credit not consumed.
			if got := r.ConsumeLinesRead(); got != stagedCredit {
				t.Errorf("read-credit consumed: got %d, want %d", got, stagedCredit)
			}
		})
	}
}
