package tools

// write_commit.go owns the shared mutation tail: shadow the old state, build
// the journal record, write atomically, then verify by re-reading the file.
// Moved out of write.go without behaviour changes.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"nabd/internal/agent"
	"nabd/internal/snap"
)

// NBD-011: maxEventBytes caps the serialized JSON of an edit event. If the
// event would exceed it, the Patch is dropped so the record (hashes, blobs)
// survives within budget. 4 MB embeds the 1 MB patch with JSON overhead.
// It is a var (not const) so tests can inject a small budget.
var maxEventBytes = 1 << 22

// --- NBD-011 event-size estimation constants ---------------------------------
//
// boundEditEvent estimates the serialized size of the edit event that the
// journal will write, so it can decide whether to drop the Patch before the
// full encode. The estimate uses two conservative allowances:
//
// jsonEscapeWorstCaseFactor: every byte of the Patch may become \u0000 (6
// bytes) in JSON. This is the absolute worst case; typical text patches
// escape far less (control chars, " and \ double, newlines become \n).
// Using 6x guarantees we never UNDER-estimate and keep a patch that would
// push the event over budget.
//
// Tradeoff: 6x is conservative. A 700 KB patch of plain text (escape factor
// ~1.05) is estimated at 4.2 MB and dropped even though it would serialize
// to ~735 KB — well within the 4 MB maxEventBytes. We accept this
// over-dropping: the alternative (full-serializing up to 4 MB on every edit
// to measure exactly) is costlier on a phone, and a dropped Patch still
// preserves the audit fields (hashes, blobs). The decision favors never
// emitting an oversized event over keeping every Patch.
//
// eventEnvelopeAllowance: the journal (agent.Loop.emitAt) stamps Seq, Parent,
// and Time on the Event before serializing — fields that boundEditEvent does
// not set when it marshals agent.Event{Type, Edit: rec} for the baseline.
// Measured directly by field (see internal/agent/event.go for the JSON tags):
//
//	Seq    int       json:"seq"              → "seq":9223372036854775807 = 18 B
//	Parent int       json:"parent,omitempty" → ,"parent":9223372036854775806 = 29 B (omitted when 0)
//	Time   time.Time json:"t"                 → ,"t":"2026-09-06T00:19:03.703040241Z" = 10 B
//
// Total worst-case envelope = 18 + 29 + 10 = 57 bytes (typical ~29 B for 5-digit seq).
// 128 bytes is ~2.2× the measured worst case, a conservative margin.
const (
	jsonEscapeWorstCaseFactor = 6
	eventEnvelopeAllowance    = 128
)

// The actual journal encodes once (store.JSONL.Append → json.Marshal(e));
// boundEditEvent's own marshal of the bare record is cheap (bounded audit
// fields), so no 4 MB encode runs on the phone per edit.

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
// The read credit consumed here is the number of lines the model actually
// read before writing (0 for a blind write or an invalid credit), recorded in
// the persisted EditRecord.
//
// NBD-011: ctx is threaded through to the diff work so cancellation can stop
// it. The record is always persisted (even if the diff fails) so /undo keeps
// its hashes and blobs; a failed diff simply leaves Patch empty.
//
// NBD-011: the "after" state and the diff (buildRecord) are computed BEFORE
// WriteAtomic, so the expensive LCS matrix allocation happens where failure
// leaves no on-disk trace. The critical window — between WriteAtomic and
// log.add — contains only log.add, shrinking the interruption window that
// the Android lowmemorykiller could exploit.
func commit(ctx context.Context, root *Root, sh *snap.Shadow, log *editLog, reg *Registry, tool, abs string, data []byte) (snap.State, snap.State, error) {
	before, err := sh.Capture(abs)
	if err != nil {
		return before, snap.State{}, err
	}
	if err := mkdirParentDirs(abs); err != nil {
		return before, snap.State{}, err
	}
	mode := os.FileMode(0o644)
	if !before.Absent && before.Mode != 0 {
		mode = before.Mode
	}
	// Compute the "after" state from the in-memory data before any project-file
	// write. CaptureBytes stores content in the shadow (a separate dir), not
	// on the project disk path, so a diff failure leaves no on-disk trace.
	after, err := sh.CaptureBytes(abs, data, mode)
	if err != nil {
		return before, snap.State{}, err
	}

	// NBD-034: Validate read credit against target path and pre-mutation content hash.
	var readLines int
	if reg != nil {
		var beforeHash string
		if !before.Absent {
			if b, err := sh.Read(before.Blob); err == nil {
				beforeHash = sha256hex(b)
			}
		}
		readLines = reg.ConsumeLinesRead(abs, beforeHash)
	}

	// The diff (LCS matrix allocation) runs here — BEFORE WriteAtomic. If it
	// aborts (budget exceeded, ctx cancelled), the project file is untouched.
	var budget *diffBudget
	if reg != nil {
		budget = reg.diffBudget
	}
	rec, rerr := buildRecord(ctx, budget, sh, before, after, data, readLines)
	if rerr != nil {
		// Clean up the orphan blob that CaptureBytes wrote to the shadow
		// store. The mutation is aborted, so no Edit will ever reference it;
		// leaving it would accumulate garbage across repeated rejections.
		_ = sh.Discard(after.Blob)
		return before, after, rerr
	}
	if err := snap.WriteAtomic(abs, data, mode); err != nil {
		return before, after, err
	}
	// From here the disk has already changed. Every exit below must leave a
	// record behind, or /undo goes blind exactly when it is needed most.
	log.add(Edit{Tool: tool, Rel: root.Rel(abs), Before: before, After: after, Record: rec})
	// Verify the write by reading back from disk and comparing against the
	// expected state computed from data above.
	actual, aerr := sh.Capture(abs)
	if aerr != nil {
		return before, after, aerr
	}
	if !snap.Unchanged(after, actual) {
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
// If the event still exceeds the budget after dropping Patch, an error is
// returned so commit() can abort before WriteAtomic.
func buildRecord(ctx context.Context, budget *diffBudget, sh *snap.Shadow, before, after snap.State, data []byte, readLines int) (*agent.EditRecord, error) {
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
			if patch, err := unifiedDiffWithBudget(ctx, budget, b, data, rec.Path); err == nil {
				rec.Patch = patch
			}
		}
	} else {
		if patch, err := unifiedDiffWithBudget(ctx, budget, nil, data, rec.Path); err == nil {
			rec.Patch = patch
		}
	}
	return boundEditEvent(rec)
}

// boundEditEvent enforces the event-size budget (NBD-011) on the serialized
// edit event. The Patch is the only unbounded field: if the event would exceed
// maxEventBytes, the Patch is dropped so the audit fields (hashes, blobs)
// survive within budget.
//
// The measurement accounts for the journal envelope: emitAt (in the agent
// loop) adds Seq, Parent, and Time to the Event before serializing, fields
// absent from the bare agent.Event{Type, Edit: rec} that this function
// marshals. The eventEnvelopeAllowance constant covers those fields. The
// Patch contribution is estimated with jsonEscapeWorstCaseFactor (6x, the
// worst case for \uXXXX encoding) rather than fully serialized, so the phone
// never encodes up to 4 MB of patch text just to make a decision. The actual
// journal encodes once (store.JSONL.Append).
//
// After dropping Patch, the size is re-checked: if the bare record still
// exceeds the budget, an error is returned so commit() can abort before
// WriteAtomic instead of writing an oversized event.
func boundEditEvent(rec *agent.EditRecord) (*agent.EditRecord, error) {
	if maxEventBytes <= 0 {
		return rec, nil
	}
	// Baseline: marshal the audit fields without the patch. This is cheap —
	// the audit fields are bounded (two 64-char hashes, two blob IDs, path,
	// read_lines, mode_before) — to get the exact encoded size of the
	// non-patch portion.
	bare := *rec
	bare.Patch = ""
	baseline, err := json.Marshal(agent.Event{Type: agent.EventEdit, Edit: &bare})
	if err != nil {
		return rec, nil
	}
	// Estimate the full event: baseline + worst-case patch escaping + envelope.
	patchEstimate := len(rec.Patch) * jsonEscapeWorstCaseFactor
	if len(baseline)+patchEstimate+eventEnvelopeAllowance <= maxEventBytes {
		return rec, nil
	}
	// The patch (with escaping + envelope) would exceed the budget. Drop it.
	rec.Patch = ""
	// Re-check after dropping: if the bare record + envelope still exceeds
	// the budget, return an error rather than writing an oversized event.
	if len(baseline)+eventEnvelopeAllowance > maxEventBytes {
		return rec, fmt.Errorf(
			"edit event exceeds %d bytes even without patch "+
				"(bare=%d + envelope_allowance=%d)",
			maxEventBytes, len(baseline), eventEnvelopeAllowance)
	}
	return rec, nil
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
