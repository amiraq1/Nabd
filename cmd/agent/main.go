package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/amiraq1/nabd/internal/tools"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
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

var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Italic(true)
	approvalStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	helpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Display    string     `json:"-"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type uiModel struct {
	viewport         viewport.Model
	textInput        textinput.Model
	messages         []Message
	pendingTools     []ToolCall
	awaitingApproval bool
	iteration        int
	registry         *tools.Registry
	status           string
	busy             bool
}

func initialModel(reg *tools.Registry) uiModel {
	ti := textinput.New()
	ti.Placeholder = "Message to NabdCoder... (Ctrl+C to quit)"
	ti.Focus()
	ti.Width = 80

	vp := viewport.New(80, 20)
	vp.SetContent("NabdCoder ready — secure tools + Termux memory limits\n")

	system := `You are NabdCoder, a coding agent specialized in the Nabd project.
Use tools when needed. Focus on security and code quality. Reply in English.`

	return uiModel{
		textInput: ti,
		viewport:  vp,
		messages:  []Message{{Role: "system", Content: system}},
		registry:  reg,
		status:    "Ready",
	}
}

func (m uiModel) Init() tea.Cmd { return textinput.Blink }

type llmResponseMsg struct{ assistantMsg Message }
type toolExecutedMsg struct{ toolMsg Message }
type errorMsg struct{ err error }

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.awaitingApproval && len(m.pendingTools) > 0 {
			key := strings.ToLower(msg.String())
			tc := m.pendingTools[0]
			if key == "y" || key == "yes" {
				m.awaitingApproval = false
				m.status = "Executing: " + tc.Function.Name
				m.refresh()
				return m, executeToolCmd(m.registry, tc)
			}
			if key == "n" || key == "no" || msg.Type == tea.KeyEsc {
				m.awaitingApproval = false
				m.pendingTools = m.pendingTools[1:]
				m.messages = append(m.messages, Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    "Rejected by user.",
					Display:    "Rejected " + tc.Function.Name,
				})
				m.refresh()
				return m.advance()
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
				m.refresh()
				m.textInput.SetValue("")
				return m, nil
			}
			m.messages = append(m.messages, Message{Role: "user", Content: input})
			m.textInput.SetValue("")
			m.busy = true
			m.iteration = 0
			m.status = "Thinking..."
			m.refresh()
			return m, callLLMCmd(m.registry, m.messages)
		}
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6
		m.textInput.Width = max(20, msg.Width-4)
		m.refresh()
	case llmResponseMsg:
		m.messages = append(m.messages, msg.assistantMsg)
		m.iteration++
		if len(msg.assistantMsg.ToolCalls) == 0 || m.iteration >= maxIterations {
			m.busy = false
			m.status = "Ready"
			m.refresh()
			return m, nil
		}
		m.pendingTools = append(m.pendingTools, msg.assistantMsg.ToolCalls...)
		m.refresh()
		return m.advance()
	case toolExecutedMsg:
		if len(m.pendingTools) > 0 {
			m.pendingTools = m.pendingTools[1:]
		}
		m.messages = append(m.messages, msg.toolMsg)
		m.refresh()
		return m.advance()
	case errorMsg:
		m.busy = false
		m.awaitingApproval = false
		m.pendingTools = nil
		m.status = "Error"
		m.messages = append(m.messages, Message{Role: "error", Content: msg.err.Error()})
		m.refresh()
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	m.viewport, _ = m.viewport.Update(msg)
	return m, cmd
}

func (m uiModel) advance() (tea.Model, tea.Cmd) {
	if len(m.pendingTools) == 0 {
		m.status = "Analyzing results..."
		m.refresh()
		return m, callLLMCmd(m.registry, m.messages)
	}
	next := m.pendingTools[0]
	if m.registry.NeedsApproval(next.Function.Name) {
		m.awaitingApproval = true
		m.status = "Approval required [y/n]"
		m.refresh()
		return m, nil
	}
	m.status = "Running: " + next.Function.Name
	m.refresh()
	return m, executeToolCmd(m.registry, next)
}

func (m uiModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("NabdCoder") + "  " + statusStyle.Render(m.status) + "\n")
	b.WriteString(m.viewport.View() + "\n")
	b.WriteString(helpStyle.Render("────────────────────────────────") + "\n")
	if m.awaitingApproval && len(m.pendingTools) > 0 {
		tc := m.pendingTools[0]
		b.WriteString(approvalStyle.Render(fmt.Sprintf("Approve %s? [Y/N]", tc.Function.Name)) + "\n")
	} else {
		b.WriteString(m.textInput.View() + "\n")
	}
	b.WriteString(helpStyle.Render("Enter: send | clear | Ctrl+C"))
	return b.String()
}

func (m *uiModel) refresh() {
	var b strings.Builder
	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			b.WriteString(userStyle.Render("You: ") + msg.Content + "\n\n")
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				b.WriteString(assistantStyle.Render("NabdCoder: ") + msg.Content + "\n\n")
			}
			for _, tc := range msg.ToolCalls {
				b.WriteString(toolStyle.Render(fmt.Sprintf("Call: %s", tc.Function.Name)) + "\n\n")
			}
		case "tool":
			disp := msg.Display
			if disp == "" {
				disp = truncate(msg.Content, maxUIDisplayChars)
			}
			b.WriteString(toolStyle.Render("Tool: "+disp) + "\n\n")
		case "error":
			b.WriteString(errorStyle.Render("Error: "+msg.Content) + "\n\n")
		}
	}
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

func callLLMCmd(reg *tools.Registry, history []Message) tea.Cmd {
	snapshot := make([]Message, 0, len(history))
	for _, msg := range history {
		if msg.Role != "error" {
			snapshot = append(snapshot, msg)
		}
	}
	return func() tea.Msg {
		resp, err := callLLM(reg, snapshot)
		if err != nil {
			return errorMsg{err}
		}
		return llmResponseMsg{assistantMsg: *resp}
	}
}

func callLLM(reg *tools.Registry, messages []Message) (*Message, error) {
	payload := map[string]interface{}{
		"model":       model,
		"messages":    messages,
		"tools":       reg.OpenAITools(),
		"temperature": 0.2,
		"max_tokens":  2048,
	}
	body, _ := json.Marshal(payload)

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
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(data), 300))
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
		return nil, fmt.Errorf("no choices")
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

func executeToolCmd(reg *tools.Registry, tc ToolCall) tea.Cmd {
	return func() tea.Msg {
		result := reg.Run(context.Background(), tc.Function.Name, json.RawMessage(tc.Function.Arguments))
		return toolExecutedMsg{
			toolMsg: Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    truncate(result, maxLLMToolOutput),
				Display:    tc.Function.Name + " -> " + truncate(result, maxUIDisplayChars),
			},
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	reg, err := tools.NewRegistry(cwd, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init tools: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(reg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("TUI error: %v\n", err)
		os.Exit(1)
	}
	_ = time.Second
}
