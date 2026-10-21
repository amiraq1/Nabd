package presentation

import (
	"strconv"
	"strings"

	"nabd/internal/event"
)

type pendingRead struct {
	record event.ReadRecord
	seq    int
}

type Projector struct {
	items               []FeedItem
	byID                map[ItemKey]int
	assistantIdx        int
	pendingReads        []pendingRead
	deniedCalls         map[string]bool
	UnhandledEventTypes map[event.EventType]int
	// touched records item keys created or mutated since the last
	// DrainTouched call. The UI uses it to re-fingerprint only the items an
	// incoming event batch actually changed, instead of hashing every
	// visible item's full text/output on every 20ms refresh (L15).
	touched map[ItemKey]bool
}

func NewProjector() *Projector {
	return &Projector{byID: map[ItemKey]int{}, assistantIdx: -1, deniedCalls: map[string]bool{}, UnhandledEventTypes: map[event.EventType]int{}}
}

func (p *Projector) Build(events []event.Event) ([]FeedItem, error) {
	p.reset()
	for i := range events {
		if err := p.Apply(events[i]); err != nil {
			return nil, err
		}
	}
	p.flushPendingReads()
	return p.Items(), nil
}

func (p *Projector) Apply(e event.Event) error {
	switch e.Type {
	case event.RunStart:
		return p.appendRunBoundary("start", e)
	case event.RunEnd:
		p.flushPendingReads()
		return p.appendRunBoundary("end", e)
	case event.UserMsg:
		return p.appendUserMsg(e)
	case event.TextDelta:
		return p.appendTextDelta(e)
	case event.TurnEnd:
		p.finalizeAssistant()
		p.flushPendingReads()
		return nil
	case event.ToolStart:
		return p.appendToolStart(e)
	case event.ToolEnd:
		return p.appendToolEnd(e)
	case event.PermAsk:
		return p.appendPermAsk(e)
	case event.PermReply:
		return p.appendPermReply(e)
	case event.Notice:
		return p.appendNotice(e)
	case event.RunError:
		p.flushPendingReads()
		return p.appendError(e)
	case event.Interrupted:
		p.flushPendingReads()
		return p.appendInterrupted(e)
	case event.EventRead:
		return p.appendReadRecord(e)
	case event.Compact, event.Rewind, event.EventEditIntent, event.EventEditAbort, event.EventEdit, event.EventCalib, event.EventRateLimit, event.EventProviderUsage:
		return nil
	case event.EventProviderRoute:
		text, ok := FormatRouteNotice(e.Route)
		if !ok {
			return nil
		}
		return p.append(FeedItem{Type: ItemNotice, ID: "notice_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: text})
	default:
		if p.UnhandledEventTypes == nil {
			p.UnhandledEventTypes = map[event.EventType]int{}
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
		if it.Error != nil {
			card := *it.Error
			it.Error = &card
		}
		out[i] = it
	}
	sortBySeq(out)
	return out
}

func (p *Projector) reset() {
	p.items = nil
	p.byID = map[ItemKey]int{}
	p.assistantIdx = -1
	p.pendingReads = nil
	p.deniedCalls = map[string]bool{}
	p.UnhandledEventTypes = map[event.EventType]int{}
	p.touched = nil
}

// touch records that the item with key was created or mutated. Every
// in-place mutation of p.items must go through here (append included), or
// the UI's fingerprint cache will serve stale lines for the item.
func (p *Projector) touch(key ItemKey) {
	if p.touched == nil {
		p.touched = make(map[ItemKey]bool)
	}
	p.touched[key] = true
}

// DrainTouched returns the keys of items created or mutated since the
// previous drain and clears the set. A nil return means nothing changed.
func (p *Projector) DrainTouched() []ItemKey {
	if len(p.touched) == 0 {
		return nil
	}
	out := make([]ItemKey, 0, len(p.touched))
	for k := range p.touched {
		out = append(out, k)
	}
	p.touched = make(map[ItemKey]bool)
	return out
}

func (p *Projector) appendRunBoundary(kind string, e event.Event) error {
	p.finalizeAssistant()
	return p.append(FeedItem{Type: ItemRunBoundary, ID: "run_" + kind + "_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: e.Text, RunBoundary: kind})
}
func (p *Projector) appendUserMsg(e event.Event) error {
	p.finalizeAssistant()
	return p.append(FeedItem{Type: ItemUserMsg, ID: "user_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: e.Text})
}
func (p *Projector) appendTextDelta(e event.Event) error {
	if p.assistantIdx < 0 || p.assistantIdx >= len(p.items) || p.items[p.assistantIdx].Type != ItemAssistant {
		it := FeedItem{Type: ItemAssistant, ID: "asst_turn_" + strconv.Itoa(e.Seq), Seq: e.Seq}
		p.assistantIdx = len(p.items)
		_ = p.append(it)
	}
	p.items[p.assistantIdx].Text += e.Text
	p.touch(p.items[p.assistantIdx].key())
	return nil
}
func (p *Projector) finalizeAssistant() { p.assistantIdx = -1 }

func (p *Projector) appendToolStart(e event.Event) error {
	card := &ToolCard{CallID: callID(e), Name: toolName(e), Args: callArgs(e.Call), Status: ToolRunning, OutputState: OutputNone}
	return p.append(FeedItem{Type: ItemTool, ID: toolID(e), Seq: e.Seq, Tool: card})
}

func (p *Projector) appendToolEnd(e event.Event) error {
	id := toolID(e)
	idx, ok := p.byID[FeedItem{Type: ItemTool, ID: id}.key()]
	if !ok || idx < 0 || idx >= len(p.items) {
		card := &ToolCard{CallID: callID(e), Name: toolName(e), Args: callArgs(e.Call)}
		p.applyToolResult(card, e.Call)
		p.applyPendingRead(card)
		return p.append(FeedItem{Type: ItemTool, ID: id, Seq: e.Seq, Tool: card})
	}
	target := &p.items[idx]
	p.touch(target.key())
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

func (p *Projector) applyToolResult(card *ToolCard, call *event.ToolCall) {
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
func (p *Projector) appendReadRecord(e event.Event) error {
	if e.Read == nil {
		return nil
	}
	if p.applyReadToLatest(*e.Read) {
		return nil
	}
	p.pendingReads = append(p.pendingReads, pendingRead{record: *e.Read, seq: e.Seq})
	return nil
}
func (p *Projector) applyReadToLatest(rec event.ReadRecord) bool {
	for i := len(p.items) - 1; i >= 0; i-- {
		card := p.items[i].Tool
		if card == nil || card.Name != "read_file" || card.Truncated {
			continue
		}
		if rec.Path != "" && card.Args != "" && card.Args != rec.Path {
			continue
		}
		applyReadRecord(card, rec)
		p.touch(p.items[i].key())
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
func applyReadRecord(card *ToolCard, rec event.ReadRecord) {
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

func (p *Projector) findToolCard(e event.Event) (int, *ToolCard) {
	id := toolID(e)
	if idx, ok := p.byID[FeedItem{Type: ItemTool, ID: id}.key()]; ok && idx >= 0 && idx < len(p.items) {
		return idx, p.items[idx].Tool
	}
	cid := callID(e)
	if cid != "" {
		for i := len(p.items) - 1; i >= 0; i-- {
			if card := p.items[i].Tool; card != nil && card.CallID == cid {
				return i, card
			}
		}
	}
	return -1, nil
}

func (p *Projector) appendPermAsk(e event.Event) error {
	if idx, card := p.findToolCard(e); card != nil {
		card.Status = ToolPending
		p.touch(p.items[idx].key())
	}
	return p.append(FeedItem{Type: ItemPermission, ID: toolID(e), Seq: e.Seq, Perm: &PermCard{Name: toolName(e), Args: callArgs(e.Call), Reason: PermissionReasonText(e), Status: PermAsked}})
}
func (p *Projector) appendPermReply(e event.Event) error {
	id := toolID(e)
	raw := e.RawDecision
	if raw == event.Deny && e.Decision != event.Deny {
		raw = e.Decision
	}
	effective := e.Decision
	isDeny := raw == event.Deny || effective == event.Deny
	if e.Call != nil && isDeny {
		p.deniedCalls[e.Call.ID] = true
	}
	if toolIdx, card := p.findToolCard(e); card != nil {
		if isDeny {
			card.Status = ToolDenied
		} else {
			card.Status = ToolRunning
		}
		p.touch(p.items[toolIdx].key())
	}
	idx, ok := p.byID[FeedItem{Type: ItemPermission, ID: id}.key()]
	if !ok || idx < 0 || idx >= len(p.items) {
		card := &PermCard{
			Name:      toolName(e),
			Reason:    PermissionReasonText(e),
			Status:    PermAllow,
			Decision:  raw,
			Effective: effective,
		}
		if raw == event.Deny || effective == event.Deny {
			card.Status = PermDeny
		}
		return p.append(FeedItem{Type: ItemPermission, ID: id, Seq: e.Seq, Perm: card})
	}
	t := &p.items[idx]
	p.touch(t.key())
	if t.Perm == nil {
		t.Perm = &PermCard{Name: toolName(e), Args: callArgs(e.Call)}
	}
	t.Perm.Decision = raw
	t.Perm.Effective = effective
	if reason := PermissionReasonText(e); reason != "" {
		t.Perm.Reason = reason
	}
	t.Perm.Status = PermAllow
	if raw == event.Deny || effective == event.Deny {
		t.Perm.Status = PermDeny
	}
	return nil
}
func (p *Projector) appendNotice(e event.Event) error {
	if e.Calib != nil {
		return nil
	}
	return p.append(FeedItem{Type: ItemNotice, ID: "notice_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: e.Text})
}
func (p *Projector) appendError(e event.Event) error {
	return p.append(FeedItem{Type: ItemError, ID: "err_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: e.Err, Error: ErrorCardFromEvent(e)})
}
func (p *Projector) appendInterrupted(e event.Event) error {
	for i := range p.items {
		if p.items[i].Tool != nil && p.items[i].Tool.Status == ToolRunning {
			p.items[i].Tool.Status = ToolCancelled
			p.touch(p.items[i].key())
		}
	}
	text := e.Text
	if text == "" {
		text = "stopped"
	}
	return p.append(FeedItem{Type: ItemError, ID: "intr_" + strconv.Itoa(e.Seq), Seq: e.Seq, Text: text, Error: NewErrorCard(event.ErrCodeCanceled, text, "")})
}
func (p *Projector) append(it FeedItem) error {
	p.byID[it.key()] = len(p.items)
	p.items = append(p.items, it)
	p.touch(it.key())
	return nil
}
func callID(e event.Event) string {
	if e.Call != nil {
		return e.Call.ID
	}
	return ""
}
func toolID(e event.Event) string {
	if id := callID(e); id != "" {
		return "tool_" + id
	}
	return "tool_seq_" + strconv.Itoa(e.Seq)
}
func toolName(e event.Event) string {
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
