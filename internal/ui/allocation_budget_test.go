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
// View measured exactly 148.0 allocs/op; budget set to 156 (measured + 8) to keep guard tight.
//
// Guarded by !race: the race detector inflates and perturbs per-run allocation
// counts, so this fixed budget is only meaningful under the normal build.
func TestViewAllocationsNonRegression(t *testing.T) {
	f := liveStreamingFeed(t)
	_ = f.View() // warm every cache before measuring

	const budget = 156
	allocs := testing.AllocsPerRun(50, func() {
		_ = f.View()
	})
	if allocs > budget {
		t.Fatalf("View allocations regressed: got %.1f, want <= %d", allocs, budget)
	}
}
