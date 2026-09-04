// Package agent: messages.go is the only translation from journal to wire.
// It is pure: give it events, get messages. Nothing else may build history,
// or two code paths will drift and the model will see a session that never
// happened.
package agent

import (
	"log/slog"
	"os"
	"strings"

	"nabd/internal/provider"
)

// Messages rebuilds provider history from a live branch. Feed it Live(),
// never the raw file: the raw file contains abandoned branches.
const maxPendingNotices = 32

// toolResultItem is a result still waiting to be flushed with its pairing
// metadata. name lets the consumer reconstruct an unknown tool's identity
// from an orphan ToolEnd; there is deliberately no path field — read
// de-duplication (FIX 4) collapsed consecutive same-path reads and dropped
// the first slice, which lost data (README 54-87, session 62).
type toolResultItem struct {
	result provider.ToolResult
	name   string
}

func Messages(evs []Event) []provider.Message {
	var (
		out            []provider.Message
		text           strings.Builder
		calls          []provider.ToolCall
		toolResults    []toolResultItem
		open           = map[string]string{} // id -> name, still awaiting a result
		openOrder      []string              // journal insertion order of open ids; map iteration is not deterministic
		pendingNotices []string
	)

	// idSeen tracks which tool_use IDs have already been appended to calls
	// in the current round. A duplicated tool_call_id breaks the provider's
	// pairing invariant (two tool_results answered by one ID), so each ID
	// must appear at most once per Messages() message.
	idSeen := map[string]bool{}

	// appendUniqueCall adds a tool call and reports whether it was actually
	// new. If the same ID was already registered this round we still emit a
	// ToolStart event (the journal says it happened) but we do not duplicate
	// the call in the message; the result block keeps a single match.
	appendUniqueCall := func(c provider.ToolCall) bool {
		if idSeen[c.ID] {
			return false
		}
		idSeen[c.ID] = true
		calls = append(calls, c)
		return true
	}

	flush := func() {
		// A tool_use with no tool_result poisons the next request on every
		// provider. If the branch was cut mid-turn, answer for the dead call.
		// Iterate openOrder (not the map) so the synthetic results keep the
		// journal's original call order — map iteration in Go is randomized.
		for _, id := range openOrder {
			name, ok := open[id]
			if !ok {
				continue
			}
			toolResults = append(toolResults, toolResultItem{
				result: provider.ToolResult{
					ID: id, Output: "cancelled: " + name, IsErr: true,
				},
				name: name,
			})
			delete(open, id)
		}
		open = map[string]string{}
		openOrder = nil

		var results []provider.ToolResult
		for _, tr := range toolResults {
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
		idSeen = map[string]bool{}
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
			appendUniqueCall(provider.ToolCall{
				ID: ev.Call.ID, Name: ev.Call.Name, Input: ev.Call.Args,
			})
			if _, ok := open[ev.Call.ID]; !ok {
				openOrder = append(openOrder, ev.Call.ID)
			}
			open[ev.Call.ID] = ev.Call.Name

		case ToolEnd:
			if ev.Call == nil {
				continue
			}
			// A matching ToolStart ends here: clear the open entry.
			// ToolEnd without a name (old archives, an unknown tool whose
			// start was never journaled) falls back to the open-name if
			// one is recorded, else the generic marker. The output is never
			// dropped: hiding the error text would conceal from the model
			// that the tool is unknown, and it would keep re-invoking it.
			name := ev.Call.Name
			if name == "" {
				if n, ok := open[ev.Call.ID]; ok {
					name = n
				} else {
					name = "unknown"
				}
			}
			if _, ok := open[ev.Call.ID]; !ok && ev.Call.ID != "" {
				appendUniqueCall(provider.ToolCall{
					ID: ev.Call.ID, Name: name, Input: ev.Call.Args,
				})
			}
			delete(open, ev.Call.ID)
			toolResults = append(toolResults, toolResultItem{
				result: provider.ToolResult{
					ID: ev.Call.ID, Output: ev.Call.Output, IsErr: !ev.Call.OK,
				},
				name: name,
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
			// garbage to the model. A log line (silenced outside debug)
			// helps catch future bugs without corrupting the TUI.
			debugWarn("journal/Messages: unknown event type", "type", ev.Type, "seq", ev.Seq)
		}
	}
	flush()
	return out
}

// debugWarn emits a slog warning only when NABD_DEBUG is set. The default
// path must never write to stderr under bubbletea: a stray log line would
// corrupt the rendered frame.
func debugWarn(msg string, args ...any) {
	if os.Getenv("NABD_DEBUG") != "" {
		slog.Warn(msg, args...)
	}
}
