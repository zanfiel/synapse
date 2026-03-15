package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// DiffPanel displays a file diff overlay with accept/reject controls.
type DiffPanel struct {
	FilePath   string
	OldContent string
	NewContent string
	ToolCallID string
	Lines      []DiffLine
	Visible    bool
	ScrollY    int
	width      int
	height     int
}

// DiffLine represents a single line in the unified diff.
type DiffLine struct {
	Type    DiffLineType
	Content string
}

type DiffLineType int

const (
	DiffContext DiffLineType = iota
	DiffAdd
	DiffRemove
	DiffHeader
)

// Open sets up the diff panel with old and new content.
func (dp *DiffPanel) Open(filePath, oldContent, newContent, toolCallID string, w, h int) {
	dp.FilePath = filePath
	dp.OldContent = oldContent
	dp.NewContent = newContent
	dp.ToolCallID = toolCallID
	dp.Visible = true
	dp.ScrollY = 0
	dp.width = w
	dp.height = h
	dp.Lines = computeDiff(oldContent, newContent)
}

// Close hides the diff panel.
func (dp *DiffPanel) Close() {
	dp.Visible = false
	dp.Lines = nil
	dp.OldContent = ""
	dp.NewContent = ""
}

// ScrollUp scrolls the diff view up.
func (dp *DiffPanel) ScrollUp(n int) {
	dp.ScrollY -= n
	if dp.ScrollY < 0 {
		dp.ScrollY = 0
	}
}

// ScrollDown scrolls the diff view down.
func (dp *DiffPanel) ScrollDown(n int) {
	dp.ScrollY += n
	maxScroll := len(dp.Lines) - dp.viewHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if dp.ScrollY > maxScroll {
		dp.ScrollY = maxScroll
	}
}

func (dp *DiffPanel) viewHeight() int {
	return dp.height - 8 // header + footer + borders
}

// computeDiff generates unified diff lines from old and new content.
func computeDiff(oldContent, newContent string) []DiffLine {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldContent, newContent, true)
	diffs = dmp.DiffCleanupSemantic(diffs)

	var lines []DiffLine

	// Convert character diffs to line-based diffs
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Use a simple line diff approach
	type lineOp struct {
		op   int // -1 remove, 0 context, 1 add
		text string
	}

	var ops []lineOp
	o, n := 0, 0

	// Match lines using LCS-style approach
	// Simple O(n*m) DP would be expensive for large files
	// Use go-diff's line mode for efficiency
	lineDiffs := dmp.DiffMain(
		strings.Join(oldLines, "\n"),
		strings.Join(newLines, "\n"),
		true,
	)
	lineDiffs = dmp.DiffCleanupSemantic(lineDiffs)

	for _, d := range lineDiffs {
		dLines := strings.Split(d.Text, "\n")
		for i, l := range dLines {
			// Skip empty trailing line from split
			if i == len(dLines)-1 && l == "" {
				continue
			}
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				ops = append(ops, lineOp{0, l})
			case diffmatchpatch.DiffDelete:
				ops = append(ops, lineOp{-1, l})
			case diffmatchpatch.DiffInsert:
				ops = append(ops, lineOp{1, l})
			}
		}
	}

	_ = o
	_ = n

	// Context collapse: show 3 lines of context around changes
	const contextLines = 3
	showLine := make([]bool, len(ops))
	for i, op := range ops {
		if op.op != 0 {
			// Mark surrounding context lines
			for j := max(0, i-contextLines); j <= min(len(ops)-1, i+contextLines); j++ {
				showLine[j] = true
			}
		}
	}

	lastShown := -1
	for i, op := range ops {
		if !showLine[i] {
			continue
		}

		// Add separator if there's a gap
		if lastShown >= 0 && i-lastShown > 1 {
			lines = append(lines, DiffLine{DiffHeader, fmt.Sprintf("@@ ... %d lines hidden ... @@", i-lastShown-1)})
		}
		lastShown = i

		switch op.op {
		case 0:
			lines = append(lines, DiffLine{DiffContext, "  " + op.text})
		case -1:
			lines = append(lines, DiffLine{DiffRemove, "─ " + op.text})
		case 1:
			lines = append(lines, DiffLine{DiffAdd, "+ " + op.text})
		}
	}

	return lines
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Render draws the diff overlay.
func (dp *DiffPanel) Render(styles Styles) string {
	panelWidth := dp.width - 4
	if panelWidth < 40 {
		panelWidth = 40
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#f59e0b")).
		Padding(0, 1).
		Width(panelWidth)

	// Header
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f59e0b"))
	header := headerStyle.Render(fmt.Sprintf("📝 %s", dp.FilePath))

	// Stats
	adds, removes := 0, 0
	for _, l := range dp.Lines {
		switch l.Type {
		case DiffAdd:
			adds++
		case DiffRemove:
			removes++
		}
	}
	statsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b"))
	stats := statsStyle.Render(fmt.Sprintf("+%d -%d", adds, removes))

	// Key hints
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b"))
	hints := hintStyle.Render("[a] accept  [r] reject  [↑↓] scroll  [esc] close")

	// Diff content
	vh := dp.viewHeight()
	if vh < 5 {
		vh = 5
	}

	var diffLines []string
	end := dp.ScrollY + vh
	if end > len(dp.Lines) {
		end = len(dp.Lines)
	}

	contentWidth := panelWidth - 4
	for i := dp.ScrollY; i < end; i++ {
		line := dp.Lines[i]
		text := line.Content
		// Truncate long lines
		if len(text) > contentWidth {
			text = text[:contentWidth-1] + "…"
		}

		var styled string
		switch line.Type {
		case DiffAdd:
			styled = lipgloss.NewStyle().Foreground(lipgloss.Color("#7ee787")).Render(text)
		case DiffRemove:
			styled = lipgloss.NewStyle().Foreground(lipgloss.Color("#f85149")).Render(text)
		case DiffHeader:
			styled = lipgloss.NewStyle().Foreground(lipgloss.Color("#58a6ff")).Bold(true).Render(text)
		default:
			styled = lipgloss.NewStyle().Foreground(lipgloss.Color("#8b949e")).Render(text)
		}
		diffLines = append(diffLines, styled)
	}

	// Pad if fewer lines than viewport
	for len(diffLines) < vh {
		diffLines = append(diffLines, "")
	}

	// Scroll indicator
	scrollInfo := ""
	if len(dp.Lines) > vh {
		pct := 0
		if len(dp.Lines)-vh > 0 {
			pct = dp.ScrollY * 100 / (len(dp.Lines) - vh)
		}
		scrollInfo = statsStyle.Render(fmt.Sprintf(" (%d%%)", pct))
	}

	var rows []string
	rows = append(rows, header+"  "+stats+scrollInfo)
	rows = append(rows, strings.Repeat("─", contentWidth))
	rows = append(rows, diffLines...)
	rows = append(rows, strings.Repeat("─", contentWidth))
	rows = append(rows, hints)

	content := strings.Join(rows, "\n")
	panel := border.Render(content)

	// Center
	panelLines := strings.Split(panel, "\n")
	panelH := len(panelLines)

	topPad := (dp.height - panelH) / 4
	if topPad < 0 {
		topPad = 0
	}
	leftPad := (dp.width - lipgloss.Width(panelLines[0])) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	var out strings.Builder
	for i := 0; i < topPad; i++ {
		out.WriteString(strings.Repeat(" ", dp.width) + "\n")
	}
	for _, line := range panelLines {
		out.WriteString(strings.Repeat(" ", leftPad) + line + "\n")
	}
	for i := 0; i < dp.height-topPad-panelH; i++ {
		out.WriteString(strings.Repeat(" ", dp.width) + "\n")
	}

	return out.String()
}
