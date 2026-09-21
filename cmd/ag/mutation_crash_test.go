package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/snap"
	"nabd/internal/store"
	"nabd/internal/tools"
)

const (
	crashBefore   = "before\n"
	crashAfter    = "after\n"
	crashConflict = "human change\n"
)

func crashHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestMutationCrashRecoveryMatrix starts a real child test process, lets it
// durably append edit_intent, kills it before it can close the mutation, and
// then exercises the same reconciliation path used by --continue.
func TestMutationCrashRecoveryMatrix(t *testing.T) {
	tests := []struct {
		name  string
		phase string
		want  tools.MutationRecoveryState
	}{
		{name: "crash before publish", phase: "not_published", want: tools.MutationNotPublished},
		{name: "crash after publish", phase: "published", want: tools.MutationPublished},
		{name: "crash with conflicting bytes", phase: "conflict", want: tools.MutationConflict},
		{name: "crash after target removal", phase: "missing", want: tools.MutationMissing},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rootDir := t.TempDir()
			journalPath := filepath.Join(t.TempDir(), "crash.jsonl")
			readyPath := filepath.Join(t.TempDir(), "ready")
			target := filepath.Join(rootDir, "notes.txt")
			if err := os.WriteFile(target, []byte(crashBefore), 0o600); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=^TestMutationCrashHelper$")
			cmd.Env = append(os.Environ(),
				"NABD_MUTATION_CRASH_HELPER=1",
				"NABD_MUTATION_CRASH_PHASE="+tc.phase,
				"NABD_MUTATION_CRASH_ROOT="+rootDir,
				"NABD_MUTATION_CRASH_JOURNAL="+journalPath,
				"NABD_MUTATION_CRASH_READY="+readyPath,
			)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}

			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(readyPath); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					t.Fatalf("crash helper did not reach the kill point: %s", stderr.String())
				}
				time.Sleep(10 * time.Millisecond)
			}

			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()

			events, err := store.Read(journalPath)
			if err != nil {
				t.Fatalf("read crash journal: %v", err)
			}
			recs := unresolvedMutationRecords(events)
			if len(recs) != 1 {
				t.Fatalf("unresolved records=%d, want 1; events=%+v", len(recs), events)
			}
			if got := unresolvedMutationIntents(events); got != 1 {
				t.Fatalf("unresolved intents=%d, want 1", got)
			}

			root, err := tools.NewRoot(rootDir)
			if err != nil {
				t.Fatal(err)
			}
			sh, err := snap.New(root.Dir())
			if err != nil {
				t.Fatal(err)
			}
			reg := tools.NewRegistry(root, sh)
			got, err := reg.ReconcileMutation(recs[0])
			if err != nil {
				t.Fatalf("ReconcileMutation: %v", err)
			}
			if got != tc.want {
				t.Fatalf("state=%q, want %q", got, tc.want)
			}

			sink := &testNoticeSink{}
			loop := &agent.Loop{Sink: sink}
			noteMutationRecovery(loop, reg, events)
			if len(sink.events) != 1 || sink.events[0].Type != agent.Notice {
				t.Fatalf("recovery notice events=%+v, want one notice", sink.events)
			}
			for _, state := range []string{"not_published=0", "published=0", "missing=0", "conflict=0"} {
				if strings.Contains(sink.events[0].Text, state) && !strings.Contains(sink.events[0].Text, string(tc.want)+"=1") {
					t.Fatalf("notice=%q has unexpected zero state %q", sink.events[0].Text, state)
				}
			}
			if !strings.Contains(sink.events[0].Text, string(tc.want)+"=1") ||
				!strings.Contains(sink.events[0].Text, "no automatic replay") {
				t.Fatalf("notice=%q does not describe %s without replay", sink.events[0].Text, tc.want)
			}

			gotBytes, readErr := os.ReadFile(target)
			switch tc.want {
			case tools.MutationMissing:
				if !os.IsNotExist(readErr) {
					t.Fatalf("target read error=%v, want missing target", readErr)
				}
			default:
				if readErr != nil {
					t.Fatal(readErr)
				}
				wantBytes := crashBefore
				if tc.want == tools.MutationPublished {
					wantBytes = crashAfter
				} else if tc.want == tools.MutationConflict {
					wantBytes = crashConflict
				}
				if string(gotBytes) != wantBytes {
					t.Fatalf("target=%q, want %q; reconciliation must not write", gotBytes, wantBytes)
				}
			}
		})
	}
}

// TestMutationCrashHelper is the child process used by
// TestMutationCrashRecoveryMatrix. It is intentionally not a production
// command: the parent kills it after the intent is durable.
func TestMutationCrashHelper(t *testing.T) {
	if os.Getenv("NABD_MUTATION_CRASH_HELPER") != "1" {
		return
	}

	rootDir := os.Getenv("NABD_MUTATION_CRASH_ROOT")
	journalPath := os.Getenv("NABD_MUTATION_CRASH_JOURNAL")
	readyPath := os.Getenv("NABD_MUTATION_CRASH_READY")
	phase := os.Getenv("NABD_MUTATION_CRASH_PHASE")

	journal, err := store.NewJSONL(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	loop := &agent.Loop{Sink: journal}
	rec := &agent.EditRecord{
		MutationID: "crash-" + phase,
		Path:       "notes.txt",
		HashBefore: crashHash(crashBefore),
		HashAfter:  crashHash(crashAfter),
	}
	if err := loop.PrepareMutation(rec); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(rootDir, "notes.txt")
	switch phase {
	case "not_published":
	case "published":
		writeAndSyncCrashTarget(t, target, crashAfter)
	case "conflict":
		writeAndSyncCrashTarget(t, target, crashConflict)
	case "missing":
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown crash phase %q", phase)
	}
	if err := os.WriteFile(readyPath, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {}
}

func writeAndSyncCrashTarget(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
