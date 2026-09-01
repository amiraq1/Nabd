import re

with open("cmd/ag/main.go", "r") as f:
    content = f.read()

handlers_code = """
	chat.OnCtx = func() string {
		ms := agent.Squeeze(agent.Messages(agent.Live(loop.Hist())), agent.KeepFullRounds)
		p := loop.Budget.Pressure(ms)
		return fmt.Sprintf("السياق %d%% (%d / %d رمزا)", int(p*100), loop.Budget.Estimate(ms), loop.Budget.Usable())
	}
	chat.OnCompact = func() string {
		import_context := True
		// Wait, context is already imported in main.go
		if err := loop.Compact(context.Background(), loop.Budget.Usable()*4/10); err != nil {
			return err.Error()
		}
		return "ضُغط السياق يدويًا"
	}
"""

content = content.replace("_, err = tea.NewProgram(chat).Run()", handlers_code + "\n\t_, err = tea.NewProgram(chat).Run()")

# I need to pass Budget to Loop initialization
loop_init = "Gate:     gate{pol},"
loop_init_new = loop_init + "\n\t\tBudget:   agent.NewBudget(),"
content = content.replace(loop_init, loop_init_new)

with open("cmd/ag/main.go", "w") as f:
    f.write(content)
