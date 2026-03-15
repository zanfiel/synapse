package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var mdRenderer *glamour.TermRenderer

func initMarkdown(width int) {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width - 4),
	)
	if err != nil {
		return
	}
	mdRenderer = r
}

// RenderMarkdown renders markdown text with syntax highlighting.
// Falls back to plain text if rendering fails.
func RenderMarkdown(text string, width int) string {
	if mdRenderer == nil {
		initMarkdown(width)
	}
	if mdRenderer == nil {
		return text
	}

	// Only render if the text looks like it has markdown
	if !hasMarkdown(text) {
		return text
	}

	rendered, err := mdRenderer.Render(text)
	if err != nil {
		return text
	}

	// Glamour adds trailing newlines, trim them
	return strings.TrimRight(rendered, "\n")
}

func hasMarkdown(text string) bool {
	indicators := []string{
		"```", "**", "##", "- ", "* ", "1. ", "> ", "| ", "[", "~~",
	}
	for _, ind := range indicators {
		if strings.Contains(text, ind) {
			return true
		}
	}
	return false
}

// RenderDiff renders a unified diff with colors.
func RenderDiff(diff string, s Styles) string {
	var sb strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#10B981")).Render(line))
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Render(line))
		case strings.HasPrefix(line, "@@"):
			sb.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#6366F1")).Render(line))
		default:
			sb.WriteString(line)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
