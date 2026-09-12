package tools

import "testing"

// TestConsumeLinesReadSignatureRequiresHash is the proof that forgetting the
// pre-mutation hash no longer compiles. The old zero-argument and
// single-argument forms of ConsumeLinesRead are gone: the method value below
// only type-checks against func(*Registry, string, string) int, so any stale
// call site is a build error rather than a silently granted read credit.
func TestConsumeLinesReadSignatureRequiresHash(t *testing.T) {
	// Compile-time proof: only the two-arg form type-checks. The old
	// zero-arg and single-arg forms no longer compile.
	var _ func(*Registry, string, string) int = (*Registry).ConsumeLinesRead
}
