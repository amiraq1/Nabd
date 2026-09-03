package ui

import (
	"strings"

	"nabd/internal/agent"
)

// Renderer turns a stream of events into scrollback lines. It exists for
// one reason: providers emit text_delta a few runes at a time, and printing
// each delta on its own line turns a two-sentence answer into thirty
// shards. The renderer buffers text and flushes it as one block the moment
// something that is not text arrives.
//
// Live and replay both go through here, so a session replays exactly as it
// was seen. Everything else is delegated to RenderEvent, unchanged.
type Renderer struct {
	Width int
	text  strings.Builder
}

// Feed consumes one event and returns what should go to the scrollback:
// possibly a flushed text block, possibly the event's own line, possibly
// nothing. Text deltas return "" and accumulate.
func (r *Renderer) Feed(e agent.Event) string {
	if e.Type == agent.TextDelta {
		r.text.WriteString(e.Text)
		return ""
	}
	out := r.Flush()
	if s := RenderEvent(e, r.width()); s != "" {
		if out != "" {
			out += "\n"
		}
		out += s
	}
	return out
}

// Flush returns the buffered text as one rendered block and clears it.
func (r *Renderer) Flush() string {
	s := strings.TrimSpace(r.text.String())
	r.text.Reset()
	if s == "" {
		return ""
	}
	return RenderEvent(agent.Event{Type: agent.TextDelta, Text: s}, r.width())
}

// Partial shows the tail of the text still streaming, at most n wrapped
// lines, for the live view. A phone screen cannot hold a whole answer and
// the scrollback gets the full thing at flush time anyway.
func (r *Renderer) Partial(n int) string {
	s := strings.TrimSpace(r.text.String())
	if s == "" {
		return ""
	}
	lines := wrap(s, r.width()-2)
	more := false
	if len(lines) > n {
		lines = lines[len(lines)-n:]
		more = true
	}
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i == 0 && more {
			b.WriteString("… ")
		} else if i == 0 {
			b.WriteString("  ")
		} else {
			b.WriteString("  ")
		}
		b.WriteString(l)
	}
	return b.String()
}

// Pending reports whether text is buffered and not yet flushed.
func (r *Renderer) Pending() bool { return r.text.Len() > 0 }

func (r *Renderer) width() int {
	if r.Width < 20 {
		return DefaultWidth
	}
	return r.Width
}
