package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"nabd/internal/provider"
)

// mockTraceProvider implements provider.Provider for testing trace conversion.
type mockTraceProvider struct {
	chunks []provider.Chunk
}

func (m *mockTraceProvider) Name() string { return "mock-trace-provider" }
func (m *mockTraceProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

type traceRecorderSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *traceRecorderSink) Emit(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *traceRecorderSink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]Event, len(s.events))
	copy(copied, s.events)
	return copied
}

// ─── 1. Schema & Serialization Tests ──────────────────────────────────────────

func TestProviderRouteEventSchema(t *testing.T) {
	ev := Event{
		Seq:  1,
		Time: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
		Type: EventProviderRoute,
		Route: &ProviderRoute{
			StreamID: "0123456789abcdef0123456789abcdef",
			Provider: "groq",
			Model:    "qwen-2.5-32b",
			Attempt:  1,
			Status:   "attempted",
			Reason:   "test failure reason",
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if m["type"] != "provider_route" {
		t.Fatalf("expected type: provider_route, got %v", m["type"])
	}

	routeObj, ok := m["route"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'route' object in JSON, got %v", m["route"])
	}

	if routeObj["stream_id"] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("stream_id mismatch: %v", routeObj["stream_id"])
	}
	if routeObj["provider"] != "groq" {
		t.Errorf("provider mismatch: %v", routeObj["provider"])
	}
	if routeObj["model"] != "qwen-2.5-32b" {
		t.Errorf("model mismatch: %v", routeObj["model"])
	}
	if fmt.Sprintf("%.0f", routeObj["attempt"]) != "1" {
		t.Errorf("attempt mismatch: %v", routeObj["attempt"])
	}
	if routeObj["status"] != "attempted" {
		t.Errorf("status mismatch: %v", routeObj["status"])
	}
	if routeObj["reason"] != "test failure reason" {
		t.Errorf("reason mismatch: %v", routeObj["reason"])
	}
}

func TestProviderRouteEventOmitsEmptyReason(t *testing.T) {
	ev := Event{
		Seq:  1,
		Type: EventProviderRoute,
		Route: &ProviderRoute{
			StreamID: "stream1",
			Provider: "anthropic",
			Model:    "claude-3",
			Attempt:  1,
			Status:   "selected",
			Reason:   "", // empty reason
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	str := string(data)
	if strings.Contains(str, `"reason"`) {
		t.Fatalf("empty reason must be omitted from JSON: %s", str)
	}
}

// ─── 2. Trace Conversion & Loop Integration ───────────────────────────────────

func TestRouteTraceConvertedToJournalEvent(t *testing.T) {
	sink := &traceRecorderSink{}
	mockP := &mockTraceProvider{
		chunks: []provider.Chunk{
			{
				Kind: provider.ChunkTrace,
				RouteTrace: &provider.ChunkRouteTrace{
					StreamID: "stream-abc",
					Provider: "groq",
					Model:    "qwen",
					Attempt:  1,
					Status:   "attempted",
				},
			},
			{
				Kind: provider.ChunkTrace,
				RouteTrace: &provider.ChunkRouteTrace{
					StreamID: "stream-abc",
					Provider: "groq",
					Model:    "qwen",
					Attempt:  1,
					Status:   "selected",
				},
			},
			{Kind: provider.ChunkText, Text: "hi"},
			{Kind: provider.ChunkStop, Stop: "end_turn"},
		},
	}

	l := &Loop{
		Provider: mockP,
		Sink:     sink,
	}

	_, _, err := l.streamTurn(context.Background(), nil)
	if err != nil {
		t.Fatalf("streamTurn error: %v", err)
	}

	var routeEvents []Event
	for _, e := range sink.Events() {
		if e.Type == EventProviderRoute {
			routeEvents = append(routeEvents, e)
		}
	}

	if len(routeEvents) != 2 {
		t.Fatalf("expected 2 EventProviderRoute events, got %d", len(routeEvents))
	}
	if routeEvents[0].Route.Status != "attempted" || routeEvents[1].Route.Status != "selected" {
		t.Fatalf("unexpected statuses: %s, %s", routeEvents[0].Route.Status, routeEvents[1].Route.Status)
	}
}

func TestRouteTracePreservesStreamID(t *testing.T) {
	streamID := "deadbeefcafebabe0123456789abcdef"
	sink := &traceRecorderSink{}

	mockP := &mockTraceProvider{
		chunks: []provider.Chunk{
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: streamID, Attempt: 1, Status: "attempted"}},
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: streamID, Attempt: 1, Status: "failed", Reason: "503"}},
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: streamID, Attempt: 2, Status: "attempted"}},
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: streamID, Attempt: 2, Status: "selected"}},
			{Kind: provider.ChunkText, Text: "done"},
			{Kind: provider.ChunkStop, Stop: "end_turn"},
		},
	}

	l := &Loop{
		Provider: mockP,
		Sink:     sink,
	}

	_, _, err := l.streamTurn(context.Background(), nil)
	if err != nil {
		t.Fatalf("streamTurn error: %v", err)
	}

	var routeEvents []Event
	for _, e := range sink.Events() {
		if e.Type == EventProviderRoute {
			routeEvents = append(routeEvents, e)
		}
	}

	if len(routeEvents) != 4 {
		t.Fatalf("expected 4 route events, got %d", len(routeEvents))
	}

	for i, re := range routeEvents {
		if re.Route.StreamID != streamID {
			t.Errorf("event %d streamID mismatch: expected %s, got %s", i, streamID, re.Route.StreamID)
		}
	}
}

func TestRouteTracePreservesAttemptOrder(t *testing.T) {
	sink := &traceRecorderSink{}
	mockP := &mockTraceProvider{
		chunks: []provider.Chunk{
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 1, Status: "attempted"}},
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 1, Status: "failed"}},
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 2, Status: "attempted"}},
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 2, Status: "selected"}},
			{Kind: provider.ChunkText, Text: "ok"},
			{Kind: provider.ChunkStop, Stop: "end_turn"},
		},
	}

	l := &Loop{
		Provider: mockP,
		Sink:     sink,
	}

	_, _, _ = l.streamTurn(context.Background(), nil)

	var attempts []int
	for _, e := range sink.Events() {
		if e.Type == EventProviderRoute {
			attempts = append(attempts, e.Route.Attempt)
		}
	}

	expected := []int{1, 1, 2, 2}
	if !reflect.DeepEqual(attempts, expected) {
		t.Fatalf("expected attempts %v, got %v", expected, attempts)
	}
}

func TestSelectedRouteRecordedExactlyOnce(t *testing.T) {
	sink := &traceRecorderSink{}
	mockP := &mockTraceProvider{
		chunks: []provider.Chunk{
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 1, Status: "attempted"}},
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 1, Status: "selected"}},
			{Kind: provider.ChunkText, Text: "t1"},
			{Kind: provider.ChunkText, Text: "t2"},
			{Kind: provider.ChunkStop, Stop: "end_turn"},
		},
	}

	l := &Loop{
		Provider: mockP,
		Sink:     sink,
	}

	_, _, _ = l.streamTurn(context.Background(), nil)

	var selectedCount int
	for _, e := range sink.Events() {
		if e.Type == EventProviderRoute && e.Route.Status == "selected" {
			selectedCount++
		}
	}

	if selectedCount != 1 {
		t.Fatalf("expected selected to be recorded exactly once, got %d", selectedCount)
	}
}

func TestExhaustedRouteRecordedExactlyOnce(t *testing.T) {
	sink := &traceRecorderSink{}
	mockP := &mockTraceProvider{
		chunks: []provider.Chunk{
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 1, Status: "failed"}},
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 1, Status: "exhausted"}},
			{Kind: provider.ChunkError, Err: errors.New("all exhausted")},
		},
	}

	l := &Loop{
		Provider: mockP,
		Sink:     sink,
	}

	_, _, _ = l.streamTurn(context.Background(), nil)

	var exhaustedCount int
	for _, e := range sink.Events() {
		if e.Type == EventProviderRoute && e.Route.Status == "exhausted" {
			exhaustedCount++
		}
	}

	if exhaustedCount != 1 {
		t.Fatalf("expected exhausted to be recorded exactly once, got %d", exhaustedCount)
	}
}

func TestParentCancellationDoesNotRecordSelectedRoute(t *testing.T) {
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel parent

	sink := &traceRecorderSink{}
	mockP := &mockTraceProvider{
		chunks: []provider.Chunk{
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 1, Status: "attempted"}},
			{Kind: provider.ChunkError, Err: context.Canceled},
		},
	}

	l := &Loop{
		Provider: mockP,
		Sink:     sink,
	}

	_, _, _ = l.streamTurn(parentCtx, nil)

	var gotSelected bool
	for _, e := range sink.Events() {
		if e.Type == EventProviderRoute && e.Route.Status == "selected" {
			gotSelected = true
		}
	}

	if gotSelected {
		t.Fatal("selected MUST NOT be recorded when parent was cancelled")
	}
}

// ─── 3. Replay & Model Message Isolation (O.4) ────────────────────────────────

func TestProviderRouteEventNeverReachesModelMessages(t *testing.T) {
	events := []Event{
		{Seq: 1, Type: UserMsg, Text: "hello"},
		{
			Seq:  2,
			Type: EventProviderRoute,
			Route: &ProviderRoute{
				StreamID: "stream1",
				Provider: "groq",
				Model:    "qwen",
				Attempt:  1,
				Status:   "selected",
			},
		},
		{Seq: 3, Type: TextDelta, Text: "world"},
		{Seq: 4, Type: TurnEnd},
	}

	msgs := Messages(events)

	// Messages must contain ONLY UserMsg and assistant message; no trace info!
	for _, m := range msgs {
		if strings.Contains(m.Text, "groq") || strings.Contains(m.Text, "stream1") || strings.Contains(m.Text, "selected") {
			t.Fatalf("EventProviderRoute leaked into model message text: %s", m.Text)
		}
	}
}

func TestReplayExplicitlyIgnoresProviderRouteEvent(t *testing.T) {
	// Replaying a stream with only EventProviderRoute events must yield no messages
	events := []Event{
		{Seq: 1, Type: EventProviderRoute, Route: &ProviderRoute{Status: "attempted"}},
		{Seq: 2, Type: EventProviderRoute, Route: &ProviderRoute{Status: "selected"}},
	}

	msgs := Messages(events)
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages from provider_route events, got %d", len(msgs))
	}
}

func TestReplayDoesNotWarnForProviderRouteEvent(t *testing.T) {
	// Replay should process EventProviderRoute without hitting the default case
	events := []Event{
		{Seq: 1, Type: EventProviderRoute, Route: &ProviderRoute{Status: "selected"}},
	}
	msgs := Messages(events)
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

func TestProviderRouteJournalNeverContainsConfiguredKeys(t *testing.T) {
	key := "gsk_myverysensitiveapikeydo_not_leak"
	ev := Event{
		Seq:  1,
		Type: EventProviderRoute,
		Route: &ProviderRoute{
			StreamID: "s1",
			Provider: "groq",
			Model:    "m",
			Attempt:  1,
			Status:   "failed",
			Reason:   provider.SanitizeBody("upstream error: "+key, []string{key}),
		},
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	jsonStr := string(data)
	if strings.Contains(jsonStr, key) {
		t.Fatalf("API key leaked in journal event JSON: %s", jsonStr)
	}
}

func TestProviderRouteReasonIsSanitizedAndBounded(t *testing.T) {
	hugeMsg := strings.Repeat("x", 10000)
	sanitized := provider.SanitizeBody(hugeMsg, nil)

	ev := Event{
		Seq:  1,
		Type: EventProviderRoute,
		Route: &ProviderRoute{
			StreamID: "s1",
			Reason:   sanitized,
		},
	}

	data, _ := json.Marshal(ev)
	if len(data) > 5000 {
		t.Fatalf("reason was not size-bounded: %d bytes", len(data))
	}
}

func TestConcurrentStreamsPreservePerStreamEventOrder(t *testing.T) {
	var mu sync.Mutex
	streamEvents := make(map[string][]int) // streamID -> sequence of attempts

	record := func(streamID string, attempt int) {
		mu.Lock()
		defer mu.Unlock()
		streamEvents[streamID] = append(streamEvents[streamID], attempt)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sid := fmt.Sprintf("stream-%d", id)
			sink := &traceRecorderSink{}
			mockP := &mockTraceProvider{
				chunks: []provider.Chunk{
					{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: sid, Attempt: 1, Status: "attempted"}},
					{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: sid, Attempt: 1, Status: "failed"}},
					{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: sid, Attempt: 2, Status: "attempted"}},
					{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: sid, Attempt: 2, Status: "selected"}},
					{Kind: provider.ChunkText, Text: "done"},
					{Kind: provider.ChunkStop, Stop: "end_turn"},
				},
			}
			l := &Loop{
				Provider: mockP,
				Sink:     sink,
			}
			_, _, _ = l.streamTurn(context.Background(), nil)
			for _, e := range sink.Events() {
				if e.Type == EventProviderRoute {
					record(e.Route.StreamID, e.Route.Attempt)
				}
			}
		}(i)
	}
	wg.Wait()

	if len(streamEvents) != 10 {
		t.Fatalf("expected 10 streams recorded, got %d", len(streamEvents))
	}

	for sid, attempts := range streamEvents {
		expected := []int{1, 1, 2, 2}
		if !reflect.DeepEqual(attempts, expected) {
			t.Errorf("stream %s had disordered attempts: %v", sid, attempts)
		}
	}
}

func TestRouteTraceDoesNotDuplicateToolExecution(t *testing.T) {
	var executedTools []string
	call := provider.ToolCall{ID: "call_1", Name: "bash", Input: json.RawMessage(`{"cmd":"ls"}`)}

	sink := &traceRecorderSink{}
	mockP := &mockTraceProvider{
		chunks: []provider.Chunk{
			{Kind: provider.ChunkTrace, RouteTrace: &provider.ChunkRouteTrace{StreamID: "sid", Attempt: 1, Status: "selected"}},
			{Kind: provider.ChunkToolCall, Call: &call},
			{Kind: provider.ChunkStop, Stop: "tool_use"},
		},
	}

	l := &Loop{
		Provider: mockP,
		Sink:     sink,
	}

	calls, _, err := l.streamTurn(context.Background(), nil)
	if err != nil {
		t.Fatalf("streamTurn error: %v", err)
	}

	for _, c := range calls {
		executedTools = append(executedTools, c.ID)
	}

	if len(executedTools) != 1 || executedTools[0] != "call_1" {
		t.Fatalf("tool calls were duplicated or missing: %v", executedTools)
	}
}

func TestUnknownHistoricalEventsRemainBackwardCompatible(t *testing.T) {
	// Replay should safely skip unknown event types without crashing
	events := []Event{
		{Seq: 1, Type: EventType("future_event_type_v2"), Text: "future content"},
		{Seq: 2, Type: UserMsg, Text: "hi"},
		{Seq: 3, Type: TextDelta, Text: "hello"},
		{Seq: 4, Type: TurnEnd},
	}

	msgs := Messages(events)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (user, assistant), got %d", len(msgs))
	}
}

func TestProviderPackageDoesNotImportAgentOrStore(t *testing.T) {
	// Architectural boundary check: internal/provider must not import internal/agent or internal/store.
	cmd := exec.Command("go", "list", "-deps", "nabd/internal/provider")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list failed: %v", err)
	}

	outStr := string(out)
	if strings.Contains(outStr, "nabd/internal/agent") {
		t.Fatal("ARCHITECTURAL VIOLATION: nabd/internal/provider imports nabd/internal/agent!")
	}
	if strings.Contains(outStr, "nabd/internal/store") {
		t.Fatal("ARCHITECTURAL VIOLATION: nabd/internal/provider imports nabd/internal/store!")
	}
}
