package provider

import (
	"testing"
	"time"
)

func TestParseRetryAfterWait(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
		err  bool
	}{
		{"empty is disabled", "", 0, false},
		{"whitespace is disabled", "   ", 0, false},
		{"explicit zero", "0", 0, false},
		{"typical", "25", 25, false},
		{"upper bound", "120", 120, false},
		{"above ceiling", "121", 0, true},
		{"negative", "-1", 0, true},
		{"not a number", "20s", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRetryAfterWait(tc.raw)
			if tc.err {
				if err == nil {
					t.Fatalf("ParseRetryAfterWait(%q) = %d, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRetryAfterWait(%q): unexpected error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ParseRetryAfterWait(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWithRetryAfterWaitClamps(t *testing.T) {
	r := &Router{}

	if got := r.WithRetryAfterWait(-5 * time.Second).retryAfterWait; got != 0 {
		t.Fatalf("negative budget = %v, want 0", got)
	}
	if got := r.WithRetryAfterWait(30 * time.Second).retryAfterWait; got != 30*time.Second {
		t.Fatalf("budget = %v, want 30s", got)
	}
	if got := r.WithRetryAfterWait(10 * time.Minute).retryAfterWait; got != maxRetryCeiling {
		t.Fatalf("oversized budget = %v, want %v", got, maxRetryCeiling)
	}
}

// The wait must never trigger by default: an unconfigured router behaves
// exactly as it did before, reporting exhaustion without pausing.
func TestShouldWaitOut(t *testing.T) {
	cases := []struct {
		name       string
		budget     time.Duration
		retryAfter time.Duration
		want       bool
	}{
		{"disabled by default", 0, 20 * time.Second, false},
		{"no retry-after", 30 * time.Second, 0, false},
		{"within budget", 30 * time.Second, 20 * time.Second, true},
		{"exactly at budget", 20 * time.Second, 20 * time.Second, true},
		{"beyond budget", 10 * time.Second, 20 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Router{retryAfterWait: tc.budget}
			if got := r.shouldWaitOut(tc.retryAfter); got != tc.want {
				t.Fatalf("shouldWaitOut(%v) with budget %v = %v, want %v",
					tc.retryAfter, tc.budget, got, tc.want)
			}
		})
	}
}
