package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nabd/internal/event"
)

// The category-only exit decision must not change the existing journal outcome:
// Run returns nil, RunEnd records normal completion, then headless exits 1.
func TestHeadlessEmptyResponseKeepsRunEndEnded(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop string
		cat  event.NoticeCategory
	}{
		{"upstream_empty", "end_turn", event.NoticeCategoryProviderEmpty},
		{"length_cut", "max_tokens", event.NoticeCategoryLengthLimit},
		{"other_silent_stop", "content_filter", event.NoticeCategoryProviderEmpty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, stderr := runHL(t, headlessConfig{
				prompt:   "hello",
				json:     true,
				provider: &scriptedProvider{turns: []scriptTurn{{stop: tc.stop}}},
			})
			if code != exitError {
				t.Fatalf("exit %d, want %d; stderr %q", code, exitError, stderr)
			}
			var ends, notices int
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				var ev event.Event
				if err := json.Unmarshal([]byte(line), &ev); err != nil {
					t.Fatalf("invalid journal JSON %q: %v", line, err)
				}
				switch ev.Type {
				case event.RunError:
					t.Fatalf("silent turn unexpectedly changed into RunError: %+v", ev)
				case event.RunEnd:
					ends++
					if !strings.HasPrefix(ev.Text, fmt.Sprintf(statusSessionEnded, "")) {
						t.Fatalf("silent turn changed RunEnd outcome: %+v", ev)
					}
				case event.Notice:
					if ev.NoticeCategory == tc.cat {
						notices++
					}
				}
			}
			if ends != 1 || notices != 1 {
				t.Fatalf("want one RunEnd and one category %d notice; got ends=%d notices=%d\n%s",
					tc.cat, ends, notices, out)
			}
		})
	}
}
