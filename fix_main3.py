import re

with open("cmd/ag/main.go", "r") as f:
    content = f.read()

# Add -continue flag
content = content.replace('sessDir := flag.String("dir", "", "session directory (default ~/.ag/sessions)")', 'sessDir := flag.String("dir", "", "session directory (default ~/.ag/sessions)")\n\tcont := flag.Bool("continue", false, "resume the latest session")')

# Replace doChat call
content = content.replace('if err := doChat(*sessDir) {', 'if err := doChat(*sessDir, *cont) {')
content = content.replace('if err := doChat(*sessDir); err != nil {', 'if err := doChat(*sessDir, *cont); err != nil {')

# Modify doChat signature
content = content.replace('func doChat(dir string) error {', 'func doChat(dir string, cont bool) error {')

# Inject --continue logic in doChat
continue_logic = """
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
		fmt.Printf("استأنفتُ %s · %d حدثًا حيًّا من %d\\n",
			filepath.Base(prev), len(live), len(evs))
	}
"""
content = content.replace('ch := make(chan agent.Event, 128)', continue_logic + '\n\tch := make(chan agent.Event, 128)')

# Add OnRewind to chat
on_rewind_code = """
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
"""
content = content.replace('chat.OnUndo =', on_rewind_code + '\n\tchat.OnUndo =')

# Add latestSession helper
latest_session_helper = """
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
"""
content += latest_session_helper

with open("cmd/ag/main.go", "w") as f:
    f.write(content)

