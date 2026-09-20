package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// HelperCommand is an internal-only command handled by cmd/ag before the
// public flag parser. It exists so the child can install a kernel sandbox
// before replacing itself with the requested shell.
const HelperCommand = "__bash-sandbox"

// Config describes the filesystem boundary for one Bash child.
//
// Root and Writable are granted all filesystem rights supported by the
// running kernel. ReadOnly is granted only execute, directory-read, and
// file-read rights. The caller must include every temporary directory it
// creates for the child in Writable.
type Config struct {
	Root     string
	Writable []string
	ReadOnly []string
}

// HelperPath returns the current executable when it can serve as the
// sandbox-installing helper. Go test binaries deliberately opt out: they do
// not contain cmd/ag's internal command dispatcher.
func HelperPath() (string, bool) {
	if runtime.GOOS != "linux" {
		return "", false
	}
	exe, err := os.Executable()
	if err != nil || strings.HasSuffix(filepath.Base(exe), ".test") {
		return "", false
	}
	return exe, true
}