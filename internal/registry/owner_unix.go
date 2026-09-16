//go:build !windows

package registry

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

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
		return fmt.Errorf("%s: يملكه uid %d ولا يملكه %s — شغّل: chown %s %s", p, stat.Uid, who, who, p)
	}
	return nil
}
