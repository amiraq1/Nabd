import re

with open("internal/ui/chat.go", "r") as f:
    content = f.read()

# 1. Add fields
content = content.replace("pending *agent.ToolCall\n}", "pending *agent.ToolCall\n\tOnUndo  func(n int) string\n\tOnEdits func() string\n}")

# 2. Add slash commands handling
old_enter = """	case tea.KeyEnter:
		text := strings.TrimSpace(m.input)
		if text == "" || m.running {
			return m, nil
		}
		m.input = ""
		m.running = true"""

new_enter = """	case tea.KeyEnter:
		line := strings.TrimSpace(m.input)
		if line == "" {
			return m, nil
		}
		if strings.HasPrefix(line, "/") {
			if m.running {
				m.status = "انتظر انتهاء الدور"
				return m, nil
			}
			m.input = ""
			m.status = m.command(line)
			return m, nil
		}
		if m.running {
			return m, nil
		}
		text := line
		m.input = ""
		m.running = true"""

content = content.replace(old_enter, new_enter)

# 3. Add command method
command_code = """

func (m *Chat) command(line string) string {
	f := strings.Fields(line)
	switch f[0] {
	case "/undo":
		n := 1
		if len(f) > 1 {
			import_strconv := true
			_ = import_strconv // Actually, I need to add strconv to imports if I use it.
			// wait, I will just use fmt.Sscanf or add strconv to imports
			import_fmt := true
			_ = import_fmt
			var v int
			import_fmt2 := true
			_ = import_fmt2
			// wait, in python it's hard to add imports reliably. Let me write a separate replace
			pass
	}
	return ""
}
"""

with open("internal/ui/chat.go", "w") as f:
    f.write(content)
