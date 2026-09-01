import re

with open("internal/agent/event.go", "r") as f:
    content = f.read()

# 1. Update ToolCall
old_toolcall = """type ToolCall struct {
	ID     string
	Name   string
	Args   json.RawMessage
	Output string
	OK     bool
}"""

new_toolcall = """type ToolCall struct {
	ID     string
	Name   string
	Args   json.RawMessage
	Output string
	OK     bool
	Exit   int
	Signal string
	MS     int64
}"""

content = content.replace(old_toolcall, new_toolcall)

# 2. Add Outcome
outcome_code = """
// Outcome is what a tool run produced. Text-only tools ignore the extra
// fields; a process cannot, because "failed" and "killed at 120s" are
// different facts and the model must be able to tell them apart.
type Outcome struct {
	Text   string
	OK     bool
	Exit   int
	Signal string
}
"""
content += outcome_code

with open("internal/agent/event.go", "w") as f:
    f.write(content)
