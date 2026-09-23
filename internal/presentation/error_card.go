package presentation

import "nabd/internal/agent"

type RetryScope string

const (
	RetryNone         RetryScope = "none"
	RetryProviderTurn RetryScope = "provider_turn"
	RetryNewMessage   RetryScope = "new_message"
)

type ErrorCard struct {
	Code         agent.ErrorCode
	Title        string
	Message      string
	ActionText   string
	Remedy       string
	Retryable    bool
	RetryScope   RetryScope
	JournalPath  string
	ToolExecuted bool
	// WaitSeconds is the router's shortest positive retry-after, in seconds
	// (0 = none reported). It is the one number the reader acts on, so the card
	// states it on its own line at every width instead of leaving it inside
	// Message, which the width ladder hides below 40 columns and truncates
	// above that.
	WaitSeconds float64
}

// ErrorCardFromEvent is a pure mapping from persisted facts. Empty legacy
// codes are unknown and are never inferred from message text.
func ErrorCardFromEvent(e agent.Event) *ErrorCard {
	code := agent.ErrorCode(e.ErrorCode)
	if code == "" {
		code = agent.ErrCodeUnknown
	}
	card := NewErrorCard(code, e.Err, e.JournalPath)
	card.WaitSeconds = e.RetryAfter
	return card
}

func ErrorCardFromError(err error) *ErrorCard {
	if err == nil {
		return nil
	}
	return NewErrorCard(agent.ErrorCodeOf(err), err.Error(), agent.JournalPathOf(err))
}

func NewErrorCard(code agent.ErrorCode, message, journalPath string) *ErrorCard {
	card := &ErrorCard{Code: code, Message: message, JournalPath: journalPath, RetryScope: RetryNone}
	switch code {
	case agent.ErrCodeProviderTemporary:
		card.Title = "Could not reach provider"
		card.ActionText = "retry provider request"
		card.Retryable = true
		card.RetryScope = RetryProviderTurn
	case agent.ErrCodeProviderAuth:
		card.Title = "Provider rejected authentication"
		card.ActionText = "check provider settings or key"
	case agent.ErrCodePersist:
		card.Title = "Session event was not saved"
		card.ActionText = "inspect the journal before continuing"
	case agent.ErrCodeBudget:
		card.Title = "Run budget reached"
		card.ActionText = "start a new session or change the limit"
		card.RetryScope = RetryNewMessage
	case agent.ErrCodeMaxTurns:
		card.Title = "Maximum turns reached"
		card.ActionText = "send a shorter follow-up or rephrase"
		card.Retryable = true
		card.RetryScope = RetryNewMessage
	case agent.ErrCodeCanceled:
		card.Title = "Run canceled"
		card.ActionText = "start a new message"
		card.RetryScope = RetryNewMessage
	case agent.ErrCodeEndpointRefused:
		card.Title = "Endpoint refused by policy"
		card.ActionText = "review endpoint policy or proxy settings"
		card.Remedy = RemedyEndpointRefused
	default:
		card.Code = agent.ErrCodeUnknown
		card.Title = "Unexpected error"
		card.ActionText = "review details before continuing"
	}
	return card
}
