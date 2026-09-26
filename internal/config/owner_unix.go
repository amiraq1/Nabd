//go:build !windows
// +build !windows

package config

import (
	"os"
	"os/user"
	"syscall"
)

// OwnerMismatchError reports a config file that exists but is owned by another
// uid. It carries the facts, not the sentence: the Arabic wording is rendered at
// the CLI boundary (cmd/ag), so this package stays on the ASCII baseline. See
// ADR-0002 (docs/DECISIONS/0002-user-facing-language.md).
type OwnerMismatchError struct {
	// Path is the config file that was refused.
	Path string
	// OwnerUID is the uid that owns the file on disk.
	OwnerUID int
	// Who describes the current user, and doubles as the chown argument.
	Who string
}

// Error is the ASCII baseline. cmd/ag renders the Arabic sentence for this type;
// nothing in this package decides which language the user reads.
func (e *OwnerMismatchError) Error() string {
	return e.Path + ": owned by uid " + itoa(e.OwnerUID) +
		", not by " + e.Who + " - run: chown " + e.Who + " " + e.Path
}

// checkOwnerPlatform (Unix) refuses the config file unless it is owned by the
// current user. A key you do not own is a key you cannot protect.
func checkOwnerPlatform(p string, fi os.FileInfo) error {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil // cannot read owner on this filesystem; skip rather than block
	}
	uid := os.Getuid()
	if int(stat.Uid) != uid {
		cur, err := user.Current()
		who := "current user"
		if err == nil {
			who = cur.Username + " (uid " + itoa(uid) + ")"
		}
		return &OwnerMismatchError{Path: p, OwnerUID: int(stat.Uid), Who: who}
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
