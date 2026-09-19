package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExportsDirEnv overrides the directory report exports are written to. It
// exists for tests and for a caller that wants reports somewhere other than
// the default.
const ExportsDirEnv = "NABD_EXPORTS_DIR"

// exportsDir returns the directory oversized reports are written to:
// $NABD_EXPORTS_DIR when set, otherwise ~/.ag/exports.
func exportsDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv(ExportsDirEnv)); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ag", "exports"), nil
}

// ensurePrivateDir creates dir (and any missing ancestors) at mode 0700. An
// existing directory is left untouched: a caller-supplied directory belongs
// to the caller. MkdirAll's mode argument is masked by the umask, so the
// explicit Chmod is what makes the created directory exactly 0700.
func ensurePrivateDir(dir string) error {
	switch _, err := os.Stat(dir); {
	case err == nil:
		return nil
	case !os.IsNotExist(err):
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

// writePrivateReport writes body to a new timestamped file under dir and
// returns its path. The directory is created 0700 and the file is created
// 0600 with an explicit Chmod, so neither can be widened by the umask.
//
// body is a redactedText, which only redactForExport can produce: this
// function performs no redaction of its own and never reads a session journal,
// and the named type makes passing raw text a compile error rather than a
// silent leak.
func writePrivateReport(dir string, body redactedText) (string, error) {
	if err := ensurePrivateDir(dir); err != nil {
		return "", err
	}
	name := "report-" + time.Now().UTC().Format("20060102-150405.000000000") + ".txt"
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(string(body)); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// saveReport writes body to the export directory. Every path that would
// otherwise lose the user's text goes through here: an oversized report, or a
// clipboard command that could not run at all.
func saveReport(body redactedText) (string, error) {
	dir, err := exportsDir()
	if err != nil {
		return "", err
	}
	return writePrivateReport(dir, body)
}

// reportSavedSuffix exports body and returns the status-line suffix naming the
// file it landed in. It returns "" when there is nothing to save or the export
// itself failed, so a caller can append it unconditionally without claiming a
// save that did not happen.
func reportSavedSuffix(body redactedText) string {
	if strings.TrimSpace(string(body)) == "" {
		return ""
	}
	path, err := saveReport(body)
	if err != nil {
		return ""
	}
	return copyFullReportSaved + path
}
