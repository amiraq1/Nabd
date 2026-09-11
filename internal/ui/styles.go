package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type WidthMode string

const (
	WidthNarrow  WidthMode = "narrow"
	WidthCompact WidthMode = "compact"
	WidthWide    WidthMode = "wide"
)

func widthMode(width int) WidthMode {
	switch {
	case width < 40:
		return WidthNarrow
	case width < 80:
		return WidthCompact
	default:
		return WidthWide
	}
}

type semanticTheme struct {
	Dim      lipgloss.Style
	Bold     lipgloss.Style
	Success  lipgloss.Style
	Error    lipgloss.Style
	Warning  lipgloss.Style
	Running  lipgloss.Style
	Info     lipgloss.Style
	UserCard lipgloss.Style
	UserRole lipgloss.Style
}

func newSemanticTheme(noColor bool) semanticTheme {
	if noColor {
		plain := lipgloss.NewStyle()
		return semanticTheme{
			Dim: plain, Bold: plain, Success: plain, Error: plain,
			Warning: plain, Running: plain, Info: plain,
			UserCard: plain, UserRole: plain,
		}
	}
	userBg := lipgloss.CompleteColor{TrueColor: "#303030", ANSI256: "236", ANSI: "8"}
	userFg := lipgloss.CompleteColor{TrueColor: "#E0E0E0", ANSI256: "254", ANSI: "15"}
	return semanticTheme{
		Dim:      lipgloss.NewStyle().Faint(true),
		Bold:     lipgloss.NewStyle().Bold(true),
		Success:  lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		Error:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		Warning:  lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		Running:  lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		Info:     lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		UserCard: lipgloss.NewStyle().Background(userBg).Foreground(userFg),
		UserRole: lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true).Background(userBg),
	}
}

func noColorRequested() bool { return os.Getenv("NO_COLOR") != "" }

func navigationHint(width int) string {
	switch widthMode(width) {
	case WidthNarrow:
		return "j/k cards · ? help · Esc input"
	case WidthCompact:
		return "j/k cards · n error · p permission · ? help · Esc input"
	default:
		return "j/k or arrows · Enter expand · n next error · p permission · g/G ends · ? hide · Esc input"
	}
}

func formatCodeSpan(s string) string {
	clean := SanitizeForDisplay(s, DisplayPolicy{AllowNewline: false, Redact: true})
	clean = strings.Join(strings.Fields(clean), " ")
	if clean == "" {
		return ""
	}
	return "`" + clean + "`"
}
