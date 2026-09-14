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
	variants := runtimeMetaVariants(meta)
	if fit, ok := firstFit(variants, avail, func(v string) string {
		return base + " · " + v
	}); ok {
		return fit
	}
	return base
}
