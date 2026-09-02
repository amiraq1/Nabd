// Package tools: bash.go runs a shell command in its own process group.
// Three guarantees only: it cannot hang on stdin, it cannot leave children
// behind, and it cannot read the provider keys. Everything else about this
// tool is the human's eye at the permission prompt.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

const (
	bashDefaultTimeout = 120 * time.Second
	bashMaxTimeout     = 10 * time.Minute
	bashTermGrace      = 400 * time.Millisecond
	bashDrainGrace     = 2 * time.Second
	bashHead           = 8 << 10
	bashTail           = 24 << 10
)

type bashTool struct{ root *Root }

func (bashTool) Name() string { return "bash" }

func (bashTool) Spec() provider.ToolSpec {
	return spec("bash",
		"Run a shell command inside the project directory. No interactive input: any command waiting for input sees EOF immediately, so pass non-interactive flags (-y, --no-input). Commands that keep background processes alive are killed when the command ends.",
		`{"type":"object","properties":{"cmd":{"type":"string","description":"the command as typed in the shell"},"timeout_s":{"type":"integer","description":"timeout in seconds (default 120, max 600)"}},"required":["cmd"]}`)
}

func (b bashTool) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {
	o, err := b.RunDetailed(ctx, raw)
	return o.Text, o.OK, err
}

func (b bashTool) RunDetailed(ctx context.Context, raw json.RawMessage) (agent.Outcome, error) {
	var a struct {
		Cmd string `json:"cmd"`
		T   int    `json:"timeout_s"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return agent.Outcome{}, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(a.Cmd) == "" {
		return agent.Outcome{}, errors.New("empty command")
	}
	to := bashDefaultTimeout
	if a.T > 0 {
		to = time.Duration(a.T) * time.Second
		if to > bashMaxTimeout {
			to = bashMaxTimeout
		}
	}

	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return agent.Outcome{}, err
	}
	defer null.Close()

	// A real pipe, not StdoutPipe: os/exec then hands the fd to the child
	// directly, so Wait returns when the process dies even if a grandchild
	// still holds the write end. StdoutPipe would deadlock on exactly that.
	pr, pw, err := os.Pipe()
	if err != nil {
		return agent.Outcome{}, err
	}
	defer pr.Close()

	cmd := exec.Command("sh", "-c", a.Cmd)
	cmd.Dir = b.root.Dir()
	cmd.Env = scrubEnv(os.Environ())
	cmd.Stdin = null
	cmd.Stdout, cmd.Stderr = pw, pw // one stream: interleaving is causality
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		pw.Close()
		return agent.Outcome{}, err
	}
	pw.Close() // the parent's copy, or the reader never sees EOF
	pgid := cmd.Process.Pid

	buf := &headTail{}
	read := make(chan struct{})
	go func() { io.Copy(buf, pr); close(read) }()

	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()

	timer := time.NewTimer(to)
	defer timer.Stop()

	var werr error
	var note string
	select {
	case werr = <-wait:
	case <-timer.C:
		note = fmt.Sprintf("killed after %s", to)
		killGroup(pgid)
		werr = <-wait
	case <-ctx.Done():
		note = "cancelled"
		killGroup(pgid)
		werr = <-wait
	}

	// Sweep the group even after a clean exit. A backgrounded process that
	// outlives the approval that spawned it is a permission with no expiry,
	// and nothing later in this program would ever ask about it again.
	killGroup(pgid)
	select {
	case <-read:
	case <-time.After(bashDrainGrace):
	}

	out := agent.Outcome{Text: buf.String(), OK: werr == nil}
	if ee, ok := werr.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			out.Exit = ws.ExitStatus()
			if ws.Signaled() {
				out.Signal = ws.Signal().String()
				out.Exit = 128 + int(ws.Signal())
			}
		}
	} else if werr != nil {
		out.Exit = -1
	}

	head := fmt.Sprintf("exit %d · %s", out.Exit, dur(time.Since(started)))
	if out.Signal != "" {
		head += " · " + out.Signal
	}
	if note != "" {
		head += " · " + note
		out.OK = false
	}
	if out.Text == "" {
		out.Text = "(no output)"
	}
	out.Text = head + "\n" + out.Text
	return out, nil
}

// killGroup addresses -pgid, not the pid: the shell is rarely the process
// that hangs, and killing only the leader orphans whatever it spawned.
func killGroup(pgid int) {
	if pgid <= 1 {
		return
	}
	syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(bashTermGrace)
	for time.Now().Before(deadline) {
		if syscall.Kill(-pgid, 0) != nil {
			return // group is gone
		}
		time.Sleep(25 * time.Millisecond)
	}
	syscall.Kill(-pgid, syscall.SIGKILL)
}

// scrubEnv removes the provider credentials. The agent has no business
// reading the key that pays for it, and `env` is one command away from
// `curl`. Everything else passes through: a shell without PATH is a toy.
func scrubEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		u := strings.ToUpper(k)
		if strings.HasSuffix(u, "_API_KEY") || strings.HasSuffix(u, "_TOKEN") ||
			strings.HasSuffix(u, "_SECRET") || strings.HasSuffix(u, "_PASSWORD") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "NABD=1")
}

// headTail keeps the opening and the ending. The middle of a long build log
// is the part nobody reads and the model pays for twice.
type headTail struct {
	head  []byte
	tail  []byte
	total int
}

func (h *headTail) Write(p []byte) (int, error) {
	h.total += len(p)
	if n := bashHead - len(h.head); n > 0 {
		if n > len(p) {
			n = len(p)
		}
		h.head = append(h.head, p[:n]...)
		p = p[n:]
	}
	if len(p) == 0 {
		return len(p), nil
	}
	h.tail = append(h.tail, p...)
	if len(h.tail) > bashTail {
		h.tail = h.tail[len(h.tail)-bashTail:]
	}
	return len(p), nil
}

func (h *headTail) String() string {
	if h.total <= len(h.head) {
		return string(h.head)
	}
	cut := h.total - len(h.head) - len(h.tail)
	return fmt.Sprintf("%s\n… %d bytes cut from the middle …\n%s",
		h.head, cut, trimToRune(h.tail))
}

// trimToRune drops a half rune at the cut, or the JSON encoder emits U+FFFD
// and the model reads a corrupted first line.
func trimToRune(b []byte) []byte {
	for i := 0; i < 4 && i < len(b); i++ {
		if r := []rune(string(b[i:])); len(r) > 0 && r[0] != '\uFFFD' {
			return b[i:]
		}
	}
	return b
}

func dur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
