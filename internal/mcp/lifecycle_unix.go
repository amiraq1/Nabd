//go:build !windows

package mcp

import (
	"syscall"
	"time"
)

func platformCheck() error { return nil }

func mcpSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// mcpKillGroup mirrors killGroup (internal/tools/bash.go:186): SIGTERM, a
// short grace, then SIGKILL. It addresses -pgid, never the bare pid: the
// leader is rarely the process that hangs, and killing only the leader
// orphans whatever it spawned. pgid <= 1 is a no-op guard.
func mcpKillGroup(pgid int) {
	if pgid <= 1 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(mcpTermGrace)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) != nil {
			return // group is gone
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// mcpGroupAlive reports whether any process still holds pgid.
func mcpGroupAlive(pgid int) bool {
	return pgid > 1 && syscall.Kill(-pgid, 0) == nil
}
