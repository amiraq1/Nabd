package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

var (
	orderTestBinaryOnce sync.Once
	orderTestBinaryPath string
	orderTestBinaryErr  error
)

func getOrderTestBinary(t *testing.T) string {
	t.Helper()
	orderTestBinaryOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "nabd_order_bin_*")
		if err != nil {
			orderTestBinaryErr = err
			return
		}
		binPath := filepath.Join(tmpDir, "nabd")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/ag/")
		cmd.Dir = "../.."
		if out, err := cmd.CombinedOutput(); err != nil {
			orderTestBinaryErr = fmt.Errorf("building binary: %v\n%s", err, string(out))
			return
		}
		orderTestBinaryPath = binPath
	})
	if orderTestBinaryErr != nil {
		t.Fatalf("failed to build binary: %v", orderTestBinaryErr)
	}
	return orderTestBinaryPath
}

// TestReplayPrintTickOrderingRepetition runs a known race-sensitive scenario 50 times
// under a real PTY and asserts that the fingerprint is byte-identical across all 50 runs.
//
// Root cause & historical defect:
// In replay.go:63, step() previously returned tea.Batch(cmds...) with tea.Println
// and tea.Tick(1ms). When the 1ms tick fired before the concurrent Println was delivered,
// advance() ran and emitted the next Println first, causing adjacent lines to swap order.
//
// Recorded pre-fix failure rate: ~13% over 50 runs under PTY on ARM64.
// With tea.Sequence(cmds...), the tick only starts after print delivery, making the
// failure rate strictly 0%.
func TestReplayPrintTickOrderingRepetition(t *testing.T) {
	if testing.Short() && os.Getenv("REPETITION") != "1" {
		t.Skip("skipping 50-run repetition test in -short mode; set REPETITION=1 to force")
	}

	binPath := getOrderTestBinary(t)
	journalPath := filepath.Join("../../testdata/replay-corpus", "perm_decision.jsonl")
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatalf("missing scenario fixture %s: %v", journalPath, err)
	}

	const iterations = 50
	var firstHash string
	var firstOutput string

	for i := 0; i < iterations; i++ {
		cmd := exec.Command(binPath, "-replay", journalPath, "-speed", "0")
		cmd.Env = []string{
			"TERM=xterm-256color",
			"HOME=/corpus/home",
			"TZ=UTC",
			"LANG=C.UTF-8",
			"LC_ALL=C.UTF-8",
			"PATH=" + os.Getenv("PATH"),
		}

		master, slave, err := pty.Open()
		if err != nil {
			t.Fatalf("iteration %d: pty.Open: %v", i, err)
		}
		_ = pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 50})

		cmd.Stdin = slave
		cmd.Stdout = slave
		cmd.Stderr = slave

		if err := cmd.Start(); err != nil {
			_ = master.Close()
			_ = slave.Close()
			t.Fatalf("iteration %d: cmd.Start: %v", i, err)
		}
		_ = slave.Close()

		var buf bytes.Buffer
		doneCopy := make(chan struct{})
		go func() {
			_, _ = io.Copy(&buf, master)
			close(doneCopy)
		}()

		_ = cmd.Wait()
		_ = master.Close()
		<-doneCopy

		raw := buf.Bytes()

		// Interpret rows via vt10x emulator screen
		vt := vt10x.New(vt10x.WithSize(50, 24))
		_, _ = vt.Write(raw)

		var rows []string
		for y := 0; y < 24; y++ {
			var line strings.Builder
			for x := 0; x < 50; x++ {
				c := vt.Cell(x, y)
				if c.Char != 0 {
					line.WriteRune(c.Char)
				} else {
					line.WriteByte(' ')
				}
			}
			trimmed := strings.TrimRight(ansi.Strip(line.String()), " ")
			if trimmed != "" && !strings.HasPrefix(trimmed, "replay ") {
				rows = append(rows, trimmed)
			}
		}
		gotOutput := strings.Join(rows, "\n")
		h := sha256.Sum256([]byte(gotOutput))
		gotHash := hex.EncodeToString(h[:])

		if i == 0 {
			firstHash = gotHash
			firstOutput = gotOutput
			continue
		}

		if gotHash != firstHash {
			// Write artifact for post-mortem debugging
			artifactFile := filepath.Join(t.TempDir(), fmt.Sprintf("flaky_run_%d.bin", i))
			_ = os.WriteFile(artifactFile, raw, 0644)
			t.Fatalf("run %d/%d fingerprint mismatch!\nGot hash: %s\nWant hash: %s\nRaw byte stream saved to: %s\n--- GOT ---\n%s\n--- WANT ---\n%s",
				i+1, iterations, gotHash, firstHash, artifactFile, gotOutput, firstOutput)
		}
	}

	t.Logf("PASS: %d/%d consecutive runs produced identical fingerprint %s", iterations, iterations, firstHash)
}
