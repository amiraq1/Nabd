import sys

with open("cmd/ag/main.go", "r") as f:
    content = f.read()

# Add strconv to imports if not there
if '"strconv"' not in content:
    content = content.replace('"strings"', '"strconv"\n\t"strings"')

# Inject chat handlers
old_chat = """	chat := ui.NewChat(loop, ch)
	chat.Approve = ap
	_, err = tea.NewProgram(chat).Run()"""

new_chat = """	chat := ui.NewChat(loop, ch)
	chat.Approve = ap

	chat.OnUndo = func(n int) string {
		var b strings.Builder
		for _, r := range reg.Undo(n) {
			mark := "✗"
			if r.OK {
				mark = "✓"
			}
			if r.Rel == "" {
				fmt.Fprintf(&b, "%s %s\\n", mark, r.Note)
				continue
			}
			fmt.Fprintf(&b, "%s %s — %s\\n", mark, r.Rel, r.Note)
		}
		s := strings.TrimRight(b.String(), "\\n")
		loop.Note("/undo " + strconv.Itoa(n) + "\\n" + s)
		return s
	}

	chat.OnEdits = func() string {
		p := reg.Pending()
		if len(p) == 0 {
			return "لا تعديلات قابلة للتراجع"
		}
		var b strings.Builder
		for i, e := range p {
			fmt.Fprintf(&b, "%d· %s %s\\n", i+1, e.Tool, e.Rel)
		}
		return strings.TrimRight(b.String(), "\\n")
	}

	_, err = tea.NewProgram(chat).Run()"""

content = content.replace(old_chat, new_chat)

with open("cmd/ag/main.go", "w") as f:
    f.write(content)
