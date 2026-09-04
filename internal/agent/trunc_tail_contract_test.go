package agent_test

import (
	"testing"

	"nabd/internal/agent"
	"nabd/internal/tools"
)

func TestTruncTailMatchesSqueezeRange(t *testing.T) {
	const want = "7-19"

	out := tools.TruncTail(7, 19, 100, 20)
	if got := agent.ReadRange(out); got != want {
		t.Fatalf("tools.TruncTail output range=%q, want %q; output=%q", got, want, out)
	}
}
