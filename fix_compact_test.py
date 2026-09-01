import re

with open("internal/agent/compact_test.go", "r") as f:
    content = f.read()

live1_block = re.search(r'\tlive1 := \[\]Event\{.*?\n\t\}\n\t// Better test:\n', content, flags=re.DOTALL).group(0)
content = content.replace(live1_block, "")

with open("internal/agent/compact_test.go", "w") as f:
    f.write(content)
