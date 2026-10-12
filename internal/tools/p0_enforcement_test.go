package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/event"
)

// This file holds the negative tests for Tasks 1, 3, and 4 of the audit spec.
//
// Compile status (FAILS on the sanctioned base — CORRECTED). An earlier version
// of this header claimed the tests build cleanly against a pre-fix checkout plus
// a behaviour-neutral scaffolding file. That claim was incomplete and is
// retracted. Rebuilt from the real pre-fix tree it fails, and the cause is a
// missing dependency the scaffold does not declare:
//
//	GOOS=linux go vet ./internal/tools/
//	internal\tools\p0_4_test.go:12:2: package nabd/internal/approval is not in std
//
//	GOOS=linux go test -c -o /dev/null ./internal/tools/
//	FAIL	nabd/internal/tools [setup failed]
//
// approvedCtx (p0_4_test.go:50) calls approval.WithToken/WithCall/Mint, and
// nabd/internal/approval does not exist at the pre-fix commit — it is a package
// introduced by the fix. The scaffold in the verification recipe declares the
// sentinels and maxExcludedReadBytes but not this package, so the recipe is
// incomplete. Adding internal/approval/ does make both commands exit 0, but that
// means importing production code from the fix into the base, which the base is
// not supposed to contain.
//
// Consequence: no valid RED exists for these tests yet. A compile failure is not
// a RED, and the base cannot currently be compiled without the fix.
//
// Run status (NOT VERIFIED). The tests have never been executed: the repo is
// unix/linux-tagged, no Linux runner is reachable from this sandbox (no docker,
// wsl.exe is blacklisted), and a Windows build is not a valid substitute — the
// suite exercises safefs' descriptor-relative open, which on Windows falls back
// to the resolve-then-open compatibility path in safefs/open_other.go.
//
// RED vs GUARD. Classification below comes from reading the pre-fix commit, not
// from running anything, and is therefore UNPROVEN. Only tests whose failure is
// demonstrated by a log are usable as the negative test audit rule 1 requires.

// --- Task 1: fail-closed when the exclusion policy cannot be determined -------

// TestP01CommitRefusesNilRegistry — RED متوقع (غير مثبت: لم يُشغَّل).
//
// Pins that commit() refuses to write when it is handed no registry. Pre-fix,
// commit() guards the excluded branch with `if reg != nil && reg.isPathExcluded(...)`,
// so a nil registry skips the exclusion decision entirely, falls through to
// buildRecord, and the file IS written (err == nil). The fix makes "cannot
// determine the exclusion policy" fail closed. On the pre-fix code this test
// therefore fails on its first assertion for the intended reason.
func TestP01CommitRefusesNilRegistry(t *testing.T) {
	r, dir := newReg(t)
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := commit(context.Background(), r.root, r.sh, &editLog{}, nil, "write_file", "main.go", []byte("changed\n"))
	if err == nil {
		t.Fatal("commit with a nil registry must fail closed, got nil error")
	}
	if b, rerr := os.ReadFile(target); rerr != nil || string(b) != "package main\n" {
		t.Fatalf("a nil-registry commit must not write the file: err=%v content=%q", rerr, string(b))
	}
}

// --- Task 3: no unbounded/direct reads on the excluded path -------------------

// TestP03ExcludedFileOverLimitRefused — RED متوقع (غير مثبت: لم يُشغَّل).
//
// Pins that a pre-existing excluded file larger than maxExcludedReadBytes is
// refused before any write. Pre-fix, the excluded branch calls
// buildExcludedRecord, which never reads the pre-existing file at all and has no
// size limit, so the write proceeds (err == nil) and the over-limit file is
// replaced. The fix reads the file through the safe path under a bound and
// refuses an over-limit target. On the pre-fix code this test fails on its first
// assertion for the intended reason. Note maxExcludedReadBytes is unused by the
// pre-fix code, so lowering it there is inert — which is exactly the defect.
func TestP03ExcludedFileOverLimitRefused(t *testing.T) {
	old := maxExcludedReadBytes
	maxExcludedReadBytes = 16
	t.Cleanup(func() { maxExcludedReadBytes = old })

	r, _, dir := newGatedReg(t, "big.bin\n")
	big := filepath.Join(dir, "big.bin")
	const size = 100
	if err := os.WriteFile(big, []byte(strings.Repeat("x", size)), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := commit(approvedCtx("big.bin"), r.root, r.sh, &editLog{}, r, "write_file", "big.bin", []byte("small\n"))
	if err == nil {
		t.Fatal("an excluded file over the read limit must be refused, got nil error")
	}
	if b, rerr := os.ReadFile(big); rerr != nil || len(b) != size {
		t.Fatalf("an over-limit excluded file must be left untouched: err=%v len=%d", rerr, len(b))
	}
}

// TestP03ExcludedFileChangedBetweenReadAndPublishRefused is a REGRESSION GUARD,
// not a RED test. OnMutationPrepared runs after the initial read and before the
// pre-publish re-verify, so changing the file there simulates a concurrent
// writer. The pre-fix commit already calls verifyUnchanged immediately before
// publishing (write_commit.go, pre-fix), so this test PASSES on the pre-fix code
// and cannot serve as the negative test for Task 3. It is kept to lock the
// re-verify against regression once the excluded path is rewritten.
func TestP03ExcludedFileChangedBetweenReadAndPublishRefused(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.env\n")
	secret := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(secret, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.OnMutationPrepared = func(rec *event.EditRecord) error {
		return os.WriteFile(secret, []byte("changed-underneath\n"), 0o644)
	}

	_, _, err := commit(approvedCtx("secret.env"), r.root, r.sh, &editLog{}, r, "write_file", "secret.env", []byte("new\n"))
	if err == nil {
		t.Fatal("a change between read and publish must be detected, got nil error")
	}
	if b, rerr := os.ReadFile(secret); rerr != nil || string(b) != "changed-underneath\n" {
		t.Fatalf("the concurrent content must remain: err=%v content=%q", rerr, string(b))
	}
}

// --- Task 4: the excluded path journals like the normal path ------------------

// TestP04ExcludedWriteJournalsIntent — RED متوقع (غير مثبت: لم يُشغَّل).
//
// Pins that an approved excluded write journals its intent through
// OnMutationPrepared carrying only the guard sentinel. Pre-fix, the excluded
// path DOES call OnMutationPrepared exactly once, but buildExcludedRecord fills
// the record with real shadow blob ids (BlobBefore/BlobAfter = after.Blob /
// before.Blob) rather than the guard, i.e. it journals content-addressed
// references to excluded content. The first assertion (exactly one prepared
// call) passes pre-fix; the guard assertion fails, which is the intended defect.
func TestP04ExcludedWriteJournalsIntent(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.env\n")
	secret := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(secret, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var prepared []*event.EditRecord
	r.OnMutationPrepared = func(rec *event.EditRecord) error {
		prepared = append(prepared, rec)
		return nil
	}

	if _, _, err := commit(approvedCtx("secret.env"), r.root, r.sh, &editLog{}, r, "write_file", "secret.env", []byte("after\n")); err != nil {
		t.Fatalf("approved excluded write failed: %v", err)
	}
	if len(prepared) != 1 {
		t.Fatalf("an excluded write must journal its intent exactly once, got %d", len(prepared))
	}
	if prepared[0].BlobBefore != excludedGuard || prepared[0].BlobAfter != excludedGuard {
		t.Fatalf("the journaled intent must carry only the guard, got before=%q after=%q", prepared[0].BlobBefore, prepared[0].BlobAfter)
	}
}

// TestP04ExcludedWriteRefusedWhenIntentJournalingFails is a REGRESSION GUARD,
// not a RED test. The pre-fix commit already calls OnMutationPrepared and
// returns early when it fails, leaving the file untouched, so this test PASSES
// on the pre-fix code. The property being protected is that the rewritten
// excluded path keeps that behaviour.
func TestP04ExcludedWriteRefusedWhenIntentJournalingFails(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.env\n")
	secret := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(secret, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.OnMutationPrepared = func(rec *event.EditRecord) error {
		return errors.New("journal unavailable")
	}

	_, _, err := commit(approvedCtx("secret.env"), r.root, r.sh, &editLog{}, r, "write_file", "secret.env", []byte("after\n"))
	if err == nil {
		t.Fatal("a failure to journal the intent must abort the excluded write, got nil error")
	}
	if b, rerr := os.ReadFile(secret); rerr != nil || string(b) != "before\n" {
		t.Fatalf("the file must not be written when the intent cannot be journaled: err=%v content=%q", rerr, string(b))
	}
}

// --- Task 1 (symlink sub-item): a guard, not a RED test -----------------------

// TestP01SymlinkToExcludedTargetIsRefused is the symlink case of Task 1: a
// symlink whose NAME is not excluded but whose TARGET is must not become a way
// to reach excluded content. It is a REGRESSION GUARD, not a RED test: the
// normal commit path reads through safefs.OpenRead, which refuses a symlink
// (O_NOFOLLOW), so a write through such a link already fails before any fix. The
// test documents and locks that behaviour. If it ever fails, the symlink
// confinement of safefs has regressed.
func TestP01SymlinkToExcludedTargetIsRefused(t *testing.T) {
	r, _, dir := newGatedReg(t, "secret.env\n")
	secret := filepath.Join(dir, "secret.env")
	if err := os.WriteFile(secret, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "notes.txt")
	if err := os.Symlink("secret.env", link); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}

	// notes.txt is not excluded by name; a write through it must not reach the
	// excluded target.
	_, _, err := commit(context.Background(), r.root, r.sh, &editLog{}, r, "write_file", "notes.txt", []byte("attacker\n"))
	if err == nil {
		t.Fatal("a write through a symlink must be refused by the safe read path")
	}
	if b, rerr := os.ReadFile(secret); rerr != nil || string(b) != "secret\n" {
		t.Fatalf("the excluded target must be unchanged: err=%v content=%q", rerr, string(b))
	}
}
