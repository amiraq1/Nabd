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

func renderItemsCached(m *Feed, items []presentation.FeedItem, width int, toolsExpanded ...bool) []string {
	isExpanded := len(toolsExpanded) > 0 && toolsExpanded[0]
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
		canUseCache := it.ID != "" && idCount[it.ID] == 1 && cached.fp == fp && cached.expanded == isExpanded
		if hasLines && (isMsg || prevIsMsg) {
			block.Lines = append(block.Lines, "")
		}
		if canUseCache {
			block.Lines = append(block.Lines, copyLines(cached.lines)...)
		} else {
			raw := renderItem(it, width, isExpanded)
			m.renderCount++
			content := make([]string, 0, len(raw))
			for _, line := range raw {
				if width > 0 && ansi.StringWidth(line) > width {
					content = append(content, strings.Split(ansi.Hardwrap(line, width, false), "\n")...)
				} else {
					content = append(content, line)
				}
			}
			content = boundRenderedLines(content, maxRenderedFeedLines)
			block.Lines = append(block.Lines, content...)
			if it.ID != "" && idCount[it.ID] == 1 {
				m.lineCache[it.ID] = cacheEntry{fp: fp, expanded: isExpanded, lines: copyLines(content)}
			}
		}
		if len(block.Lines) > 0 {
			hasLines = true
		}
		blocks = append(blocks, block)
		prevIsMsg = isMsg
	}
	lines, _ := flattenBlocks(blocks)
	return boundRenderedLines(lines, maxRenderedFeedLines)
}

func copyLines(lines []string) []string {
	if lines == nil {
		return nil
	}
	out := make([]string, len(lines))
	copy(out, lines)
	return out
}

func flattenBlocks(blocks []ItemUIBlock) ([]string, []int) {
	var lines []string
	offsets := make([]int, len(blocks))
	for i, block := range blocks {
		offsets[i] = len(lines)
		lines = append(lines, block.Lines...)
	}
	return lines, offsets
}
