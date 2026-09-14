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
//   - The CI benchmark step runs BenchmarkRefreshStreaming (a different path) and
//     reported 1014 allocs/op on amd64 vs 1022 on arm64 — a ~1% cross-architecture
//     delta. The same delta applied to f.View() puts the amd64 figure at ~148-150.
//     (CI does not currently run BenchmarkRefreshLiveStreaming, so this is inferred
//     from the sibling benchmark; add it to the benchmark step to report it directly.)
//   - Budget 170 = +15% over 148. This is intentional headroom: tightening to
//     148+8 = 156 would leave only ~5% margin, and an amd64 f.View() of 150 would
//     then risk flapping. Keep 170 until BenchmarkRefreshLiveStreaming is gated in CI
//     and the true amd64 f.View() number is known.
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
