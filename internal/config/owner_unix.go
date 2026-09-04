//go:build !windows
// +build !windows

package config

import (
	"fmt"
	"os"
	"os/user"
	"syscall"
)

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
		return fmt.Errorf("%s: يملكه uid %d ولا يملكه %s — شغّل: chown %s %s", p, stat.Uid, who, who, p)
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
