package ui

import (
	"fmt"

	"nabd/internal/presentation"
)

const maxRenderedFeedLines = 12000

const retentionNoticeID = "ui_retention_notice"

// visibleFeedItems applies the item-retention cap without hiding that history
// was omitted from the in-memory feed. The synthetic notice occupies one slot,
// so the returned slice never exceeds maxVisibleFeedItems. The journal remains
// the complete source of truth.
func visibleFeedItems(items []presentation.FeedItem) []presentation.FeedItem {
	if len(items) <= maxVisibleFeedItems {
		return items
	}

	retained := maxVisibleFeedItems - 1
	start := len(items) - retained
	hidden := start
	firstVisible := items[start]

	out := make([]presentation.FeedItem, 0, maxVisibleFeedItems)
	out = append(out, presentation.FeedItem{
		Type: presentation.ItemNotice,
		ID:   retentionNoticeID,
		Seq:  firstVisible.Seq,
		Text: fmt.Sprintf(
			"… %d older items hidden · session journal has full history …",
			hidden,
		),
	})
	out = append(out, items[start:]...)
	return out
}

// boundRenderedLines prevents a single pathological response from retaining
// an unbounded viewport backing slice. The journal remains the source of truth.
func boundRenderedLines(lines []string, limit int) []string {
	if limit <= 0 || len(lines) <= limit {
		return lines
	}
	bounded := make([]string, limit)
	copy(bounded, lines[len(lines)-limit:])
	return bounded
}
