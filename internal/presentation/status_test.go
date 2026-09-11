package presentation_test

import (
	"reflect"
	"testing"
	"time"

	"nabd/internal/agent"
	"nabd/internal/presentation"
)

func TestStatusProjectorTracksCallIDAndPermission(t *testing.T) {
	now := time.Unix(10, 0)
	events := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.TurnStart},
		{Seq: 3, Type: agent.ToolStart, Time: now, Call: &agent.ToolCall{ID: "call-1", Name: "read_file", Args: []byte(`{"path":"README.md"}`)}},
		{Seq: 4, Type: agent.PermAsk, Call: &agent.ToolCall{ID: "call-1", Name: "read_file"}},
	}
	p := presentation.NewStatusProjector()
	for _, e := range events {
		p.Apply(e)
	}
	status := p.Status()
	if status.Phase != presentation.PhasePermission {
		t.Fatalf("phase = %q, want permission", status.Phase)
	}
	if len(status.ActiveTools) != 1 || status.ActiveTools[0].CallID != "call-1" {
		t.Fatalf("active tools = %+v, want call-1", status.ActiveTools)
	}
	if status.ActiveTools[0].StartedAt != now {
		t.Fatalf("started at = %v, want %v", status.ActiveTools[0].StartedAt, now)
	}
}

func TestStatusIncrementalEqualsReplay(t *testing.T) {
	events := []agent.Event{
		{Seq: 1, Type: agent.RunStart},
		{Seq: 2, Type: agent.TurnStart},
		{Seq: 3, Type: agent.ToolStart, Call: &agent.ToolCall{ID: "c1", Name: "bash"}},
		{Seq: 4, Type: agent.ToolEnd, Call: &agent.ToolCall{ID: "c1", Name: "bash", OK: true}},
		{Seq: 5, Type: agent.EventProviderUsage, Usage: &agent.ProviderUsage{PromptTokens: 12, CompletionTokens: 7}},
		{Seq: 6, Type: agent.TurnEnd},
	}
	incremental := presentation.NewStatusProjector()
	for _, e := range events {
		incremental.Apply(e)
	}
	replay := presentation.NewStatusProjector()
	got := replay.Build(events)
	if !reflect.DeepEqual(incremental.Status(), got) {
		t.Fatalf("incremental status %+v differs from replay %+v", incremental.Status(), got)
	}
}

func TestRunErrorWithoutCodeIsUnknown(t *testing.T) {
	p := presentation.NewStatusProjector()
	p.Apply(agent.Event{Type: agent.RunError, Err: "provider failed"})
	if p.Status().LastError == nil || p.Status().LastError.Code != presentation.ErrCodeUnknown {
		t.Fatalf("status = %+v, want unknown error code", p.Status())
	}
}
