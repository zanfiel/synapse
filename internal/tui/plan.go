package tui

import (
	"fmt"
	"strings"
)

// PlanPanel is an overlay that shows a generated plan.
// The plan is generated in read-only mode, then can be executed.
type PlanPanel struct {
	Active   bool
	Task     string
	Content  *strings.Builder
	Lines    []string
	ScrollY  int
	Width    int
	Height   int
	Done     bool // agent finished generating
}

func (p *PlanPanel) Open(task string, width, height int) {
	p.Active = true
	p.Task = task
	p.Content = &strings.Builder{}
	p.Lines = nil
	p.ScrollY = 0
	p.Width = width
	p.Height = height
	p.Done = false
}

func (p *PlanPanel) Close() {
	p.Active = false
	p.Content = nil
	p.Lines = nil
}

func (p *PlanPanel) AppendText(text string) {
	if p.Content == nil {
		return
	}
	p.Content.WriteString(text)
	p.Lines = strings.Split(p.Content.String(), "\n")
}

func (p *PlanPanel) ScrollUp(n int) {
	p.ScrollY -= n
	if p.ScrollY < 0 {
		p.ScrollY = 0
	}
}

func (p *PlanPanel) ScrollDown(n int) {
	p.ScrollY += n
	maxScroll := len(p.Lines) - (p.Height - 8)
	if maxScroll < 0 {
		maxScroll = 0
	}
	if p.ScrollY > maxScroll {
		p.ScrollY = maxScroll
	}
}

func (p *PlanPanel) Render(styles Styles) string {
	if !p.Active {
		return ""
	}

	visibleH := p.Height - 6 // header + footer + borders
	if visibleH < 3 {
		visibleH = 3
	}
	contentW := p.Width - 4 // side borders + padding

	// Header
	var sb strings.Builder
	header := fmt.Sprintf("  📋 Plan: %s  ", truncate(p.Task, contentW-16))
	sb.WriteString(styles.Header.Render(header))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("─", p.Width))
	sb.WriteString("\n")

	// Content
	lines := p.Lines
	if lines == nil {
		lines = []string{"Generating plan..."}
	}

	start := p.ScrollY
	if start >= len(lines) {
		start = max(0, len(lines)-1)
	}
	end := start + visibleH
	if end > len(lines) {
		end = len(lines)
	}

	for i := start; i < end; i++ {
		line := lines[i]
		if len(line) > contentW {
			line = line[:contentW]
		}
		sb.WriteString("  ")
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	// Pad remaining
	for i := end - start; i < visibleH; i++ {
		sb.WriteString("\n")
	}

	// Footer
	sb.WriteString(strings.Repeat("─", p.Width))
	sb.WriteString("\n")
	if p.Done {
		sb.WriteString(styles.Dim.Render("  enter=execute plan  esc=close  ↑/↓=scroll"))
	} else {
		sb.WriteString(styles.Dim.Render("  generating...  esc=cancel  ↑/↓=scroll"))
	}

	return sb.String()
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
