//go:build unix

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcessTree puts the child in its own process group and kills the
// whole group when the context is canceled, so grandchildren cannot leak.
func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
}
