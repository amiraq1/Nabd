package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBash_HeadTailConcurrentWritesAndSnapshot tests headTail directly under
// heavy concurrent write and string snapshot operations, ensuring zero data races,
// no slice corruption, and complete UTF-8 boundary preservation.
func TestBash_HeadTailConcurrentWritesAndSnapshot(t *testing.T) {
	buf := &headTail{}
	var writersWg sync.WaitGroup
	var readersWg sync.WaitGroup

	const writers = 8
	const readers = 4
	const iterations = 500

	stop := make(chan struct{})

	// Readers calling String() concurrently
	for i := 0; i < readers; i++ {
		readersWg.Add(1)
		go func() {
			defer readersWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s := buf.String()
					if len(s) > 0 {
						// Ensure no corrupted Unicode replacement char at the boundary
						if strings.Contains(s, "\uFFFD") {
							t.Errorf("found U+FFFD replacement character in headTail output: %q", s)
						}
					}
				}
			}
		}()
	}

	// Writers writing Arabic and English chunks concurrently
	for i := 0; i < writers; i++ {
		writersWg.Add(1)
		go func(id int) {
			defer writersWg.Done()
			arabicChunk := []byte(fmt.Sprintf("رسالة رقم %d: نبض - منصة ذكية لتطوير البرمجيات بأمان وجودة عالية\n", id))
			for j := 0; j < iterations; j++ {
				_, _ = buf.Write(arabicChunk)
			}
		}(i)
	}

	// Wait for writers to complete, then signal readers to stop
	writersWg.Wait()
	close(stop)
	readersWg.Wait()

	// Final snapshot check
	finalStr := buf.String()
	if len(finalStr) == 0 {
		t.Fatal("expected non-empty headTail buffer")
	}
	if strings.Contains(finalStr, "\uFFFD") {
		t.Fatalf("final snapshot contains corrupted UTF-8: %s", finalStr)
	}
}

// TestBash_GrandchildHoldsStdoutOpen verifies that when a background grandchild
// process continues running and keeps stdout open after the main command exits,
// Nabd unblocks cleanly after bashDrainGrace without hanging or leaking goroutines.
func TestBash_GrandchildHoldsStdoutOpen(t *testing.T) {
	r, _ := newReg(t)

	// Command launches background grandchild that writes continuously to stdout,
	// while the leader command exits immediately.
	cmd := `( while true; do echo "grandchild output data"; sleep 0.05; done ) & exit 0`
	raw, _ := json.Marshal(map[string]any{
		"cmd":       cmd,
		"timeout_s": 5,
	})

	start := time.Now()
	out, err := r.RunDetailed(context.Background(), "bash", raw)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("RunDetailed failed: %v", err)
	}

	// The process should finish shortly after bashDrainGrace (around 2s),
	// well before the 5s timeout.
	if elapsed > 4*time.Second {
		t.Fatalf("command hung waiting for grandchild: took %v (expected ~2s drain grace)", elapsed)
	}

	if !strings.Contains(out.Text, "grandchild output data") {
		t.Logf("output text: %s", out.Text)
	}
}

// TestBash_ContextCancellationStopsExecution verifies prompt termination on ctx cancellation.
func TestBash_ContextCancellationStopsExecution(t *testing.T) {
	r, _ := newReg(t)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	raw, _ := json.Marshal(map[string]any{
		"cmd":       "sleep 10",
		"timeout_s": 30,
	})

	start := time.Now()
	out, err := r.RunDetailed(ctx, "bash", raw)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("RunDetailed unexpected error: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("cancellation did not terminate promptly: took %v", elapsed)
	}
	if out.OK {
		t.Fatal("cancelled command must not report OK")
	}
	if !strings.Contains(out.Text, "cancelled") {
		t.Fatalf("expected 'cancelled' in output header, got: %q", out.Text)
	}
}

// TestBash_ExceedsHeadTailCut verifies correct middle cut formatting and rune trimming.
func TestBash_ExceedsHeadTailCut(t *testing.T) {
	buf := &headTail{}

	// Write more than bashHead (8KB) + bashTail (24KB)
	chunk := []byte(strings.Repeat("سلسلة طويلة من الحروف العربية لاختبار القطع في المنتصف.\n", 1000))
	n, err := buf.Write(chunk)
	if err != nil || n != len(chunk) {
		t.Fatalf("Write: n=%d, err=%v", n, err)
	}

	str := buf.String()
	if !strings.Contains(str, "bytes cut from the middle") {
		t.Fatalf("expected middle cut notice in long output")
	}
	if strings.Contains(str, "\uFFFD") {
		t.Fatalf("corrupted UTF-8 detected after middle cut: %s", str)
	}
}
