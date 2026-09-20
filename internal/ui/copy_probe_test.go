package ui

import (
	"bytes"
	"testing"
)

func TestCopyProbe(t *testing.T) {
	f := NewFeed()
	var buf bytes.Buffer
	f.SetClipboardWriter(&buf)
	f.lines = []string{"alpha", "beta"}
	f.copyFullReport()
	t.Logf("status=%q bytes=%d", f.status, buf.Len())
}
