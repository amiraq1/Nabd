package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	AgentName         = "NabdCoder"
	defaultBaseURL    = "http://127.0.0.1:8080/v1"
	defaultModel      = "local"
	maxIterations     = 10
	maxLLMToolOutput  = 8000
	maxUIDisplayChars = 400
)

var (
	baseURL = getEnv("NABD_BASE_URL", defaultBaseURL)
	model   = getEnv("NABD_MODEL", defaultModel)
	apiKey  = getEnv("NABD_API_KEY", "sk-no-key")
)

func getEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Styles
var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Italic(true)
	approvalStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	helpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	nabdStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

// ==================== OpenAI Wire Structs ====================

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Display    string     `json:"-"`
}

var toolDefinitions = []map[string]interface{}{
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "read_file",
			"description": "Read the contents of a file inside the project",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "Relative path to the file"},
				},
				"required": []string{"path"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "write_file",
			"description": "Write or update a file (requires user approval and validates Go syntax)",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path":    map[string]interface{}{"type": "string", "description": "Relative path to the file"},
					"content": map[string]interface{}{"type": "string", "description": "Full file content"},
				},
				"required": []string{"path", "content"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "run_command",
			"description": "Run a shell command such as go test or git status (requires user approval)",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{"type": "string", "description": "The command to execute"},
				},
				"required": []string{"command"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "list_dir",
			"description": "List the contents of a directory inside the project",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string", "description": "Relative path (default .)"},
				},
			},
		},
	},
}

// ==================== TUI Model ====================

type uiModel struct {
	viewport         viewport.Model
	textInput        textinput.Model
	messages         []Message
	pendingTools     []ToolCall
	awaitingApproval bool
	iteration        int
	workDir          string
	status           string
	busy             bool
	width            int
	height           int
	err              error
}

func initialModel() uiModel {
	ti := textinput.New()
	ti.Placeholder = "Message to NabdCoder... (Ctrl+C to quit)"
	ti.Focus()
	ti.CharLimit = 2000
	ti.Width = 80

	vp := viewport.New(80, 20)
	vp.SetContent(nabdStyle.Render("NabdCoder") + " — specialized coding agent for Nabd\n")

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	systemPrompt := `You are NabdCoder, a coding agent specialized in the Nabd project (a secure Go approval system).

Project background:
- Nabd is an Approval System with Shadow Storage and Undo/Restore.
- Focuses on security: preventing key leaks, protecting shadow reads, controlling hook writes.
- Written in Go, runs on Windows and Termux/Linux.
- Contains a Threat Model, RED baseline, and strict tests.

Rules:
- Focus on code quality and security.
- Use tools when needed (read files, edit, run tests).
- When writing new code, follow the existing project style.
- Do not suggest changes that break the approval system or shadow storage.
- Reply in English.`

	return uiModel{
		textInput: ti,
		viewport:  vp,
		workDir:   cwd,
		messages: []Message{
			{Role: "system", Content: systemPrompt},
		},
		status: "Ready | Nabd",
	}
}

func (m uiModel) Init() tea.Cmd {
	return textinput.Blink
}

type llmResponseMsg struct {
	assistantMsg Message
}
type toolExecutedMsg struct {
	toolMsg Message
}
type errorMsg struct {
	err error
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.awaitingApproval && len(m.pendingTools) > 0 {
			key := strings.ToLower(msg.String())
			tc := m.pendingTools[0]
			if key == "y" || key == "yes" {
				m.awaitingApproval = false
				m.status = "Executing approved tool: " + tc.Function.Name
				m.refreshViewport()
				return m, executeToolCmd(m.workDir, tc)
			}
			if key == "n" || key == "no" || msg.Type == tea.KeyEsc {
				m.awaitingApproval = false
				m.pendingTools = m.pendingTools[1:]
				rejectMsg := Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    "Tool execution rejected by user via Nabd approval gate.",
					Display:    fmt.Sprintf("Rejected execution of %s", tc.Function.Name),
				}
				m.messages = append(m.messages, rejectMsg)
				m.refreshViewport()
				return m.advanceQueueOrLLM()
			}
		}

		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.busy {
				return m, nil
			}
			input := strings.TrimSpace(m.textInput.Value())
			if input == "" {
				return m, nil
			}
			if input == "exit" || input == "quit" {
				return m, tea.Quit
			}
			if input == "clear" {
				m.messages = m.messages[:1]
				m.pendingTools = nil
				m.awaitingApproval = false
				m.refreshViewport()
				m.textInput.SetValue("")
				return m, nil
			}

			m.messages = append(m.messages, Message{Role: "user", Content: input})
			m.textInput.SetValue("")
			m.busy = true
			m.iteration = 0
			m.status = "Thinking..."
			m.refreshViewport()
			return m, callLLMCmd(m.messages)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6
		m.textInput.Width = maxInt(20, msg.Width-4)
		m.refreshViewport()

	case llmResponseMsg:
		m.messages = append(m.messages, msg.assistantMsg)
		m.iteration++
		if len(msg.assistantMsg.ToolCalls) == 0 || m.iteration >= maxIterations {
			m.busy = false
			m.status = "Ready | Nabd"
			m.refreshViewport()
			return m, nil
		}
		m.pendingTools = append(m.pendingTools, msg.assistantMsg.ToolCalls...)
		m.refreshViewport()
		return m.advanceQueueOrLLM()

	case toolExecutedMsg:
		if len(m.pendingTools) > 0 {
			m.pendingTools = m.pendingTools[1:]
		}
		m.messages = append(m.messages, msg.toolMsg)
		m.refreshViewport()
		return m.advanceQueueOrLLM()

	case errorMsg:
		m.busy = false
		m.awaitingApproval = false
		m.pendingTools = nil
		m.status = "Error"
		m.err = msg.err
		m.messages = append(m.messages, Message{Role: "error", Content: msg.err.Error()})
		m.refreshViewport()
		return m, nil
	}

	m.textInput, cmd = m.textInput.Update(msg)
	cmds = append(cmds, cmd)
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m uiModel) advanceQueueOrLLM() (tea.Model, tea.Cmd) {
	if len(m.pendingTools) == 0 {
		m.status = "Analyzing tool results..."
		m.refreshViewport()
		return m, callLLMCmd(m.messages)
	}

	nextTool := m.pendingTools[0]
	if requiresApproval(nextTool.Function.Name) {
		m.awaitingApproval = true
		m.status = "Waiting for approval [y/n]"
		m.refreshViewport()
		return m, nil
	}

	m.status = "Running tool: " + nextTool.Function.Name
	m.refreshViewport()
	return m, executeToolCmd(m.workDir, nextTool)
}

func requiresApproval(toolName string) bool {
	return toolName == "write_file" || toolName == "run_command"
}

func (m uiModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("NabdCoder") + "  " + statusStyle.Render(m.status) + "\n")
	b.WriteString(m.viewport.View() + "\n")
	b.WriteString(helpStyle.Render("──────────────────────────────────────") + "\n")
	if m.awaitingApproval && len(m.pendingTools) > 0 {
		tc := m.pendingTools[0]
		b.WriteString(approvalStyle.Render(fmt.Sprintf("Approval required for %s(%s) — press [Y] to approve or [N] to reject", tc.Function.Name, truncateUTF8(tc.Function.Arguments, 80))) + "\n")
	} else {
		b.WriteString(m.textInput.View() + "\n")
	}
	b.WriteString(helpStyle.Render("Enter: send | clear | Ctrl+C: quit"))
	return b.String()
}

func (m *uiModel) refreshViewport() {
	var content strings.Builder
	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			content.WriteString(userStyle.Render("You: ") + msg.Content + "\n\n")
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				content.WriteString(assistantStyle.Render("NabdCoder: ") + msg.Content + "\n\n")
			}
			for _, tc := range msg.ToolCalls {
				content.WriteString(toolStyle.Render(fmt.Sprintf("Calling: %s(%s)", tc.Function.Name, truncateUTF8(tc.Function.Arguments, 120))) + "\n\n")
			}
		case "tool":
			disp := msg.Display
			if disp == "" {
				disp = truncateUTF8(msg.Content, maxUIDisplayChars)
			}
			content.WriteString(toolStyle.Render("Tool: "+disp) + "\n\n")
		case "error":
			content.WriteString(errorStyle.Render("Error: "+msg.Content) + "\n\n")
		}
	}
	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

func callLLMCmd(history []Message) tea.Cmd {
	snapshot := make([]Message, 0, len(history))
	for _, msg := range history {
		if msg.Role != "error" {
			snapshot = append(snapshot, msg)
		}
	}

	return func() tea.Msg {
		resp, err := callLLM(snapshot)
		if err != nil {
			return errorMsg{err: err}
		}
		return llmResponseMsg{assistantMsg: *resp}
	}
}

func callLLM(messages []Message) (*Message, error) {
	payload := map[string]interface{}{
		"model":       model,
		"messages":    messages,
		"tools":       toolDefinitions,
		"temperature": 0.2,
		"max_tokens":  2048,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncateUTF8(string(data), 300))
	}

	var result struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices returned by the model")
	}

	msg := result.Choices[0].Message
	msg.Role = "assistant"
	for i := range msg.ToolCalls {
		if msg.ToolCalls[i].ID == "" {
			msg.ToolCalls[i].ID = fmt.Sprintf("call_%d", i+1)
		}
		if msg.ToolCalls[i].Type == "" {
			msg.ToolCalls[i].Type = "function"
		}
	}
	return &msg, nil
}

func executeToolCmd(workDir string, tc ToolCall) tea.Cmd {
	return func() tea.Msg {
		var args map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)

		rawOut := executeTool(workDir, tc.Function.Name, args)
		llmOut := truncateUTF8(rawOut, maxLLMToolOutput)
		uiOut := fmt.Sprintf("%s -> %s", tc.Function.Name, truncateUTF8(rawOut, maxUIDisplayChars))

		return toolExecutedMsg{
			toolMsg: Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    llmOut,
				Display:    uiOut,
			},
		}
	}
}

func resolveSafePath(workDir, relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		relPath = "."
	}
	absRoot, err := filepath.Abs(workDir)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absRoot, filepath.Clean(relPath))
	rel, err := filepath.Rel(absRoot, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("security rejection (Path Guard): path outside workspace: %s", relPath)
	}
	return target, nil
}

func executeTool(workDir, name string, args map[string]interface{}) string {
	switch name {
	case "read_file":
		path, _ := args["path"].(string)
		safePath, err := resolveSafePath(workDir, path)
		if err != nil {
			return "Error: " + err.Error()
		}
		return toolReadFile(safePath)

	case "write_file":
		path, _ := args["path"].(string)
		content, _ := args["content"].(string)
		safePath, err := resolveSafePath(workDir, path)
		if err != nil {
			return "Error: " + err.Error()
		}
		return toolWriteFile(safePath, content)

	case "run_command":
		cmd, _ := args["command"].(string)
		return toolRunCommand(workDir, cmd)

	case "list_dir":
		path, _ := args["path"].(string)
		safePath, err := resolveSafePath(workDir, path)
		if err != nil {
			return "Error: " + err.Error()
		}
		return toolListDir(safePath)

	default:
		return "Unknown tool: " + name
	}
}

func toolReadFile(safePath string) string {
	data, err := os.ReadFile(safePath)
	if err != nil {
		return "Error: " + err.Error()
	}
	return string(data)
}

func toolWriteFile(safePath, content string) string {
	if strings.HasSuffix(safePath, ".go") {
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, filepath.Base(safePath), content, parser.AllErrors); err != nil {
			return fmt.Sprintf("Rejected save due to Go syntax error:\n%v", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(safePath), 0755); err != nil {
		return "Error: " + err.Error()
	}
	if err := os.WriteFile(safePath, []byte(content), 0644); err != nil {
		return "Error: " + err.Error()
	}
	return "Successfully wrote: " + safePath
}

func toolRunCommand(workDir, command string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "GOMEMLIMIT=512MiB", "GOGC=50")

	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "Error: command timed out (60 seconds)\n" + string(out)
	}
	if err != nil {
		return fmt.Sprintf("Error: %s\n%s", err.Error(), string(out))
	}
	return string(out)
}

func toolListDir(safePath string) string {
	entries, err := os.ReadDir(safePath)
	if err != nil {
		return "Error: " + err.Error()
	}
	var b strings.Builder
	for _, e := range entries {
		typ := "F"
		if e.IsDir() {
			typ = "D"
		}
		b.WriteString(fmt.Sprintf("%s  %s\n", typ, e.Name()))
	}
	return b.String()
}

func truncateUTF8(s string, maxChars int) string {
	if utf8.RuneCountInString(s) <= maxChars {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxChars]) + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running NabdCoder: %v\n", err)
		os.Exit(1)
	}
}
