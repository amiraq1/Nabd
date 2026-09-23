package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/redact"
	"nabd/internal/store"
)

// chunkProvider streams each string as its own text chunk, then finishes with a
// stop — or, when after is set, triggers cancellation and reports it.
type chunkProvider struct {
	name   string
	chunks []string
	after  func()
}

func (p *chunkProvider) Name() string {
	if p.name == "" {
		return "chunky"
	}
	return p.name
}

func (p *chunkProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(p.chunks)+2)
	go func() {
		defer close(ch)
		for _, c := range p.chunks {
			ch <- provider.Chunk{Kind: provider.ChunkText, Text: c}
		}
		if p.after != nil {
			p.after()
			<-ctx.Done()
			ch <- provider.Chunk{Kind: provider.ChunkError, Err: ctx.Err()}
			return
		}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

func readOnlyJournal(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
	}
	t.Fatal("no journal was written")
	return ""
}

func setUpProject(t *testing.T) {
	t.Helper()
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)
}

// TestJournalRedactsSecretSplitAcrossDeltas proves a key streamed across two and
// then three chunks is redacted in the journal, not stored piecewise.
func TestJournalRedactsSecretSplitAcrossDeltas(t *testing.T) {
	setUpProject(t)
	sess := t.TempDir()
	const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"

	var stdout, stderr bytes.Buffer
	code := runHeadless(headlessConfig{
		prompt: "go", mode: perm.ModeDeny, sessDir: sess,
		stdout: &stdout, stderr: &stderr,
		provider: &chunkProvider{chunks: []string{
			"before " + secret[:13],
			secret[13:26],
			secret[26:] + " after",
		}},
	})
	if code != exitSettled {
		t.Fatalf("exit %d stderr=%q", code, stderr.String())
	}
	journal := readOnlyJournal(t, sess)
	if strings.Contains(journal, secret) {
		t.Fatalf("secret leaked into the journal:\n%s", journal)
	}
	if !strings.Contains(journal, redact.Token) {
		t.Fatalf("journal has no redaction token:\n%s", journal)
	}
}

// TestHeadlessJSONRedactsSplitSecret is the same for --json stdout.
func TestHeadlessJSONRedactsSplitSecret(t *testing.T) {
	setUpProject(t)
	sess := t.TempDir()
	const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"

	var stdout, stderr bytes.Buffer
	code := runHeadless(headlessConfig{
		prompt: "go", json: true, mode: perm.ModeDeny, sessDir: sess,
		stdout: &stdout, stderr: &stderr,
		provider: &chunkProvider{chunks: []string{
			"before " + secret[:13],
			secret[13:26],
			secret[26:] + " after",
		}},
	})
	if code != exitSettled {
		t.Fatalf("exit %d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, secret) {
		t.Fatalf("secret leaked into --json output:\n%s", out)
	}
	if !strings.Contains(out, redact.Token) {
		t.Fatalf("--json output has no redaction token:\n%s", out)
	}
}

// TestStreamFlushedOnInterrupt cancels mid-secret through the
// headlessInterruptContext seam and proves the held bytes are flushed redacted,
// with the non-secret text before them preserved.
func TestStreamFlushedOnInterrupt(t *testing.T) {
	setUpProject(t)
	sess := t.TempDir()
	const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"

	interrupt := make(chan struct{})
	restore := headlessInterruptContext
	headlessInterruptContext = func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-interrupt
			cancel()
		}()
		return ctx, cancel
	}
	t.Cleanup(func() { headlessInterruptContext = restore })

	var stdout, stderr bytes.Buffer
	code := runHeadless(headlessConfig{
		prompt: "go", mode: perm.ModeDeny, sessDir: sess,
		stdout: &stdout, stderr: &stderr,
		provider: &chunkProvider{
			chunks: []string{"hello ", secret},
			after:  func() { close(interrupt) },
		},
	})
	if code != exitInterrupted {
		t.Fatalf("exit %d, want %d", code, exitInterrupted)
	}
	journal := readOnlyJournal(t, sess)
	if strings.Contains(journal, secret) {
		t.Fatalf("secret fragment leaked on interrupt:\n%s", journal)
	}
	if !strings.Contains(journal, "hello ") {
		t.Fatalf("non-secret text before the secret was lost:\n%s", journal)
	}
	if !strings.Contains(journal, redact.Token) {
		t.Fatalf("no redaction token on interrupt:\n%s", journal)
	}
}

// TestReplayOldJournalUnchanged proves an old-format journal still replays and
// rewinds identically: Live follows the rewind branch to the corrected turn.
func TestReplayOldJournalUnchanged(t *testing.T) {
	evs, err := store.Read("../../testdata/replay-corpus/rewind.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	branch := agent.Live(evs)

	var texts []string
	for _, e := range branch {
		if e.Type == agent.TextDelta {
			texts = append(texts, e.Text)
		}
	}
	joined := strings.Join(texts, "|")
	if !strings.Contains(joined, "result 2") || strings.Contains(joined, "result 1") {
		t.Fatalf("replay branch changed: %v", texts)
	}

	found := false
	for _, m := range agent.Messages(branch) {
		if strings.Contains(m.Text, "corrected command") {
			found = true
		}
	}
	if !found {
		t.Fatalf("rewind branch lost the corrected command")
	}
}
