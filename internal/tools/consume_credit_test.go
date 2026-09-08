package tools

import "testing"

// TestConsumeLinesReadSignatureRequiresHash is the proof that forgetting
// the pre-mutation hash no longer compiles. The old forms
//   r.ConsumeLinesRead()
//   r.ConsumeLinesRead(abs)
// are not in this signature. If this test file type-checks, they are gone.
func TestConsumeLinesReadSignatureRequiresHash(t *testing.T) {
	var fn func(*Registry, string, string) int = (*Registry).ConsumeLinesRead
	if fn == nil {
		t.Fatal("ConsumeLinesRead method value is nil")
	}
}
