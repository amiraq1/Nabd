// Command ag is nabd: a coding agent that fits in a thumb's reach.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nabd/internal/agent"
	"nabd/internal/perm"
	"nabd/internal/provider"
	"nabd/internal/snap"
	"nabd/internal/store"
	"nabd/internal/tools"
	"nabd/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

const system = `أنت nabd، وكيل برمجة يعمل داخل طرفية هاتف بعرض ٥٠ عمودًا.
أجب بإيجاز شديد. لا تكرر السؤال، لا تعتذر، لا تسرد قوائم بلا داعٍ.
سطران يكفيان حين يكفيان.`

var version = "dev"

func main() {
	loadEnv()
	replay := flag.String("replay", "", "replay a session.jsonl and exit")
	speed := flag.Float64("speed", 1, "replay multiplier; 0 is instant")
	sessDir := flag.String("dir", "", "session directory (default ~/.ag/sessions)")
	cont := flag.Bool("continue", false, "resume the latest session")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	if *replay != "" {
		if err := doReplay(*replay, *speed); err != nil {
			die(err)
		}
		return
	}
	if err := doChat(*sessDir, *cont); err != nil {
		die(err)
	}
}

func doReplay(path string, speed float64) error {
	events, err := store.Read(path)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("no events in %s", path)
	}
	_, err = tea.NewProgram(ui.NewReplay(events, speed)).Run()
	return err
}

func doChat(dir string, cont bool) error {
	prov, err := pickProvider()
	if err != nil {
		return err
	}

	path, err := sessionPath(dir)
	if err != nil {
		return err
	}
	journal, err := store.NewJSONL(path)
	if err != nil {
		return err
	}
	defer journal.Close()

	// The UI is a sink too, behind a buffered channel: a slow terminal
	// must not stall the loop, and the journal must never wait on paint.
	root, err := tools.NewRoot("")
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
		Budget:   agent.NewBudget(),
		Human:    ap,
	}
	if cont {
		prev, err := latestSession()
		if err != nil {
			return err
		}
		evs, err := store.Read(prev)
		if err != nil {
			return err
		}
		live := agent.Live(evs)
		loop.Seed(live)
		fmt.Printf("استأنفتُ %s · %d حدثًا حيًّا من %d\n",
			filepath.Base(prev), len(live), len(evs))
	}

	cwd, _ := os.Getwd()
	if err := loop.Start(fmt.Sprintf("nabd "+version+" · %s · %s",
		prov.Name(), filepath.Base(cwd))); err != nil {
		return err
	}

	chat := ui.NewChat(loop, ch)
	chat.Approve = ap

	chat.OnRewind = func(n int) string {
		txt, err := loop.Rewind(n)
		if err != nil {
			return err.Error()
		}
		chat.SetInput(txt) // the cut turn comes back to the prompt, editable
		s := fmt.Sprintf("رجعتُ %d دور", n)
		if k := len(reg.Pending()); k > 0 {
			s += fmt.Sprintf(" · %d تعديل على القرص لم يُلغَ (/undo %d)", k, k)
		}
		return s
	}

	chat.OnUndo = func(n int) string {
		var b strings.Builder
		for _, r := range reg.Undo(n) {
			mark := "✗"
			if r.OK {
				mark = "✓"
			}
			if r.Rel == "" {
				fmt.Fprintf(&b, "%s %s\n", mark, r.Note)
				continue
			}
			fmt.Fprintf(&b, "%s %s — %s\n", mark, r.Rel, r.Note)
		}
		s := strings.TrimRight(b.String(), "\n")
		// The journal is the single source of truth: emit the undo as a
		// Notice so the event survives in session.jsonl and reaches the UI
		// through the event channel. Returning "" keeps the status line from
		// duplicating what the Notice renders.
		loop.Note(fmt.Sprintf("/undo %d — %s", n, s))
		return ""
	}

	chat.OnEdits = func() string {
		p := reg.Pending()
		if len(p) == 0 {
			return "لا تعديلات قابلة للتراجع"
		}
		var b strings.Builder
		for i, e := range p {
			fmt.Fprintf(&b, "%d· %s %s\n", i+1, e.Tool, e.Rel)
		}
		return strings.TrimRight(b.String(), "\n")
	}

	chat.OnCtx = func() string {
		ms := agent.Squeeze(agent.Messages(agent.Live(loop.Hist())), agent.KeepFullRounds)
		p := loop.Budget.Pressure(ms)
		return fmt.Sprintf("السياق %d%% (%d / %d رمزا)", int(p*100), loop.Budget.Estimate(ms), loop.Budget.Usable())
	}
	chat.OnCompact = func() string {
		// Wait, context is already imported in main.go
		if err := loop.Compact(context.Background(), loop.Budget.Usable()*4/10); err != nil {
			return err.Error()
		}
		return "ضُغط السياق يدويًا"
	}

	_, err = tea.NewProgram(chat).Run()
	if err != nil {
		return err
	}
	fmt.Println("session:", path)
	return nil
}

// chanSink hands events to the UI, dropping nothing but never blocking
// forever: if the UI is gone, the journal still gets everything.
type chanSink chan agent.Event

func (c chanSink) Emit(e agent.Event) error {
	select {
	case c <- e:
	case <-time.After(2 * time.Second):
	}
	return nil
}

func sessionPath(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".ag", "sessions")
	}
	name := time.Now().UTC().Format("20060102-150405") + ".jsonl"
	return filepath.Join(dir, name), nil
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "ag:", err)
	os.Exit(1)
}

// loadEnv reads ~/.ag/env and populates missing environment variables.
func loadEnv() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	b, err := os.ReadFile(filepath.Join(home, ".ag", "env"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.IndexByte(line, '='); idx > 0 {
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			v = strings.Trim(v, `"'`)
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
}

// pickProvider prefers whatever key is present. NABD_PROVIDER forces one.
func pickProvider() (provider.Provider, error) {
	switch os.Getenv("NABD_PROVIDER") {
	case "nvidia":
		return provider.NewNVIDIA()
	case "anthropic":
		return provider.NewAnthropic()
	case "openrouter":
		return provider.NewOpenRouter()
	case "groq":
		return provider.NewGroq(), nil
	}
	if os.Getenv("GROQ_API_KEY") != "" {
		return provider.NewGroq(), nil
	}
	if os.Getenv("OPENROUTER_API_KEY") != "" {
		return provider.NewOpenRouter()
	}
	if os.Getenv("NVIDIA_API_KEY") != "" {
		return provider.NewNVIDIA()
	}
	return provider.NewAnthropic()
}

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
		g.p.Record(tool, agent.AllowSession)
	}
}

func latestSession() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ag", "sessions")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var last string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			last = e.Name()
		}
	}
	if last == "" {
		return "", fmt.Errorf("لا جلسات سابقة")
	}
	return filepath.Join(dir, last), nil
}
