package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"nabd/internal/store"
)

// rawExportWarning is written to stderr for a raw (unredacted) export so the
// operator is told the output may carry sensitive cleartext. It never reaches
// stdout, which stays pure JSONL.
const rawExportWarning = "warning: exported journal may contain sensitive cleartext data\n"

// exportConflictFlags lists run-mode flags that cannot be combined with
// --export. The comparison is by flag name as registered on the command line.
var exportConflictFlags = []string{
	"replay", "speed", "dir", "continue", "version",
	"feed", "feed-touch", "p", "json", "max-turns", "permission-mode",
}

// checkExportFlags validates an --export invocation before any run mode is
// dispatched. provided holds the flag names explicitly set on the command line
// (from flag.Visit), so a default value is never mistaken for a supplied flag.
func checkExportFlags(exportPath string, redact bool, narg int, provided map[string]bool) error {
	if !provided["export"] {
		if redact {
			return errors.New("--redact requires --export")
		}
		return nil
	}
	if exportPath == "" {
		return errors.New("--export requires a journal path")
	}

	var conflicts []string
	for _, name := range exportConflictFlags {
		if provided[name] {
			conflicts = append(conflicts, "--"+name)
		}
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("--export cannot be combined with %s", strings.Join(conflicts, ", "))
	}
	if narg > 0 {
		return errors.New("--export does not accept positional arguments")
	}
	return nil
}

// exportJournal writes a session journal to stdout. Without redactOutput the
// source bytes are copied verbatim, so unknown fields, blank lines, and a
// truncated final line survive untouched. With redactOutput every event is
// re-encoded through the same redaction and encoding path used by the live
// journal and --json. The source file is only ever opened for reading.
func exportJournal(path string, redactOutput bool, stdout, stderr io.Writer) error {
	if !redactOutput {
		if stderr != nil {
			_, _ = io.WriteString(stderr, rawExportWarning)
		}
		return copyJournalBytes(path, stdout)
	}

	events, err := store.Read(path)
	if err != nil {
		return err
	}
	sink := jsonlStdout{w: stdout, redact: redactJournalEvent}
	for _, e := range events {
		if err := sink.Emit(e); err != nil {
			return err
		}
	}
	return nil
}

// copyJournalBytes streams the journal to stdout without parsing it.
func copyJournalBytes(path string, stdout io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(stdout, f)
	return err
}
