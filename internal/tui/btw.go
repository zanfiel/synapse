package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// BtwPanel is the ephemeral side-question overlay.
// The question and answer are never saved to session history.
type BtwPanel struct {
	Question string
	output   strings.Builder
	loading  bool
	width    int
	height   int
}

func (p *BtwPanel) Open(question string, w, h int) {
	p.Question = question
	p.output.Reset()
	p.loading = true
	p.width = w
	p.height = h
}

func (p *BtwPanel) Append(text string) {
	p.output.WriteString(text)
}

func (p *BtwPanel) Done() {
	p.loading = false
}

func (p *BtwPanel) Render(styles Styles) string {
	panelWidth := 70
	if p.width < 80 {
		panelWidth = p.width - 8
	}
	if panelWidth < 40 {
		panelWidth = 40
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#a78bfa")).
		Padding(1, 2).
		Width(panelWidth)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#a78bfa")).
		Render("💬 btw")

	question := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#e2e8f0")).
		Italic(true).
		Render(p.Question)

	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#334155")).
		Render(strings.Repeat("─", panelWidth-6))

	var answer string
	if p.loading && p.output.Len() == 0 {
		answer = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")).Render("thinking...")
	} else {
		answer = p.output.String()
	}

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748b")).
		Render("space/enter/esc  dismiss")

	content := strings.Join([]string{title, question, divider, answer, "", hint}, "\n")
	panel := border.Render(content)

	// Center horizontally, 1/3 from top
	panelLines := strings.Split(panel, "\n")
	panelH := len(panelLines)
	topPad := (p.height - panelH) / 3
	if topPad < 1 {
		topPad = 1
	}
	leftPad := (p.width - lipgloss.Width(panelLines[0])) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	var out strings.Builder
	for i := 0; i < topPad; i++ {
		out.WriteString(strings.Repeat(" ", p.width) + "\n")
	}
	for _, line := range panelLines {
		out.WriteString(strings.Repeat(" ", leftPad) + line + "\n")
	}
	return out.String()
}
