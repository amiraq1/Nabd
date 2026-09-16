package agent

import "fmt"

// ReadStatus records the current read state of a file within a turn.
type ReadStatus struct {
	Path       string
	Truncated  bool
	NextOffset int
	LinesRead  int
	TotalLines int
	Offset     int
}

// ReadTracker evaluates whether file reads in a turn were truncated and
// remained incomplete when the model generated its response.
type ReadTracker struct {
	order  []string
	byPath map[string]*ReadStatus
}

// NewReadTracker creates an empty ReadTracker.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		byPath: make(map[string]*ReadStatus),
	}
}

// Record updates the tracking state for a file with a new ReadRecord.
func (rt *ReadTracker) Record(rec ReadRecord) {
	if rec.Path == "" {
		return
	}

	existing, seen := rt.byPath[rec.Path]
	if !seen {
		rt.order = append(rt.order, rec.Path)
		rt.byPath[rec.Path] = &ReadStatus{
			Path:       rec.Path,
			Truncated:  rec.Truncated,
			NextOffset: rec.NextOffset,
			LinesRead:  rec.LinesRead,
			TotalLines: rec.TotalLines,
			Offset:     rec.Offset,
		}
		return
	}

	if rec.TotalLines > 0 {
		existing.TotalLines = rec.TotalLines
	}

	if rec.Truncated {
		// A continuation that is also truncated keeps the resource truncated.
		existing.Truncated = true
		if rec.NextOffset > 0 {
			existing.NextOffset = rec.NextOffset
		}
		if rec.LinesRead > 0 {
			if rec.Offset > 1 && existing.LinesRead > 0 {
				existing.LinesRead += rec.LinesRead
			} else if rec.LinesRead > existing.LinesRead {
				existing.LinesRead = rec.LinesRead
			}
		}
		return
	}

	// The subsequent read is not truncated (it reached EOF for its requested range).
	// Verify that it actually completed the file without leaving an unread gap.
	if existing.Truncated && existing.NextOffset > 0 && rec.Offset > existing.NextOffset {
		// Continuation started past the required offset — an unread gap was left.
		existing.Truncated = true
		return
	}

	// No gap and not truncated: the file read has been completed to the end.
	existing.Truncated = false
	if rec.Offset > 1 && existing.LinesRead > 0 {
		existing.LinesRead += rec.LinesRead
	} else if rec.LinesRead > existing.LinesRead {
		existing.LinesRead = rec.LinesRead
	}
}

// IncompleteReads returns all resources that have a truncated read that was
// not subsequently completed within the turn, in insertion order.
func (rt *ReadTracker) IncompleteReads() []ReadStatus {
	if rt == nil {
		return nil
	}
	var out []ReadStatus
	for _, p := range rt.order {
		if st, ok := rt.byPath[p]; ok && st.Truncated {
			out = append(out, *st)
		}
	}
	return out
}

// EvaluateTruncatedReads inspects a sequence of events and returns the list of
// files that had truncated reads that were never completed.
func EvaluateTruncatedReads(events []Event) []ReadStatus {
	tracker := NewReadTracker()
	for _, e := range events {
		if e.Type == EventRead && e.Read != nil {
			tracker.Record(*e.Read)
		}
	}
	return tracker.IncompleteReads()
}

// FormatTruncatedReadWarning renders the user-facing warning marking an answer
// based on an incomplete read. It includes the path, lines read, and total lines.
func FormatTruncatedReadWarning(st ReadStatus) string {
	return fmt.Sprintf("partial read: %s · read %d of %d lines", st.Path, st.LinesRead, st.TotalLines)
}
