//go:build windows

package tools

import (
	"os"
	"os/exec"
	"strconv"
)

// configureProcessTree kills the whole process tree on Windows. Windows has no
// Unix-style process groups, so taskkill /T is used as a best effort.
func configureProcessTree(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if err := kill.Run(); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
