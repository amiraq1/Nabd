package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	AgentName      = "NabdCoder"
	defaultBaseURL = "http://127.0.0.1:8080/v1"
	defaultModel   = "local"
	maxIterations  = 10
)

var (
	baseURL = defaultBaseURL
	model   = defaultModel
	apiKey  = "sk-no-key"
)

// Styles
var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Italic(true)
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	helpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	nabdStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

type Message struct {
	Role    string
	Content string
}

type model struct {
	viewport  viewport.Model
	textInput textinput.Model
	messages  []Message
	status    string
	busy      bool
	width     int
	height    int
	err       error
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "رسالة لـ NabdCoder... (Ctrl+C للخروج)"
	ti.Focus()
	ti.CharLimit = 2000
	ti.Width = 80

	vp := viewport.New(80, 20)
	vp.SetContent(nabdStyle.Render("NabdCoder") + " — وكيل متخصص في مشروع Nabd\n")

	systemPrompt := `أنت NabdCoder، وكيل برمجة متخصص في مشروع Nabd (مستودع Go أمني).

خلفية المشروع:
- Nabd هو نظام موافقات (Approval System) مع Shadow Storage وUndo/Restore.
- يركز على الأمان: منع تسرب المفاتيح، حماية shadow reads، التحكم في hook writes.
- مكتوب بـ Go، ويعمل على Windows وTermux/Linux.
- يحتوي على Threat Model وRED baseline واختبارات صارمة.

قواعد العمل:
- ركز على جودة الكود والأمان.
- استخدم الأدوات عند الحاجة (قراءة ملفات، تعديل، تشغيل اختبارات).
- عند كتابة كود جديد التزم بأسلوب المشروع الموجود.
- لا تقترح تغييرات تكسر نظام الموافقات أو shadow storage.
- أجب بالعربية ما لم يطلب المستخدم غير ذلك.

الأدوات المتاحة:
- read_file(path)
- write_file(path, content)
- run_command(command)
- list_dir(path)`

	m := model{
		textInput: ti,
		viewport:  vp,
		messages: []Message{
			{Role: "system", Content: systemPrompt},
		},
		status: "جاهز | Nabd",
	}
	return m
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
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
				m.refreshViewport()
				m.textInput.SetValue("")
				return m, nil
			}

			m.messages = append(m.messages, Message{Role: "user", Content: input})
			m.textInput.SetValue("")
			m.busy = true
			m.status = "يفكر في Nabd..."
			m.refreshViewport()
			return m, m.callAgent()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6
		m.textInput.Width = msg.Width - 4
		m.refreshViewport()

	case agentDoneMsg:
		m.busy = false
		m.status = "جاهز | Nabd"
		m.refreshViewport()
		return m, nil

	case errorMsg:
		m.busy = false
		m.status = "خطأ"
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

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("NabdCoder") + "  " + statusStyle.Render(m.status) + "\n")
	b.WriteString(m.viewport.View() + "\n")
	b.WriteString(helpStyle.Render("──────────────────────────────────────") + "\n")
	b.WriteString(m.textInput.View() + "\n")
	b.WriteString(helpStyle.Render("Enter: إرسال | clear: مسح | Ctrl+C: خروج"))
	return b.String()
}

func (m *model) refreshViewport() {
	var content strings.Builder
	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			content.WriteString(userStyle.Render("أنت: ") + msg.Content + "\n\n")
		case "assistant":
			content.WriteString(assistantStyle.Render("NabdCoder: ") + msg.Content + "\n\n")
		case "tool":
			content.WriteString(toolStyle.Render("🔧 ") + msg.Content + "\n\n")
		case "error":
			content.WriteString(errorStyle.Render("❌ ") + msg.Content + "\n\n")
		}
	}
	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

// ==================== Agent Logic ====================

type agentDoneMsg struct{}
type errorMsg struct{ err error }

func (m model) callAgent() tea.Cmd {
	return func() tea.Msg {
		history := make([]Message, len(m.messages))
		copy(history, m.messages)

		for i := 0; i < maxIterations; i++ {
			resp, err := callLLM(history)
			if err != nil {
				return errorMsg{err}
			}
			if resp.Content != "" {
				history = append(history, Message{Role: "assistant", Content: resp.Content})
			}
			if len(resp.ToolCalls) == 0 {
				m.messages = history
				return agentDoneMsg{}
			}
			for _, tc := range resp.ToolCalls {
				result := executeTool(tc)
				history = append(history, Message{
					Role:    "tool",
					Content: fmt.Sprintf("%s → %s", tc.Name, truncate(result, 400)),
				})
			}
		}
		m.messages = history
		return agentDoneMsg{}
	}
}

// ==================== LLM + Tools ====================

type ToolCall struct {
	Name      string
	Arguments map[string]interface{}
}

type AgentResponse struct {
	Content   string
	ToolCalls []ToolCall
}

func callLLM(messages []Message) (*AgentResponse, error) {
	type apiMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	apiMessages := make([]apiMsg, len(messages))
	for i, m := range messages {
		apiMessages[i] = apiMsg{Role: m.Role, Content: m.Content}
	}

	payload := map[string]interface{}{
		"model":       model,
		"messages":    apiMessages,
		"temperature": 0.2,
		"max_tokens":  2048,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", baseURL+"/chat/completions", bytes.NewReader(body))
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
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(data[:min(300, len(data))]))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices")
	}

	msg := result.Choices[0].Message
	ar := &AgentResponse{Content: msg.Content}
	for _, tc := range msg.ToolCalls {
		var args map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		ar.ToolCalls = append(ar.ToolCalls, ToolCall{Name: tc.Function.Name, Arguments: args})
	}
	return ar, nil
}

func executeTool(tc ToolCall) string {
	switch tc.Name {
	case "read_file":
		path, _ := tc.Arguments["path"].(string)
		return toolReadFile(path)
	case "write_file":
		path, _ := tc.Arguments["path"].(string)
		content, _ := tc.Arguments["content"].(string)
		return toolWriteFile(path, content)
	case "run_command":
		cmd, _ := tc.Arguments["command"].(string)
		return toolRunCommand(cmd)
	case "list_dir":
		path, _ := tc.Arguments["path"].(string)
		if path == "" {
			path = "."
		}
		return toolListDir(path)
	default:
		return "أداة غير معروفة: " + tc.Name
	}
}

func toolReadFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "خطأ: " + err.Error()
	}
	return string(data)
}

func toolWriteFile(path, content string) string {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "خطأ: " + err.Error()
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "خطأ: " + err.Error()
	}
	return "تم الكتابة: " + path
}

func toolRunCommand(command string) string {
	cmd := exec.Command("sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("خطأ: %s\n%s", err.Error(), string(out))
	}
	return string(out)
}

func toolListDir(path string) string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "خطأ: " + err.Error()
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("خطأ في تشغيل NabdCoder: %v\n", err)
		os.Exit(1)
	}
}
