package ui

import (
	"strings"

	"nabd/internal/presentation"

	"github.com/charmbracelet/x/ansi"
)

type ItemUIBlock struct {
	Item  presentation.FeedItem
	Lines []string
}

func renderBlocks(items []presentation.FeedItem, width int, toolsExpanded ...bool) []ItemUIBlock {
	isExpanded := len(toolsExpanded) > 0 && toolsExpanded[0]
	blocks := make([]ItemUIBlock, 0, len(items))
	var prevIsMsg bool
	hasLines := false
	for _, it := range items {
		isMsg := it.Type == presentation.ItemUserMsg || it.Type == presentation.ItemAssistant
		block := ItemUIBlock{Item: it}
		if hasLines && (isMsg || prevIsMsg) {
			block.Lines = append(block.Lines, "")
		}
		for _, line := range renderItem(it, width, isExpanded) {
			if width > 0 && ansi.StringWidth(line) > width {
				block.Lines = append(block.Lines, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
			} else {
				block.Lines = append(block.Lines, line)
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

func renderItemsCached(m *Feed, items []presentation.FeedItem, width int, toolsExpanded ...bool) ([]string, []int) {
	// Per-card expansion replaces the single incoming flag. The variadic
	// parameter stays for call-site and test compatibility.
	_ = toolsExpanded
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
	for i, it := range items {
		isMsg := it.Type == presentation.ItemUserMsg || it.Type == presentation.ItemAssistant
		block := ItemUIBlock{Item: it}
		isExpanded := m.expansionOf(it)
		expandVal := expandCollapsed
		if isExpanded && it.Type == presentation.ItemTool {
			expandVal = expandOpened
		}
		isSelected := m.navigationMode && i == m.selectedItem
		fp := it.Fingerprint()
		cached := m.lineCache[it.ID]
		canUseCache := it.ID != "" && idCount[it.ID] == 1 &&
			cached.fp == fp && cached.expanded == expandVal &&
			cached.selected == isSelected
		needsSep := hasLines && (isMsg || prevIsMsg)
		if canUseCache {
			n := len(cached.lines)
			if needsSep {
				n++
			}
			block.Lines = make([]string, 0, n)
			if needsSep {
				block.Lines = append(block.Lines, "")
			}
			block.Lines = append(block.Lines, cached.lines...)
		} else {
			if needsSep {
				block.Lines = append(block.Lines, "")
			}
			contentWidth := width - selectionPrefixWidth
			if contentWidth < 1 {
				contentWidth = 1
			}
			prefix := selectionPrefix(isSelected)
			raw := renderItem(it, contentWidth, isExpanded)
			m.renderCount++
			content := make([]string, 0, len(raw))
			for _, line := range raw {
				if contentWidth > 0 && ansi.StringWidth(line) > contentWidth {
					for _, w := range strings.Split(ansi.Hardwrap(line, contentWidth, false), "\n") {
						content = append(content, prefix+w)
					}
				} else {
					content = append(content, prefix+line)
				}
			}
			content = boundRenderedLines(content, maxRenderedFeedLines)
			block.Lines = append(block.Lines, content...)
			if it.ID != "" && idCount[it.ID] == 1 {
				m.lineCache[it.ID] = cacheEntry{
					fp:       fp,
					expanded: expandVal,
					selected: isSelected,
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
	lines, offsets := flattenBlocks(blocks)
	trimmed := len(lines) - maxRenderedFeedLines
	if trimmed < 0 {
		trimmed = 0
	}
	return boundRenderedLines(lines, maxRenderedFeedLines), shiftOffsets(offsets, trimmed)
}

// copyLines returns a fresh, independent slice of lines. The caller's slice is
// frequently a sub-slice of a larger backing array (e.g. content already appended
// to block.Lines, or a windowed view), so a shallow alias would let later mutation
// of the cache entry corrupt the shared backing array. The copy severs that alias.
func copyLines(lines []string) []string {
	if lines == nil {
		return nil
	}
	out := make([]string, len(lines))
	copy(out, lines)
	return out
}

func flattenBlocks(blocks []ItemUIBlock) ([]string, []int) {
	totalLines := 0
	for _, block := range blocks {
		totalLines += len(block.Lines)
	}
	lines := make([]string, 0, totalLines)
	offsets := make([]int, len(blocks))
	for i, block := range blocks {
		offsets[i] = len(lines)
		lines = append(lines, block.Lines...)
	}
	return lines, offsets
}
