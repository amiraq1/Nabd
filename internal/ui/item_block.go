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