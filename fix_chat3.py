import re

with open("internal/ui/chat.go", "r") as f:
    content = f.read()

# Add fields
content = content.replace("OnRewind func(n int) string", "OnRewind func(n int) string\n\tOnCtx    func() string\n\tOnCompact func() string")

# Add cases in command
compact_cases = """	case "/ctx":
		if m.OnCtx == nil {
			return "—"
		}
		return m.OnCtx()
	case "/compact":
		if m.OnCompact == nil {
			return "—"
		}
		return m.OnCompact()
"""
content = content.replace('case "/undo":', compact_cases + '\tcase "/undo":')

# Update /help
content = content.replace('"/undo [n] · /edits · ctrl+c إيقاف · ctrl+d خروج"', '"/undo [n] · /edits · /ctx · /compact · ctrl+c · ctrl+d"')

with open("internal/ui/chat.go", "w") as f:
    f.write(content)
