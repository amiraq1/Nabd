import re

with open("cmd/ag/main.go", "r") as f:
    content = f.read()

# Extract the cont block
cont_block = re.search(r'\tif cont \{.*?\n\t\}\n', content, flags=re.DOTALL).group(0)

# Remove it
content = content.replace(cont_block, "")

# Insert it after loop initialization
loop_init = """	loop := &agent.Loop{
		Provider: prov,
		Tools:    reg,
		Sink:     agent.Fanout{journal, chanSink(ch)},
		System:   system,
		Gate:     gate{pol},
		Human:    ap,
	}"""

content = content.replace(loop_init, loop_init + "\n" + cont_block)

with open("cmd/ag/main.go", "w") as f:
    f.write(content)
