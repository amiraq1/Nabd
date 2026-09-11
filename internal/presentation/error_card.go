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
	Retryable    bool
	RetryScope   RetryScope
	JournalPath  string
	ToolExecuted bool
}

// ErrorCardFromEvent is a pure mapping from persisted facts. Empty legacy
// codes are unknown and are never inferred from message text.
func ErrorCardFromEvent(e agent.Event) *ErrorCard {
	code := agent.ErrorCode(e.ErrorCode)
	if code == "" {
		code = agent.ErrUnknown
	}
	return NewErrorCard(code, e.Err, e.JournalPath)
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
	case agent.ErrProviderTemporary:
		card.Title = "Could not reach provider"
		card.ActionText = "retry provider request"
		card.Retryable = true
		card.RetryScope = RetryProviderTurn
	case agent.ErrProviderAuth:
		card.Title = "Provider rejected authentication"
		card.ActionText = "check provider settings or key"
	case agent.ErrPersist:
		card.Title = "Session event was not saved"
		card.ActionText = "inspect the journal before continuing"
	case agent.ErrBudget:
		card.Title = "Run budget reached"
		card.ActionText = "start a new session or change the limit"
		card.RetryScope = RetryNewMessage
	case agent.ErrMaxTurnsCode:
		card.Title = "Maximum turns reached"
		card.ActionText = "send a shorter follow-up or rephrase"
		card.Retryable = true
		card.RetryScope = RetryNewMessage
	case agent.ErrCanceled:
		card.Title = "Run canceled"
		card.ActionText = "start a new message"
		card.RetryScope = RetryNewMessage
	default:
		card.Code = agent.ErrUnknown
		card.Title = "Unexpected error"
		card.ActionText = "review details before continuing"
	}
	return card
}
