package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nabd/internal/perm"
	"nabd/internal/provider"
)

// blockingProvider signals readiness by creating ready, then blocks until the
// run's context is cancelled, modeling an in-flight turn that a SIGINT cuts.
type blockingProvider struct{ ready string }

func (p *blockingProvider) Name() string { return "blocker" }

func (p *blockingProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	if p.ready != "" {
		_ = os.WriteFile(p.ready, []byte("1"), 0o600)
	}
	ch := make(chan provider.Chunk, 1)
	go func() {
		defer close(ch)
		<-ctx.Done()
		ch <- provider.Chunk{Kind: provider.ChunkError, Err: ctx.Err()}
	}()
	return ch, nil
}

// TestHeadlessRealSIGINTSubprocess proves a genuine SIGINT delivered to a
// headless run — not the headlessInterruptContext seam — exits 130 and records
// the stopped session status. It re-executes the test binary as a child so the
// parent can signal the child only, never itself.
func TestHeadlessRealSIGINTSubprocess(t *testing.T) {
	if os.Getenv("NABD_SIGINT_CHILD") == "1" {
		code := runHeadless(headlessConfig{
			prompt:   "block",
			mode:     perm.ModeDeny,
			sessDir:  os.Getenv("NABD_SIGINT_SESSDIR"),
			stdout:   io.Discard,
			stderr:   io.Discard,
			provider: &blockingProvider{ready: os.Getenv("NABD_SIGINT_READY")},
		})
		os.Exit(code)
	}
	if runtime.GOOS == "windows" {
		t.Skip("cannot deliver a signal to a child process on " + runtime.GOOS)
	}

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sess := t.TempDir()
	ready := filepath.Join(t.TempDir(), "ready")

	cmd := exec.Command(os.Args[0], "-test.run=TestHeadlessRealSIGINTSubprocess", "-test.v")
	cmd.Dir = proj
	cmd.Env = append(os.Environ(),
		"NABD_SIGINT_CHILD=1",
		"NABD_SIGINT_SESSDIR="+sess,
		"NABD_SIGINT_READY="+ready,
	)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("child never reached its blocking turn:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("signal child: %v", err)
	}
	err := cmd.Wait()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != exitInterrupted {
		t.Fatalf("child exit = %v, want %d; output:\n%s", err, exitInterrupted, out.String())
	}
	journal := readOnlyJournal(t, sess)
	if !strings.Contains(journal, "أوقفت الجلسة") {
		t.Fatalf("journal run_end does not carry the stopped status:\n%s", journal)
	}
}
