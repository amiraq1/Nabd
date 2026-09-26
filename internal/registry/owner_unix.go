//go:build !windows

package registry

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// OwnerMismatchError reports a registry file that exists but is owned by another
// uid. Same shape as config.OwnerMismatchError: the facts travel on the error and
// the Arabic sentence is rendered at the CLI boundary (cmd/ag), so this package
// stays on the ASCII baseline. See ADR-0002.
type OwnerMismatchError struct {
	// Path is the registry file that was refused.
	Path string
	// OwnerUID is the uid that owns the file on disk.
	OwnerUID int
	// Who describes the current user, and doubles as the chown argument.
	Who string
}

// Error is the ASCII baseline. cmd/ag renders the Arabic sentence for this type.
func (e *OwnerMismatchError) Error() string {
	return e.Path + ": owned by uid " + strconv.Itoa(e.OwnerUID) +
		", not by " + e.Who + " - run: chown " + e.Who + " " + e.Path
}

func checkOwner(p string, fi os.FileInfo) error {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	uid := os.Getuid()
	if int(stat.Uid) != uid {
		cur, err := user.Current()
		who := "current user"
		if err == nil {
			who = cur.Username + " (uid " + strconv.Itoa(uid) + ")"
		}
		return &OwnerMismatchError{Path: p, OwnerUID: int(stat.Uid), Who: who}
	}
	return nil
}
