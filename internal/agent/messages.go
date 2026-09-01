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
func Messages(evs []Event) []provider.Message {
	var (
		out     []provider.Message
		text    strings.Builder
		calls   []provider.ToolCall
		results []provider.ToolResult
		open    = map[string]string{} // id -> name, still awaiting a result
	)

	flush := func() {
		// A tool_use with no tool_result poisons the next request on every
		// provider. If the branch was cut mid-turn, answer for the dead call.
		for id, name := range open {
			results = append(results, provider.ToolResult{
				ID: id, Output: "أُلغي: " + name, IsErr: true,
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
				Role: provider.User, Text: "ملخّص ما سبق من الجلسة:\n" + e.Text,
			})

		case Notice:
			// A human command that changed the world. The model must hear it,
			// or it will keep reasoning about an edit that no longer exists.
			flush()
			out = append(out, provider.Message{Role: provider.User, Text: "«نظام» " + e.Text})

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
