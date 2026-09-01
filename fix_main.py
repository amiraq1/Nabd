import sys

with open("cmd/ag/main.go", "r") as f:
    content = f.read()

# 1. Update imports
content = content.replace('"nabd/internal/provider"', '"nabd/internal/perm"\n\t"nabd/internal/provider"\n\t"nabd/internal/snap"')

# 2. Update doChat setup
old_setup = """	root, err := tools.NewRoot("")
	if err != nil {
		return err
	}
	ch := make(chan agent.Event, 128)
	loop := &agent.Loop{
		Provider: prov,
		Tools:    tools.NewRegistry(root),
		Sink:     agent.Fanout{journal, chanSink(ch)},
		System:   system,
	}

	cwd, _ := os.Getwd()
	if err := loop.Start(fmt.Sprintf("nabd v0.2 · %s · %s",
		prov.Name(), filepath.Base(cwd))); err != nil {
		return err
	}

	_, err = tea.NewProgram(ui.NewChat(loop, ch)).Run()"""

new_setup = """	root, err := tools.NewRoot("")
	if err != nil {
		return err
	}
	sh, err := snap.New(root.Dir())
	if err != nil {
		return err
	}
	reg := tools.NewRegistry(root, sh)
	pol := perm.New(reg)
	ap := ui.NewApprover()

	ch := make(chan agent.Event, 128)
	loop := &agent.Loop{
		Provider: prov,
		Tools:    reg,
		Sink:     agent.Fanout{journal, chanSink(ch)},
		System:   system,
		Gate:     gate{pol},
		Human:    ap,
	}

	cwd, _ := os.Getwd()
	if err := loop.Start(fmt.Sprintf("nabd v0.2 · %s · %s",
		prov.Name(), filepath.Base(cwd))); err != nil {
		return err
	}

	chat := ui.NewChat(loop, ch)
	chat.Approve = ap
	_, err = tea.NewProgram(chat).Run()"""

content = content.replace(old_setup, new_setup)

# 3. Add gate type
gate_code = """

// gate translates the policy's vocabulary into the loop's. It is the only
// place the two packages meet, and it fails closed by default.
type gate struct{ p *perm.Policy }

func (g gate) Check(tool string) (agent.Verdict, string) {
	v, why := g.p.Check(tool)
	switch v {
	case perm.Allow:
		return agent.VerdictAllow, why
	case perm.Deny:
		return agent.VerdictDeny, why
	}
	return agent.VerdictAsk, why
}

func (g gate) Record(tool string, d agent.Decision) {
	if d == agent.AllowSession {
		g.p.Record(tool, perm.AllowSession)
	}
}
"""

content += gate_code

with open("cmd/ag/main.go", "w") as f:
    f.write(content)
