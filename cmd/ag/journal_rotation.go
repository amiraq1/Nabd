package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nabd/internal/store"
)

// maybeRotateOversizedJournal implements the size trigger for journal
// rotation (M2). When the session journal at path exceeds
// store.MaxJournalBytes, the full history is archived aside under a
// non-.jsonl name (invisible to latest-session discovery and purge) and the
// live file is rewritten to contain only the live branch, byte-for-byte.
//
// Safety properties:
//   - The archive is a hard link (falling back to a copy), so the original
//     bytes survive even if the rewrite crashes midway; the operation is
//     idempotent and can simply be retried.
//   - The rewrite preserves raw source lines, so unknown fields and the
//     original redaction stand untouched.
//   - The temp file never ends in .jsonl, and stale ones are cleaned
//     best-effort, so a crash cannot pollute session discovery.
//
// It returns rotated=false when the journal is within bounds. Any error
// leaves the original file intact; callers should warn and continue on the
// oversized file rather than stranding the session.
func maybeRotateOversizedJournal(path string, warn io.Writer) (rotated bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.Size() <= store.MaxJournalBytes {
		return false, nil
	}

	live, raw, total, err := store.ReadLiveBranch(path, true)
	if err != nil {
		return false, fmt.Errorf("cannot resolve live branch: %w", err)
	}
	if len(raw) != len(live) {
		return false, fmt.Errorf("live branch/raw line count mismatch (%d vs %d)", len(live), len(raw))
	}

	archive := fmt.Sprintf("%s.archived-%d", path, time.Now().UnixNano())
	if err := linkOrCopyFile(path, archive); err != nil {
		return false, fmt.Errorf("cannot archive full history: %w", err)
	}

	dir := filepath.Dir(path)
	cleanStaleRotationTemps(dir)
	tmp, err := os.CreateTemp(dir, ".rotmp-*")
	if err != nil {
		return false, fmt.Errorf("cannot stage rotated journal: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	for _, line := range raw {
		if _, err = tmp.Write(line); err != nil {
			_ = tmp.Close()
			return false, fmt.Errorf("cannot write rotated journal: %w", err)
		}
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("cannot sync rotated journal: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return false, fmt.Errorf("cannot close rotated journal: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return false, fmt.Errorf("cannot install rotated journal: %w", err)
	}
	if err = fsyncDir(dir); err != nil {
		// The rotated journal is already installed: the rotation happened.
		// Report it as done so the caller warns about the sync failure
		// instead of claiming rotation was skipped and retrying.
		return true, fmt.Errorf("cannot sync session directory: %w", err)
	}

	if warn != nil {
		fmt.Fprintf(warn, "note: journal exceeded %d MB; archived %d events to %s and continued on the live branch (%d events)\n",
			store.MaxJournalBytes>>20, total, filepath.Base(archive), len(live))
	}
	return true, nil
}

// linkOrCopyFile archives src at dst, preferring a hard link (instant, no
// extra disk use) and falling back to a byte copy on filesystems without
// hard-link support.
func linkOrCopyFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, cpErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if cpErr != nil {
		_ = os.Remove(dst)
		return cpErr
	}
	if syncErr != nil {
		_ = os.Remove(dst)
		return syncErr
	}
	return closeErr
}

// cleanStaleRotationTemps removes temp files left by an interrupted rotation.
// Best-effort: rotation must never fail because cleanup did.
func cleanStaleRotationTemps(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, ".rotmp-") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// fsyncDir persists a rename in dir.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
