// Package tools: write.go holds the only two tools that change the disk.
// Every mutation is shadowed first, written atomically, then re-read and
// compared by hash. A write that cannot be proven did not happen.
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
)

const (
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
// safe to call before any side effect. The required string fields are the
// ones named in required.
func parseMutatingRequest(raw json.RawMessage, required ...string) (mutatingRequest, error) {
	var m mutatingRequest
	if err := decodeStrict(raw, &m); err != nil {
		return m, err
	}
	for _, name := range required {
		switch name {
		case "path":
			if m.Path == nil {
				return m, errors.New("path is required")
			}
		case "content":
			if m.Content == nil {
				return m, errors.New("content is required")
			}
		case "old":
			if m.Old == nil {
				return m, errors.New("old is required")
			}
		case "new":
			if m.New == nil {
				return m, errors.New("new is required")
			}
		}
	}
	return m, nil
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
	if raw != nil && len(raw) > 0 && raw[0] == '{' {
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

// Edit is what the shadow recorded around one mutation, kept in order so a
// later /undo has something to walk backwards through.
type Edit struct {
	Tool   string
	Rel    string
	Before snap.State
	After  snap.State
	Record *agent.EditRecord // persisted fingerprint, emitted as an event
}

type editLog struct {
	mu sync.Mutex
	l  []Edit
}

func (e *editLog) add(x Edit) {
	e.mu.Lock()
	e.l = append(e.l, x)
	e.mu.Unlock()
}

func (e *editLog) all() []Edit {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Edit(nil), e.l...)
}

// commit is the shared tail of both tools: shadow, write, verify, log.
// readLines is the number of lines the model actually read before writing
// (0 for a blind write), recorded in the persisted EditRecord.
func commit(root *Root, sh *snap.Shadow, log *editLog, tool, abs string, data []byte, readLines int) (snap.State, snap.State, error) {
	before, err := sh.Capture(abs)
	if err != nil {
		return before, snap.State{}, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return before, snap.State{}, err
	}
	mode := os.FileMode(0o644)
	if !before.Absent && before.Mode != 0 {
		mode = before.Mode
	}
	if err := snap.WriteAtomic(abs, data, mode); err != nil {
		return before, snap.State{}, err
	}
	// From here the disk has already changed. Every exit below must leave a
	// record behind, or /undo goes blind exactly when it is needed most.
	after, aerr := sh.Capture(abs)
	rec := buildRecord(sh, before, after, data, readLines)
	log.add(Edit{Tool: tool, Rel: root.Rel(abs), Before: before, After: after, Record: rec})
	if aerr != nil {
		return before, after, aerr
	}
	want, err := sh.CaptureBytes(abs, data, mode)
	if err != nil {
		return before, after, err
	}
	if !snap.Unchanged(want, after) {
		return before, after, errors.New("write did not verify on disk as-is")
	}
	return before, after, nil
}

// buildRecord fingerprints one mutation for the journal: SHA-256 of the
// content on both sides, a unified diff, and the number of lines read.
// HashBefore is empty only when the file did not exist before (creation).
func buildRecord(sh *snap.Shadow, before, after snap.State, data []byte, readLines int) *agent.EditRecord {
	rec := &agent.EditRecord{
		Path:       after.Rel,
		HashAfter:  sha256hex(data),
		ReadLines:  readLines,
		BlobAfter:  after.Blob,
		BlobBefore: before.Blob,
		ModeBefore: before.Mode,
	}
	if !before.Absent {
		if b, err := sh.Read(before.Blob); err == nil {
			rec.HashBefore = sha256hex(b)
			rec.Patch = unifiedDiff(b, data, rec.Path)
		}
	} else {
		rec.Patch = unifiedDiff(nil, data, rec.Path)
	}
	return rec
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// unifiedDiff renders before→after as a unified diff. It is line-based and
// minimal: unchanged lines are shared context. A nil before means creation.
func unifiedDiff(before, after []byte, path string) string {
	bl := splitLines(before)
	al := splitLines(after)

	// LCS table for the two line sequences.
	n, m := len(bl), len(al)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if bl[i] == al[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				if lcs[i+1][j] >= lcs[i][j+1] {
					lcs[i][j] = lcs[i+1][j]
				} else {
					lcs[i][j] = lcs[i][j+1]
				}
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	i, j := 0, 0
	var hunk []string
	hunkStart, hunkOld, hunkNew := 0, 0, 0
	flush := func() {
		if len(hunk) == 0 {
			return
		}
		// Count old/new lines in the hunk for the @@ header.
		old, new := 0, 0
		for _, l := range hunk {
			switch l[0] {
			case '-':
				old++
			case '+':
				new++
			default:
				old, new = old+1, new+1
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", hunkStart, old, hunkStart+hunkOld, new)
		for _, l := range hunk {
			b.WriteString(l)
			b.WriteByte('\n')
		}
		hunk = nil
	}
	for i < n && j < m {
		switch {
		case bl[i] == al[j]:
			if len(hunk) > 0 {
				if len(hunk) >= 3 { // flush a substantial hunk
					flush()
				} else {
					hunk = append(hunk, " "+bl[i])
				}
			}
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			if len(hunk) == 0 {
				hunkStart, hunkOld, hunkNew = i, 0, 0
			}
			hunk = append(hunk, "-"+bl[i])
			hunkOld++
			i++
		default:
			if len(hunk) == 0 {
				hunkStart, hunkOld, hunkNew = i, 0, 0
			}
			hunk = append(hunk, "+"+al[j])
			hunkNew++
			j++
		}
	}
	for ; i < n; i++ {
		if len(hunk) == 0 {
			hunkStart, hunkOld, hunkNew = i, 0, 0
		}
		hunk = append(hunk, "-"+bl[i])
		hunkOld++
	}
	for ; j < m; j++ {
		if len(hunk) == 0 {
			hunkStart, hunkOld, hunkNew = i, 0, 0
		}
		hunk = append(hunk, "+"+al[j])
		hunkNew++
	}
	flush()
	return b.String()
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

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
	m, err := parseMutatingRequest(raw, "path", "content")
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
	// Consume read-credit only after the request passed validation, and before
	// the mutation boundary that legitimately spends it.
	readLines := w.reg.ConsumeLinesRead()
	before, after, err := commit(w.root, w.sh, w.log, "write_file", abs, []byte(content), readLines)
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
	m, err := parseMutatingRequest(raw, "path", "old", "new")
	if err != nil {
		return "", false, err
	}
	old := deref(m.Old)
	new := deref(m.New)
	all := derefBool(m.All)
	if old == "" {
		return "", false, errors.New("old text is empty; use write_file for a new file")
	}
	abs, err := w.root.Resolve(deref(m.Path))
	if err != nil {
		return "", false, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", false, err
	}
	if st.Size() > maxEditBytes {
		return "", false, fmt.Errorf("file is %d bytes, limit is %d", st.Size(), maxEditBytes)
	}
	src, err := os.ReadFile(abs)
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
	// Consume read-credit only after the request passed validation, and before
	// the mutation boundary that legitimately spends it.
	readLines := w.reg.ConsumeLinesRead()
	if _, _, err := commit(w.root, w.sh, w.log, "edit_file", abs, []byte(out), readLines); err != nil {
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
