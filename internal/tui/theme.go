package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme defines all colors for the TUI.
type Theme struct {
	Name        string
	Accent      lipgloss.Color
	Dim         lipgloss.Color
	Error       lipgloss.Color
	Success     lipgloss.Color
	Tool        lipgloss.Color
	Think       lipgloss.Color
	Text        lipgloss.Color
	UserInput   lipgloss.Color
	Border      lipgloss.Color
	Header      lipgloss.Color
	StatusBg    lipgloss.Color
}

var themes = map[string]Theme{
	"synapse": {
		Name:    "synapse",
		Accent:  lipgloss.Color("#7C3AED"),
		Dim:     lipgloss.Color("#6B7280"),
		Error:   lipgloss.Color("#EF4444"),
		Success: lipgloss.Color("#10B981"),
		Tool:    lipgloss.Color("#F59E0B"),
		Think:   lipgloss.Color("#6366F1"),
		Text:    lipgloss.Color("#E5E7EB"),
		UserInput: lipgloss.Color("#93C5FD"),
		Border:  lipgloss.Color("#374151"),
		Header:  lipgloss.Color("#7C3AED"),
		StatusBg: lipgloss.Color("#1F2937"),
	},
	"tokyo": {
		Name:    "tokyo",
		Accent:  lipgloss.Color("#7AA2F7"),
		Dim:     lipgloss.Color("#565F89"),
		Error:   lipgloss.Color("#F7768E"),
		Success: lipgloss.Color("#9ECE6A"),
		Tool:    lipgloss.Color("#E0AF68"),
		Think:   lipgloss.Color("#BB9AF7"),
		Text:    lipgloss.Color("#C0CAF5"),
		UserInput: lipgloss.Color("#7DCFFF"),
		Border:  lipgloss.Color("#3B4261"),
		Header:  lipgloss.Color("#7AA2F7"),
		StatusBg: lipgloss.Color("#1A1B26"),
	},
	"dracula": {
		Name:    "dracula",
		Accent:  lipgloss.Color("#BD93F9"),
		Dim:     lipgloss.Color("#6272A4"),
		Error:   lipgloss.Color("#FF5555"),
		Success: lipgloss.Color("#50FA7B"),
		Tool:    lipgloss.Color("#F1FA8C"),
		Think:   lipgloss.Color("#FF79C6"),
		Text:    lipgloss.Color("#F8F8F2"),
		UserInput: lipgloss.Color("#8BE9FD"),
		Border:  lipgloss.Color("#44475A"),
		Header:  lipgloss.Color("#BD93F9"),
		StatusBg: lipgloss.Color("#282A36"),
	},
	"gruvbox": {
		Name:    "gruvbox",
		Accent:  lipgloss.Color("#D79921"),
		Dim:     lipgloss.Color("#928374"),
		Error:   lipgloss.Color("#CC241D"),
		Success: lipgloss.Color("#98971A"),
		Tool:    lipgloss.Color("#D65D0E"),
		Think:   lipgloss.Color("#B16286"),
		Text:    lipgloss.Color("#EBDBB2"),
		UserInput: lipgloss.Color("#83A598"),
		Border:  lipgloss.Color("#504945"),
		Header:  lipgloss.Color("#D79921"),
		StatusBg: lipgloss.Color("#282828"),
	},
	"catppuccin": {
		Name:    "catppuccin",
		Accent:  lipgloss.Color("#CBA6F7"),
		Dim:     lipgloss.Color("#6C7086"),
		Error:   lipgloss.Color("#F38BA8"),
		Success: lipgloss.Color("#A6E3A1"),
		Tool:    lipgloss.Color("#F9E2AF"),
		Think:   lipgloss.Color("#F5C2E7"),
		Text:    lipgloss.Color("#CDD6F4"),
		UserInput: lipgloss.Color("#89DCEB"),
		Border:  lipgloss.Color("#45475A"),
		Header:  lipgloss.Color("#CBA6F7"),
		StatusBg: lipgloss.Color("#1E1E2E"),
	},
	"nord": {
		Name:    "nord",
		Accent:  lipgloss.Color("#88C0D0"),
		Dim:     lipgloss.Color("#4C566A"),
		Error:   lipgloss.Color("#BF616A"),
		Success: lipgloss.Color("#A3BE8C"),
		Tool:    lipgloss.Color("#EBCB8B"),
		Think:   lipgloss.Color("#B48EAD"),
		Text:    lipgloss.Color("#ECEFF4"),
		UserInput: lipgloss.Color("#81A1C1"),
		Border:  lipgloss.Color("#3B4252"),
		Header:  lipgloss.Color("#88C0D0"),
		StatusBg: lipgloss.Color("#2E3440"),
	},
}

func GetTheme(name string) Theme {
	if t, ok := themes[name]; ok {
		return t
	}
	return themes["synapse"]
}

func ThemeNames() []string {
	var names []string
	for name := range themes {
		names = append(names, name)
	}
	return names
}

// Styles generates lipgloss styles from a theme.
type Styles struct {
	Header       lipgloss.Style
	Assistant    lipgloss.Style
	ToolName     lipgloss.Style
	ToolResult   lipgloss.Style
	Think        lipgloss.Style
	Error        lipgloss.Style
	Success      lipgloss.Style
	InputPrompt  lipgloss.Style
	UserInput    lipgloss.Style
	Status       lipgloss.Style
	Usage        lipgloss.Style
	Dim          lipgloss.Style
	Border       lipgloss.Style
	StatusBar    lipgloss.Style
	SearchPrompt lipgloss.Style
}

func NewStyles(t Theme) Styles {
	return Styles{
		Header:      lipgloss.NewStyle().Bold(true).Foreground(t.Header),
		Assistant:   lipgloss.NewStyle().Foreground(t.Text),
		ToolName:    lipgloss.NewStyle().Bold(true).Foreground(t.Tool),
		ToolResult:  lipgloss.NewStyle().Foreground(t.Dim),
		Think:       lipgloss.NewStyle().Italic(true).Foreground(t.Think),
		Error:       lipgloss.NewStyle().Foreground(t.Error),
		Success:     lipgloss.NewStyle().Foreground(t.Success),
		InputPrompt: lipgloss.NewStyle().Bold(true).Foreground(t.Accent),
		UserInput:   lipgloss.NewStyle().Foreground(t.UserInput),
		Status:      lipgloss.NewStyle().Foreground(t.Dim),
		Usage:       lipgloss.NewStyle().Foreground(t.Dim).Italic(true),
		Dim:         lipgloss.NewStyle().Foreground(t.Dim),
		Border:      lipgloss.NewStyle().Foreground(t.Border),
		StatusBar:   lipgloss.NewStyle().Background(t.StatusBg).Foreground(t.Text),
		SearchPrompt: lipgloss.NewStyle().Bold(true).Foreground(t.Success),
	}
}

func (s Styles) Separator(width int) string {
	return s.Border.Render(strings.Repeat("─", width))
}

func (s Styles) FormatToolCall(name, args string) string {
	return s.ToolName.Render(fmt.Sprintf("⚡ %s", name)) + " " + s.Dim.Render(args)
}

func (s Styles) FormatToolResult(result string) string {
	return s.ToolResult.Render("  → " + result)
}
