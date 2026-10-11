//go:build windows

package mcp

import (
	"errors"
	"syscall"
)

// Server processes are not supported on Windows: there is no Setpgid
// equivalent with the same group-kill semantics, and silent orphans would
// result. Refuse clearly rather than run degraded (mirrors secure_windows.go).
func platformCheck() error {
	return errors.New("mcp: server processes are not supported on windows")
}

func mcpSysProcAttr() *syscall.SysProcAttr { return nil }

func mcpKillGroup(pgid int) {}

func mcpGroupAlive(pgid int) bool { return false }
