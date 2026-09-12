// Package tools: write.go holds the only two tools that change the disk.
// Every mutation is shadowed first, written atomically, then re-read and
// compared by hash. A write that cannot be proven did not happen.
//
// This file owns argument validation and the two tool implementations.
// The shared mutation tail (shadow, write, verify, journal record) lives in
// write_commit.go, and the unified-diff/LCS machinery lives in write_diff.go.
//
// Mutation request policy (NBD-010): both mutating tools validate their
// arguments through a shared, parse-then-validate boundary BEFORE any
// filesystem side effect (Resolve, Stat, ReadFile, MkdirAll, Capture,
// ConsumeLinesRead). The request representation distinguishes absent, null,
// explicit empty string, and non-empty string, so null/absent required fields
// are rejected while explicit empty values are accepted. Wrong types, unknown
// fields, duplicate keys, and invalid JSON are all rejected at this boundary.
// The read-credit is consumed only after validation passes and before the
// mutation, so a rejected request never spends it.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
)

// NBD-011: maxWriteBytes/maxEditBytes are the per-tool output ceilings. They
// are vars (not const) so tests can inject small limits and production limits
// can be tuned after G1 measurement (Task 7).
var (
	maxWriteBytes = 1 << 20
	maxEditBytes  = 2 << 20
)

// --- NBD-010 strict request validation -------------------------------------
// A mutating request must prove it is well-formed before it touches the disk.
// The representation below distinguishes absent (nil) from present (non-nil);
// a present string may be empty (""), which is a valid explicit value. Null
// and absent are both nil and are rejected for required fields. Wrong JSON
// types, unknown fields, and duplicate keys are rejected by the decoder.

// mutatingRequest is the shared shape both tools decode into. Every field is
// a pointer so absent and null both read as nil (rejected when required),
// while an explicit "" reads as a non-nil pointer to "" (accepted).
type mutatingRequest struct {
	Path    *string `json:"path"`
	Content *string `json:"content"` // write_file
	Old     *string `json:"old"`     // edit_file
	New     *string `json:"new"`     // edit_file
	All     *bool   `json:"all"`     // edit_file, optional
}

// parseMutatingRequest decodes raw into a validated mutatingRequest. It
// rejects invalid JSON, duplicate keys, unknown fields, wrong types, and
// absent/null required fields. It performs NO filesystem access, so it is
// safe to call before any side effect.
//
// tool selects the per-tool schema (allowed + required fields) from the
// toolSchemas map below. Because mutatingRequest is the shared struct (the
// union of both tools' schemas), DisallowUnknownFields alone cannot reject a
// field that is a member of the struct but belongs to the OTHER tool — e.g.
// write_file carrying "old"/"new"/"all", or edit_file carrying "content". The
// allowed set enforces the per-tool boundary: any non-nil field outside it is
// rejected as an unknown field. Required and allowed are derived from ONE
// schema entry so they cannot drift.
//
// Contract note on null: a cross-tool field present with a JSON null value
// (e.g. {"path":"x","content":"y","old":null}) decodes to nil and is NOT
// rejected — only non-nil cross-tool fields are rejected. This is a known
// gap: the stated contract is "non-nil cross-tool fields are rejected", not
// "cross-tool keys are rejected regardless of value". Practical impact is nil
// (a null carries no information). Tracked as [DEFERRED]: key-presence
// detection would require decoding into a map first.
func parseMutatingRequest(raw json.RawMessage, tool string) (mutatingRequest, error) {
	var m mutatingRequest
	if err := decodeStrict(raw, &m); err != nil {
		return m, err
	}
	schema, ok := toolSchemas[tool]
	if !ok {
		return m, fmt.Errorf("unknown tool: %s", tool)
	}
	if m.Content != nil {
		if !schema.allowed["content"] {
			return m, errors.New("unknown field: content")
		}
	}
	if m.Old != nil {
		if !schema.allowed["old"] {
			return m, errors.New("unknown field: old")
		}
	}
	if m.New != nil {
		if !schema.allowed["new"] {
			return m, errors.New("unknown field: new")
		}
	}
	if m.All != nil {
		if !schema.allowed["all"] {
			return m, errors.New("unknown field: all")
		}
	}
	if m.Path == nil && schema.required["path"] {
		return m, errors.New("path is required")
	}
	if m.Content == nil && schema.required["content"] {
		return m, errors.New("content is required")
	}
	if m.Old == nil && schema.required["old"] {
		return m, errors.New("old is required")
	}
	if m.New == nil && schema.required["new"] {
		return m, errors.New("new is required")
	}
	return m, nil
}

// toolSchemas is the single source of truth for each mutating tool's field
// policy. allowed lists the JSON keys the tool may carry; required lists the
// keys that must be present (non-null). Both are derived from one entry per
// tool so they cannot drift.
type toolSchema struct {
	allowed  map[string]bool
	required map[string]bool
}

var toolSchemas = map[string]toolSchema{
	"write_file": {
		allowed:  map[string]bool{"path": true, "content": true},
		required: map[string]bool{"path": true, "content": true},
	},
	"edit_file": {
		allowed:  map[string]bool{"path": true, "old": true, "new": true, "all": true},
		required: map[string]bool{"path": true, "old": true, "new": true},
	},
}

// decodeStrict decodes raw into dst while rejecting duplicate keys and unknown
// fields. It uses a *string/*bool target so absent and null both yield nil.
func decodeStrict(raw json.RawMessage, dst interface{}) error {
	// Duplicate-key detection: encoding/json silently takes the last value.
	// We decode into a map once and compare key counts; a mismatch means a
	// key appeared more than once.
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// rejectDuplicateKeys reports an error if raw (a JSON object) contains any key
// more than once. Non-object top-level values are ignored here (the typed
// decode will reject them).
func rejectDuplicateKeys(raw json.RawMessage) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		// Not an object or invalid JSON: let the typed decoder report it.
		return nil
	}
	// Count raw top-level keys by tokenizing; maps collapse duplicates.
	if len(raw) > 0 && raw[0] == '{' {
		n := 0
		dec := json.NewDecoder(bytes.NewReader(raw))
		tok, err := dec.Token()
		if err == nil {
			if d, ok := tok.(json.Delim); ok && d == '{' {
				for dec.More() {
					_, _ = dec.Token() // key
					var skip json.RawMessage
					_ = dec.Decode(&skip) // value
					n++
				}
			}
		}
		if n > len(probe) {
			return errors.New("duplicate key in request")
		}
	}
	return nil
}

// deref returns the string value of a present pointer, or "" if absent.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefBool returns the bool value of a present pointer, or false if absent.
func derefBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// --- end NBD-010 strict request validation ---------------------------------

type writeFile struct {
	root *Root
	sh   *snap.Shadow
	log  *editLog
	reg  *Registry
}

var _ Classified = writeFile{}

func (writeFile) Class() perm.Class { return perm.Mutating }

func (writeFile) Name() string { return "write_file" }

func (writeFile) Spec() provider.ToolSpec {
	return spec("write_file",
		"Write a whole file inside the project, creating missing directories. Old content is fully replaced; read the file first if it exists.",
		`{"type":"object","properties":{
			"path": {"type": "string", "description": "relative path inside the project"},
			"content": {"type": "string", "description": "the full new content"}
		}, "required":["path", "content"]}`)
}

func (w writeFile) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {
	m, err := parseMutatingRequest(raw, "write_file")
	if err != nil {
		return "", false, err
	}
	content := deref(m.Content)
	if len(content) > maxWriteBytes {
		return "", false, fmt.Errorf("content is %d bytes, limit is %d", len(content), maxWriteBytes)
	}
	abs, err := w.root.Resolve(deref(m.Path))
	if err != nil {
		return "", false, err
	}
	before, after, err := commit(ctx, w.root, w.sh, w.log, w.reg, "write_file", abs, []byte(content))
	if err != nil {
		return "", false, err
	}
	verb := "replaced"
	if before.Absent {
		verb = "created"
	}
	return fmt.Sprintf("%s %s (%d bytes, %d lines)", verb, w.root.Rel(abs), after.Size, linesIn(content)), true, nil
}

type editFile struct {
	root *Root
	sh   *snap.Shadow
	log  *editLog
	reg  *Registry
}

var _ Classified = editFile{}

func (editFile) Class() perm.Class { return perm.Mutating }

func (editFile) Name() string { return "edit_file" }

func (editFile) Spec() provider.ToolSpec {
	return spec("edit_file",
		"Replace one text with another inside an existing file. The old text must be unique in the file, or pass all=true to replace every occurrence.",
		`{"type":"object","properties":{
			"path": {"type": "string", "description": "relative path inside the project"},
			"old": {"type": "string", "description": "the exact old text to replace"},
			"new": {"type": "string", "description": "the replacement text"},
			"all": {"type": "boolean", "description": "replace all occurrences"}
		}, "required":["path", "old", "new"]}`)
}

func (w editFile) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {
	m, err := parseMutatingRequest(raw, "edit_file")
	if err != nil {
		return "", false, err
	}
	old := deref(m.Old)
	new := deref(m.New)
	all := derefBool(m.All)
	if old == "" {
		return "", false, errors.New("old text is empty; use write_file for a new file")
	}
	rel, abs, err := writePathFromRoot(w.root, deref(m.Path))
	if err != nil {
		return "", false, err
	}
	// T3: the size check and the read share one descriptor, so the file that
	// was measured is provably the file that was read.
	src, err := readSourceFromRoot(w.root, rel, abs, maxEditBytes)
	if err != nil {
		return "", false, err
	}
	if hasNUL(src) {
		return "", false, errors.New("binary file")
	}
	n := strings.Count(string(src), old)
	switch {
	case n == 0:
		return "", false, errors.New("old text not found in " + w.root.Rel(abs))
	case n > 1 && !all:
		return "", false, fmt.Errorf("old text occurs %d times; widen the snippet to make it unique or pass all=true", n)
	}
	reps := n
	var out string
	if all {
		out = strings.ReplaceAll(string(src), old, new)
	} else {
		out = strings.Replace(string(src), old, new, 1)
		reps = 1
	}
	// NBD-011: bound the mutating output. edit_file may grow a file past the
	// edit ceiling when new is larger than old (especially with all=true);
	// reject before the write so the limit is a hard ceiling on disk.
	if len(out) > maxEditBytes {
		return "", false, fmt.Errorf("edit output is %d bytes, limit is %d", len(out), maxEditBytes)
	}
	if _, _, err := commit(ctx, w.root, w.sh, w.log, w.reg, "edit_file", abs, []byte(out)); err != nil {
		return "", false, err
	}
	return fmt.Sprintf("edited %s (%d replacements, %d lines → %d)",
		w.root.Rel(abs), reps, linesIn(string(src)), linesIn(out)), true, nil
}

func linesIn(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

func hasNUL(b []byte) bool {
	if len(b) > 8000 {
		b = b[:8000]
	}
	return bytes.IndexByte(b, 0) >= 0
}
