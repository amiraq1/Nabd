import re

with open("internal/tools/registry.go", "r") as f:
    content = f.read()

# Add Detailed interface and RunDetailed
new_funcs = """
type Detailed interface {
	RunDetailed(context.Context, json.RawMessage) (agent.Outcome, error)
}

func (r *Registry) RunDetailed(ctx context.Context, name string, raw json.RawMessage) (agent.Outcome, error) {
	t, ok := r.byName[name]
	if !ok {
		return agent.Outcome{}, fmt.Errorf("أداة غير معروفة: %s", name)
	}
	if d, ok := t.(Detailed); ok {
		return d.RunDetailed(ctx, raw)
	}
	txt, good, err := t.Run(ctx, raw)
	return agent.Outcome{Text: txt, OK: good}, err
}
"""
content += new_funcs

with open("internal/tools/registry.go", "w") as f:
    f.write(content)
