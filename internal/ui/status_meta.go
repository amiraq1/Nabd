package ui

import (
	"time"
)

// statusLineWithMeta appends runtime metadata (turn · tokens · elapsed ·
// provider) to the already-sanitized status text, choosing the widest variant
// that still fits.
//
// It never truncates the base status: the phase ("Generating…", "Permission
// Required", "run ended with an error") is the part the user must be able to
// read. Metadata is additive, so if nothing fits, the row is unchanged.
//
// avail is the width budget for the returned string, i.e. the terminal width
// minus whatever prefix the caller will add.
func (m *Feed) statusLineWithMeta(base string, avail int) string {
	if base == "" || m.statusProj == nil || avail <= 0 {
		return base
	}
	meta := m.statusProj.Meta(time.Now())
	// The row times the current run, not the session: the projector clocks
	// from the session's RunStart, so a resumed session or a second run would
	// otherwise report the session's age next to a live request. reqStartedAt
	// is the request clock (trySend), which is what the row is describing.
	if m.running && !m.reqStartedAt.IsZero() {
		if d := time.Since(m.reqStartedAt); d > 0 {
			meta.Elapsed = d
		}
	}
	variants := runtimeMetaVariants(meta)
	if fit, ok := firstFit(variants, avail, func(v string) string {
		return base + " · " + v
	}); ok {
		return fit
	}
	return base
}
