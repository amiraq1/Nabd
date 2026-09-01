import re

with open("internal/agent/loop.go", "r") as f:
    content = f.read()

# 1. Add fields to Loop
content = content.replace("Gate     Gate\n\tHuman    Asker", "Gate     Gate\n\tHuman    Asker\n\tBudget   *Budget\n\twarned   bool")

# 2. Update Run method to calculate pressure and pass ms to streamTurn
run_loop = """	for turn := 0; turn < maxTurns; turn++ {
		calls, stop, err := l.streamTurn(ctx)"""

run_loop_new = """	for turn := 0; turn < maxTurns; turn++ {
		ms := Squeeze(Messages(Live(l.hist)), KeepFullRounds)
		if p := l.Budget.Pressure(ms); p > 0.75 {
			if err := l.Compact(ctx, l.Budget.Usable()*4/10); err != nil {
				l.emit(Event{Type: Notice, Text: "تعذّر الضغط: " + err.Error()})
			} else {
				ms = Squeeze(Messages(Live(l.hist)), KeepFullRounds)
				l.emit(Event{Type: Notice, Text: fmt.Sprintf("ضُغط السياق · %d%% ← %d%%",
					int(p*100), int(l.Budget.Pressure(ms)*100))})
				l.warned = false
			}
		} else if p > 0.6 && !l.warned {
			l.warned = true
			l.emit(Event{Type: Notice, Text: fmt.Sprintf("السياق %d%%", int(p*100))})
		}

		calls, stop, err := l.streamTurn(ctx, ms)"""

content = content.replace(run_loop, run_loop_new)

# 3. Update streamTurn signature and usage of ms
content = content.replace("func (l *Loop) streamTurn(ctx context.Context) ([]provider.ToolCall, string, error) {", "func (l *Loop) streamTurn(ctx context.Context, ms []provider.Message) ([]provider.ToolCall, string, error) {")
content = content.replace("Messages(Live(l.hist))", "ms")

with open("internal/agent/loop.go", "w") as f:
    f.write(content)

