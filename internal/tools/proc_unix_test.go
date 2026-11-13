//go:build unix

package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessGroupKill(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "sh", pidFile)
	configureProcessTree(cmd)
	cmd.WaitDelay = 2 * time.Second
	_ = cmd.Run()

	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("expected deadline, got %v", ctx.Err())
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child pid was not written: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err == syscall.ESRCH {
			return
		}
		if err != nil && err != syscall.EPERM {
			t.Fatalf("kill(0, %d): %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived the process-group kill", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
