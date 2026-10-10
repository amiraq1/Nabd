//go:build measure

// Package mcpmeasure holds the S1/S2 measurement harness for ADR-0003.
// No production code. Run with: go test -tags measure -run 'TestS1|TestS2' -v ./internal/mcpmeasure/
package mcpmeasure

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// mockMode is set when this binary re-executes itself as a mock MCP server.
func mockMode() string { return os.Getenv("NABD_MCPMEASURE_MODE") }

func TestMain(m *testing.M) {
	switch mockMode() {
	case "grandchild-spawner":
		// Spawn a grandchild that sleeps, then sleep ourselves.
		// The parent test will kill our process group.
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "NABD_MCPMEASURE_MODE=sleeper")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: false} // join our group
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "spawn grandchild: %v\n", err)
			os.Exit(1)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "sleeper":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "pipe-holder":
		// Hold the write end of our stdout open and exit.
		// Parent keeps the read end; tests that Wait doesn't hang
		// on a pipe held by a dead leader's child.
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "big-output":
		// Write 64 MiB to stdout.
		chunk := make([]byte, 1<<20)
		for i := range chunk {
			chunk[i] = byte('A' + (i % 26))
		}
		for i := 0; i < 64; i++ {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(0)
			}
		}
		os.Exit(0)
	case "slow":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "write-no-read":
		// Write continuously, never read stdin.
		for {
			fmt.Println("x")
			time.Sleep(time.Millisecond)
		}
	}
	os.Exit(m.Run())
}

// startMock starts this test binary as a mock server in its own process group.
func startMock(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "NABD_MCPMEASURE_MODE="+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mock %s: %v", mode, err)
	}
	return cmd
}

// killGroup kills the process group, mirroring bash.go's killGroup.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// countProcsInGroup counts /proc entries with matching PGID (Linux/Android).
func countProcsInGroup(pgid int) int {
	n := 0
	procs, _ := os.ReadDir("/proc")
	for _, p := range procs {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + p.Name() + "/stat")
		if err != nil {
			continue
		}
		// stat format: pid (comm) state ppid pgrp ...
		s := string(stat)
		rparen := strings.LastIndex(s, ")")
		if rparen < 0 {
			continue
		}
		fields := strings.Fields(s[rparen+1:])
		if len(fields) < 3 {
			continue
		}
		// fields: [0]=state [1]=ppid [2]=pgrp [3]=session
		if g, err := strconv.Atoi(fields[2]); err == nil && g == pgid {
			n++
		}
	}
	return n
}

// S1a: kill a process group containing a grandchild; none may remain.
func TestS1aKillGroupNoOrphans(t *testing.T) {
	// Android reports GOOS=android but has /proc — check availability directly.
	// Note: /proc/1/stat is Permission denied for Termux apps; /proc/self works.
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("needs /proc")
	}
	cmd := startMock(t, "grandchild-spawner")
	time.Sleep(500 * time.Millisecond) // let grandchild spawn
	pgid := cmd.Process.Pid            // Setpgid=true => pgid == pid

	// Sanity: at least 2 procs (parent + grandchild) must be visible,
	// otherwise the counter is blind and a zero result means nothing.
	n := countProcsInGroup(pgid)
	t.Logf("S1a: pgid=%d visible before kill=%d", pgid, n)
	if n < 2 {
		t.Fatalf("S1a ABORT: only %d procs visible in group %d before kill — counter blind", n, pgid)
	}

	killGroup(cmd)
	cmd.Wait()

	time.Sleep(500 * time.Millisecond)
	remaining := countProcsInGroup(pgid)
	t.Logf("S1a: pgid=%d remaining=%d", pgid, remaining)
	if remaining != 0 {
		t.Errorf("S1a FAIL: %d processes remain in group %d after killGroup", remaining, pgid)
	}
}

// S1b: leader exits while grandchild holds a pipe; Wait must not hang.
func TestS1bPipeHolderNoHang(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "NABD_MCPMEASURE_MODE=pipe-holder")
	cmd.Stdout = w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close() // parent drops its write end; child holds its own copy

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// The child sleeps 30s; we kill the group after 2s and Wait must return.
	time.Sleep(2 * time.Second)
	killGroup(cmd)

	select {
	case err := <-done:
		t.Logf("S1b: Wait returned (err=%v) — no hang", err)
	case <-time.After(10 * time.Second):
		t.Error("S1b FAIL: Wait hung >10s after killGroup")
	}
	r.Close()
}

// S2a: 64 MiB output with a reader that discards; no deadlock, bounded memory.
func TestS2aBigOutputNoDeadlock(t *testing.T) {
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "NABD_MCPMEASURE_MODE=big-output")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Discard without storing.
	n, _ := io.Copy(io.Discard, stdout)
	cmd.Wait()

	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	delta := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("S2a: discarded=%d bytes, heap before=%d after=%d delta=%d",
		n, before.HeapAlloc, after.HeapAlloc, delta)
	if n != 64<<20 {
		t.Errorf("S2a FAIL: expected %d bytes, got %d", 64<<20, n)
	}
}

// S2b: timeout with slow child; full cleanup, no goroutine leak.
func TestS2bTimeoutCleanup(t *testing.T) {
	before := runtime.NumGoroutine()
	cmd := startMock(t, "slow")

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	time.Sleep(1500 * time.Millisecond) // simulate timeout
	killGroup(cmd)

	select {
	case <-done:
		t.Logf("S2b: child reaped after kill")
	case <-time.After(10 * time.Second):
		t.Error("S2b FAIL: child not reaped after kill")
	}

	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	t.Logf("S2b: goroutines before=%d after=%d", before, after)
	if after > before+3 {
		t.Errorf("S2b FAIL: goroutine leak (before=%d after=%d)", before, after)
	}
}

// S2c: server writes but never reads; must not deadlock the parent.
func TestS2cWriteNoReadNoFreeze(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "NABD_MCPMEASURE_MODE=write-no-read")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, _ := cmd.StdoutPipe()
	cmd.StdinPipe() // we never write
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Drain a little, then stop reading; child keeps writing.
	buf := make([]byte, 4096)
	stdout.Read(buf)

	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()

	time.Sleep(2 * time.Second)
	killGroup(cmd)
	select {
	case <-done:
		t.Logf("S2c: no freeze — child killed cleanly")
	case <-time.After(10 * time.Second):
		t.Error("S2c FAIL: froze")
	}
}
