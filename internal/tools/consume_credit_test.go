package tools

import "testing"

// TestConsumeLinesReadSignatureRequiresHash is the proof that forgetting the
// pre-mutation hash no longer compiles. The old zero-argument and
// single-argument forms of ConsumeLinesRead are gone: the method value below
// only type-checks against func(*Registry, string, string) int, so any stale
// call site is a build error rather than a silently granted read credit.
func TestConsumeLinesReadSignatureRequiresHash(t *testing.T) {
	var fn func(*Registry, string, string) int = (*Registry).ConsumeLinesRead
	if fn == nil {
		t.Fatal("ConsumeLinesRead method value is nil")
	}
}
