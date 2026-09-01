import re

with open("internal/ui/chat.go", "r") as f:
    content = f.read()

# Change NewChat to return *Chat
content = content.replace("func NewChat(r Runner, events <-chan agent.Event) Chat {", "func NewChat(r Runner, events <-chan agent.Event) *Chat {")
content = content.replace("return Chat{runner:", "return &Chat{runner:")

# Change receivers from (m Chat) to (m *Chat)
content = re.sub(r'func \(m Chat\) (Init|Update|key|View)', r'func (m *Chat) \1', content)

# Add OnRewind field
content = content.replace("OnUndo  func(n int) string", "OnUndo  func(n int) string\n\tOnRewind func(n int) string")

# Add SetInput method
set_input_code = """
func (m *Chat) SetInput(s string) {
	m.input = s
}
"""
content += set_input_code

# Add /rewind case in command
rewind_case = """	case "/rewind":
		n := 1
		if len(f) > 1 {
			if v, err := strconv.Atoi(f[1]); err == nil && v > 0 {
				n = v
			}
		}
		if m.OnRewind == nil {
			return "لا رجوع في هذه النسخة"
		}
		return m.OnRewind(n)
"""
content = content.replace('switch f[0] {\n\tcase "/undo":', 'switch f[0] {\n' + rewind_case + '\tcase "/undo":')

with open("internal/ui/chat.go", "w") as f:
    f.write(content)

