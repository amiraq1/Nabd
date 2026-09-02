package agent

import (
	"context"
	"testing"
	"nabd/internal/provider"
)

type mockProvider struct {
	chunks []provider.Chunk
}

func (m mockProvider) Name() string { return "mock" }

func (m mockProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

type mockTools struct {
	allowed bool
}

func (m mockTools) Specs() []provider.ToolSpec {
	// The gate tests model a KNOWN tool going through the permission
	// ladder; the loop intercepts unknown tools before the gate.
	return []provider.ToolSpec{{Name: "test_tool"}}
}
func (m mockTools) Check(tool string) (Verdict, string) {
	if m.allowed {
		return VerdictAsk, ""
	}
	return VerdictDeny, "mock deny"
}
func (m mockTools) Run(ctx context.Context, c provider.ToolCall) (string, bool, error) {
	return "mock output", true, nil
}
func (m mockTools) Record(string, Decision) {}
func (m mockTools) Ask(ctx context.Context, c ToolCall) Decision {
	if m.allowed {
		return AllowOnce
	}
	return Deny
}

type mockSink struct {
	fn func(Event) error
}

func (m mockSink) Emit(e Event) error {
	if m.fn != nil {
		return m.fn(e)
	}
	return nil
}

func TestToolStartEmission(t *testing.T) {
	tests := []struct {
		name      string
		allowed   bool
		wantStart bool
	}{
		{"RejectedTool", false, true},
		{"AcceptedTool", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &Loop{
				Provider: mockProvider{
					chunks: []provider.Chunk{
						{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{ID: "call_1", Name: "test_tool"}},
						{Kind: provider.ChunkStop, Stop: "tool_calls"},
					},
				},
				Tools:  mockTools{allowed: tt.allowed},
				Budget: &Budget{},
				Gate:   &mockTools{allowed: tt.allowed},
				Human:  &mockTools{allowed: tt.allowed},
			}

			var events []Event
			l.Sink = mockSink{fn: func(e Event) error {
				events = append(events, e)
				return nil
			}}

			_ = l.Run(context.Background(), "test")

			startSeen := false
			replySeen := false
			for _, e := range events {
				if e.Type == PermReply {
					replySeen = true
				}
				if e.Type == ToolStart {
					startSeen = true
					if tt.allowed && !replySeen {
						t.Errorf("ToolStart emitted before PermReply for accepted tool")
					}
				}
			}

			if startSeen != tt.wantStart {
				t.Errorf("ToolStart emission = %v, want %v", startSeen, tt.wantStart)
			}
		})
	}
}

func TestToolPairing(t *testing.T) {
	cases := []struct {
		name     string
		toolName string
		allowed  bool
	}{
		{"UnknownTool", "unknown_tool", true},
		{"DeniedTool", "test_tool", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := &Loop{
				Provider: mockProvider{
					chunks: []provider.Chunk{
						{Kind: provider.ChunkToolCall, Call: &provider.ToolCall{ID: "call_pair", Name: tc.toolName}},
						{Kind: provider.ChunkStop, Stop: "tool_calls"},
					},
				},
				Tools:  mockTools{allowed: tc.allowed},
				Budget: &Budget{},
				Gate:   &mockTools{allowed: tc.allowed},
				Human:  &mockTools{allowed: tc.allowed},
			}

			var events []Event
			l.Sink = mockSink{fn: func(e Event) error {
				events = append(events, e)
				return nil
			}}

			_ = l.Run(context.Background(), "test")

			var endFound bool
			for i, e := range events {
				if e.Type == ToolEnd {
					endFound = true
					if i == 0 || events[i-1].Type != ToolStart {
						t.Errorf("ToolEnd at index %d has no preceding ToolStart", i)
					}
					if events[i-1].Call == nil || events[i].Call == nil || events[i-1].Call.ID != events[i].Call.ID {
						t.Errorf("ToolStart and ToolEnd call ID mismatch: start=%v end=%v", events[i-1].Call, events[i].Call)
					}
				}
			}
			if !endFound {
				t.Fatalf("expected ToolEnd event for %s, got none", tc.name)
			}
		})
	}
}

