package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/redact"
	"nabd/internal/snap"
	"nabd/internal/store"
	"nabd/internal/tools"
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

func journalPath(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatal("no journal was written")
	return ""
}

func readOnlyJournal(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(journalPath(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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

// multiChunkProvider streams each turn as several text chunks, then either a
// tool call (so a later turn and a branch point exist) or a clean stop.
type multiTurn struct {
	chunks []string
	call   *provider.ToolCall
}

type multiChunkProvider struct {
	turns []multiTurn
	i     int
}

func (p *multiChunkProvider) Name() string { return "multichunk" }

func (p *multiChunkProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	if p.i >= len(p.turns) {
		return nil, errors.New("no more scripted turns")
	}
	tn := p.turns[p.i]
	p.i++
	ch := make(chan provider.Chunk, len(tn.chunks)+4)
	go func() {
		defer close(ch)
		for _, c := range tn.chunks {
			ch <- provider.Chunk{Kind: provider.ChunkText, Text: c}
		}
		if tn.call != nil {
			ch <- provider.Chunk{Kind: provider.ChunkToolCall, Call: tn.call}
			ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "tool_use"}
			return
		}
		ch <- provider.Chunk{Kind: provider.ChunkStop, Stop: "end_turn"}
	}()
	return ch, nil
}

type noopSink struct{}

func (noopSink) Emit(agent.Event) error { return nil }

// turnTexts splits a live branch into the concatenated TextDelta text of each
// turn, so a held delta that lands in the wrong turn is visible.
func turnTexts(branch []agent.Event) []string {
	var turns []string
	var cur strings.Builder
	open := false
	for _, e := range branch {
		switch e.Type {
		case agent.TurnStart:
			cur.Reset()
			open = true
		case agent.TextDelta:
			cur.WriteString(e.Text)
		case agent.TurnEnd:
			if open {
				turns = append(turns, cur.String())
				open = false
			}
		}
	}
	return turns
}

// TestRewindNewJournalWithHeldDeltas writes a new journal whose stream redactor
// holds back and drops deltas (a secret split across three chunks), then proves
// the journal is a valid tree: unique strictly-increasing seqs, every Parent
// resolves, agent.Live returns the whole chain, the redacted text lands in the
// right turns, and a rewind on the replayed session succeeds.
func TestRewindNewJournalWithHeldDeltas(t *testing.T) {
	setUpProject(t)
	sess := t.TempDir()
	const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"

	prov := &multiChunkProvider{turns: []multiTurn{
		{chunks: []string{"intro ", secret[:13], secret[13:26], secret[26:], " outro"}, call: writeCall("x.txt", "hi")},
		{chunks: []string{"done"}},
	}}

	var stdout, stderr bytes.Buffer
	code := runHeadless(headlessConfig{
		prompt: "go", mode: perm.ModeDeny, sessDir: sess,
		stdout: &stdout, stderr: &stderr,
		provider: prov,
	})
	if code != exitSettled {
		t.Fatalf("exit %d stderr=%q", code, stderr.String())
	}

	evs, err := store.Read(journalPath(t, sess))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("journal is empty")
	}

	// Unique and strictly increasing seqs.
	seen := map[int]bool{}
	for i, e := range evs {
		if seen[e.Seq] {
			t.Fatalf("duplicate seq %d", e.Seq)
		}
		seen[e.Seq] = true
		if i > 0 && e.Seq <= evs[i-1].Seq {
			t.Fatalf("seq not strictly increasing: %d then %d", evs[i-1].Seq, e.Seq)
		}
	}
	// Every Parent resolves (0 is root).
	for _, e := range evs {
		if e.Parent != 0 && !seen[e.Parent] {
			t.Fatalf("seq %d has dangling parent %d", e.Seq, e.Parent)
		}
	}
	// Live follows the whole chain, not truncated at a held delta.
	branch := agent.Live(evs)
	if len(branch) != len(evs) {
		t.Fatalf("Live returned %d of %d events: chain truncated at a held delta", len(branch), len(evs))
	}
	if branch[0].Type != agent.RunStart {
		t.Fatalf("Live chain does not start at RunStart: %s", branch[0].Type)
	}

	// The redacted text is intact and each turn keeps its own text.
	turns := turnTexts(branch)
	if len(turns) < 2 {
		t.Fatalf("expected at least 2 turns, got %d: %q", len(turns), turns)
	}
	if strings.Contains(strings.Join(turns, ""), secret) {
		t.Fatalf("secret leaked into the journal: %q", turns)
	}
	if got := turns[0]; !strings.Contains(got, "intro ") || !strings.Contains(got, redact.Token) || !strings.HasSuffix(got, "outro") {
		t.Fatalf("turn 1 text is wrong (held tail lost or misplaced): %q", got)
	}
	if got := turns[len(turns)-1]; got != "done" {
		t.Fatalf("last turn text = %q, want %q", got, "done")
	}

	// A rewind on the replayed session succeeds.
	loop := newSessionLoop(nil, nil, nil, nil)
	loop.Sink = noopSink{}
	loop.Seed(evs)
	if _, err := loop.Rewind(1); err != nil {
		t.Fatalf("rewind on the replayed journal failed: %v", err)
	}
}

// TestLiveRewindThenContinueKeepsJournalChain runs a live session, streams a
// split secret, runs a tool call, rewinds the LIVE loop to the branch point,
// runs another turn, and then reads the journal. Every Parent must resolve,
// agent.Live must return the whole post-rewind branch, and the journal and the
// in-memory history must agree that the branch point is the Rewind event.
func TestLiveRewindThenContinueKeepsJournalChain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	root, err := tools.NewRoot("")
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(root, sh)
	pol := perm.New(reg)
	pol.SetMode(perm.ModeDeny)

	path := filepath.Join(t.TempDir(), "s.jsonl")
	journal, err := store.NewJSONLWithOptions(path, journalStoreOptions())
	if err != nil {
		t.Fatal(err)
	}

	const secret = "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"
	loop := newSessionLoop(&multiChunkProvider{turns: []multiTurn{
		{chunks: []string{"turn one ", secret[:13], secret[13:26], secret[26:], " end"}, call: writeCall("x.txt", "hi")},
		{chunks: []string{"turn two"}},
		{chunks: []string{"turn three"}},
	}}, reg, gate{pol}, silentAsker{})
	loop.Sink = newStreamRedactSink(agent.Fanout{journal})

	if err := loop.Start("t", root.Dir()); err != nil {
		t.Fatal(err)
	}
	if err := loop.Run(context.Background(), "one"); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if _, err := loop.Rewind(1); err != nil {
		t.Fatalf("live rewind: %v", err)
	}
	if err := loop.Run(context.Background(), "two"); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	_ = loop.End("done")
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	evs, err := store.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]bool{}
	for _, e := range evs {
		if seen[e.Seq] {
			t.Fatalf("duplicate seq %d", e.Seq)
		}
		seen[e.Seq] = true
	}
	for _, e := range evs {
		if e.Parent != 0 && !seen[e.Parent] {
			t.Fatalf("seq %d has dangling parent %d", e.Seq, e.Parent)
		}
	}
	branch := agent.Live(evs)
	if len(branch) == 0 || branch[0].Seq != 1 {
		t.Fatalf("Live branch does not start at the root: %+v", branch)
	}

	journalRewinds, histRewinds := 0, 0
	for _, e := range branch {
		if e.Type == agent.Rewind {
			journalRewinds++
		}
	}
	for _, e := range agent.Live(loop.Hist()) {
		if e.Type == agent.Rewind {
			histRewinds++
		}
	}
	if journalRewinds != 1 || histRewinds != 1 {
		t.Fatalf("branch point disagrees: journal rewinds=%d history rewinds=%d, want 1 each", journalRewinds, histRewinds)
	}
}
