// Package agent: messages.go is the only translation from journal to wire.
// It is pure: give it events, get messages. Nothing else may build history,
// or two code paths will drift and the model will see a session that never
// happened.
package agent

import (
	"strings"

	"nabd/internal/provider"
)

// Messages rebuilds provider history from a live branch. Feed it Live(),
// never the raw file: the raw file contains abandoned branches.
const maxPendingNotices = 32

func Messages(evs []Event) []provider.Message {
	var (
		out            []provider.Message
		text           strings.Builder
		calls          []provider.ToolCall
		results        []provider.ToolResult
		open           = map[string]string{} // id -> name, still awaiting a result
		pendingNotices []string
	)

	flush := func() {
		// A tool_use with no tool_result poisons the next request on every
		// provider. If the branch was cut mid-turn, answer for the dead call.
		for id, name := range open {
			results = append(results, provider.ToolResult{
				ID: id, Output: "cancelled: " + name, IsErr: true,
			})
			delete(open, id)
		}
		body := strings.TrimSpace(text.String())
		if body != "" || len(calls) > 0 {
			out = append(out, provider.Message{Role: provider.Assistant, Text: body, ToolCalls: calls})
		}
		if len(results) > 0 {
			out = append(out, provider.Message{Role: provider.User, ToolResults: results})
		}
		for _, n := range pendingNotices {
			out = append(out, provider.Message{Role: provider.User, Text: "«notice» " + n})
		}
		pendingNotices = nil
		text.Reset()
		calls, results = nil, nil
	}

	for _, e := range evs {
		switch e.Type {
		case UserMsg:
			flush()
			out = append(out, provider.Message{Role: provider.User, Text: e.Text})

		case Compact:
			flush()
			out = append(out, provider.Message{
				Role: provider.User, Text: "Session summary of what came before:\n" + e.Text,
			})

		case Notice:
			// A human command or system event that changed the world. The
			// model must hear it, or it will keep reasoning about an edit
			// that no longer exists. Framed as an event notice, NOT as a
			// system directive: this is a user-role message on every
			// provider, and pretending it is "system" would mislead the
			// model into treating a notice as an instruction.
			if len(open) > 0 || len(calls) > 0 {
				if len(pendingNotices) < maxPendingNotices {
					pendingNotices = append(pendingNotices, e.Text)
				} else {
					pendingNotices[maxPendingNotices-1] = "(notices truncated: cap reached)"
				}
				continue
			}
			flush()
			out = append(out, provider.Message{Role: provider.User, Text: "«notice» " + e.Text})

		case TextDelta:
			if len(results) > 0 { // results closed the previous round
				flush()
			}
			text.WriteString(e.Text)

		case ToolStart:
			if e.Call == nil {
				continue
			}
			if len(results) > 0 {
				flush()
			}
			calls = append(calls, provider.ToolCall{
				ID: e.Call.ID, Name: e.Call.Name, Input: e.Call.Args,
			})
			open[e.Call.ID] = e.Call.Name

		case ToolEnd:
			if e.Call == nil {
				continue
			}
			delete(open, e.Call.ID)
			results = append(results, provider.ToolResult{
				ID: e.Call.ID, Output: e.Call.Output, IsErr: !e.Call.OK,
			})

		case TurnEnd, Interrupted, RunError:
			flush()
		}
	}
	flush()
	return out
}
