package presentation

import (
	"strconv"

	"nabd/internal/agent"
)

// Projector turns a stream of agent events into a stable feed of UI items.
// It is pure: no I/O, no Bubble Tea, no styling. The UI reads Items().
type Projector struct {
	items []FeedItem

	// Internal indices for incremental assembly.
	byID         map[string]int // key -> index in items
	assistantIdx int            // index in items of current assistant message (-1 if none)
}

// NewProjector creates an empty projector.
func NewProjector() *Projector {
	return &Projector{
		byID:         map[string]int{},
		assistantIdx: -1,
	}
}

// Build reconstructs the whole feed from a complete event list. Use this
// when loading a journal from disk or after compaction.
func (p *Projector) Build(events []agent.Event) ([]FeedItem, error) {
	p.reset()
	for i := range events {
		if err := p.Apply(events[i]); err != nil {
			return nil, err
		}
	}
	return p.Items(), nil
}

// Apply incorporates one event into the feed. Idempotent on unknown events
// (they are skipped). The result is readable via Items().
func (p *Projector) Apply(e agent.Event) error {
	switch e.Type {
	case agent.RunStart:
		return p.appendRunBoundary("start", e)
	case agent.RunEnd:
		return p.appendRunBoundary("end", e)

	case agent.UserMsg:
		return p.appendUserMsg(e)

	case agent.TextDelta:
		return p.appendTextDelta(e)
	case agent.TurnEnd:
		// End of an assistant turn: finalize any in-progress message.
		p.finalizeAssistant()
		return nil

	case agent.ToolStart:
		return p.appendToolStart(e)
	case agent.ToolEnd:
		return p.appendToolEnd(e)

	case agent.PermAsk:
		return p.appendPermAsk(e)
	case agent.PermReply:
		return p.appendPermReply(e)

	case agent.Notice:
		return p.appendNotice(e)
	case agent.RunError:
		return p.appendError(e)
	case agent.Interrupted:
		return p.appendInterrupted(e)

	case agent.Compact, agent.Rewind, agent.EventEdit, agent.EventRead,
		agent.EventCalib, agent.EventRateLimit, agent.EventProviderUsage:
		// Journal/audit/operator-visible events: intentionally not shown in
		// the conversation feed. They live in the journal, not on the screen.
		return nil

	default:
		// Unknown type: skip rather than risk sending garbage to the model.
		return nil
	}
}

// Items returns the current feed. The returned slice is a copy so callers
// cannot mutate projector state. Order is stable and deterministic.
func (p *Projector) Items() []FeedItem {
	out := make([]FeedItem, len(p.items))
	copy(out, p.items)
	sortBySeq(out)
	return out
}

// reset clears all internal state.
func (p *Projector) reset() {
	p.items = nil
	p.byID = map[string]int{}
	p.assistantIdx = -1
}

// appendRunBoundary adds a run start/end marker.
func (p *Projector) appendRunBoundary(kind string, e agent.Event) error {
	p.finalizeAssistant()
	it := FeedItem{
		Type:        ItemRunBoundary,
		ID:          "run_" + kind + "_" + strconv.Itoa(e.Seq),
		Seq:         e.Seq,
		Text:        e.Text,
		RunBoundary: kind,
	}
	return p.append(it)
}

// appendUserMsg adds a user message item.
func (p *Projector) appendUserMsg(e agent.Event) error {
	p.finalizeAssistant()
	it := FeedItem{
		Type: ItemUserMsg,
		ID:   "user_" + strconv.Itoa(e.Seq),
		Seq:  e.Seq,
		Text: e.Text,
	}
	return p.append(it)
}

// appendTextDelta appends to the current assistant message, creating it if
// needed. Multiple deltas collapse into one FeedItem.
func (p *Projector) appendTextDelta(e agent.Event) error {
	if p.assistantIdx < 0 || p.assistantIdx >= len(p.items) || p.items[p.assistantIdx].Type != ItemAssistant {
		it := FeedItem{
			Type: ItemAssistant,
			ID:   "asst_turn_" + strconv.Itoa(e.Seq),
			Seq:  e.Seq,
		}
		p.assistantIdx = len(p.items)
		p.append(it)
	}
	p.items[p.assistantIdx].Text += e.Text
	return nil
}

// finalizeAssistant commits the current streaming assistant message to the
// feed and clears the streaming pointer.
func (p *Projector) finalizeAssistant() {
	p.assistantIdx = -1
}

// appendToolStart adds a tool card in running state.
func (p *Projector) appendToolStart(e agent.Event) error {
	id := toolID(e)
	card := &ToolCard{
		Name:   toolName(e),
		Args:   callArgs(e.Call),
		Status: ToolRunning,
	}
	it := FeedItem{
		Type: ItemTool,
		ID:   id,
		Seq:  e.Seq,
		Tool: card,
	}
	return p.append(it)
}

// appendToolEnd transitions the matching tool card to done/failed/denied.
func (p *Projector) appendToolEnd(e agent.Event) error {
	id := toolID(e)
	key := FeedItem{Type: ItemTool, ID: id}.key()
	idx, ok := p.byID[key]
	if !ok || idx < 0 || idx >= len(p.items) {
		// Orphan ToolEnd (old archive, --continue): synthesize the card.
		card := &ToolCard{
			Name:   toolName(e),
			Args:   callArgs(e.Call),
			Status: ToolDone,
		}
		it := FeedItem{Type: ItemTool, ID: id, Seq: e.Seq, Tool: card}
		return p.append(it)
	}
	target := &p.items[idx]
	if target.Tool == nil {
		target.Tool = &ToolCard{
			Name: toolName(e),
			Args: callArgs(e.Call),
		}
	}
	// Update status in place.
	switch {
	case e.Call != nil && !e.Call.OK:
		if e.Call.Exit != 0 || e.Call.Signal != "" {
			target.Tool.Status = ToolFailed
		} else {
			// Denied by permission gate.
			target.Tool.Status = ToolDenied
		}
	default:
		target.Tool.Status = ToolDone
	}
	if e.Call != nil {
		target.Tool.Output = e.Call.Output
		target.Tool.Duration = e.Call.MS
		target.Tool.ExitCode = e.Call.Exit
		target.Tool.Signal = e.Call.Signal
		target.Tool.Err = callErr(e.Call.Output, e.Call.OK)
		target.Tool.Truncated = len(e.Call.Output) > 0 && isTruncated(e.Call.Output)
	}
	return nil
}

// appendPermAsk adds a permission request card.
func (p *Projector) appendPermAsk(e agent.Event) error {
	id := toolID(e)
	card := &PermCard{
		Name:   toolName(e),
		Args:   callArgs(e.Call),
		Status: PermAsked,
	}
	it := FeedItem{
		Type: ItemPermission,
		ID:   id,
		Seq:  e.Seq,
		Perm: card,
	}
	return p.append(it)
}

// appendPermReply transitions the matching permission card.
func (p *Projector) appendPermReply(e agent.Event) error {
	id := toolID(e)
	key := FeedItem{Type: ItemPermission, ID: id}.key()
	idx, ok := p.byID[key]
	if !ok || idx < 0 || idx >= len(p.items) {
		// Synthesize a card for orphan reply.
		card := &PermCard{
			Name:      toolName(e),
			Status:    PermDeny,
			Decision:  e.Decision,
			Effective: e.RawDecision,
		}
		if e.RawDecision == agent.Deny {
			card.Status = PermDeny
		} else {
			card.Status = PermAllow
		}
		it := FeedItem{Type: ItemPermission, ID: id, Seq: e.Seq, Perm: card}
		return p.append(it)
	}
	target := &p.items[idx]
	if target.Perm == nil {
		target.Perm = &PermCard{
			Name: toolName(e),
			Args: callArgs(e.Call),
		}
	}
	target.Perm.Decision = e.Decision
	target.Perm.Effective = e.RawDecision
	switch {
	case e.RawDecision == agent.Deny:
		target.Perm.Status = PermDeny
	default:
		target.Perm.Status = PermAllow
	}
	return nil
}

// appendNotice adds a notice/status item.
func (p *Projector) appendNotice(e agent.Event) error {
	it := FeedItem{
		Type: ItemNotice,
		ID:   "notice_" + strconv.Itoa(e.Seq),
		Seq:  e.Seq,
		Text: e.Text,
	}
	return p.append(it)
}

// appendError adds a run error item.
func (p *Projector) appendError(e agent.Event) error {
	it := FeedItem{
		Type: ItemError,
		ID:   "err_" + strconv.Itoa(e.Seq),
		Seq:  e.Seq,
		Text: e.Err,
	}
	return p.append(it)
}

// appendInterrupted adds an interrupted item.
func (p *Projector) appendInterrupted(e agent.Event) error {
	text := e.Text
	if text == "" {
		text = "stopped"
	}
	it := FeedItem{
		Type: ItemError,
		ID:   "intr_" + strconv.Itoa(e.Seq),
		Seq:  e.Seq,
		Text: text,
	}
	return p.append(it)
}

// append adds an item to the feed and registers it in the index.
func (p *Projector) append(it FeedItem) error {
	p.byID[it.key()] = len(p.items)
	p.items = append(p.items, it)
	return nil
}

// toolID derives a stable id for a tool/permission event from the call id
// or, failing that, from the event seq.
func toolID(e agent.Event) string {
	if e.Call != nil && e.Call.ID != "" {
		return "tool_" + e.Call.ID
	}
	return "tool_seq_" + strconv.Itoa(e.Seq)
}

// toolName returns a human-readable tool name.
func toolName(e agent.Event) string {
	if e.Call != nil && e.Call.Name != "" {
		return e.Call.Name
	}
	return "tool"
}

// callErr extracts a concise error indication from a tool result.
func callErr(output string, ok bool) string {
	if ok {
		return ""
	}
	if output == "" {
		return "failed"
	}
	return "" // detailed output already in .Output
}

// isTruncated detects the truncation marker ForStore appends.
func isTruncated(s string) bool {
	return len(s) > 12 && s[len(s)-12:] == "truncated %d bytes]" // rough heuristic
}
