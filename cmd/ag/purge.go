package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runPurgeCommand removes only regular session journals directly inside the
// selected session directory. It never traverses subdirectories or follows
// symlinks. A dry run is the default; --yes is required before deletion.
func runPurgeCommand(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("purge", flag.ContinueOnError)
	fs.SetOutput(errOut)
	dir := fs.String("dir", "", "session directory (default ~/.ag/sessions)")
	before := fs.String("before", "", "only journals modified before RFC3339 timestamp")
	yes := fs.Bool("yes", false, "confirm deletion")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(errOut, "usage: nabd purge [--dir <path>] [--before <RFC3339>] [--yes]")
		return 2
	}

	cutoff, err := parsePurgeCutoff(*before)
	if err != nil {
		fmt.Fprintln(errOut, "nabd purge:", err)
		return 2
	}

	target := *dir
	if target == "" {
		target, err = defaultSessionDir()
	} else {
		target = filepath.Clean(target)
	}
	if err != nil {
		fmt.Fprintln(errOut, "nabd purge:", err)
		return 1
	}

	files, err := purgeCandidates(target, cutoff)
	if err != nil {
		fmt.Fprintln(errOut, "nabd purge:", err)
		return 1
	}
	if !*yes {
		fmt.Fprintf(out, "would purge %d session journal(s) from %s\n", len(files), target)
		if len(files) > 0 {
			fmt.Fprintln(out, "dry run: re-run with --yes to delete them; stop nabd first")
		}
		return 0
	}

	removed := 0
	for _, path := range files {
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(errOut, "nabd purge: remove %s: %v\n", path, err)
			continue
		}
		removed++
	}
	if removed != len(files) {
		fmt.Fprintf(out, "purged %d/%d session journal(s) from %s\n", removed, len(files), target)
		return 1
	}
	fmt.Fprintf(out, "purged %d session journal(s) from %s\n", removed, target)
	return 0
}

func parsePurgeCutoff(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("--before must be RFC3339: %w", err)
	}
	return t, nil
}

func purgeCandidates(dir string, cutoff time.Time) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if !cutoff.IsZero() && !info.ModTime().Before(cutoff) {
			continue
		}
		files = append(files, path)
	}
	return files, nil
}
