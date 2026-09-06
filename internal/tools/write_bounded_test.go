package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
)

// diffBoundsBackup restores the configurable diff/event/output bounds after a
// test.
type diffBoundsBackup struct {
	maxEditBytes  int
	maxDiffLines  int
	maxDiffCells  int
	maxPatchBytes int
	maxEventBytes int
}

// setDiffBounds overrides the configurable bounds for the duration of a test and
// restores them on cleanup. Tests use small budgets to avoid huge fixtures.
func setDiffBounds(t *testing.T, edit, lines, cells, patch, event int) {
	t.Helper()
	back := diffBoundsBackup{
		maxEditBytes:  maxEditBytes,
		maxDiffLines:  maxDiffLines,
		maxDiffCells:  maxDiffCells,
		maxPatchBytes: maxPatchBytes,
		maxEventBytes: maxEventBytes,
	}
	t.Cleanup(func() {
		maxEditBytes = back.maxEditBytes
		maxDiffLines = back.maxDiffLines
		maxDiffCells = back.maxDiffCells
		maxPatchBytes = back.maxPatchBytes
		maxEventBytes = back.maxEventBytes
	})
	maxEditBytes = edit
	maxDiffLines = lines
	maxDiffCells = cells
	maxPatchBytes = patch
	maxEventBytes = event
}

// TestEditOutputExceedsLimit verifies NBD-011: edit_file whose replacement output
// would exceed the edit ceiling is rejected before any filesystem mutation. Uses
// a small edit ceiling and a bounded fixture: a file with many occurrences of a
// short token, each replaced by a token just over the per-occurrence budget so
// the aggregate output exceeds the ceiling without allocating a memory bomb.
func TestEditOutputExceedsLimit(t *testing.T) {
	// Small ceiling; the fixture stays bounded (a few KB on disk).
	setDiffBounds(t, 500, 100000, 1<<30, 1<<20, 1<<22)
	r, dir := newReg(t)
	ctx := context.Background()

	// 100 occurrences of "ab\n" => 300 bytes input. Replacing each "ab" with a
	// 10-byte string => 100*(10-2) = 800 bytes of growth => 1100 > 500 ceiling.
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("ab\n")
	}
	path := filepath.Join(dir, "rep.txt")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeHash := fileSHA256(t, path)

	raw, _ := json.Marshal(map[string]any{
		"path": "rep.txt",
		"old":  "ab",
		"new":  "XXXXXXXXXX", // 10 bytes, replaces every "ab" (2 bytes)
		"all":  true,
	})
	_, ok, err := r.Run(ctx, providerToolCall("edit_file", raw))
	if ok || err == nil {
		t.Fatalf("edit_file producing oversized output must be rejected, got ok=%v err=%v", ok, err)
	}
	// File must be untouched.
	if got := fileSHA256(t, path); got != beforeHash {
		t.Errorf("file mutated despite rejection: before=%s after=%s", beforeHash, got)
	}
}

// TestDiffWorkBudgetBounded verifies NBD-011: the diff algorithm respects a work
// budget and does not build a full quadratic matrix for oversized input. With a
// tight cell budget and inputs that would exceed it, the diff must abort rather
// than allocate O(n*m).
func TestDiffWorkBudgetBounded(t *testing.T) {
	// Two inputs each with many unique lines => n*m far exceeds a small cell
	// budget. The bounded diff must return an error without building the full
	// matrix.
	setDiffBounds(t, 1<<20, 100000, 1000, 1<<20, 1<<20) // cells budget tiny
	before := strings.Repeat("before-line\n", 500)
	after := strings.Repeat("after-line\n", 500)

	_, err := unifiedDiff(context.Background(), []byte(before), []byte(after), "x.txt")
	if err == nil {
		t.Fatalf("expected diff to fail under a tiny cell budget, got nil error")
	}
}

// TestDiffBudgetRejectionLeavesFileWrittenAndRecordIntact verifies NBD-011:
// when the diff budget is exceeded, the diff aborts (Patch is empty) but the
// write still succeeds and the EditRecord retains all audit fields
// (hashes, blobs). This is a regression test for the move-diff-before-write
// restructure: the diff failure must NOT block the write, and the record must
// remain complete for /undo.
func TestDiffBudgetRejectionLeavesFileWrittenAndRecordIntact(t *testing.T) {
	// Tiny cell budget so the diff aborts, but the write must still proceed.
	setDiffBounds(t, 1<<20, 100000, 10, 1<<20, 1<<22)
	r, dir := newReg(t)
	ctx := context.Background()

	path := filepath.Join(dir, "budget.txt")
	before := "line one\nline two\nline three\n"
	after := "completely\ndifferent\ncontent\nhere\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeHash := fileSHA256(t, path)

	raw, _ := json.Marshal(map[string]any{
		"path":    "budget.txt",
		"content": after,
	})
	_, ok, err := r.Run(ctx, providerToolCall("write_file", raw))
	if err != nil || !ok {
		t.Fatalf("write_file must succeed even when diff budget is exceeded: ok=%v err=%v", ok, err)
	}

	// File must be correctly written on disk — diff failure doesn't abort the write.
	b, _ := os.ReadFile(path)
	if string(b) != after {
		t.Fatalf("file content mismatch after diff-budget rejection: got %q, want %q", string(b), after)
	}
	if got := fileSHA256(t, path); got == beforeHash {
		t.Error("file hash unchanged; content was not written despite success")
	}

	// The record must retain all audit fields even though the patch was dropped.
	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("no EditRecord persisted after diff-budget rejection")
	}
	if rec.HashBefore == "" {
		t.Error("HashBefore empty; must survive diff failure")
	}
	if rec.HashAfter == "" {
		t.Error("HashAfter empty; must survive diff failure")
	}
	if rec.BlobBefore == "" {
		t.Error("BlobBefore empty; must survive diff failure")
	}
	if rec.BlobAfter == "" {
		t.Error("BlobAfter empty; must survive diff failure")
	}
	// Patch must be empty (the diff was rejected, not the write).
	if rec.Patch != "" {
		t.Errorf("Patch should be empty after diff-budget rejection, got %d bytes", len(rec.Patch))
	}
}

// TestDiffRespectsCancellation verifies NBD-011: a cancelled context terminates
// expensive diff work promptly instead of running to completion.
func TestDiffRespectsCancellation(t *testing.T) {
	setDiffBounds(t, 1<<20, 100000, 1<<30, 1<<20, 1<<20) // huge budget, so only ctx stops it
	before := strings.Repeat("cancel-before\n", 4000)
	after := strings.Repeat("cancel-after\n", 4000)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := unifiedDiff(ctx, []byte(before), []byte(after), "c.txt")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("diff did not terminate promptly on cancellation: took %v", elapsed)
	}
}

// TestEventSizeBounded verifies NBD-011: an event whose serialized JSON would
// exceed the configured event budget is stored with the unbounded Patch field
// omitted, while the audit fields (hashes, blobs) survive. The budget is set
// between the bare-record size and the record+patch size so the patch is the
// thing that pushes it over and the only thing dropped.
func TestEventSizeBounded(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	path := filepath.Join(dir, "ev.txt")
	content := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"path":    "ev.txt",
		"content": "uno\ndos\ntres\n",
	})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file failed: ok=%v err=%v", ok, err)
	}
	full := r.LastEdit()
	if full == nil {
		t.Fatal("no EditRecord persisted")
	}
	// Audit fields must survive.
	if full.HashBefore == "" || full.HashAfter == "" {
		t.Fatalf("audit hashes lost: before=%q after=%q", full.HashBefore, full.HashAfter)
	}
	if full.BlobBefore == "" || full.BlobAfter == "" {
		t.Fatalf("recovery blobs lost: before=%q after=%q", full.BlobBefore, full.BlobAfter)
	}
	if full.Patch == "" {
		t.Fatalf("expected a non-empty patch for this change; got empty")
	}

	// Compute the serialized size of the bare record (patch dropped) and of the
	// full record. The budget is set between them so bounding must drop the
	// patch to fit.
	bare := *full
	bare.Patch = ""
	bareBytes, err := json.Marshal(agent.Event{Type: agent.EventEdit, Edit: &bare})
	if err != nil {
		t.Fatal(err)
	}
	fullBytes, err := json.Marshal(agent.Event{Type: agent.EventEdit, Edit: full})
	if err != nil {
		t.Fatal(err)
	}
	if len(fullBytes) <= len(bareBytes) {
		t.Fatalf("patch added no size; cannot exercise bounding (bare=%d full=%d)", len(bareBytes), len(fullBytes))
	}
	// Budget fits the bare record (plus envelope allowance) but not the full
	// one. The envelope allowance accounts for the journal's Seq/Parent/Time
	// fields that boundEditEvent does not measure directly.
	setDiffBounds(t, 1<<20, 100000, 1<<30, 1<<20, len(bareBytes)+eventEnvelopeAllowance)

	// Repeat the write under the tight budget; the record must be bounded.
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file under budget failed: ok=%v err=%v", ok, err)
	}
	rec := r.LastEdit()
	// Audit fields survive.
	if rec.HashBefore == "" || rec.HashAfter == "" {
		t.Fatalf("audit hashes lost under budget: before=%q after=%q", rec.HashBefore, rec.HashAfter)
	}
	if rec.BlobBefore == "" || rec.BlobAfter == "" {
		t.Fatalf("recovery blobs lost under budget: before=%q after=%q", rec.BlobBefore, rec.BlobAfter)
	}
	// The patch must have been dropped to fit the budget.
	if rec.Patch != "" {
		t.Fatalf("patch should have been dropped to fit budget; got %d bytes", len(rec.Patch))
	}
	b, err := json.Marshal(agent.Event{Type: agent.EventEdit, Edit: rec})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > maxEventBytes {
		t.Fatalf("event exceeds budget: serialized %d bytes, budget %d", len(b), maxEventBytes)
	}
}

// TestEventSizeUnderBudgetKeepsPatch verifies NBD-011 Task 3: when the budget
// is large enough to hold the full event (patch included) after the envelope
// allowance and worst-case escaping estimate, the Patch is NOT dropped.
// This exercises the "just under budget" boundary with the new estimation
// logic. If the estimate over-drops, this test catches it.
func TestEventSizeUnderBudgetKeepsPatch(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	path := filepath.Join(dir, "under.txt")
	content := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"path":    "under.txt",
		"content": "uno\ndos\ntres\n",
	})

	// First write at default budget to get the record and patch.
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file failed: ok=%v err=%v", ok, err)
	}
	full := r.LastEdit()
	if full == nil || full.Patch == "" {
		t.Fatal("expected non-empty patch from first write")
	}

	// Budget set to exactly accommodate the estimation:
	// baseline + 6*len(patch) + envelope + 1 (one byte of headroom).
	bare := *full
	bare.Patch = ""
	bareBytes, err := json.Marshal(agent.Event{Type: agent.EventEdit, Edit: &bare})
	if err != nil {
		t.Fatal(err)
	}
	budget := len(bareBytes) + len(full.Patch)*jsonEscapeWorstCaseFactor + eventEnvelopeAllowance + 1
	setDiffBounds(t, 1<<20, 100000, 1<<30, 1<<20, budget)

	// Reset and rewrite the same change.
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, err := r.Run(ctx, providerToolCall("write_file", raw))
	if err != nil || !ok {
		t.Fatalf("write_file under generous budget failed: ok=%v err=%v", ok, err)
	}

	rec := r.LastEdit()
	if rec == nil {
		t.Fatal("no EditRecord persisted")
	}
	// Patch must survive — the budget accommodated the estimate.
	if rec.Patch == "" {
		t.Fatal("patch should survive under generous budget, got empty patch")
	}
	// Audit fields must survive.
	if rec.HashBefore == "" || rec.HashAfter == "" {
		t.Errorf("audit hashes lost: before=%q after=%q", rec.HashBefore, rec.HashAfter)
	}
	if rec.BlobBefore == "" || rec.BlobAfter == "" {
		t.Errorf("recovery blobs lost: before=%q after=%q", rec.BlobBefore, rec.BlobAfter)
	}
	// The serialized event (bare + envelope allowance) must not exceed budget.
	b, _ := json.Marshal(agent.Event{Type: agent.EventEdit, Edit: rec})
	if len(b)+eventEnvelopeAllowance > budget {
		t.Errorf("event+%d envelope = %d bytes exceeds budget %d", eventEnvelopeAllowance, len(b)+eventEnvelopeAllowance, budget)
	}
}

// TestPatchHeaderValidity verifies NBD-011: the unified-diff hunk headers track
// the new-file line position independently of the old-file position, so the
// produced patch is syntactically valid for git apply --check. A regression here
// would produce headers whose new-side start does not match the actual hunk
// position (e.g. after insertions/deletions shift the new-file line number).
func TestPatchHeaderValidity(t *testing.T) {
	if err := runGitApplyCheck(t); err != nil {
		t.Skipf("git apply not usable in this environment: %v", err)
	}

	r, dir := newReg(t)
	ctx := context.Background()

	// A file where insertions and deletions shift the new-file line numbers
	// away from the old-file line numbers.
	path := filepath.Join(dir, "p.txt")
	before := "keep1\nDEL1\nkeep2\nDEL2\nkeep3\n"
	after := "keep1\nADD1\nADD2\nkeep2\nkeep3\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": "p.txt", "content": after})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file failed: ok=%v err=%v", ok, err)
	}
	rec := r.LastEdit()
	if rec == nil || rec.Patch == "" {
		t.Fatal("no patch produced")
	}
	if err := gitApplyCheck(t, before, rec.Patch); err != nil {
		t.Fatalf("patch failed git apply --check:\n--- patch ---\n%s\n--- error ---\n%v", rec.Patch, err)
	}
}

// TestPatchTrailingContextAfterChange is a regression test for the trailing-context
// bug in unifiedDiff: when a hunk has more than ctxSize (3) unchanged lines after
// the last edit, the old code kept the LAST ctxSize lines and dropped the first one
// (the line closest to the change). That produced a patch whose hunk header range
// did not cover the dropped context line, so git apply --check rejected it.
//
// The trigger is >=4 trailing context lines after an edit. Existing tests had at most
// one trailing context line (keep3), so the buggy branch never executed.
func TestPatchTrailingContextAfterChange(t *testing.T) {
	if err := runGitApplyCheck(t); err != nil {
		t.Skipf("git apply not usable in this environment: %v", err)
	}

	r, dir := newReg(t)
	ctx := context.Background()

	path := filepath.Join(dir, "trail.txt")
	// One edit (C→NEW) followed by 7 unchanged lines. The trailing context exceeds
	// ctxSize (3), which exercises the buggy branch: when pending reaches length 4,
	// the old code kept pending[1:4] and dropped pending[0] (the line closest to the
	// change). The fix keeps pending[:3] instead.
	before := "A\nB\nC\nD\nE\nF\nG\nH\nI\nJ\n"
	after := "A\nB\nNEW\nD\nE\nF\nG\nH\nI\nJ\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": "trail.txt", "content": after})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file failed: ok=%v err=%v", ok, err)
	}
	rec := r.LastEdit()
	if rec == nil || rec.Patch == "" {
		t.Fatal("no patch produced")
	}
	if err := gitApplyCheck(t, before, rec.Patch); err != nil {
		t.Fatalf("patch failed git apply --check (trailing context bug):\n--- patch ---\n%s\n--- error ---\n%v", rec.Patch, err)
	}
	// Round-trip: applying the patch must reproduce the after content exactly.
	result, err := gitApply(t, before, rec.Patch)
	if err != nil {
		t.Fatalf("git apply failed: %v", err)
	}
	if result != after {
		t.Errorf("round-trip mismatch:\n got=%q\nwant=%q", result, after)
	}
}

// gitApplyCheck writes before to a temp file named to match the patch path,
// applies the patch with git apply --check, and returns any error. The patch
// references a/<base> and b/<base>; git strips the a/ b/ prefix and applies
// relative to dir.
func gitApplyCheck(t *testing.T, before, patch string) error {
	t.Helper()
	dir := t.TempDir()
	base := patchBaseName(patch)
	target := filepath.Join(dir, base)
	if err := os.WriteFile(target, []byte(before), 0o644); err != nil {
		return err
	}
	patchPath := filepath.Join(dir, "file.patch")
	if err := os.WriteFile(patchPath, []byte(patch), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("git", "apply", "--check", patchPath)
	cmd.Dir = dir // run inside the dir so a/<base> / b/<base> resolve
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// patchBaseName extracts the file base name from a unified-diff +++ header.
func patchBaseName(patch string) string {
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			return strings.TrimPrefix(line, "+++ b/")
		}
	}
	return "file.txt"
}

// runGitApplyCheck returns nil if `git apply --check` is usable in this env.
func runGitApplyCheck(t *testing.T) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0o644); err != nil {
		return err
	}
	p := filepath.Join(dir, "f.patch")
	if err := os.WriteFile(p, []byte("--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-a\n+b\n"), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("git", "apply", "--check", p)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitApply applies a patch to before-content in a temp dir and returns the
// resulting file content. Used for round-trip fidelity tests.
func gitApply(t *testing.T, before, patch string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	base := patchBaseName(patch)
	target := filepath.Join(dir, base)
	if err := os.WriteFile(target, []byte(before), 0o644); err != nil {
		return "", err
	}
	patchPath := filepath.Join(dir, "file.patch")
	if err := os.WriteFile(patchPath, []byte(patch), 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command("git", "apply", patchPath)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	result, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

// TestPatchRoundTripNoTrailingNewline verifies NBD-011 Task 5: a file without
// a trailing newline produces a patch that, when applied to the before content,
// yields a byte-for-byte match with HashAfter. Without the
// "\ No newline at end of file" marker, git apply either fails or inserts a
// spurious trailing newline, breaking the round trip.
//
// RED: before the fix, the patch omits the marker and git apply fails on a
// no-newline file.
func TestPatchRoundTripNoTrailingNewline(t *testing.T) {
	if err := runGitApplyCheck(t); err != nil {
		t.Skipf("git apply not usable in this environment: %v", err)
	}

	r, dir := newReg(t)
	ctx := context.Background()

	path := filepath.Join(dir, "nonl.txt")
	before := "hello" // no trailing newline
	after := "world"  // no trailing newline
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(map[string]any{
		"path":    "nonl.txt",
		"content": after,
	})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file failed: ok=%v err=%v", ok, err)
	}

	rec := r.LastEdit()
	if rec == nil || rec.Patch == "" {
		t.Fatal("no patch produced")
	}

	// Round-trip: apply the patch to the before content.
	result, err := gitApply(t, before, rec.Patch)
	if err != nil {
		t.Fatalf("git apply failed:\n--- patch ---\n%s\n--- error ---\n%v", rec.Patch, err)
	}
	if result != after {
		t.Errorf("round-trip content mismatch: got %q, want %q", result, after)
	}
	// SHA256 of the applied result must equal HashAfter.
	resultHash := sha256hex([]byte(result))
	if resultHash != rec.HashAfter {
		t.Errorf("round-trip hash mismatch: got %s, want %s", resultHash, rec.HashAfter)
	}
}

// TestPatchRoundTripWithTrailingNewline verifies the round-trip also holds
// when both before and after have trailing newlines (no marker needed).
func TestPatchRoundTripWithTrailingNewline(t *testing.T) {
	if err := runGitApplyCheck(t); err != nil {
		t.Skipf("git apply not usable in this environment: %v", err)
	}

	r, dir := newReg(t)
	ctx := context.Background()

	path := filepath.Join(dir, "nl.txt")
	before := "hello\n"
	after := "world\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(map[string]any{
		"path":    "nl.txt",
		"content": after,
	})
	if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
		t.Fatalf("write_file failed: ok=%v err=%v", ok, err)
	}

	rec := r.LastEdit()
	if rec == nil || rec.Patch == "" {
		t.Fatal("no patch produced")
	}

	result, err := gitApply(t, before, rec.Patch)
	if err != nil {
		t.Fatalf("git apply failed:\n--- patch ---\n%s\n--- error ---\n%v", rec.Patch, err)
	}
	if result != after {
		t.Errorf("round-trip content mismatch: got %q, want %q", result, after)
	}
	resultHash := sha256hex([]byte(result))
	if resultHash != rec.HashAfter {
		t.Errorf("round-trip hash mismatch: got %s, want %s", resultHash, rec.HashAfter)
	}
}

// TestPatchRoundTripMixedNewline verifies the marker appears on the correct
// side: old without newline, new with newline (and vice versa).
func TestPatchRoundTripMixedNewline(t *testing.T) {
	if err := runGitApplyCheck(t); err != nil {
		t.Skipf("git apply not usable in this environment: %v", err)
	}

	type tc struct {
		name   string
		before string
		after  string
	}
	cases := []tc{
		{"old_no_nl_new_nl", "hello", "hello\nworld\n"},
		{"old_nl_new_no_nl", "hello\nworld\n", "hello"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, dir := newReg(t)
			ctx := context.Background()
			path := filepath.Join(dir, c.name+".txt")
			if err := os.WriteFile(path, []byte(c.before), 0o644); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{
				"path":    c.name + ".txt",
				"content": c.after,
			})
			if _, ok, err := r.Run(ctx, providerToolCall("write_file", raw)); err != nil || !ok {
				t.Fatalf("write_file failed: ok=%v err=%v", ok, err)
			}
			rec := r.LastEdit()
			if rec == nil || rec.Patch == "" {
				t.Fatal("no patch produced")
			}

			result, err := gitApply(t, c.before, rec.Patch)
			if err != nil {
				t.Fatalf("git apply failed:\n--- patch ---\n%s\n--- error ---\n%v", rec.Patch, err)
			}
			if result != c.after {
				t.Errorf("round-trip content mismatch: got %q, want %q", result, c.after)
			}
			resultHash := sha256hex([]byte(result))
			if resultHash != rec.HashAfter {
				t.Errorf("round-trip hash mismatch: got %s, want %s", resultHash, rec.HashAfter)
			}
		})
	}
}

// BenchmarkUnifiedDiff measures the worst-case LCS matrix cost: two inputs
// of N lines each with NO shared lines, so every cell in the (n+1)*(m+1)
// table is computed. This is the calibration benchmark recorded in
// docs/TECH_DEBT.md (G1). It uses the package-default limits (maxDiffCells
// = 4_000_000), which permits the 2000x2000 case exactly.
func BenchmarkUnifiedDiff(b *testing.B) {
	for _, n := range []int{100, 500, 1000, 2000} {
		var before, after strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&before, "before-line-%d\n", i)
			fmt.Fprintf(&after, "after-line-%d\n", i)
		}
		bb := []byte(before.String())
		ba := []byte(after.String())
		b.Run(fmt.Sprintf("lines-%d", n), func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = unifiedDiff(ctx, bb, ba, "bench.txt")
			}
		})
	}
}

// TestEventSizeExceedsBudgetAfterPatchDrop verifies NBD-011 Task 3: when the
// event budget is so small that even the bare record (audit fields without
// Patch) exceeds it — after accounting for the journal envelope allowance —
// boundEditEvent must return an explicit error instead of writing an oversized
// event. The current code (which does not account for the envelope and never
// returns an error) passes this through silently.
//
// RED proof: before the fix, write_file returns ok=true err=<nil> even with
// maxEventBytes=10 (the bare record alone is ~200+ bytes).
func TestEventSizeExceedsBudgetAfterPatchDrop(t *testing.T) {
	r, dir := newReg(t)
	ctx := context.Background()

	path := filepath.Join(dir, "tiny.txt")
	seed := []byte("original\n")
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"path":    "tiny.txt",
		"content": "changed\n",
	})

	// Budget far below the bare-record size. Even without a Patch, the audit
	// fields + envelope exceed this.
	setDiffBounds(t, 1<<20, 100000, 1<<30, 1<<20, 10)

	_, ok, err := r.Run(ctx, providerToolCall("write_file", raw))
	if ok || err == nil {
		t.Fatalf("expected error when bare record exceeds event budget, got ok=%v err=%v", ok, err)
	}
	// The file must not have been written — commit() returns before WriteAtomic
	// when buildRecord fails.
	b, _ := os.ReadFile(path)
	if string(b) != "original\n" {
		t.Errorf("file modified despite rejection: got %q, want %q", string(b), "original\n")
	}
}
