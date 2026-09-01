import re

with open("internal/tools/registry.go", "r") as f:
    content = f.read()

bad_func = """func NewRegistry(root *Root, sh *snap.Shadow) *Registry {
	r := &Registry{root: root, sh: sh, byName: map[string]Tool{}}
	r.add(
		readTool{root},
		globTool{root},
		grepTool{root},
		writeTool{root: root, sh: sh},
		editTool{root: root, sh: sh},
		bashTool{root: root},
	)
	return r
}"""

good_func = """func NewRegistry(root *Root, sh *snap.Shadow) *Registry {
	log := &editLog{}
	r := &Registry{root: root, sh: sh, edits: log, byName: map[string]Tool{}}
	r.add(readFile{root}, globFiles{root}, grepFiles{root})
	r.add(writeFile{root, sh, log}, editFile{root, sh, log})
	r.add(bashTool{root})
	return r
}"""

content = content.replace(bad_func, good_func)

with open("internal/tools/registry.go", "w") as f:
    f.write(content)
