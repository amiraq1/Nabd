package ui

const maxRenderedFeedLines = 12000

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
