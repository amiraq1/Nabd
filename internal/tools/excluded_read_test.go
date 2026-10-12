package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/event"
	"nabd/internal/perm"
)

// newGatedReg builds a registry wired exactly as cmd/ag wires it: one
// *perm.Policy is both the tool gate (via the registry's own classifier) and the
// path gate, with the session root's .gitignore loaded into it. A test writes
// that .gitignore to declare what the project excludes.
//
// Assertions ask the policy directly with CheckRead, and exercise the refusal
// end-to-end through read_file/grep, so both halves of the wiring are under
// test: SetIgnoreFile on the policy and SetPathGate on the registry.
func newGatedReg(t *testing.T, ignoreContent string) (*Registry, *perm.Policy, string) {
	t.Helper()
	r, dir := newReg(t)
	if ignoreContent != "" {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(ignoreContent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pol := perm.New(r) // the Registry is the Classifier, as in cmd/ag
	pol.SetIgnoreFile(dir)
	r.SetPathGate(pol)
	return r, pol, dir
}

func TestReadFileRefusesIgnoredPath(t *testing.T) {
	r, _, dir := newGatedReg(t, "secrets.env\n")
	secret := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(secret, []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Control: the same bytes under a non-ignored name must read normally.
	if err := os.WriteFile(filepath.Join(dir, "open.txt"), []byte("visible\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := json.RawMessage(`{"path":"secrets.env"}`)
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if ok || err == nil {
		t.Fatalf("read_file on an ignored path must be refused, got ok=%v err=%v out=%q", ok, err, out)
	}
	// The message must name the ignore rule — the refusal is a permission
	// decision about the path, not a containment error and not a stat miss.
	if !strings.Contains(err.Error(), "excluded by the session .gitignore") || !strings.Contains(err.Error(), `"secrets.env"`) {
		t.Fatalf("refusal must name the rule and the pattern, got: %v", err)
	}
	// A refusal must not leak bytes, whatever its wording.
	if strings.Contains(out+err.Error(), "token=whatever") {
		t.Fatalf("refused read leaked content: out=%q err=%v", out, err)
	}

	// Control read: the identical call against a non-ignored sibling succeeds,
	// so the refusal is attributable to the .gitignore line and nothing else.
	ctrl, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":"open.txt"}`)))
	if err != nil || !ok {
		t.Fatalf("control read_file failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(ctrl, "visible") {
		t.Fatalf("control read got %q", ctrl)
	}
}

// TestReadFileRefusalIsNotFromPathindex pins the acceptance requirement that the
// refusal happens on a direct path: no @ picker, no path index involved. The
// index is never built in this test (newReg does not run pathindex), so a pass
// here can only be explained by the permission layer.
func TestReadFileRefusalIsNotFromPathindex(t *testing.T) {
	r, pol, dir := newGatedReg(t, "/vendor/\n")
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor", "lib", "x.so"), []byte("binary-ish\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := json.RawMessage(`{"path":"vendor/lib/x.so"}`)
	_, ok, err := r.Run(context.Background(), providerToolCall("read_file", raw))
	if ok || err == nil {
		t.Fatalf("direct read into an excluded dir must be refused, got ok=%v err=%v", ok, err)
	}
	if !strings.Contains(err.Error(), "excluded by the session .gitignore") {
		t.Fatalf("refusal must come from the ignore rule, got: %v", err)
	}
	// And the same verdict comes from the policy on the same rel, quoted for the
	// trace: the tools layer is a pass-through, not a second opinion.
	if v, why := pol.CheckRead("vendor/lib/x.so"); v != perm.Deny || !strings.Contains(why, `"vendor/lib/x.so"`) {
		t.Fatalf("policy must independently refuse: v=%v why=%q", v, why)
	}
}

// TestReadFileWithoutGateUnchanged pins the pre-rule behaviour for any registry
// built without a path gate: an ignored path reads normally, so the feature
// cannot smuggle itself into sessions that did not wire it.
func TestReadFileWithoutGateUnchanged(t *testing.T) {
	r, dir := newReg(t)
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("secrets.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":"secrets.env"}`)))
	if err != nil || !ok || !strings.Contains(out, "token=whatever") {
		t.Fatalf("ungated registry must read the file, got ok=%v err=%v out=%q", ok, err, out)
	}
}

// TestReadFileAllowReadsOverrideReadsIgnoredPath covers the declared override:
// the same policy in allow-reads mode lets the read through.
// TestGrepSkipsIgnoredFilesWithDisclosure covers the grep half: a file the rule
// excludes is not searched, and the summary says so instead of pretending the
// pattern found nothing.
func TestGrepSkipsIgnoredFilesWithDisclosure(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.txt\n")
	for name, content := range map[string]string{
		"secret.txt":  "needle here\n",
		"visible.txt": "no match\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"needle","path":"."}`)))
	if err != nil || !ok {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "needle here") {
		t.Fatalf("grep leaked an excluded file's content: %q", out)
	}
	if !strings.Contains(out, "1 files excluded by the session .gitignore") {
		t.Fatalf("grep must disclose the exclusion, got: %q", out)
	}
	// And the visible file is still searched normally.
	if !strings.Contains(out, "no match") {
		t.Fatalf("grep stopped searching visible files: %q", out)
	}
}

// TestGrepFromExcludedDirCannotEscape pins the `grep needle .` attack: when the
// search root is inside an excluded directory, every file under it carries the
// excluded prefix and is skipped — with the disclosure — rather than streamed
// out as match lines.
func TestGrepFromExcludedDirCannotEscape(t *testing.T) {
	r, _, dir := newGatedReg(t, "build/\n")
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build", "out.txt"), []byte("needle in build\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"needle","path":"build"}`)))
	if err != nil || !ok {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "needle in build") {
		t.Fatalf("grep leaked excluded-dir content: %q", out)
	}
	if !strings.Contains(out, "1 files excluded by the session .gitignore") {
		t.Fatalf("grep must disclose the skip, got: %q", out)
	}
}

// TestGrepNoExclusionsNoDisclosure pins that the disclosure line appears only
// when something was actually skipped — a summary that cries wolf devalues the
// one that matters.
func TestGrepNoExclusionsNoDisclosure(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.txt\n")
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("needle here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := r.Run(context.Background(), providerToolCall("grep", json.RawMessage(`{"pattern":"needle","path":"."}`)))
	if err != nil || !ok {
		t.Fatalf("grep failed: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "needle here") {
		t.Fatalf("expected the visible match, got %q", out)
	}
	if strings.Contains(out, "excluded by the session .gitignore") {
		t.Fatalf("disclosure must not appear when nothing was skipped: %q", out)
	}
}
func TestReadFileAllowReadsOverrideReadsIgnoredPath(t *testing.T) {
	r, pol, dir := newGatedReg(t, "secrets.env\n")
	pol.SetMode(perm.ModeAllowReads)
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok, err := r.Run(context.Background(), providerToolCall("read_file", json.RawMessage(`{"path":"secrets.env"}`)))
	if err != nil || !ok || !strings.Contains(out, "token=whatever") {
		t.Fatalf("allow-reads must read the ignored file, got ok=%v err=%v out=%q", ok, err, out)
	}
}

// TestEditFileRefusesIgnoredPathAndDoesNotLeakContent proves that edit_file
// cannot touch an ignored file in any mode, refusing before reading and never
// leaking any byte of content in output, error, diff, or shadow.
func TestEditFileRefusesIgnoredPathAndDoesNotLeakContent(t *testing.T) {
	r, _, dir := newGatedReg(t, "secrets.env\n")
	secret := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(secret, []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := json.RawMessage(`{"path":"secrets.env","old":"token=","new":"token=leaked"}`)
	out, ok, err := r.Run(context.Background(), providerToolCall("edit_file", raw))
	if ok || err == nil {
		t.Fatalf("edit_file on an ignored path must be refused, got ok=%v err=%v out=%q", ok, err, out)
	}
	if !strings.Contains(err.Error(), "excluded by the session .gitignore") {
		t.Fatalf("refusal must name the ignore rule, got: %v", err)
	}
	// Non-disclosure: neither out nor err may leak the secret content
	if strings.Contains(out+err.Error(), "token=whatever") {
		t.Fatalf("refused edit leaked content: out=%q err=%v", out, err)
	}
	// Disk content must be untouched
	disk, err := os.ReadFile(secret)
	if err != nil || string(disk) != "token=whatever\n" {
		t.Fatalf("disk content modified despite refusal: %q", string(disk))
	}
}

// TestExcludedWriteRefusedWithoutApprovalStoresNothing proves the B3-GREEN /
// ADR-5 Option A contract end-to-end through the registry: a write_file to a path
// excluded by .gitignore is refused when no explicit approval is present, its
// content never reaches .ag/shadow, and the on-disk file is left untouched.
func TestExcludedWriteRefusedWithoutApprovalStoresNothing(t *testing.T) {
	r, _, dir := newGatedReg(t, "secrets.env\ndist/\n")
	storePath := filepath.Join(r.root.Dir(), ".ag", "shadow")

	secret := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(secret, []byte("token=whatever\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	distOld := filepath.Join(dir, "dist", "old.txt")
	if err := os.WriteFile(distOld, []byte("build_secret_old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// edit_file on secrets.env stays refused (unchanged pre-rule behaviour).
	_, _, _ = r.Run(context.Background(), providerToolCall("edit_file", json.RawMessage(`{"path":"secrets.env","old":"token=","new":"token=leaked"}`)))

	// write_file to an excluded path WITHOUT approval must be refused.
	_, ok1, err1 := r.Run(context.Background(), providerToolCall("write_file", json.RawMessage(`{"path":"dist/old.txt","content":"leaked-content\n"}`)))
	if ok1 || err1 == nil {
		t.Fatalf("write_file to excluded dist/old.txt must be refused without approval, got ok=%v err=%v", ok1, err1)
	}
	if !strings.Contains(err1.Error(), "excluded by the session .gitignore") {
		t.Fatalf("refusal of dist/old.txt must name the ignore rule, got: %v", err1)
	}
	_, ok2, err2 := r.Run(context.Background(), providerToolCall("write_file", json.RawMessage(`{"path":"secrets.env","content":"leaked-content\n"}`)))
	if ok2 || err2 == nil {
		t.Fatalf("write_file to excluded secrets.env must be refused without approval, got ok=%v err=%v", ok2, err2)
	}

	// Disk content must be untouched: the refusal happened before any write.
	if b, err := os.ReadFile(distOld); err != nil || string(b) != "build_secret_old\n" {
		t.Fatalf("excluded dist/old.txt was modified despite refusal: %v content=%q", err, string(b))
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "token=whatever\n" {
		t.Fatalf("excluded secrets.env was modified despite refusal: %v content=%q", err, string(b))
	}

	// No excluded content may have been persisted to the shadow store.
	if shadowContainsMarker(t, storePath, []byte("leaked-content\n")) {
		t.Errorf("excluded content was persisted to .ag/shadow — this is the P0-4 vulnerability")
	}
}

// TestWriteFileToExcludedPathApprovedSucceedsButNotUndoable proves the approved
// half of the B3-GREEN / ADR-5 Option A contract: when an explicit one-shot
// approval is present, write_file to an excluded path DOES write the file to
// disk, but it stores NO blob in the journal (BlobBefore/BlobAfter carry only
// the excludedGuard sentinel, Patch is empty, and no digest is persisted), and a
// later /undo refuses with ErrUndoExcluded instead of restoring or deleting.
func TestWriteFileToExcludedPathApprovedSucceedsButNotUndoable(t *testing.T) {
	r, _, dir := newGatedReg(t, "dist/\n")
	distFile := filepath.Join(dir, "dist", "bundle.js")

	// Without approval, the write is refused.
	if _, ok, err := r.Run(context.Background(), providerToolCall("write_file", json.RawMessage(`{"path":"dist/bundle.js","content":"console.log(1);\n"}`))); ok || err == nil {
		t.Fatalf("write_file to dist/bundle.js must be refused without approval, got ok=%v err=%v", ok, err)
	}

	// With an explicit one-shot approval token, the write proceeds.
	out, ok, err := r.Run(approvedCtx("dist/bundle.js"), providerToolCall("write_file", json.RawMessage(`{"path":"dist/bundle.js","content":"console.log(1);\n"}`)))
	if !ok || err != nil {
		t.Fatalf("approved write_file to dist/bundle.js failed: ok=%v err=%v out=%q", ok, err, out)
	}
	if b, rerr := os.ReadFile(distFile); rerr != nil || string(b) != "console.log(1);\n" {
		t.Fatalf("file not written properly: %v, content=%q", rerr, string(b))
	}

	// The journal record stores no content: only the excludedGuard sentinel.
	first := r.LastEdit()
	if first == nil {
		t.Fatal("expected LastEdit record for the approved excluded write")
	}
	if first.Patch != "" {
		t.Fatalf("Patch must be empty for excluded path, got %q", first.Patch)
	}
	if first.BlobBefore != excludedGuard || first.BlobAfter != excludedGuard {
		t.Fatalf("excluded record must carry only the excludedGuard sentinel, got before=%q after=%q", first.BlobBefore, first.BlobAfter)
	}
	if first.HashBefore != "" || first.HashAfter != "" {
		t.Fatalf("excluded record must not persist any digest, got before=%q after=%q", first.HashBefore, first.HashAfter)
	}

	// /undo must refuse with ErrUndoExcluded and must NOT delete the file.
	res := r.PersistedUndo([]*event.EditRecord{first}, 1)
	if len(res) != 1 || res[0].OK {
		t.Fatalf("undo of excluded write must refuse, got %+v", res)
	}
	if !strings.Contains(res[0].Note, "cannot be undone") {
		t.Fatalf("undo must report ErrUndoExcluded, got note %q", res[0].Note)
	}
	if _, statErr := os.Stat(distFile); statErr != nil {
		t.Fatalf("undo must NOT delete an excluded file, got stat err: %v", statErr)
	}
}

// TestUndoExcludedWriteReturnsErrUndoExcluded proves the undo half of the
// B3-GREEN / ADR-5 Option A contract: an approved write to an excluded path is
// tracked in the journal but is NOT reversible. /undo returns ErrUndoExcluded and
// leaves the on-disk file exactly as the write left it — never restoring the
// pre-write content and never deleting a file the agent created.
func TestUndoExcludedWriteReturnsErrUndoExcluded(t *testing.T) {
	r, _, dir := newGatedReg(t, "dist/\nsecrets.env\n")

	// 1. Creation case: an approved write creates a new excluded file. /undo must
	// refuse and must NOT delete it. The token is single-use, so a fresh one is
	// minted per write.
	distPath := filepath.Join(dir, "dist", "new.txt")
	if out, ok, err := r.Run(approvedCtx("dist/new.txt"), providerToolCall("write_file", json.RawMessage(`{"path":"dist/new.txt","content":"created in dist\n"}`))); !ok || err != nil {
		t.Fatalf("approved creation failed: ok=%v err=%v out=%q", ok, err, out)
	}
	if b, err := os.ReadFile(distPath); err != nil || string(b) != "created in dist\n" {
		t.Fatalf("file not written: %v, content=%q", err, string(b))
	}
	resCreate := r.PersistedUndo([]*event.EditRecord{r.LastEdit()}, 1)
	if len(resCreate) != 1 || resCreate[0].OK {
		t.Fatalf("undo of excluded creation must refuse, got %+v", resCreate)
	}
	if !strings.Contains(resCreate[0].Note, "cannot be undone") {
		t.Fatalf("undo must report ErrUndoExcluded, got note %q", resCreate[0].Note)
	}
	if _, statErr := os.Stat(distPath); statErr != nil {
		t.Fatalf("undo must NOT delete a newly created excluded file, got stat err: %v", statErr)
	}

	// 2. Replacement case: an approved write overwrites a pre-existing excluded
	// file. /undo must refuse and must leave the file at its written (new)
	// content, not restore the secret.
	secretPath := filepath.Join(dir, "secrets.env")
	origContent := "SECRET_TOKEN=initial_secret_12345\n"
	if err := os.WriteFile(secretPath, []byte(origContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, ok, err := r.Run(approvedCtx("secrets.env"), providerToolCall("write_file", json.RawMessage(`{"path":"secrets.env","content":"SECRET_TOKEN=overwritten_by_agent\n"}`))); !ok || err != nil {
		t.Fatalf("approved replacement failed: ok=%v err=%v out=%q", ok, err, out)
	}
	if b, err := os.ReadFile(secretPath); err != nil || string(b) != "SECRET_TOKEN=overwritten_by_agent\n" {
		t.Fatalf("file not overwritten: %v, content=%q", err, string(b))
	}
	resReplace := r.PersistedUndo([]*event.EditRecord{r.LastEdit()}, 1)
	if len(resReplace) != 1 || resReplace[0].OK {
		t.Fatalf("undo of excluded replacement must refuse, got %+v", resReplace)
	}
	if !strings.Contains(resReplace[0].Note, "cannot be undone") {
		t.Fatalf("undo must report ErrUndoExcluded, got note %q", resReplace[0].Note)
	}
	if b, err := os.ReadFile(secretPath); err != nil || string(b) != "SECRET_TOKEN=overwritten_by_agent\n" {
		t.Fatalf("undo must leave the excluded file at its written content, got %v content=%q", err, string(b))
	}
}

// TestExcludedWriteWithApprovalConsumesReadCredit proves that read credit staged
// on the Registry is still consumed by an approved write_file to an excluded path
// (path and pre-mutation hash match), recording the read lines in the EditRecord
// and clearing the staged credit — while persisting NO digest of the excluded
// content (HashBefore is empty).
func TestExcludedWriteWithApprovalConsumesReadCredit(t *testing.T) {
	r, _, dir := newGatedReg(t, "dist/\n")

	distFile := filepath.Join(dir, "dist", "bundle.js")
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	initialContent := "console.log('original');\n"
	if err := os.WriteFile(distFile, []byte(initialContent), 0o644); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256([]byte(initialContent))
	beforeHash := hex.EncodeToString(h[:])

	// Stage read credit for dist/bundle.js
	credit := event.ReadCredit{
		Path:      "dist/bundle.js",
		Hash:      beforeHash,
		Offset:    1,
		Limit:     10,
		LinesRead: 42,
	}
	r.SetReadCredit(credit)

	// Verify credit is staged
	if staged := r.ReadCredit(); staged != credit {
		t.Fatalf("staged credit mismatch: got %+v, want %+v", staged, credit)
	}

	// Approved write to the excluded path (single-use token bound to this path).
	out, ok, err := r.Run(approvedCtx("dist/bundle.js"), providerToolCall("write_file", json.RawMessage(`{"path":"dist/bundle.js","content":"console.log('updated');\n"}`)))
	if !ok || err != nil {
		t.Fatalf("approved write_file failed: ok=%v err=%v out=%q", ok, err, out)
	}

	// Staged credit must be consumed (empty)
	if empty := r.ReadCredit(); empty != (event.ReadCredit{}) {
		t.Fatalf("staged read credit was not consumed: %+v", empty)
	}

	// EditRecord must record the consumed read lines but persist NO digest.
	last := r.LastEdit()
	if last == nil {
		t.Fatal("expected LastEdit record")
	}
	if last.ReadLines != 42 {
		t.Fatalf("LastEdit.ReadLines = %d, want 42", last.ReadLines)
	}
	if last.HashBefore != "" {
		t.Fatalf("LastEdit.HashBefore must be empty for excluded path (no digest persisted), got %q", last.HashBefore)
	}
	if last.BlobBefore != excludedGuard || last.BlobAfter != excludedGuard {
		t.Fatalf("excluded record must carry only the excludedGuard sentinel, got before=%q after=%q", last.BlobBefore, last.BlobAfter)
	}
}
