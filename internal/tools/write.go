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

// NBD-011: maxWriteBytes/maxEditBytes are the per-tool output ceilings. They
// are vars (not const) so tests can inject small limits and production limits
// can be tuned after G1 measurement (Task 7).
var (
	maxWriteBytes = 1 << 20
	maxEditBytes  = 2 << 20
)

// NBD-011: configurable bounds for diff work and output. They are vars (not
// const) so tests can inject small budgets without constructing huge fixtures,
// and so production limits can be tuned after G1 measurement (Task 7). The
// values below are the safety-reviewed starting ceilings.
var (
	// maxDiffLines caps each side of the diff; inputs larger than this are not
	// fed to the LCS matrix. 3000 lines is far above typical single edits.
	maxDiffLines = 3000
	// maxDiffCells caps the matrix work (n*m). If n*m would exceed it, the diff
	// aborts rather than allocate quadratic state. 4M cells * 8 B/int (arm64)
	// = 32 MB, calibrated AT the G1 measurement: a 2000x2000 diff allocated
	// 33.6 MB and ran in ~24 ms. The ceiling is set at the measured worst case
	// (0x margin), not below it: 2000*2000 == 4_000_000 exactly. The (n*m)
	// guard `m > maxDiffCells/n` therefore rejects 2001x2000 and larger
	// BEFORE allocating. Larger edits are rejected before alloc; this ceiling
	// is the deterministic boundary, not a soft target.
	maxDiffCells = 4_000_000
	// maxPatchBytes caps the raw unified-diff output string. A 2000-line
	// complete rewrite is well under 100 KB; 1 MB gives >10x headroom.
	maxPatchBytes = 1 << 20
	// maxEventBytes caps the serialized JSON of an edit event. If the event
	// would exceed it, the Patch is dropped so the record (hashes, blobs)
	// survives within budget. 4 MB embeds the 1 MB patch with JSON overhead.
	maxEventBytes = 1 << 22
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
//
// allowed is the per-tool field set: only those JSON keys may be present.
// Because mutatingRequest is the shared struct (the union of both tools'
// schemas), DisallowUnknownFields alone cannot reject a field that is a
// member of the struct but belongs to the OTHER tool — e.g. write_file
// carrying "old"/"new"/"all", or edit_file carrying "content". The allowed
// set enforces the per-tool boundary: any non-nil field outside it is
// rejected as an unknown field.
func parseMutatingRequest(raw json.RawMessage, allowed []string, required ...string) (mutatingRequest, error) {
	var m mutatingRequest
	if err := decodeStrict(raw, &m); err != nil {
		return m, err
	}
	perm := make(map[string]struct{}, len(allowed))
	for _, f := range allowed {
		perm[f] = struct{}{}
	}
	if m.Content != nil {
		if _, ok := perm["content"]; !ok {
			return m, errors.New("unknown field: content")
		}
	}
	if m.Old != nil {
		if _, ok := perm["old"]; !ok {
			return m, errors.New("unknown field: old")
		}
	}
	if m.New != nil {
		if _, ok := perm["new"]; !ok {
			return m, errors.New("unknown field: new")
		}
	}
	if m.All != nil {
		if _, ok := perm["all"]; !ok {
			return m, errors.New("unknown field: all")
		}
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
//
// NBD-011: ctx is threaded through to the diff work so cancellation can stop
// it. The record is always persisted (even if the diff fails) so /undo keeps
// its hashes and blobs; a failed diff simply leaves Patch empty.
func commit(ctx context.Context, root *Root, sh *snap.Shadow, log *editLog, tool, abs string, data []byte, readLines int) (snap.State, snap.State, error) {
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
	rec := buildRecord(ctx, sh, before, after, data, readLines)
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
//
// NBD-011: the diff can fail (budget exceeded, cancellation). The record is
// always returned with its hashes and blobs intact; on diff failure Patch is
// empty. The record is then passed through boundEditEvent so the serialized
// event stays within the configured budget (dropping only Patch if needed).
func buildRecord(ctx context.Context, sh *snap.Shadow, before, after snap.State, data []byte, readLines int) *agent.EditRecord {
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
			// A diff failure must not lose the record: keep hashes/blobs and
			// simply leave Patch empty.
			if patch, err := unifiedDiff(ctx, b, data, rec.Path); err == nil {
				rec.Patch = patch
			}
		}
	} else {
		if patch, err := unifiedDiff(ctx, nil, data, rec.Path); err == nil {
			rec.Patch = patch
		}
	}
	return boundEditEvent(rec)
}

// boundEditEvent enforces the event-size budget (NBD-011) on the serialized
// edit event. The Patch is the only unbounded field: if marshaling the event
// would exceed maxEventBytes, the Patch is dropped so the audit fields
// (hashes, blobs) survive within budget. Sizing uses the actual encoded JSON
// bytes, which accounts for escaping.
func boundEditEvent(rec *agent.EditRecord) *agent.EditRecord {
	if maxEventBytes <= 0 {
		return rec
	}
	b, err := json.Marshal(agent.Event{Type: agent.EventEdit, Edit: rec})
	if err != nil {
		return rec
	}
	if len(b) <= maxEventBytes {
		return rec
	}
	rec.Patch = ""
	return rec
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// unifiedDiff renders before→after as a unified diff. It is line-based and
// minimal: unchanged lines are shared context. A nil before means creation.
//
// NBD-011: unifiedDiff now respects a work budget (maxDiffLines, maxDiffCells)
// and ctx cancellation. If the inputs are too large or the context is cancelled,
// it returns an error and no patch. The caller (buildRecord) must persist the
// record without the patch so hashes and blobs survive. Hunk headers track the
// new-file line position independently so the patch is syntactically valid.
func unifiedDiff(ctx context.Context, before, after []byte, path string) (string, error) {
	// Check cancellation before doing any work.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	bl := splitLines(before)
	al := splitLines(after)

	n, m := len(bl), len(al)
	// Bound the input sides.
	if n > maxDiffLines || m > maxDiffLines {
		return "", fmt.Errorf("diff input too large: %d×%d lines exceeds %d", n, m, maxDiffLines)
	}
	// Bound the matrix work and detect integer overflow in n*m. If the product
	// would overflow int or exceed the cell budget, abort before allocating.
	if n != 0 && m > maxDiffCells/n {
		return "", fmt.Errorf("diff work budget exceeded: %d×%d exceeds %d cells", n, m, maxDiffCells)
	}
	// LCS table for the two line sequences. Cancellation is checked once per
	// outer iteration so a cancelled context terminates the build promptly.
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return "", err
		}
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

	// Backtrack the LCS into an edit script: ('=',line) unchanged,
	// ('-',line) deleted, ('+',line) inserted. Cancellation is checked during
	// the backtrack so a cancelled context terminates it.
	type op struct {
		kind byte
		line string
	}
	ops := make([]op, 0, n+m)
	i, j := n, m
	for i > 0 || j > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i > 0 && j > 0 && bl[i-1] == al[j-1] {
			ops = append(ops, op{'=', bl[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || lcs[i-1][j] >= lcs[i][j-1]) {
			ops = append(ops, op{'+', al[j-1]})
			j--
		} else {
			ops = append(ops, op{'-', bl[i-1]})
			i--
		}
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}

	const ctxSize = 3
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	oldLine, newLine := 1, 1
	type hunk struct {
		oldStart, newStart int
		lines              []string
	}
	var hunks []hunk
	cur := -1
	startHunk := func() {
		if cur < 0 {
			hunks = append(hunks, hunk{})
			cur = len(hunks) - 1
		}
	}
	var pending []string
	flushPendingTo := func(idx int) {
		if len(pending) == 0 {
			return
		}
		hunks[idx].lines = append(hunks[idx].lines, pending...)
		pending = nil
	}
	for _, o := range ops {
		switch o.kind {
		case '=':
			if cur < 0 {
				if len(pending) < ctxSize {
					pending = append(pending, " "+o.line)
				}
				oldLine++
				newLine++
				continue
			}
			pending = append(pending, " "+o.line)
			oldLine++
			newLine++
			if len(pending) > ctxSize {
				trailing := pending
				if len(trailing) > ctxSize {
					trailing = trailing[len(trailing)-ctxSize:]
				}
				hunks[cur].lines = append(hunks[cur].lines, trailing...)
				cur = -1
				pending = nil
			}
		case '-':
			if cur < 0 && len(pending) > 0 {
				startHunk()
				hunks[cur].oldStart = oldLine - len(pending)
				hunks[cur].newStart = newLine - len(pending)
				flushPendingTo(cur)
			}
			startHunk()
			if len(hunks[cur].lines) == 0 && hunks[cur].oldStart == 0 {
				hunks[cur].oldStart = oldLine
				hunks[cur].newStart = newLine
			}
			hunks[cur].lines = append(hunks[cur].lines, "-"+o.line)
			oldLine++
		case '+':
			if cur < 0 && len(pending) > 0 {
				startHunk()
				hunks[cur].oldStart = oldLine - len(pending)
				hunks[cur].newStart = newLine - len(pending)
				flushPendingTo(cur)
			}
			startHunk()
			if len(hunks[cur].lines) == 0 && hunks[cur].oldStart == 0 {
				hunks[cur].oldStart = oldLine
				hunks[cur].newStart = newLine
			}
			hunks[cur].lines = append(hunks[cur].lines, "+"+o.line)
			newLine++
		}
	}
	if cur >= 0 {
		if len(pending) > ctxSize {
			pending = pending[len(pending)-ctxSize:]
		}
		if len(pending) > 0 {
			hunks[cur].lines = append(hunks[cur].lines, pending...)
		}
	}

	for _, h := range hunks {
		oldCnt, newCnt := 0, 0
		for _, l := range h.lines {
			switch l[0] {
			case '-':
				oldCnt++
			case '+':
				newCnt++
			default:
				oldCnt++
				newCnt++
			}
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.oldStart, oldCnt, h.newStart, newCnt)
		for _, l := range h.lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	out := b.String()
	if len(out) > maxPatchBytes {
		return "", fmt.Errorf("patch output exceeds %d bytes", maxPatchBytes)
	}
	return out, nil
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
	m, err := parseMutatingRequest(raw, []string{"path", "content"}, "path", "content")
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
	before, after, err := commit(ctx, w.root, w.sh, w.log, "write_file", abs, []byte(content), readLines)
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
	m, err := parseMutatingRequest(raw, []string{"path", "old", "new", "all"}, "path", "old", "new")
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
	if st.Size() > int64(maxEditBytes) {
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
	// NBD-011: bound the mutating output. edit_file may grow a file past the
	// edit ceiling when new is larger than old (especially with all=true);
	// reject before the write so the limit is a hard ceiling on disk.
	if len(out) > maxEditBytes {
		return "", false, fmt.Errorf("edit output is %d bytes, limit is %d", len(out), maxEditBytes)
	}
	// Consume read-credit only after the request passed validation, and before
	// the mutation boundary that legitimately spends it.
	readLines := w.reg.ConsumeLinesRead()
	if _, _, err := commit(ctx, w.root, w.sh, w.log, "edit_file", abs, []byte(out), readLines); err != nil {
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
