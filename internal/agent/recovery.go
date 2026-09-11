package agent

import "context"

// RetryProvider retries only the provider turn against the already-journaled
// conversation. It never replays a previous tool call. Any newly requested
// tool goes through runCalls and therefore the permission gate again.
func (l *Loop) RetryProvider(ctx context.Context) error {
	ms := Squeeze(Messages(Live(l.Hist())), l.keepFullRounds())
	calls, _, err := l.streamTurn(ctx, ms)
	if err != nil {
		_ = l.emit(RunErrorEvent(err))
		return err
	}
	if len(calls) == 0 {
		return nil
	}
	interrupted, err := l.runCalls(ctx, calls)
	if err != nil {
		_ = l.emit(RunErrorEvent(err))
		return err
	}
	if interrupted {
		_ = l.emit(Event{Type: Interrupted, Text: "ctrl+c"})
	}
	return nil
}
