package presentation

import (
	"fmt"
	"sort"
	"time"

	"nabd/internal/agent"
)

// SessionPhase describes the current user-visible phase of a run.
type SessionPhase string

const (
	PhaseIdle       SessionPhase = "idle"
	PhaseThinking   SessionPhase = "thinking"
	PhaseTool       SessionPhase = "tool"
	PhasePermission SessionPhase = "permission"
	PhaseCompacting SessionPhase = "compacting"
	PhaseCanceling  SessionPhase = "canceling"
	PhaseError      SessionPhase = "error"
	PhaseEnded      SessionPhase = "ended"
)

type ActiveTool struct {
	CallID    string
	Name      string
	Summary   string
	StartedAt time.Time
}

type UsageStatus struct {
	PromptTokens     int
	CompletionTokens int
	SpendUsed        *int64
	SpendLimit       *int64
	Complete         bool
}

type ErrorCode = agent.ErrorCode

const (
	ErrCodeProviderAuth    = agent.ErrCodeProviderAuth
	ErrCodePersist         = agent.ErrCodePersist
	ErrCodeBudget          = agent.ErrCodeBudget
	ErrCodeCanceled        = agent.ErrCodeCanceled
	ErrCodeEndpointRefused = agent.ErrCodeEndpointRefused
	ErrCodeUnknown         = agent.ErrCodeUnknown
)

const RemedyEndpointRefused = agent.RemedyEndpointRefused

type PresentedError struct {
	Code      ErrorCode
	Message   string
	Retryable bool
}

type SessionStatus struct {
	Phase       SessionPhase
	ActiveTools []ActiveTool
	Turn        int
	TurnLimit   int
	Usage       UsageStatus
	CanCancel   bool
	CanRetry    bool
	LastError   *PresentedError
}

type SessionView struct {
	Items  []FeedItem
	Status SessionStatus
}

// StatusProjector incrementally derives user-facing status from events. It is
// deliberately separate from the feed projector so transient state never gets
// persisted as a second source of truth.
//
// The route/timing fields below back RuntimeMeta (see status_meta.go). They are
// unexported on purpose: SessionStatus is compared field-by-field in tests and
// by callers, so presentation-only metadata must not widen that struct.
type StatusProjector struct {
	status SessionStatus
	tools  map[string]ActiveTool

	provider  string
	model     string
	startedAt time.Time
	endedAt   time.Time
}

func NewStatusProjector() *StatusProjector {
	return &StatusProjector{status: SessionStatus{Phase: PhaseIdle}, tools: map[string]ActiveTool{}}
}

func (p *StatusProjector) Reset() {
	p.status = SessionStatus{Phase: PhaseIdle}
	p.tools = map[string]ActiveTool{}
	p.provider = ""
	p.model = ""
	p.startedAt = time.Time{}
	p.endedAt = time.Time{}
}

func (p *StatusProjector) Apply(e agent.Event) {
	if p.tools == nil {
		p.Reset()
	}
	switch e.Type {
	case agent.RunStart:
		p.status.Phase = PhaseIdle
		p.status.LastError = nil
		p.startedAt = e.Time
		p.endedAt = time.Time{}
	case agent.TurnStart:
		p.status.Turn++
		p.status.Phase = PhaseThinking
		p.status.CanCancel = true
		if p.startedAt.IsZero() {
			p.startedAt = e.Time
		}
	case agent.ToolStart:
		if e.Call != nil {
			p.tools[e.Call.ID] = ActiveTool{CallID: e.Call.ID, Name: e.Call.Name, Summary: summarizeCall(e.Call), StartedAt: e.Time}
		}
		p.status.Phase = PhaseTool
		p.status.CanCancel = true
	case agent.ToolEnd:
		if e.Call != nil {
			delete(p.tools, e.Call.ID)
			if !e.Call.OK {
				p.status.LastError = &PresentedError{Code: ErrCodeUnknown, Message: e.Call.Output, Retryable: false}
			}
		}
		p.status.Phase = phaseAfterTools(p.tools)
	case agent.PermAsk:
		p.status.Phase = PhasePermission
		p.status.CanCancel = true
	case agent.PermReply:
		p.status.Phase = phaseAfterTools(p.tools)
	case agent.Compact:
		p.status.Phase = PhaseCompacting
	case agent.Interrupted:
		p.status.Phase = PhaseCanceling
		p.status.LastError = &PresentedError{Code: ErrCodeCanceled, Message: "run canceled", Retryable: true}
		p.endedAt = e.Time
	case agent.RunError:
		code := ErrorCode(e.ErrorCode)
		if code == "" {
			code = ErrCodeUnknown
		}
		p.status.LastError = &PresentedError{Code: code, Message: e.Err, Retryable: code != ErrCodePersist}
		p.status.Phase = PhaseError
		p.endedAt = e.Time
	case agent.RunEnd:
		p.status.Phase = PhaseEnded
		p.status.CanCancel = false
		p.endedAt = e.Time
	case agent.EventProviderUsage:
		if e.Usage != nil {
			p.status.Usage.PromptTokens = e.Usage.PromptTokens
			p.status.Usage.CompletionTokens = e.Usage.CompletionTokens
			p.status.Usage.Complete = true
		}
	case agent.EventProviderRoute:
		// Only a committed route is reported. "attempted", "failed", "waiting",
		// and "blocked" describe routes that did not serve the request, so
		// showing them as the current provider would be a false claim.
		if e.Route != nil && e.Route.Status == "selected" && e.Route.Provider != "" {
			p.provider = e.Route.Provider
			p.model = e.Route.Model
		}
	}
	p.status.ActiveTools = p.activeTools()
}

func (p *StatusProjector) Build(events []agent.Event) SessionStatus {
	p.Reset()
	for _, e := range events {
		p.Apply(e)
	}
	return p.Status()
}

func (p *StatusProjector) Status() SessionStatus {
	out := p.status
	out.ActiveTools = append([]ActiveTool(nil), p.status.ActiveTools...)
	if p.status.LastError != nil {
		err := *p.status.LastError
		out.LastError = &err
	}
	return out
}

func (p *StatusProjector) activeTools() []ActiveTool {
	out := make([]ActiveTool, 0, len(p.tools))
	for _, tool := range p.tools {
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CallID < out[j].CallID })
	return out
}

func phaseAfterTools(tools map[string]ActiveTool) SessionPhase {
	if len(tools) > 0 {
		return PhaseTool
	}
	return PhaseThinking
}

func summarizeCall(c *agent.ToolCall) string {
	if c == nil || len(c.Args) == 0 {
		return ""
	}
	return string(c.Args)
}

func (s SessionStatus) String() string {
	if len(s.ActiveTools) > 0 {
		return fmt.Sprintf("%s · %s", s.Phase, s.ActiveTools[0].Name)
	}
	return string(s.Phase)
}
