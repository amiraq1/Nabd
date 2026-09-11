package presentation

import (
	"strconv"
	"strings"

	"nabd/internal/agent"
)

type pendingRead struct {
	record agent.ReadRecord
	seq    int
}

type Projector struct {
	items               []FeedItem
	byID                map[string]int
	assistantIdx        int
	pendingReads        []pendingRead
	deniedCalls         map[string]bool
	UnhandledEventTypes map[agent.EventType]int
}

func NewProjector() *Projector {
	return &Projector{byID: map[string]int{}, assistantIdx: -1, deniedCalls: map[string]bool{}, UnhandledEventTypes: map[agent.EventType]int{}}
}

func (p *Projector) Build(events []agent.Event) ([]FeedItem, error) {
	p.reset()
	for i := range events {
		if err := p.Apply(events[i]); err != nil {
			return nil, err
		}
	}
	p.flushPendingReads()
	return p.Items(), nil
}

func (p *Projector) Apply(e agent.Event) error {
	switch e.Type {
	case agent.RunStart:
		return p.appendRunBoundary("start", e)
	case agent.RunEnd:
		p.flushPendingReads()
		return p.appendRunBoundary("end", e)
	case agent.UserMsg:
		return p.appendUserMsg(e)
	case agent.TextDelta:
		return p.appendTextDelta(e)
	case agent.TurnEnd:
		p.finalizeAssistant()
		p.flushPendingReads()
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
		p.flushPendingReads()
		return p.appendError(e)
	case agent.Interrupted:
		p.flushPendingReads()
		return p.appendInterrupted(e)
	case agent.EventRead:
		return p.appendReadRecord(e)
	case agent.Compact, agent.Rewind, agent.EventEdit, agent.EventCalib, agent.EventRateLimit, agent.EventProviderUsage:
		return nil
	case agent.EventProviderRoute:
		text, ok := FormatRouteNotice(e.Route)
		if !ok {
			return nil
		}
		return p.append(FeedItem{Type: ItemNotice, ID: "notice_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: text})
	default:
		if p.UnhandledEventTypes == nil {
			p.UnhandledEventTypes = map[agent.EventType]int{}
		}
		p.UnhandledEventTypes[e.Type]++
		return nil
	}
}

func (p *Projector) Items() []FeedItem {
	out := make([]FeedItem, len(p.items))
	for i := range p.items {
		it := p.items[i]
		if it.Tool != nil {
			card := *it.Tool
			if it.Tool.NextOffset != nil {
				next := *it.Tool.NextOffset
				card.NextOffset = &next
			}
			it.Tool = &card
		}
		if it.Perm != nil {
			perm := *it.Perm
			it.Perm = &perm
		}
		out[i] = it
	}
	sortBySeq(out)
	return out
}

func (p *Projector) reset() {
	p.items = nil
	p.byID = map[string]int{}
	p.assistantIdx = -1
	p.pendingReads = nil
	p.deniedCalls = map[string]bool{}
	p.UnhandledEventTypes = map[agent.EventType]int{}
}

func (p *Projector) appendRunBoundary(kind string, e agent.Event) error {
	p.finalizeAssistant()
	return p.append(FeedItem{Type: ItemRunBoundary, ID: "run_" + kind + "_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: e.Text, RunBoundary: kind})
}
func (p *Projector) appendUserMsg(e agent.Event) error {
	p.finalizeAssistant()
	return p.append(FeedItem{Type: ItemUserMsg, ID: "user_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: e.Text})
}
func (p *Projector) appendTextDelta(e agent.Event) error {
	if p.assistantIdx < 0 || p.assistantIdx >= len(p.items) || p.items[p.assistantIdx].Type != ItemAssistant {
		it := FeedItem{Type: ItemAssistant, ID: "asst_turn_" + strconv.Itoa(e.Seq), Seq: e.Seq}
		p.assistantIdx = len(p.items)
		_ = p.append(it)
	}
	p.items[p.assistantIdx].Text += e.Text
	return nil
}
func (p *Projector) finalizeAssistant() { p.assistantIdx = -1 }

func (p *Projector) appendToolStart(e agent.Event) error {
	card := &ToolCard{CallID: callID(e), Name: toolName(e), Args: callArgs(e.Call), Status: ToolRunning, OutputState: OutputNone}
	return p.append(FeedItem{Type: ItemTool, ID: toolID(e), Seq: e.Seq, Tool: card})
}

func (p *Projector) appendToolEnd(e agent.Event) error {
	id := toolID(e)
	idx, ok := p.byID[FeedItem{Type: ItemTool, ID: id}.key()]
	if !ok || idx < 0 || idx >= len(p.items) {
		card := &ToolCard{CallID: callID(e), Name: toolName(e), Args: callArgs(e.Call)}
		p.applyToolResult(card, e.Call)
		p.applyPendingRead(card)
		return p.append(FeedItem{Type: ItemTool, ID: id, Seq: e.Seq, Tool: card})
	}
	target := &p.items[idx]
	if target.Tool == nil {
		target.Tool = &ToolCard{CallID: callID(e), Name: toolName(e), Args: callArgs(e.Call)}
	}
	if target.Tool.CallID == "" {
		target.Tool.CallID = callID(e)
	}
	p.applyToolResult(target.Tool, e.Call)
	p.applyPendingRead(target.Tool)
	return nil
}

func (p *Projector) applyToolResult(card *ToolCard, call *agent.ToolCall) {
	if call == nil {
		card.Status = ToolFailed
		card.OutputState = OutputUnavailable
		card.Err = "tool result unavailable"
		return
	}
	if call.Name != "" {
		card.Name = call.Name
	}
	if card.Args == "" {
		card.Args = callArgs(call)
	}
	card.Status = ToolDone
	if !call.OK {
		if p.deniedCalls[call.ID] {
			card.Status = ToolDenied
		} else {
			card.Status = ToolFailed
		}
	}
	card.Output = call.Output
	card.OutputState = outputState(call.Output)
	card.Duration = call.MS
	card.ExitCode = call.Exit
	card.Signal = call.Signal
	card.Err = callErr(call.Output, call.OK)
}
func outputState(output string) OutputState {
	if output == "" {
		return OutputNone
	}
	if isTruncated(output) {
		return OutputTruncated
	}
	return OutputSaved
}

// EventRead has no CallID in the stable journal contract. Tool lifecycle pairing
// remains CallID-based; read metadata uses the production ordering/path only as
// a compatibility key and supports both pre- and post-ToolEnd archives.
func (p *Projector) appendReadRecord(e agent.Event) error {
	if e.Read == nil {
		return nil
	}
	if p.applyReadToLatest(*e.Read) {
		return nil
	}
	p.pendingReads = append(p.pendingReads, pendingRead{record: *e.Read, seq: e.Seq})
	return nil
}
func (p *Projector) applyReadToLatest(rec agent.ReadRecord) bool {
	for i := len(p.items) - 1; i >= 0; i-- {
		card := p.items[i].Tool
		if card == nil || card.Name != "read_file" || card.Truncated {
			continue
		}
		if rec.Path != "" && card.Args != "" && card.Args != rec.Path {
			continue
		}
		applyReadRecord(card, rec)
		return true
	}
	return false
}
func (p *Projector) applyPendingRead(card *ToolCard) {
	if card == nil || card.Name != "read_file" {
		return
	}
	for i := len(p.pendingReads) - 1; i >= 0; i-- {
		rec := p.pendingReads[i].record
		if rec.Path != "" && card.Args != "" && card.Args != rec.Path {
			continue
		}
		applyReadRecord(card, rec)
		p.pendingReads = append(p.pendingReads[:i], p.pendingReads[i+1:]...)
		return
	}
}
func applyReadRecord(card *ToolCard, rec agent.ReadRecord) {
	card.Truncated = rec.Truncated
	if rec.NextOffset > 0 {
		next := rec.NextOffset
		card.NextOffset = &next
	}
}
func (p *Projector) flushPendingReads() {
	for _, pending := range p.pendingReads {
		card := &ToolCard{CallID: "read_record_" + strconv.Itoa(pending.seq), Name: "read_file", Args: pending.record.Path, Status: ToolDone, OutputState: OutputUnavailable, Truncated: pending.record.Truncated}
		if pending.record.NextOffset > 0 {
			next := pending.record.NextOffset
			card.NextOffset = &next
		}
		_ = p.append(FeedItem{Type: ItemTool, ID: "tool_read_record_" + strconv.Itoa(pending.seq), Seq: pending.seq, Tool: card})
	}
	p.pendingReads = nil
}

func (p *Projector) appendPermAsk(e agent.Event) error {
	return p.append(FeedItem{Type: ItemPermission, ID: toolID(e), Seq: e.Seq, Perm: &PermCard{Name: toolName(e), Args: callArgs(e.Call), Status: PermAsked}})
}
func (p *Projector) appendPermReply(e agent.Event) error {
	id := toolID(e)
	raw := e.RawDecision
	if raw == agent.Deny && e.Decision != agent.Deny {
		raw = e.Decision
	}
	if e.Call != nil && raw == agent.Deny {
		p.deniedCalls[e.Call.ID] = true
	}
	idx, ok := p.byID[FeedItem{Type: ItemPermission, ID: id}.key()]
	if !ok || idx < 0 || idx >= len(p.items) {
		card := &PermCard{Name: toolName(e), Status: PermAllow, Decision: e.Decision, Effective: raw}
		if raw == agent.Deny {
			card.Status = PermDeny
		}
		return p.append(FeedItem{Type: ItemPermission, ID: id, Seq: e.Seq, Perm: card})
	}
	t := &p.items[idx]
	if t.Perm == nil {
		t.Perm = &PermCard{Name: toolName(e), Args: callArgs(e.Call)}
	}
	t.Perm.Decision = e.Decision
	t.Perm.Effective = raw
	t.Perm.Status = PermAllow
	if raw == agent.Deny {
		t.Perm.Status = PermDeny
	}
	return nil
}
func (p *Projector) appendNotice(e agent.Event) error {
	if e.Calib != nil {
		return nil
	}
	return p.append(FeedItem{Type: ItemNotice, ID: "notice_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: e.Text})
}
func (p *Projector) appendError(e agent.Event) error {
	return p.append(FeedItem{Type: ItemError, ID: "err_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: e.Err})
}
func (p *Projector) appendInterrupted(e agent.Event) error {
	for i := range p.items {
		if p.items[i].Tool != nil && p.items[i].Tool.Status == ToolRunning {
			p.items[i].Tool.Status = ToolCancelled
		}
	}
	text := e.Text
	if text == "" {
		text = "stopped"
	}
	return p.append(FeedItem{Type: ItemError, ID: "intr_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: text})
}
func (p *Projector) append(it FeedItem) error {
	p.byID[it.key()] = len(p.items)
	p.items = append(p.items, it)
	return nil
}
func callID(e agent.Event) string {
	if e.Call != nil {
		return e.Call.ID
	}
	return ""
}
func toolID(e agent.Event) string {
	if id := callID(e); id != "" {
		return "tool_" + id
	}
	return "tool_seq_" + strconv.Itoa(e.Seq)
}
func toolName(e agent.Event) string {
	if e.Call != nil && e.Call.Name != "" {
		return e.Call.Name
	}
	return "tool"
}
func callErr(output string, ok bool) string {
	if ok {
		return ""
	}
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "failed"
	}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if line == "" {
			continue
		}
		if len(line) > 200 {
			return line[:197] + "..."
		}
		return line
	}
	return "failed"
}
func isTruncated(s string) bool {
	return strings.HasSuffix(s, " bytes]") && strings.Contains(s, "...[truncated ")
}
