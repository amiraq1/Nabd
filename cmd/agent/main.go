package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	AgentName = "NabdCoder"
	maxIter   = 8
)

var (
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	toolStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	helpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
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
	registry  *tools.Registry
}

func initialModel(reg *tools.Registry) model {
	ti := textinput.New()
	ti.Placeholder = "رسالة لـ NabdCoder..."
	ti.Focus()
	ti.Width = 80

	vp := viewport.New(80, 20)
	vp.SetContent("NabdCoder جاهز — أدوات محمية + قيود ذاكرة Termux\n")

	system := `أنت NabdCoder، وكيل متخصص في مشروع Nabd.
استخدم الأدوات المتاحة:
- read_file(path)
- edit_file(path, old_text, new_text)  ← مع فحص syntax
- go_verify(pkg)                        ← مع قيود ذاكرة
- list_dir(path)

ركز على الأمان وجودة الكود. أجب بالعربية.`

	return model{
		textInput: ti,
		viewport:  vp,
		messages:  []Message{{Role: "system", Content: system}},
		status:    "جاهز",
		registry:  reg,
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			if input == "" || input == "exit" {
				if input == "exit" {
					return m, tea.Quit
				}
				return m, nil
			}
			if input == "clear" {
				m.messages = m.messages[:1]
				m.refresh()
				m.textInput.SetValue("")
				return m, nil
			}
			m.messages = append(m.messages, Message{Role: "user", Content: input})
			m.textInput.SetValue("")
			m.busy = true
			m.status = "يفكر..."
			m.refresh()
			return m, m.runAgent()
		}
	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6
		m.textInput.Width = msg.Width - 4
		m.refresh()
	case agentDone:
		m.busy = false
		m.status = "جاهز"
		m.refresh()
		return m, nil
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	m.viewport, _ = m.viewport.Update(msg)
	return m, cmd
}

func (m model) View() string {
	return titleStyle.Render("NabdCoder") + "  " + statusStyle.Render(m.status) + "\n" +
		m.viewport.View() + "\n" +
		helpStyle.Render("────────────────────────────────") + "\n" +
		m.textInput.View() + "\n" +
		helpStyle.Render("Enter: إرسال | clear | Ctrl+C")
}

func (m *model) refresh() {
	var b strings.Builder
	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			b.WriteString(userStyle.Render("أنت: ") + msg.Content + "\n\n")
		case "assistant":
			b.WriteString(assistantStyle.Render("NabdCoder: ") + msg.Content + "\n\n")
		case "tool":
			b.WriteString(toolStyle.Render("🔧 ") + msg.Content + "\n\n")
		}
	}
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

type agentDone struct{}

func (m model) runAgent() tea.Cmd {
	return func() tea.Msg {
		history := append([]Message{}, m.messages...)
		ctx := context.Background()

		for i := 0; i < maxIter; i++ {
			resp := callLLMStub(history)
			if resp.Content != "" {
				history = append(history, Message{Role: "assistant", Content: resp.Content})
			}
			if len(resp.ToolCalls) == 0 {
				m.messages = history
				return agentDone{}
			}
			for _, tc := range resp.ToolCalls {
				args, _ := json.Marshal(tc.Arguments)
				result := m.registry.Run(ctx, tc.Name, args)
				history = append(history, Message{
					Role:    "tool",
					Content: tc.Name + " → " + result,
				})
			}
		}
		m.messages = history
		return agentDone{}
	}
}

type toolCall struct {
	Name      string
	Arguments map[string]interface{}
}
type llmResp struct {
	Content   string
	ToolCalls []toolCall
}

func callLLMStub(msgs []Message) llmResp {
	last := msgs[len(msgs)-1].Content
	if strings.Contains(strings.ToLower(last), "test") || strings.Contains(last, "اختبار") {
		return llmResp{
			Content: "سأشغل go_verify للتحقق من الاختبارات.",
			ToolCalls: []toolCall{{
				Name:      "go_verify",
				Arguments: map[string]interface{}{"pkg": "./..."},
			}},
		}
	}
	return llmResp{Content: "فهمت. أخبرني بالتعديل المطلوب أو الملف الذي تريد مراجعته."}
}

func terminalApprover(toolName, summary string) bool {
	fmt.Printf("\n[بوابة الأمان] الأداة: %s\nالمعاملات: %s\nموافق؟ [y/N]: ", toolName, summary)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	ans := strings.TrimSpace(strings.ToLower(line))
	return ans == "y" || ans == "yes" || ans == "نعم"
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "خطأ: %v\n", err)
		os.Exit(1)
	}

	reg, err := tools.NewRegistry(cwd, terminalApprover)
	if err != nil {
		fmt.Fprintf(os.Stderr, "خطأ في تهيئة الأدوات: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(reg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("خطأ TUI: %v\n", err)
		os.Exit(1)
	}
	_ = time.Second
}
