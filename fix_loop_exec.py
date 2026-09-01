import re

with open("internal/agent/loop.go", "r") as f:
    content = f.read()

# Modify l.exec definition
old_exec = """func (l *Loop) exec(ctx context.Context, c provider.ToolCall) (out string, ok bool, err error) {
	if l.Tools == nil {
		return "", false, fmt.Errorf("لا أدوات في هذه النسخة: %s", c.Name)
	}
	defer func() {
		if r := recover(); r != nil {
			out, ok, err = "", false, fmt.Errorf("panic في %s: %v", c.Name, r)
		}
	}()
	return l.Tools.Run(ctx, c)
}"""

new_exec = """func (l *Loop) exec(ctx context.Context, c provider.ToolCall) (out Outcome, err error) {
	if l.Tools == nil {
		return Outcome{OK: false}, fmt.Errorf("لا أدوات في هذه النسخة: %s", c.Name)
	}
	defer func() {
		if r := recover(); r != nil {
			out = Outcome{OK: false}
			err = fmt.Errorf("panic في %s: %v", c.Name, r)
		}
	}()
	if d, ok := l.Tools.(interface {
		RunDetailed(context.Context, string, []byte) (Outcome, error)
	}); ok {
		return d.RunDetailed(ctx, c.Name, c.Input)
	}
	txt, good, e := l.Tools.Run(ctx, c)
	return Outcome{Text: txt, OK: good}, e
}"""

content = content.replace(old_exec, new_exec)

# Modify call site in runCalls
old_call = """		out, ok, err := l.exec(ctx, c)
		if err != nil {
			out, ok = err.Error(), false
		}

		results = append(results, provider.ToolResult{ID: c.ID, Output: out, IsErr: !ok})
		if eerr := l.emit(Event{Type: ToolEnd, Call: &ToolCall{
			ID: c.ID, Name: c.Name, Output: out, OK: ok,
		}}); eerr != nil {"""

new_call = """		import_time := True
		start := time.Now()
		out, err := l.exec(ctx, c)
		if err != nil {
			out.Text, out.OK = err.Error(), false
		}
		ms := time.Since(start).Milliseconds()

		results = append(results, provider.ToolResult{ID: c.ID, Output: out.Text, IsErr: !out.OK})
		if eerr := l.emit(Event{Type: ToolEnd, Call: &ToolCall{
			ID: c.ID, Name: c.Name, Output: out.Text, OK: out.OK,
			Exit: out.Exit, Signal: out.Signal, MS: ms,
		}}); eerr != nil {"""

content = content.replace(old_call, new_call)
with open("internal/agent/loop.go", "w") as f:
    f.write(content)
