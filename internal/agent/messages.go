// Package agent: messages.go is the only translation from journal to wire.
// It is pure: give it events, get messages. Nothing else may build history,
// or two code paths will drift and the model will see a session that never
// happened.
package agent

import (
	"log/slog"
	"strings"

	"nabd/internal/provider"
)

// Messages rebuilds provider history from a live branch. Feed it Live(),
// never the raw file: the raw file contains abandoned branches.
const maxPendingNotices = 32

type toolResultItem struct {
	result provider.ToolResult
	name   string
	path   string
}

func Messages(evs []Event) []provider.Message {
	var (
		out            []provider.Message
		text           strings.Builder
		calls          []provider.ToolCall
		toolResults    []toolResultItem
		callPaths      = map[string]string{}
		open           = map[string]string{} // id -> name, still awaiting a result
		pendingNotices []string
	)

	flush := func() {
		// A tool_use with no tool_result poisons the next request on every
		// provider. If the branch was cut mid-turn, answer for the dead call.
		for id, name := range open {
			toolResults = append(toolResults, toolResultItem{
				result: provider.ToolResult{
					ID: id, Output: "cancelled: " + name, IsErr: true,
				},
				name: name,
				path: callPaths[id],
			})
			delete(open, id)
		}

		// After collecting all toolResult messages, deduplicate:
		// If consecutive results call the same tool on the same path,
		// keep only the last one — earlier reads are subsets.
		deduped := toolResults[:0]
		keptIDs := make(map[string]bool)
		for i, tr := range toolResults {
			if i+1 < len(toolResults) &&
				tr.name == toolResults[i+1].name &&
				tr.path != "" &&
				tr.path == toolResults[i+1].path {
				continue // superseded by the next result for the same file
			}
			deduped = append(deduped, tr)
			keptIDs[tr.result.ID] = true
		}

		// Keep matching tool calls so provider API pairing invariants remain sound.
		if len(deduped) < len(toolResults) && len(calls) > 0 {
			dedupedCalls := calls[:0]
			for _, c := range calls {
				if keptIDs[c.ID] {
					dedupedCalls = append(dedupedCalls, c)
				}
			}
			calls = dedupedCalls
		}

		var results []provider.ToolResult
		for _, tr := range deduped {
			results = append(results, tr.result)
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
		calls, toolResults = nil, nil
	}

	for _, ev := range evs {
		switch ev.Type {
		case UserMsg:
			flush()
			out = append(out, provider.Message{Role: provider.User, Text: ev.Text})

		case Compact:
			flush()
			out = append(out, provider.Message{
				Role: provider.User, Text: "Session summary of what came before:\n" + ev.Text,
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
					pendingNotices = append(pendingNotices, ev.Text)
				} else {
					pendingNotices[maxPendingNotices-1] = "(notices truncated: cap reached)"
				}
				continue
			}
			flush()
			out = append(out, provider.Message{Role: provider.User, Text: "«notice» " + ev.Text})

		case TextDelta:
			if len(toolResults) > 0 { // results closed the previous round
				flush()
			}
			text.WriteString(ev.Text)

		case ToolStart:
			if ev.Call == nil {
				continue
			}
			if len(toolResults) > 0 {
				flush()
			}
			calls = append(calls, provider.ToolCall{
				ID: ev.Call.ID, Name: ev.Call.Name, Input: ev.Call.Args,
			})
			open[ev.Call.ID] = ev.Call.Name
			if p := pathOf(ev.Call.Args); p != "" {
				callPaths[ev.Call.ID] = p
			}

		case ToolEnd:
			if ev.Call == nil {
				continue
			}
			delete(open, ev.Call.ID)
			p := pathOf(ev.Call.Args)
			if p == "" {
				p = callPaths[ev.Call.ID]
			}
			name := ev.Call.Name
			if name == "" {
				name = open[ev.Call.ID]
			}
			toolResults = append(toolResults, toolResultItem{
				result: provider.ToolResult{
					ID: ev.Call.ID, Output: ev.Call.Output, IsErr: !ev.Call.OK,
				},
				name: name,
				path: p,
			})

		case TurnEnd, Interrupted, RunError:
			flush()

		case EventRateLimit:
			// Rate-limit events are operator-visible only; they must not
			// reach the model or they would pollute the conversation with
			// infrastructure noise.
			continue

		case RunStart, TurnStart, PermAsk, PermReply, Rewind, EventEdit, EventRead, EventCalib:
			// Known journal/audit events that produce no model messages.
			continue

		default:
			// Unknown event type — skip it rather than risk sending
			// garbage to the model. A log line helps catch future bugs.
			slog.Warn("journal/Messages: unknown event type", "type", ev.Type, "seq", ev.Seq)
		}
	}
	flush()
	return out
}
