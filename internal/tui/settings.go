package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/zanfiel/synapse/internal/config"
)

// SettingsItem represents a single configurable setting.
type SettingsItem struct {
	Key         string
	Label       string
	Description string
	Kind        string // "toggle", "cycle", "text"
	Options     []string
	ConfigKey   string // maps to config.Config field name (empty = TUI-only)
	Section     string // group header
}

// SettingsPanel manages the overlay state.
type SettingsPanel struct {
	Items   []SettingsItem
	Cursor  int
	Visible bool
	width   int
	height  int
	// Text editing
	Editing  bool
	EditBuf  string
	EditCurX int
}

var settingsDefinitions = []SettingsItem{
	// ── AI ──
	{Key: "provider", Label: "Provider", Description: "Preferred provider on next launch", Kind: "cycle", Options: []string{"anthropic", "openai"}, ConfigKey: "provider", Section: "AI"},
	{Key: "model", Label: "Model", Description: "Active language model", Kind: "cycle", Options: []string{
		// Claude
		"claude-opus-4.6",
		"claude-sonnet-4.6",
		"claude-opus-4.5",
		"claude-sonnet-4.5",
		"claude-opus-41",
		"claude-sonnet-4",
		"claude-haiku-4.5",
		// GPT
		"gpt-5.4",
		"gpt-5.3-codex",
		"gpt-5.2-codex",
		"gpt-5.2",
		"gpt-5.1-codex-max",
		"gpt-5.1-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.1",
		"gpt-5",
		"gpt-5-mini",
		"gpt-4.1",
		"gpt-4o",
		// Gemini
		"gemini-3.1-pro-preview",
		"gemini-3-pro-preview",
		"gemini-3-flash-preview",
		"gemini-2.5-pro",
		// Other
		"grok-code-fast-1",
	}, ConfigKey: "model", Section: ""},
	{Key: "planning_model", Label: "Planning Model", Description: "Reserved for future dedicated planner; /plan currently uses the active model", Kind: "cycle", Options: []string{
		"claude-opus-4.6",
		"claude-opus-4.5",
		"claude-sonnet-4.6",
		"gpt-5.4",
		"gpt-5",
		"gpt-4o",
	}, ConfigKey: "planning_model", Section: ""},
	{Key: "reasoning", Label: "Reasoning Level", Description: "Extended thinking budget", Kind: "cycle", Options: []string{"off", "low", "medium", "high", "xhigh"}, ConfigKey: "reasoning_level", Section: ""},
	{Key: "max_tokens", Label: "Max Tokens", Description: "Maximum response length", Kind: "cycle", Options: []string{"4096", "8192", "16384", "32768"}, ConfigKey: "max_tokens", Section: ""},
	{Key: "anthropic_api_key", Label: "Anthropic API Key", Description: "API key for Anthropic provider", Kind: "text", ConfigKey: "anthropic_api_key", Section: ""},

	// ── Engram ──
	{Key: "engram_url", Label: "Engram URL", Description: "Memory server URL (e.g. http://localhost:4200)", Kind: "text", ConfigKey: "engram_url", Section: "Memory"},
	{Key: "engram_token", Label: "Engram Token", Description: "API token for Engram", Kind: "text", ConfigKey: "engram_token", Section: ""},
	{Key: "auto_capture", Label: "Auto Capture", Description: "Auto-store discoveries and decisions to Engram", Kind: "toggle", ConfigKey: "auto_capture", Section: ""},

	// ── Behavior ──
	{Key: "diff_review", Label: "Diff Review", Description: "Show diffs before applying file changes", Kind: "toggle", ConfigKey: "diff_review", Section: "Behavior"},
	{Key: "bash_confirm", Label: "Bash Confirmation", Description: "Require approval before shell commands", Kind: "toggle", Section: ""},
	{Key: "show_thinking", Label: "Show Thinking", Description: "Display thinking indicator during reasoning", Kind: "toggle", Section: ""},
	{Key: "tool_verbose", Label: "Tool Verbosity", Description: "Show full tool output vs compact", Kind: "cycle", Options: []string{"compact", "full"}, Section: ""},
	{Key: "auto_scroll", Label: "Auto-scroll", Description: "Follow streaming output automatically", Kind: "toggle", Section: ""},

	// ── Display ──
	{Key: "theme", Label: "Theme", Description: "Color theme", Kind: "cycle", Section: "Display"},
}

func NewSettingsPanel() SettingsPanel {
	items := make([]SettingsItem, len(settingsDefinitions))
	copy(items, settingsDefinitions)
	for i := range items {
		if items[i].Key == "theme" {
			items[i].Options = ThemeNames()
		}
	}
	return SettingsPanel{Items: items}
}

func (sp *SettingsPanel) Open(w, h int) {
	sp.Visible = true
	sp.width = w
	sp.height = h
	sp.Editing = false
}

func (sp *SettingsPanel) Close() {
	sp.Visible = false
	sp.Editing = false
}

func (sp *SettingsPanel) Up() {
	if sp.Editing {
		return
	}
	if sp.Cursor > 0 {
		sp.Cursor--
	}
}

func (sp *SettingsPanel) Down() {
	if sp.Editing {
		return
	}
	if sp.Cursor < len(sp.Items)-1 {
		sp.Cursor++
	}
}

// StartEdit enters text editing mode for the current item.
func (sp *SettingsPanel) StartEdit(currentValue string) {
	sp.Editing = true
	sp.EditBuf = currentValue
	sp.EditCurX = len([]rune(currentValue))
}

// FinishEdit exits text editing mode and returns the new value.
func (sp *SettingsPanel) FinishEdit() string {
	sp.Editing = false
	val := sp.EditBuf
	sp.EditBuf = ""
	sp.EditCurX = 0
	return val
}

// CancelEdit exits text editing without saving.
func (sp *SettingsPanel) CancelEdit() {
	sp.Editing = false
	sp.EditBuf = ""
	sp.EditCurX = 0
}

// EditKeyPress handles a key press during text editing.
func (sp *SettingsPanel) EditKeyPress(char rune) {
	runes := []rune(sp.EditBuf)
	runes = append(runes[:sp.EditCurX], append([]rune{char}, runes[sp.EditCurX:]...)...)
	sp.EditBuf = string(runes)
	sp.EditCurX++
}

// EditBackspace deletes the character before the cursor.
func (sp *SettingsPanel) EditBackspace() {
	if sp.EditCurX > 0 {
		runes := []rune(sp.EditBuf)
		runes = append(runes[:sp.EditCurX-1], runes[sp.EditCurX:]...)
		sp.EditBuf = string(runes)
		sp.EditCurX--
	}
}

// EditLeft moves the cursor left.
func (sp *SettingsPanel) EditLeft() {
	if sp.EditCurX > 0 {
		sp.EditCurX--
	}
}

// EditRight moves the cursor right.
func (sp *SettingsPanel) EditRight() {
	runes := []rune(sp.EditBuf)
	if sp.EditCurX < len(runes) {
		sp.EditCurX++
	}
}

// CurrentItem returns the currently selected item.
func (sp *SettingsPanel) CurrentItem() *SettingsItem {
	if sp.Cursor >= 0 && sp.Cursor < len(sp.Items) {
		return &sp.Items[sp.Cursor]
	}
	return nil
}

// Render draws the settings overlay panel.
func (sp *SettingsPanel) Render(styles Styles, values map[string]string) string {
	panelWidth := 62
	if sp.width < 70 {
		panelWidth = sp.width - 8
	}
	if panelWidth < 30 {
		panelWidth = 30
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#38bdf8")).
		Padding(1, 2).
		Width(panelWidth)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#38bdf8")).
		Render("⚙ Settings")

	hintText := "↑↓ navigate  ←→/space toggle  enter edit  esc close"
	if sp.Editing {
		hintText = "type value  enter save  esc cancel"
	}
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#64748b")).
		Render(hintText)

	savedNote := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#22c55e")).
		Render("Changes auto-save to config.json")

	var rows []string
	rows = append(rows, title)
	rows = append(rows, hint)
	rows = append(rows, savedNote)
	rows = append(rows, "")

	innerWidth := panelWidth - 6

	for i, item := range sp.Items {
		// Section header
		if item.Section != "" {
			sectionStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#f59e0b")).
				Bold(true)
			if i > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, "  "+sectionStyle.Render("─ "+item.Section+" ─"))
		}

		val := values[item.Key]
		isSelected := i == sp.Cursor

		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8"))
		if isSelected {
			labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#e2e8f0")).Bold(true)
		}

		var valDisplay string
		if item.Kind == "toggle" {
			if val == "on" || val == "true" {
				valDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e")).Render("● ON")
			} else {
				valDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")).Render("○ OFF")
			}
		} else if item.Kind == "text" {
			if isSelected && sp.Editing {
				// Show editable text field with cursor
				cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true)
				runes := []rune(sp.EditBuf)
				before := string(runes[:sp.EditCurX])
				after := string(runes[sp.EditCurX:])
				valDisplay = lipgloss.NewStyle().Foreground(lipgloss.Color("#e2e8f0")).Render(before) +
					cursorStyle.Render("▏") +
					lipgloss.NewStyle().Foreground(lipgloss.Color("#94a3b8")).Render(after)
			} else {
				// Show masked or truncated value
				display := val
				if item.Key == "anthropic_api_key" || item.Key == "engram_token" {
					if len(display) > 8 {
						display = display[:4] + "..." + display[len(display)-4:]
					} else if display == "" {
						display = "(not set)"
					}
				}
				if len(display) > 30 {
					display = display[:27] + "..."
				}
				if display == "" {
					display = "(not set)"
				}
				valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8"))
				if display == "(not set)" {
					valStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")).Italic(true)
				}
				valDisplay = valStyle.Render(display)
			}
		} else {
			valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Bold(true)
			valDisplay = valStyle.Render(val)
		}

		label := labelStyle.Render(item.Label)
		gap := innerWidth - lipgloss.Width(label) - lipgloss.Width(valDisplay)
		if gap < 1 {
			gap = 1
		}

		cursor := "  "
		if isSelected {
			cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("#38bdf8")).Render("▸ ")
		}

		row := cursor + label + strings.Repeat(" ", gap) + valDisplay
		rows = append(rows, row)

		if isSelected && !sp.Editing {
			descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b")).Italic(true)
			rows = append(rows, "    "+descStyle.Render(item.Description))
		}
	}

	content := strings.Join(rows, "\n")
	panel := border.Render(content)

	panelLines := strings.Split(panel, "\n")
	panelH := len(panelLines)

	topPad := (sp.height - panelH) / 3
	if topPad < 1 {
		topPad = 1
	}
	leftPad := (sp.width - lipgloss.Width(panelLines[0])) / 2
	if leftPad < 0 {
		leftPad = 0
	}

	var out strings.Builder
	for i := 0; i < topPad; i++ {
		out.WriteString(strings.Repeat(" ", sp.width) + "\n")
	}
	for _, line := range panelLines {
		out.WriteString(strings.Repeat(" ", leftPad) + line + "\n")
	}
	for i := 0; i < sp.height-topPad-panelH; i++ {
		out.WriteString(strings.Repeat(" ", sp.width) + "\n")
	}

	return out.String()
}

// CycleValue moves to the next option for a cycle-type setting.
func CycleValue(current string, options []string, forward bool) string {
	if len(options) == 0 {
		return current
	}
	idx := 0
	for i, opt := range options {
		if opt == current {
			idx = i
			break
		}
	}
	if forward {
		idx = (idx + 1) % len(options)
	} else {
		idx = (idx - 1 + len(options)) % len(options)
	}
	return options[idx]
}

// ToggleValue flips a boolean setting.
func ToggleValue(current string) string {
	if current == "on" || current == "true" {
		return "off"
	}
	return "on"
}

// FormatSettingsStatus returns a compact one-liner for the status bar.
func FormatSettingsStatus(values map[string]string) string {
	var parts []string
	if v, ok := values["bash_confirm"]; ok && (v == "off" || v == "false") {
		parts = append(parts, "⚠ no-confirm")
	}
	if v, ok := values["reasoning"]; ok && v != "off" && v != "" {
		parts = append(parts, fmt.Sprintf("🧠%s", v))
	}
	return strings.Join(parts, " ")
}

// PersistToConfig writes the current settings values to the config file.
func PersistToConfig(cfg *config.Config, key, value string) {
	if cfg == nil {
		return
	}
	switch key {
	case "provider":
		cfg.Provider = value
	case "model":
		cfg.Model = value
	case "planning_model":
		cfg.PlanningModel = value
	case "reasoning":
		cfg.ReasoningLevel = value
	case "max_tokens":
		n := 16384
		fmt.Sscanf(value, "%d", &n)
		cfg.MaxTokens = n
	case "anthropic_api_key":
		cfg.AnthropicAPIKey = value
	case "engram_url":
		cfg.EngramURL = value
	case "engram_token":
		cfg.EngramToken = value
	case "auto_capture":
		cfg.AutoCapture = (value == "on" || value == "true")
	case "diff_review":
		cfg.DiffReview = (value == "on" || value == "true")
	case "theme":
		cfg.Theme = value
	}
	cfg.Save()
}
