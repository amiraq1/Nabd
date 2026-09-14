//go:build !race

package ui

import (
	"testing"
)

// TestRenderItemsCachedAllocationsNonRegression locks the allocation ceiling on cached renders
// to prevent the 51 -> 26 win from quietly eroding.
//
// Guarded by !race: the race detector inflates and perturbs per-run allocation
// counts, so these fixed budgets are only meaningful under the normal build.
func TestRenderItemsCachedAllocationsNonRegression(t *testing.T) {
	f := liveStreamingFeed(t)
	items := f.proj.Items()
	_ = f.View() // warm every cache before measuring

	const budget = 26
	allocs := testing.AllocsPerRun(50, func() {
		_, _ = renderItemsCached(f, items, f.width, false)
	})
	if allocs > budget {
		t.Fatalf("renderItemsCached allocations regressed: got %.1f, want <= %d", allocs, budget)
	}
}

// TestViewAllocationsNonRegression ensures that View rendering on a live streaming feed
// does not regress in total allocations across layout, chrome, and cached items.
//
// Measured basis for the budget:
//   - f.View() on the liveStreamingFeed fixture (width 120) measured 148 allocs/op
//     on arm64 (via BenchmarkRefreshLiveStreaming).
//   - The amd64 figure for f.View() is NOT currently known: the CI benchmark step
//     runs BenchmarkRefreshStreaming, not BenchmarkRefreshLiveStreaming, so the PR's
//     "tighten to max(arm64, amd64) + 8" follow-up cannot be acted on yet. (This
//     file lives under !race; BenchmarkRefreshLiveStreaming is added to ci.yml step 20
//     so the amd64 number becomes known on the next run.)
//   - Until that number is measured, the 170 ceiling (148 + 22, ~15% headroom) is the
//     responsible choice: allocation counts are deterministic per code path, not a
//     noise quantity that scales with architecture, so the safe move on unknown data
//     is a loose bound rather than a tight one that may flap on amd64.
//
// Guarded by !race: the race detector inflates and perturbs per-run allocation
// counts, so this fixed budget is only meaningful under the normal build.
func TestViewAllocationsNonRegression(t *testing.T) {
	f := liveStreamingFeed(t)
	_ = f.View() // warm every cache before measuring

	const budget = 170
	allocs := testing.AllocsPerRun(50, func() {
		_ = f.View()
	})
	if allocs > budget {
		t.Fatalf("View allocations regressed: got %.1f, want <= %d", allocs, budget)
	}
}
