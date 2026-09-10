package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/payload"
	"nabd/internal/provider"
	"nabd/internal/snap"
)

// NBD-420: every rule is justified by a measured round saved.
//
// A malformed call costs a whole round: the request goes out, an error comes
// back, and a second request fixes it. The round is the dominant term — NBD-401
// measured a 5.00x request-count spread against 2.85x for history — so the
// gate here is requests, and a rule that saves none is deleted.
//
// The harness is NBD-401's: a real Loop with a scripted provider, no network,
// no keys. The provider behaves like a model that reads its error and corrects
// itself: it emits the malformed call, and after an errored result it emits the
// correct one. Each fixture runs twice, repair disabled and enabled.
//
// cumulative_in is reported for both runs but is not the gate: its unit is the
// estimator (EstimateText, chars/4 for ASCII), not a tokenizer, so it describes
// direction and not amount.
//
// Reproduce: go test ./internal/tools -run TestRepairRulesSaveARound -count=1 -v

// repairRoundProvider is a model that corrects itself after an error.
type repairRoundProvider struct {
	first    provider.ToolCall // emitted on the first turn
	correct  provider.ToolCall // emitted once a result came back as an error
	requests int
	msgs     [][]provider.Message
}

func (p *repairRoundProvider) Name() string { return "repair-round-counter" }

func (p *repairRoundProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.requests++
	ms := make([]provider.Message, len(req.Messages))
	copy(ms, req.Messages)
	p.msgs = append(p.msgs, ms)

	var chunks []provider.Chunk
	switch {
	case !sawToolResult(ms):
		chunks = toolCallChunks(p.first)
	case lastResultErrored(ms):
		chunks = toolCallChunks(p.correct)
	default:
		chunks = []provider.Chunk{
			{Kind: provider.ChunkText, Text: "done"},
			{Kind: provider.ChunkStop, Stop: "end_turn"},
		}
	}

	ch := make(chan provider.Chunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func toolCallChunks(c provider.ToolCall) []provider.Chunk {
	call := c
	return []provider.Chunk{
		{Kind: provider.ChunkToolCall, Call: &call},
		{Kind: provider.ChunkStop, Stop: "tool_use"},
	}
}

func sawToolResult(ms []provider.Message) bool {
	for _, m := range ms {
		if len(m.ToolResults) > 0 {
			return true
		}
	}
	return false
}

// lastResultErrored reports whether the newest tool result was an error — the
// signal the modelled model reads before correcting itself.
func lastResultErrored(ms []provider.Message) bool {
	for i := len(ms) - 1; i >= 0; i-- {
		if len(ms[i].ToolResults) == 0 {
			continue
		}
		rs := ms[i].ToolResults
		return rs[len(rs)-1].IsErr
	}
	return false
}

func cumulativeInput(msgs [][]provider.Message) int {
	overhead := payload.CodeBudgetTokens()
	total := 0
	for _, ms := range msgs {
		total += overhead + agent.EstimateMessages(ms)
	}
	return total
}

// runRoundFixture drives the loop once with repair disabled or enabled.
func runRoundFixture(t *testing.T, repairOn bool, first, correct provider.ToolCall) (int, int) {
	t.Helper()
	dir := t.TempDir()
	reg := newFixtureRegistry(t, dir)
	reg.repairOff = !repairOn

	prov := &repairRoundProvider{first: first, correct: correct}
	sink := &recordingSink{}
	loop := &agent.Loop{
		Provider: prov,
		Tools:    reg,
		Sink:     sink,
		System:   payload.DefaultSystemPrompt,
		MaxTurns: 12,
		Gate:     allowReadsGate{},
		Budget:   agent.NewBudget(),
	}
	if err := loop.Start("repair-rounds", dir); err != nil {
		t.Fatalf("loop.Start: %v", err)
	}
	if err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatalf("loop.Run: %v", err)
	}
	return prov.requests, cumulativeInput(prov.msgs)
}

// newFixtureRegistry builds a registry over the given root holding a readable
// file, so a repaired read can actually succeed.
func newFixtureRegistry(t *testing.T, dir string) *Registry {
	t.Helper()
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		t.Fatal(err)
	}
	body := "1|package fixture\n2|// line two\n3|// line three\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewRegistry(root, sh)
}

// repairFixtures are the malformed calls, one per rule, each paired with the
// call the model would write after reading the error.
func repairFixtures() []struct {
	rule    string
	first   provider.ToolCall
	correct provider.ToolCall
} {
	obj := func(m map[string]any) json.RawMessage {
		b, _ := json.Marshal(m)
		return b
	}
	return []struct {
		rule    string
		first   provider.ToolCall
		correct provider.ToolCall
	}{
		{
			rule:    RuleToolAlias,
			first:   provider.ToolCall{ID: "c1", Name: "read", Input: obj(map[string]any{"path": "a.go"})},
			correct: provider.ToolCall{ID: "c2", Name: "read_file", Input: obj(map[string]any{"path": "a.go"})},
		},
		{
			rule:    RuleUnwrapString,
			first:   provider.ToolCall{ID: "c1", Name: "read_file", Input: json.RawMessage(`"{\"path\":\"a.go\"}"`)},
			correct: provider.ToolCall{ID: "c2", Name: "read_file", Input: obj(map[string]any{"path": "a.go"})},
		},
		{
			rule:    RuleUnwrapText,
			first:   provider.ToolCall{ID: "c1", Name: "read_file", Input: json.RawMessage(`here is the call {"path":"a.go"} end`)},
			correct: provider.ToolCall{ID: "c2", Name: "read_file", Input: obj(map[string]any{"path": "a.go"})},
		},
		{
			rule:    RuleFieldAlias,
			first:   provider.ToolCall{ID: "c1", Name: "read_file", Input: obj(map[string]any{"filename": "a.go"})},
			correct: provider.ToolCall{ID: "c2", Name: "read_file", Input: obj(map[string]any{"path": "a.go"})},
		},
		{
			rule:    RuleIntegerText,
			first:   provider.ToolCall{ID: "c1", Name: "read_file", Input: obj(map[string]any{"path": "a.go", "offset": "2"})},
			correct: provider.ToolCall{ID: "c2", Name: "read_file", Input: obj(map[string]any{"path": "a.go", "offset": 2})},
		},
		{
			rule:    RuleMarkdownPath,
			first:   provider.ToolCall{ID: "c1", Name: "read_file", Input: obj(map[string]any{"path": "[a.go](a.go)"})},
			correct: provider.ToolCall{ID: "c2", Name: "read_file", Input: obj(map[string]any{"path": "a.go"})},
		},
	}
}

// TestRepairRulesSaveARound is the acceptance gate: each rule must save at
// least one request in at least one fixture, or it is deleted.
func TestRepairRulesSaveARound(t *testing.T) {
	t.Logf("%-22s %9s %9s %11s %11s %8s", "rule", "req_off", "req_on", "cumul_off", "cumul_on", "saved")

	accepted := 0
	for _, fx := range repairFixtures() {
		fx := fx
		t.Run(fx.rule, func(t *testing.T) {
			reqsOff, cumulOff := runRoundFixture(t, false, fx.first, fx.correct)
			reqsOn, cumulOn := runRoundFixture(t, true, fx.first, fx.correct)

			saved := reqsOff - reqsOn
			t.Logf("%-22s %9d %9d %11d %11d %8d", fx.rule, reqsOff, reqsOn, cumulOff, cumulOn, saved)

			if reqsOn > reqsOff {
				t.Errorf("repair cost a round: %d requests with repair, %d without", reqsOn, reqsOff)
			}
			if saved < 1 {
				t.Errorf("rule %s saves no round (%d → %d requests): it must be deleted, not kept",
					fx.rule, reqsOff, reqsOn)
			}
			if cumulOn > cumulOff {
				t.Errorf("rule %s raised cumulative input: %d → %d", fx.rule, cumulOff, cumulOn)
			}
		})
		accepted++
	}
	if accepted == 0 {
		t.Fatal("no fixtures ran; the gate would pass vacuously")
	}
}

// TestRepairLeavesTheCleanPathIdentical measures the other half: a session of
// correct calls must be byte-for-byte the same cost with the layer enabled.
// A repair layer that changes anything on the clean path is rejected.
func TestRepairLeavesTheCleanPathIdentical(t *testing.T) {
	dir := t.TempDir()
	reg := newFixtureRegistry(t, dir)

	// A provider that only ever emits the correct call, twice, then answers.
	mk := func() provider.Provider {
		return &scriptedCleanProvider{}
	}

	run := func(repairOn bool) (int, int, []string) {
		r := newFixtureRegistry(t, dir)
		r.repairOff = !repairOn
		var repairNotices []string
		r.OnRepair = func(f Fix) { repairNotices = append(repairNotices, f.Notice()) }

		prov := mk()
		loop := &agent.Loop{
			Provider: prov, Tools: r, Sink: &recordingSink{},
			System: payload.DefaultSystemPrompt, MaxTurns: 12,
			Gate: allowReadsGate{}, Budget: agent.NewBudget(),
		}
		if err := loop.Start("clean", dir); err != nil {
			t.Fatal(err)
		}
		if err := loop.Run(context.Background(), "go"); err != nil {
			t.Fatal(err)
		}
		cp := prov.(*scriptedCleanProvider)
		return cp.requests, cumulativeInput(cp.msgs), repairNotices
	}
	_ = reg

	reqsOff, cumulOff, noticesOff := run(false)
	reqsOn, cumulOn, noticesOn := run(true)

	t.Logf("clean path: requests %d→%d · cumulative_in %d→%d · repair notices %d→%d",
		reqsOff, reqsOn, cumulOff, cumulOn, len(noticesOff), len(noticesOn))

	if reqsOff != reqsOn {
		t.Errorf("the clean path changed request count: %d → %d", reqsOff, reqsOn)
	}
	if cumulOff != cumulOn {
		t.Errorf("the clean path changed cumulative input: %d → %d", cumulOff, cumulOn)
	}
	if len(noticesOn) != 0 {
		t.Errorf("the clean path produced repair notices: %v", noticesOn)
	}
}

// scriptedCleanProvider emits only well-formed calls, then answers.
type scriptedCleanProvider struct {
	requests int
	msgs     [][]provider.Message
}

func (p *scriptedCleanProvider) Name() string { return "clean-path" }

func (p *scriptedCleanProvider) Stream(_ context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	p.requests++
	ms := make([]provider.Message, len(req.Messages))
	copy(ms, req.Messages)
	p.msgs = append(p.msgs, ms)

	var chunks []provider.Chunk
	switch p.requests {
	case 1:
		chunks = toolCallChunks(provider.ToolCall{ID: "k1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)})
	case 2:
		chunks = toolCallChunks(provider.ToolCall{ID: "k2", Name: "read_file", Input: json.RawMessage(`{"path":"a.go","offset":2}`)})
	default:
		chunks = []provider.Chunk{
			{Kind: provider.ChunkText, Text: "done"},
			{Kind: provider.ChunkStop, Stop: "end_turn"},
		}
	}

	ch := make(chan provider.Chunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// TestRepairSeenByTheGate proves contract 2 at the loop level: the permission
// gate is asked about the corrected name, never the one the model wrote.
func TestRepairSeenByTheGate(t *testing.T) {
	dir := t.TempDir()
	reg := newFixtureRegistry(t, dir)

	var asked []string
	gate := &recordingGate{asked: &asked}

	prov := &repairRoundProvider{
		first:   provider.ToolCall{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)},
		correct: provider.ToolCall{ID: "c2", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)},
	}
	loop := &agent.Loop{
		Provider: prov, Tools: reg, Sink: &recordingSink{},
		System: payload.DefaultSystemPrompt, MaxTurns: 12,
		Gate: gate, Budget: agent.NewBudget(),
	}
	if err := loop.Start("gate-sees-repair", dir); err != nil {
		t.Fatal(err)
	}
	if err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	if len(asked) == 0 {
		t.Fatal("the gate was never consulted")
	}
	for _, name := range asked {
		if name == "read" {
			t.Fatalf("the gate was asked about the raw name; it must see the corrected one. asked=%v", asked)
		}
	}
	if asked[0] != "read_file" {
		t.Fatalf("gate saw %q first, want the corrected \"read_file\"", asked[0])
	}
}

// recordingGate records the names it is asked about and allows them.
type recordingGate struct{ asked *[]string }

func (g *recordingGate) Check(tool string) (agent.Verdict, string) {
	*g.asked = append(*g.asked, tool)
	return agent.VerdictAllow, ""
}
func (g *recordingGate) Record(string, agent.Decision) {}
func (g *recordingGate) Effective(_ string, d agent.Decision) agent.Decision {
	return d
}

// TestRepairNoticeReachesTheJournal proves contract 1 end to end: a repair
// appears as a Notice event before the tool result it enabled.
func TestRepairNoticeReachesTheJournal(t *testing.T) {
	dir := t.TempDir()
	reg := newFixtureRegistry(t, dir)
	sink := &recordingSink{}
	reg.OnRepair = func(f Fix) {
		_ = sink.Emit(agent.Event{Type: agent.Notice, Text: f.Notice()})
	}

	prov := &repairRoundProvider{
		first:   provider.ToolCall{ID: "c1", Name: "read", Input: json.RawMessage(`{"path":"a.go"}`)},
		correct: provider.ToolCall{ID: "c2", Name: "read_file", Input: json.RawMessage(`{"path":"a.go"}`)},
	}
	loop := &agent.Loop{
		Provider: prov, Tools: reg, Sink: sink,
		System: payload.DefaultSystemPrompt, MaxTurns: 12,
		Gate: allowReadsGate{}, Budget: agent.NewBudget(),
	}
	if err := loop.Start("notice", dir); err != nil {
		t.Fatal(err)
	}
	if err := loop.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	var noticeIdx, toolEndIdx = -1, -1
	for i, e := range sink.events {
		if e.Type == agent.Notice && strings.Contains(e.Text, RuleToolAlias) && noticeIdx < 0 {
			noticeIdx = i
		}
		if e.Type == agent.ToolEnd && toolEndIdx < 0 {
			toolEndIdx = i
		}
	}
	if noticeIdx < 0 {
		t.Fatal("no repair notice reached the journal")
	}
	if toolEndIdx >= 0 && noticeIdx > toolEndIdx {
		t.Fatalf("the notice came after the tool result (notice=%d, toolEnd=%d): a repair must be announced before execution",
			noticeIdx, toolEndIdx)
	}
}
