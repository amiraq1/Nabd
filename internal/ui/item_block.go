package ui

import (
	"strings"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

// ItemUIBlock is the UI-side rendered block of one feed item: the display
// lines it occupies in the viewport, bound to the source item it came from.
// It is the unit Batch 2 caches: a fingerprint of Item lets an unchanged
// block reuse its Lines instead of re-rendering (line memory).
type ItemUIBlock struct {
	Item  presentation.FeedItem
	Lines []string
}

// renderBlocks converts feed items to per-item rendered blocks. The block
// pipeline owns every behaviour the old line pipeline had, so flattening the
// blocks reproduces the old output byte-for-byte:
//   - a blank line separates adjacent message items (user/assistant);
//   - every stored line is guaranteed to fit within width terminal cells;
//   - each block carries its source item, ready for Batch 2 fingerprinting.
func renderBlocks(items []presentation.FeedItem, width int, toolsExpanded ...bool) []ItemUIBlock {
	isExpanded := false
	if len(toolsExpanded) > 0 {
		isExpanded = toolsExpanded[0]
	}
	blocks := make([]ItemUIBlock, 0, len(items))
	var prevIsMsg bool
	hasLines := false
	for _, it := range items {
		isMsg := it.Type == presentation.ItemUserMsg || it.Type == presentation.ItemAssistant
		block := ItemUIBlock{Item: it}
		if hasLines && (isMsg || prevIsMsg) {
			block.Lines = append(block.Lines, "")
		}
		raw := renderItem(it, width, isExpanded)
		for _, l := range raw {
			// Guarantee every stored line fits within width terminal cells.
			if width > 0 && ansi.StringWidth(l) > width {
				block.Lines = append(block.Lines, strings.Split(ansi.Hardwrap(l, width, false), "\n")...)
			} else {
				block.Lines = append(block.Lines, l)
			}
		}
		if len(block.Lines) > 0 {
			hasLines = true
		}
		blocks = append(blocks, block)
		prevIsMsg = isMsg
	}
	return blocks
}

// renderItemsCached is like renderItems but uses a per-item line cache.
// The cache is keyed by FeedItem.ID; empty IDs are never cached.
// Duplicate IDs within the same call bypass the cache for correctness.
func renderItemsCached(m *Feed, items []presentation.FeedItem, width int, toolsExpanded ...bool) []string {
	isExpanded := false
	if len(toolsExpanded) > 0 {
		isExpanded = toolsExpanded[0]
	}

	// Detect duplicate IDs to bypass cache for shared IDs.
	idCount := make(map[string]int, len(items))
	for _, it := range items {
		if it.ID != "" {
			idCount[it.ID]++
		}
	}

	if m.lineCache == nil {
		m.lineCache = make(map[string]cacheEntry)
	}

	blocks := make([]ItemUIBlock, 0, len(items))
	var prevIsMsg bool
	hasLines := false
	for _, it := range items {
		isMsg := it.Type == presentation.ItemUserMsg || it.Type == presentation.ItemAssistant
		block := ItemUIBlock{Item: it}

		fp := it.Fingerprint()
		cached := m.lineCache[it.ID]
		canUseCache := it.ID != "" && idCount[it.ID] == 1 &&
			cached.fp == fp && cached.expanded == isExpanded

		if canUseCache {
			// Add separator before cached content (not included in cache).
			if hasLines && (isMsg || prevIsMsg) {
				block.Lines = append(block.Lines, "")
			}
			// Copy to prevent aliasing: consumer must not corrupt cached copy.
			block.Lines = append(block.Lines, copyLines(cached.lines)...)
		} else {
			if hasLines && (isMsg || prevIsMsg) {
				block.Lines = append(block.Lines, "")
			}
			raw := renderItem(it, width, isExpanded)
			m.renderCount++
			content := make([]string, 0, len(raw))
			for _, l := range raw {
				// Guarantee every stored line fits within width terminal cells.
				if width > 0 && ansi.StringWidth(l) > width {
					wrapped := strings.Split(ansi.Hardwrap(l, width, false), "\n")
					content = append(content, wrapped...)
				} else {
					content = append(content, l)
				}
			}
			block.Lines = append(block.Lines, content...)
			// Store ONLY the hard-wrapped content (without separator) in cache.
			// The separator is context-dependent and added at retrieval time.
			if it.ID != "" && idCount[it.ID] == 1 {
				m.lineCache[it.ID] = cacheEntry{
					fp:       fp,
					expanded: isExpanded,
					lines:    copyLines(content),
				}
			}
		}

		if len(block.Lines) > 0 {
			hasLines = true
		}
		blocks = append(blocks, block)
		prevIsMsg = isMsg
	}
	lines, _ := flattenBlocks(blocks)
	return lines
}

// copyLines returns a new slice with the same content.
func copyLines(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// flattenBlocks flattens per-item blocks back into the flat line list and
// the starting line index of each block. This is the only consumer of the
// block boundary: the line pipeline (renderItems/renderItemsWithOffsets)
// delegates here so the block is the single unit of rendering and memory.
func flattenBlocks(blocks []ItemUIBlock) ([]string, []int) {
	var lines []string
	offsets := make([]int, len(blocks))
	for i, b := range blocks {
		offsets[i] = len(lines)
		lines = append(lines, b.Lines...)
	}
	return lines, offsets
}
