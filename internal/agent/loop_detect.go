package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
)

// ErrToolLoop is returned when the model repeats an identical tool call past the abort threshold.
// It is a semantic circuit breaker that aborts before max-turns is exhausted.
var ErrToolLoop = errors.New("tool execution loop detected: identical call repeated past threshold")

const (
	// LoopNoticeThreshold is the number of identical tool executions that triggers
	// an injected conversational notice advising the model to change its approach.
	LoopNoticeThreshold = 3

	// LoopAbortThreshold is the number of identical tool executions that triggers
	// an immediate hard cut aborting the run.
	LoopAbortThreshold = 5
)

// toolCallFingerprint identifies a tool execution by its tool name, canonical input,
// and outcome (success/failure and output text).
type toolCallFingerprint struct {
	tool       string
	inputHash  [32]byte
	outputHash [32]byte
}

// canonicalInputHash returns a SHA-256 digest of the input JSON with sorted keys.
// If the input is empty, it returns the hash of an empty byte slice.
// If the input is not valid JSON, it hashes the raw bytes directly.
func canonicalInputHash(raw []byte) [32]byte {
	if len(raw) == 0 {
		return sha256.Sum256(nil)
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err == nil {
		if canon, err := json.Marshal(v); err == nil {
			return sha256.Sum256(canon)
		}
	}
	return sha256.Sum256(raw)
}

// outcomeHash returns a SHA-256 digest of the tool execution outcome:
// ok status byte (0x01 for success, 0x00 for failure) followed by output/error text.
func outcomeHash(ok bool, text string) [32]byte {
	h := sha256.New()
	if ok {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	h.Write([]byte(text))
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// computeFingerprint constructs the (tool, hash(input), hash(output-error)) fingerprint.
func computeFingerprint(tool string, input []byte, ok bool, output string) toolCallFingerprint {
	return toolCallFingerprint{
		tool:       tool,
		inputHash:  canonicalInputHash(input),
		outputHash: outcomeHash(ok, output),
	}
}

// loopDetector tracks tool call fingerprints for an active Run().
type loopDetector struct {
	counts map[toolCallFingerprint]int
}

func newLoopDetector() *loopDetector {
	return &loopDetector{
		counts: make(map[toolCallFingerprint]int),
	}
}

// record registers a tool execution and returns its repetition count.
func (d *loopDetector) record(fp toolCallFingerprint) int {
	if d == nil {
		return 1
	}
	if d.counts == nil {
		d.counts = make(map[toolCallFingerprint]int)
	}
	d.counts[fp]++
	return d.counts[fp]
}
