package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/zanfiel/synapse/internal/agent"
	"github.com/zanfiel/synapse/internal/config"
	"github.com/zanfiel/synapse/internal/git"
	"github.com/zanfiel/synapse/internal/skills"
)

// MCPStatus is an interface for querying MCP server state without importing the mcp package.
type MCPStatus interface {
	Status() map[string]bool
	AllTools() []MCPToolInfo
}

// MCPToolInfo mirrors mcp.ToolInfo for the TUI.
type MCPToolInfo struct {
	Name        string
	Description string
	ServerName  string
}

// Mode for input handling
type InputMode int

const (
	ModeNormal InputMode = iota
	ModeSearch
	ModeConfirm
	ModeSettings
	ModeBtw
	ModeDiff
	ModePlan
)

// renderTickMsg fires at 30fps to batch viewport updates during streaming.
type renderTickMsg struct{}

type Model struct {
	agent        *agent.Agent
	modelName    string
	theme        Theme
	styles       Styles
	sessionID    string
	providerName string

	// Layout
	viewport viewport.Model
	input    textinput.Model
	width    int
	height   int
	ready    bool

	// State
	mode      InputMode
	streaming bool
	thinking  bool
	output    *strings.Builder // raw output accumulator (pointer to survive copies)
	dirty     bool             // viewport needs refresh on next tick

	// Confirm mode
	confirmPrompt string
	confirmYes    func()
	confirmNo     func()

	// Search
	searchInput textinput.Model
	searchQuery string
	searchHits  []int // line indices
	searchIdx   int

	// Settings panel
	settings       SettingsPanel
	settingsValues map[string]string

	// Tracking
	currentThink   *strings.Builder
	toolCalls      int
	lastUsage      string
	verifiedModel  string
	startTime      time.Time
	history        []string
	historyIdx     int
	gitInfo        string
	tokenCount     int
	contextMax     int
	reasoningLevel string

	// Display settings
	showThinking bool
	toolVerbose  string // "compact" or "full"
	autoScroll   bool

	// Pending markdown block
	pendingText *strings.Builder
	inCodeBlock bool

	// Tab completion
	completer   *Completer
	completions []string
	completeIdx int

	// Message queue — messages typed during streaming
	queuedMessages []string

	// Run generation — incremented each run, stale events ignored
	runGeneration int

	// Token tracking — separate cumulative from context
	cumulativeTokens int // total tokens spent across session
	contextTokens    int // actual current context size (message tokens)

	// Channel for agent events
	eventChan chan agent.Event

	// MCP client reference for /mcp command
	mcpClient MCPStatus

	// Callbacks
	onSessionTitle func(title string)
	onBashConfirm  bool
	usageFunc      func() string // returns formatted usage stats

	// Btw side-question overlay
	btw     BtwPanel
	btwChan chan agent.Event

	// Config persistence
	config *config.Config

	// Diff panel
	diff DiffPanel

	// Plan panel
	plan     PlanPanel
	planChan chan agent.Event
}

type btwEventMsg struct {
	events []agent.Event
}

type planEventMsg struct {
	events []agent.Event
}

type agentEventMsg struct {
	events []agent.Event
	gen    int
}

func NewModel(ag *agent.Agent, modelName, themeName, sessionID string, contextMax int, reasoningLevel string) Model {
	return NewModelWithConfig(ag, modelName, themeName, sessionID, contextMax, reasoningLevel, nil)
}

func NewModelWithConfig(ag *agent.Agent, modelName, themeName, sessionID string, contextMax int, reasoningLevel string, cfg *config.Config) Model {
	theme := GetTheme(themeName)
	styles := NewStyles(theme)

	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Focus()
	ti.CharLimit = 0
	ti.Width = 80

	si := textinput.New()
	si.Placeholder = "Search..."
	si.CharLimit = 100

	// Initialize settings values from config if available
	providerName := "anthropic"
	if cfg != nil && cfg.Provider != "" {
		providerName = cfg.Provider
	}

	sv := map[string]string{
		"bash_confirm":  "on",
		"show_thinking": "on",
		"tool_verbose":  "compact",
		"auto_scroll":   "on",
		"reasoning":     reasoningLevel,
		"model":         modelName,
		"theme":         themeName,
		"provider":      providerName,
		"max_tokens":    "16384",
		"diff_review":   "on",
		"auto_capture":  "off",
	}

	// Populate from config
	if cfg != nil {
		if cfg.MaxTokens > 0 {
			sv["max_tokens"] = fmt.Sprintf("%d", cfg.MaxTokens)
		}
		if cfg.DiffReview {
			sv["diff_review"] = "on"
		}
		if cfg.AutoCapture {
			sv["auto_capture"] = "on"
		}
		if cfg.PlanningModel != "" {
			sv["planning_model"] = cfg.PlanningModel
		} else {
			sv["planning_model"] = "claude-opus-4-6"
		}
		if cfg.AnthropicAPIKey != "" {
			sv["anthropic_api_key"] = cfg.AnthropicAPIKey
		}
		if cfg.EngramURL != "" {
			sv["engram_url"] = cfg.EngramURL
		}
		if cfg.EngramToken != "" {
			sv["engram_token"] = cfg.EngramToken
		}
	}

	return Model{
		agent:          ag,
		modelName:      modelName,
		theme:          theme,
		styles:         styles,
		sessionID:      sessionID,
		providerName:   providerName,
		input:          ti,
		searchInput:    si,
		output:         &strings.Builder{},
		currentThink:   &strings.Builder{},
		pendingText:    &strings.Builder{},
		completer:      NewCompleter(ag.WorkDir()),
		historyIdx:     -1,
		contextMax:     contextMax,
		reasoningLevel: reasoningLevel,
		showThinking:   true,
		toolVerbose:    "compact",
		autoScroll:     true,
		onBashConfirm:  true,
		settings:       NewSettingsPanel(),
		settingsValues: sv,
		config:         cfg,
	}
}

// SetConfig sets the config reference for persistence.
func (m *Model) SetConfig(cfg *config.Config) {
	m.config = cfg
}

func (m *Model) SetGitInfo(info string) {
	m.gitInfo = info
}

func (m *Model) SetTokenCount(n int) {
	m.contextTokens = n
}

func (m *Model) OnSessionTitle(fn func(string)) {
	m.onSessionTitle = fn
}

func (m *Model) SetMCPClient(mc MCPStatus) {
	m.mcpClient = mc
}

func (m *Model) SetUsageFunc(fn func() string) {
	m.usageFunc = fn
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tea.SetWindowTitle("⚡ Synapse"),
		textinput.Blink,
	)
}

// renderTick returns a Cmd that fires after ~33ms (30fps).
func renderTick() tea.Cmd {
	return tea.Tick(33*time.Millisecond, func(t time.Time) tea.Msg {
		return renderTickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerH := 1
		statusH := 1
		inputH := 1
		vpHeight := m.height - headerH - statusH - inputH - 2

		if !m.ready {
			m.viewport = viewport.New(m.width, vpHeight)
			m.viewport.MouseWheelEnabled = true
			m.viewport.MouseWheelDelta = 3
			m.viewport.SetContent("")
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = vpHeight
		}

		m.input.Width = m.width - 4
		m.searchInput.Width = m.width - 12
		initMarkdown(m.width - 4)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case btwEventMsg:
		for _, evt := range msg.events {
			switch evt.Type {
			case agent.EventText:
				m.btw.Append(evt.Text)
			case agent.EventDone, agent.EventError:
				m.btw.Done()
				return m, nil
			}
		}
		if m.mode == ModeBtw {
			return m, waitForBtwEvent(m.btwChan)
		}
		return m, nil

	case planEventMsg:
		for _, evt := range msg.events {
			switch evt.Type {
			case agent.EventText:
				m.plan.AppendText(evt.Text)
			case agent.EventDone, agent.EventError:
				m.plan.Done = true
				return m, nil
			}
		}
		if m.mode == ModePlan {
			return m, waitForPlanEvent(m.planChan)
		}
		return m, nil

	case agentEventMsg:
		// Ignore events from cancelled/stale runs
		if msg.gen != m.runGeneration {
			return m, nil
		}
		var lastCmd tea.Cmd
		for _, evt := range msg.events {
			var next tea.Model
			next, lastCmd = m.handleAgentEvent(evt)
			m = next.(Model)
			if !m.streaming {
				break
			}
		}
		return m, lastCmd

	case renderTickMsg:
		// Batch viewport refresh at 30fps during streaming
		if m.dirty {
			m.viewport.SetContent(m.output.String())
			if m.autoScroll {
				m.viewport.GotoBottom()
			}
			m.dirty = false
		}
		// Keep ticking while streaming
		if m.streaming {
			return m, renderTick()
		}
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		if m.streaming {
			m.agent.Cancel()
			if m.eventChan != nil {
				go func(ch chan agent.Event) {
					for range ch {
					}
				}(m.eventChan)
			}
			m.streaming = false
			m.queuedMessages = nil
			m.appendOutput("\n" + m.styles.Error.Render("⚡ Cancelled") + "\n\n")
			return m, nil
		}
		return m, tea.Quit
	}

	switch m.mode {
	case ModeSettings:
		return m.handleSettingsKey(msg)
	case ModeSearch:
		return m.handleSearchKey(msg)
	case ModeConfirm:
		return m.handleConfirmKey(msg)
	case ModeBtw:
		return m.handleBtwKey(msg)
	case ModeDiff:
		return m.handleDiffKey(msg)
	case ModePlan:
		return m.handlePlanKey(msg)
	default:
		return m.handleNormalKey(msg)
	}
}

func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	item := m.settings.CurrentItem()

	// Text editing mode
	if m.settings.Editing {
		switch msg.Type {
		case tea.KeyEsc:
			m.settings.CancelEdit()
			return m, nil
		case tea.KeyEnter:
			newVal := m.settings.FinishEdit()
			if item != nil {
				m.settingsValues[item.Key] = newVal
				PersistToConfig(m.config, item.Key, newVal)
			}
			return m, nil
		case tea.KeyBackspace:
			m.settings.EditBackspace()
			return m, nil
		case tea.KeyLeft:
			m.settings.EditLeft()
			return m, nil
		case tea.KeyRight:
			m.settings.EditRight()
			return m, nil
		default:
			if msg.Type == tea.KeyRunes {
				for _, r := range msg.Runes {
					m.settings.EditKeyPress(r)
				}
			} else if msg.Type == tea.KeySpace {
				m.settings.EditKeyPress(' ')
			}
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.settings.Close()
		m.mode = ModeNormal
		m.input.Focus()
		return m, textinput.Blink

	case tea.KeyUp:
		m.settings.Up()
		return m, nil

	case tea.KeyDown:
		m.settings.Down()
		return m, nil

	case tea.KeyRight:
		if item != nil && item.Kind != "text" {
			m.applySettingChange(item, true)
		}
		return m, nil

	case tea.KeyLeft:
		if item != nil && item.Kind != "text" {
			m.applySettingChange(item, false)
		}
		return m, nil

	case tea.KeyEnter:
		if item != nil {
			if item.Kind == "text" {
				m.settings.StartEdit(m.settingsValues[item.Key])
			} else {
				m.applySettingChange(item, true)
			}
		}
		return m, nil

	case tea.KeySpace:
		if item != nil && item.Kind != "text" {
			m.applySettingChange(item, true)
		}
		return m, nil
	}

	// Also handle 'q' to close
	if msg.String() == "q" {
		m.settings.Close()
		m.mode = ModeNormal
		m.input.Focus()
		return m, textinput.Blink
	}

	return m, nil
}

func (m Model) handleDiffKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// Close without accepting — reject the edit
		m.diff.Close()
		m.mode = ModeNormal
		m.input.Focus()
		return m, textinput.Blink

	case tea.KeyUp:
		m.diff.ScrollUp(3)
		return m, nil

	case tea.KeyDown:
		m.diff.ScrollDown(3)
		return m, nil

	case tea.KeyPgUp:
		m.diff.ScrollUp(20)
		return m, nil

	case tea.KeyPgDown:
		m.diff.ScrollDown(20)
		return m, nil
	}

	switch msg.String() {
	case "a": // accept
		m.diff.Close()
		m.mode = ModeNormal
		m.input.Focus()
		// Send accept on confirmCh if available
		return m, textinput.Blink

	case "r": // reject
		m.diff.Close()
		m.mode = ModeNormal
		m.input.Focus()
		return m, textinput.Blink

	case "j":
		m.diff.ScrollDown(1)
		return m, nil
	case "k":
		m.diff.ScrollUp(1)
		return m, nil
	}

	return m, nil
}

func (m Model) handlePlanKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.agent.Cancel()
		m.plan.Close()
		m.mode = ModeNormal
		m.input.Focus()
		return m, textinput.Blink

	case tea.KeyEnter:
		if m.plan.Done && m.plan.Content != nil {
			// Execute the plan: send the plan as context + "execute this plan" to agent
			planText := m.plan.Content.String()
			m.plan.Close()
			m.mode = ModeNormal
			m.input.Focus()
			m.streaming = true
			m.startTime = time.Now()
			m.toolCalls = 0
			m.lastUsage = ""
			m.verifiedModel = ""
			m.pendingText.Reset()
			m.runGeneration++
			m.appendOutput(m.styles.UserInput.Render("▸ [executing plan]") + "\n\n")
			events := make(chan agent.Event, 500)
			m.eventChan = events
			prompt := "Execute the following plan. Do not re-plan, just implement each step:\n\n" + planText
			go m.agent.Run(prompt, events)
			return m, tea.Batch(waitForEvent(events, m.runGeneration), renderTick())
		}
		return m, nil

	case tea.KeyUp:
		m.plan.ScrollUp(3)
		return m, nil
	case tea.KeyDown:
		m.plan.ScrollDown(3)
		return m, nil
	case tea.KeyPgUp:
		m.plan.ScrollUp(20)
		return m, nil
	case tea.KeyPgDown:
		m.plan.ScrollDown(20)
		return m, nil
	}

	switch msg.String() {
	case "j":
		m.plan.ScrollDown(1)
		return m, nil
	case "k":
		m.plan.ScrollUp(1)
		return m, nil
	}

	return m, nil
}

func (m *Model) applySettingChange(item *SettingsItem, forward bool) {
	key := item.Key
	current := m.settingsValues[key]

	var newVal string
	if item.Kind == "toggle" {
		newVal = ToggleValue(current)
	} else {
		newVal = CycleValue(current, item.Options, forward)
	}
	m.settingsValues[key] = newVal

	// Apply side effects
	switch key {
	case "bash_confirm":
		m.onBashConfirm = (newVal == "on")
	case "show_thinking":
		m.showThinking = (newVal == "on")
	case "tool_verbose":
		m.toolVerbose = newVal
	case "auto_scroll":
		m.autoScroll = (newVal == "on")
	case "reasoning":
		m.reasoningLevel = newVal
		m.agent.SetReasoning(newVal)
	case "model":
		m.modelName = newVal
		m.agent.SetModel(newVal)
	case "theme":
		m.theme = GetTheme(newVal)
		m.styles = NewStyles(m.theme)
	case "provider":
		m.providerName = newVal
	}

	// Persist to config.json
	PersistToConfig(m.config, key, newVal)
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.streaming {
		switch msg.Type {
		case tea.KeyEsc:
			// Escape cancels streaming
			m.agent.Cancel()
			if m.eventChan != nil {
				go func(ch chan agent.Event) {
					for range ch {
					}
				}(m.eventChan)
			}
			m.streaming = false
			m.queuedMessages = nil
			m.appendOutput("\n" + m.styles.Error.Render("⚡ Cancelled") + "\n\n")
			m.contextTokens = m.agent.TokenCount()
			return m, nil
		case tea.KeyPgUp:
			m.viewport.HalfViewUp()
			return m, nil
		case tea.KeyPgDown:
			m.viewport.HalfViewDown()
			return m, nil
		case tea.KeyEnter:
			// Cancel current run and send new message immediately
			input := strings.TrimSpace(m.input.Value())
			if input != "" {
				m.agent.Cancel()
				m.streaming = false
				m.queuedMessages = nil
				if m.eventChan != nil {
					go func(ch chan agent.Event) {
						for range ch {
						}
					}(m.eventChan)
				}

				m.appendOutput("\n" + m.styles.Dim.Render("  ⚡ interrupted") + "\n\n")

				// Start new run
				m.history = append(m.history, input)
				m.historyIdx = len(m.history)
				m.input.SetValue("")
				m.streaming = true
				m.startTime = time.Now()
				m.toolCalls = 0
				m.lastUsage = ""
				m.verifiedModel = ""
				m.pendingText.Reset()
				m.runGeneration++

				if m.onSessionTitle != nil && len(m.history) == 1 {
					title := input
					if len(title) > 60 {
						title = title[:60] + "..."
					}
					m.onSessionTitle(title)
				}

				m.appendOutput(m.styles.UserInput.Render("▸ "+input) + "\n\n")

				events := make(chan agent.Event, 500)
				m.eventChan = events
				go m.agent.Run(input, events)
				return m, tea.Batch(waitForEvent(events, m.runGeneration), renderTick())
			}
			return m, nil
		default:
			// Let typing continue into input field while streaming
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}

	// Ctrl+S opens settings
	if msg.Type == tea.KeyCtrlS {
		m.mode = ModeSettings
		m.settings.Open(m.width, m.height)
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEnter:
		if msg.Alt {
			runes := []rune(m.input.Value())
			pos := m.input.Position()
			newRunes := append(runes[:pos], append([]rune{'\n'}, runes[pos:]...)...)
			newVal := string(newRunes)
			m.input.SetValue(newVal)
			m.input.SetCursor(pos + 1)
			return m, nil
		}

		input := strings.TrimSpace(m.input.Value())
		if input == "" {
			return m, nil
		}

		if strings.HasPrefix(input, "/") {
			m2, cmd, handled := m.handleCommand(input)
			if handled {
				return m2, cmd
			}
		}

		m.history = append(m.history, input)
		m.historyIdx = len(m.history)
		m.input.SetValue("")
		m.streaming = true
		m.startTime = time.Now()
		m.toolCalls = 0
		m.lastUsage = ""
		m.verifiedModel = ""
		m.pendingText.Reset()
		m.runGeneration++

		if m.onSessionTitle != nil && len(m.history) == 1 {
			title := input
			if len(title) > 60 {
				title = title[:60] + "..."
			}
			m.onSessionTitle(title)
		}

		m.appendOutput(m.styles.UserInput.Render("▸ "+input) + "\n\n")

		events := make(chan agent.Event, 500)
		m.eventChan = events
		go m.agent.Run(input, events)

		return m, tea.Batch(waitForEvent(events, m.runGeneration), renderTick())

	case tea.KeyUp:
		if len(m.history) > 0 && m.historyIdx > 0 {
			m.historyIdx--
			m.input.SetValue(m.history[m.historyIdx])
			m.input.CursorEnd()
		}
		return m, nil

	case tea.KeyDown:
		if m.historyIdx < len(m.history)-1 {
			m.historyIdx++
			m.input.SetValue(m.history[m.historyIdx])
			m.input.CursorEnd()
		} else {
			m.historyIdx = len(m.history)
			m.input.SetValue("")
		}
		return m, nil

	case tea.KeyCtrlF:
		m.mode = ModeSearch
		m.searchInput.Focus()
		m.searchInput.SetValue("")
		return m, textinput.Blink

	case tea.KeyPgUp:
		m.viewport.HalfViewUp()
		return m, nil

	case tea.KeyPgDown:
		m.viewport.HalfViewDown()
		return m, nil

	case tea.KeyTab:
		input := m.input.Value()
		if input == "" {
			return m, nil
		}
		if len(m.completions) == 0 {
			m.completions = m.completer.Complete(input)
			m.completeIdx = 0
		} else {
			m.completeIdx = (m.completeIdx + 1) % len(m.completions)
		}
		if len(m.completions) > 0 {
			m.input.SetValue(m.completions[m.completeIdx])
			m.input.CursorEnd()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.completions = nil
	m.completeIdx = 0
	return m, cmd
}

func (m Model) handleCommand(input string) (Model, tea.Cmd, bool) {
	lower := strings.ToLower(input)

	// /skills — list all skills
	if lower == "/skills" {
		all := skills.All()
		if len(all) == 0 {
			m.appendOutput(m.styles.Dim.Render("No skills loaded.\n"))
		} else {
			var sb strings.Builder
			sb.WriteString("Skills:\n")
			for _, s := range all {
				desc := s.Description
				if desc == "" {
					desc = s.Name
				}
				sb.WriteString(fmt.Sprintf("  /%s — %s\n", s.Name, desc))
			}
			m.appendOutput(m.styles.Status.Render(sb.String()))
		}
		m.input.SetValue("")
		return m, nil, true
	}

	// Check if input is a skill invocation (e.g. /commit, /test)
	if strings.HasPrefix(lower, "/") && !strings.Contains(lower[1:], " ") {
		skillName := lower[1:]
		if skill, ok := skills.Get(skillName); ok {
			m.input.SetValue("")
			m.history = append(m.history, "/"+skillName)
			m.historyIdx = len(m.history)
			m.streaming = true
			m.startTime = time.Now()
			m.toolCalls = 0
			m.lastUsage = ""
			m.verifiedModel = ""
			m.pendingText.Reset()
			m.runGeneration++
			m.appendOutput(m.styles.UserInput.Render("▸ /"+skillName) + "\n\n")
			events := make(chan agent.Event, 500)
			m.eventChan = events
			go m.agent.Run(skill.Template, events)
			return m, tea.Batch(waitForEvent(events, m.runGeneration), renderTick()), true
		}
	}

	switch {
	case strings.HasPrefix(lower, "/plan "):
		task := strings.TrimSpace(input[6:])
		if task == "" {
			m.input.SetValue("")
			return m, nil, true
		}
		m.input.SetValue("")
		m.mode = ModePlan
		m.plan.Open(task, m.width, m.height)
		ch := make(chan agent.Event, 100)
		m.planChan = ch
		planPrompt := "You are in PLAN MODE. Analyze the task and create a detailed step-by-step implementation plan. " +
			"Do NOT execute anything — only plan. List files to modify, changes needed, and order of operations.\n\nTask: " + task
		go m.agent.BtwQuery(planPrompt, ch)
		return m, waitForPlanEvent(ch), true

	case strings.HasPrefix(lower, "/btw "):
		question := strings.TrimSpace(input[5:])
		if question == "" {
			m.input.SetValue("")
			return m, nil, true
		}
		m.input.SetValue("")
		m.mode = ModeBtw
		m.btw.Open(question, m.width, m.height)
		ch := make(chan agent.Event, 100)
		m.btwChan = ch
		go m.agent.BtwQuery(question, ch)
		return m, waitForBtwEvent(ch), true

	case lower == "/quit" || lower == "/exit" || lower == "/q":
		return m, tea.Quit, true

	case lower == "/settings" || lower == "/set":
		m.mode = ModeSettings
		m.settings.Open(m.width, m.height)
		m.input.SetValue("")
		return m, nil, true

	case lower == "/clear":
		m.agent.Reset()
		m.output = &strings.Builder{}
		m.dirty = false
		m.viewport.SetContent("")
		m.input.SetValue("")
		return m, nil, true

	case lower == "/compact":
		m.input.SetValue("")
		m.streaming = true
		m.startTime = time.Now()
		m.runGeneration++
		events := make(chan agent.Event, 500)
		m.eventChan = events
		go func() {
			defer close(events)
			if err := m.agent.Compact(events); err != nil {
				events <- agent.Event{Type: agent.EventError, Text: err.Error()}
			} else {
				events <- agent.Event{Type: agent.EventDone}
			}
		}()
		return m, tea.Batch(waitForEvent(events, m.runGeneration), renderTick()), true

	case lower == "/model":
		m.appendOutput(m.styles.Status.Render(fmt.Sprintf("Current model: %s\n", m.modelName)))
		m.input.SetValue("")
		return m, nil, true

	case strings.HasPrefix(lower, "/model "):
		newModel := strings.TrimSpace(input[7:])
		m.agent.SetModel(newModel)
		m.modelName = newModel
		m.settingsValues["model"] = newModel
		m.appendOutput(m.styles.Success.Render(fmt.Sprintf("✓ Switched to %s\n", newModel)))
		m.input.SetValue("")
		return m, nil, true

	case strings.HasPrefix(lower, "/theme "):
		newTheme := strings.TrimSpace(input[7:])
		m.theme = GetTheme(newTheme)
		m.styles = NewStyles(m.theme)
		m.settingsValues["theme"] = m.theme.Name
		m.appendOutput(m.styles.Success.Render(fmt.Sprintf("✓ Theme: %s\n", m.theme.Name)))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/theme":
		m.appendOutput(m.styles.Status.Render("Themes: " + strings.Join(ThemeNames(), ", ") + "\n"))
		m.input.SetValue("")
		return m, nil, true

	case strings.HasPrefix(lower, "/reason ") || strings.HasPrefix(lower, "/thinking "):
		var level string
		if strings.HasPrefix(lower, "/reason ") {
			level = strings.TrimSpace(input[8:])
		} else {
			level = strings.TrimSpace(input[10:])
		}
		m.reasoningLevel = level
		m.agent.SetReasoning(level)
		m.settingsValues["reasoning"] = level
		m.appendOutput(m.styles.Success.Render(fmt.Sprintf("✓ Reasoning: %s\n", level)))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/sessions":
		m.appendOutput(m.styles.Status.Render("Use 'synapse sessions' to list sessions\n"))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/git":
		m.gitInfo = git.Summary(m.agent.WorkDir())
		if m.gitInfo == "" {
			m.appendOutput(m.styles.Dim.Render("Not a git repository\n"))
		} else {
			m.appendOutput(m.styles.Status.Render(m.gitInfo + "\n"))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/help" || lower == "/?":
		help := "Commands:\n" +
			"  /quit, /exit    Exit Synapse\n" +
			"  /clear          Clear conversation\n" +
			"  /compact        Compress history\n" +
			"  /settings       Open settings panel (Ctrl+S)\n" +
			"  /model [name]   Show/switch model\n" +
			"  /theme [name]   Show/switch theme\n" +
			"  /reason <lvl>   Set reasoning (off/low/medium/high/xhigh)\n" +
			"  /plan <task>    Generate a one-shot implementation plan\n" +
			"  /git            Show git status\n" +
			"  /undo           Undo last file change\n" +
			"  /undo all       Undo ALL file changes\n" +
			"  /changes        List tracked file changes\n" +
			"  /todo           Show task list\n" +
			"  /empire         Show infrastructure fleet\n" +
			"  /fleet          Show fleet health status\n" +
			"  /fleet check    Run health checks now\n" +
			"  /mcp            Show MCP server status & tools\n" +
			"  /ssh <server>   Resolve server SSH command\n" +
			"  /habits         Show workflow patterns\n" +
			"  /me             Show identity profile\n" +
			"  /tasks          Show background tasks\n" +
			"  /cost           Show session cost\n" +
			"  /branch         Fork conversation here\n" +
			"  /export         Export session JSON\n" +
			"  /help           Show this help\n" +
			"  Ctrl+S          Settings panel\n" +
			"  Ctrl+F          Search output\n" +
			"  Ctrl+C          Cancel streaming\n" +
			"  Alt+Enter       Newline (multiline input)\n" +
			"  Tab             Auto-complete\n" +
			"  PgUp/PgDn       Scroll\n"
		m.appendOutput(m.styles.Dim.Render(help))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/cost":
		m.appendOutput(m.styles.Status.Render(fmt.Sprintf(
			"Context: %dk/%dk (%.0f%%) | Session total: %dk (~$%.4f)\n",
			m.contextTokens/1000, m.contextMax/1000,
			float64(m.contextTokens)/float64(m.contextMax)*100,
			m.cumulativeTokens/1000, m.estimateCost())))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/usage":
		if m.usageFunc != nil {
			m.appendOutput(m.styles.Status.Render(m.usageFunc()))
		} else {
			m.appendOutput(m.styles.Dim.Render("Usage tracking not available\n"))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/branch":
		m.appendOutput(m.styles.Success.Render("Use 'synapse branch <session-id>' from CLI\n"))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/export":
		m.appendOutput(m.styles.Success.Render(fmt.Sprintf("Use 'synapse export %s' from CLI\n", m.sessionID)))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/tasks":
		m.appendOutput(m.styles.Dim.Render("No background tasks.\n"))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/undo":
		result, err := m.agent.UndoLast()
		if err != nil {
			m.appendOutput(m.styles.Error.Render(fmt.Sprintf("✗ %s\n", err)))
		} else {
			m.appendOutput(m.styles.Success.Render(fmt.Sprintf("✓ %s\n", result)))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/undo all":
		result, err := m.agent.UndoAll()
		if err != nil {
			m.appendOutput(m.styles.Error.Render(fmt.Sprintf("✗ %s\n", err)))
		} else {
			m.appendOutput(m.styles.Success.Render(fmt.Sprintf("✓ %s\n", result)))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/changes":
		result := m.agent.ListChanges()
		if result == "" {
			m.appendOutput(m.styles.Dim.Render("No changes tracked.\n"))
		} else {
			m.appendOutput(m.styles.Status.Render(result + "\n"))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/todo":
		result := m.agent.ListTodos()
		m.appendOutput(m.styles.Status.Render(result + "\n"))
		m.input.SetValue("")
		return m, nil, true

	case lower == "/empire":
		if p := m.agent.Profile(); p != nil {
			m.appendOutput(m.styles.Status.Render(p.ServerList() + "\n"))
		} else {
			m.appendOutput(m.styles.Dim.Render("No identity profile loaded.\n"))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/fleet":
		if fp := m.agent.FleetPulse(); fp != nil {
			m.appendOutput(m.styles.Status.Render(fp.StatusSummary() + "\n"))
		} else {
			m.appendOutput(m.styles.Dim.Render("Fleet Pulse not enabled. Set fleet_enabled: true in config.\n"))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/fleet check":
		if fp := m.agent.FleetPulse(); fp != nil {
			m.appendOutput(m.styles.Dim.Render("Checking all servers...\n"))
			go func() {
				fp.CheckAll()
			}()
			m.appendOutput(m.styles.Success.Render("✓ Fleet check started (background)\n"))
		} else {
			m.appendOutput(m.styles.Dim.Render("Fleet Pulse not enabled.\n"))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/mcp":
		if m.mcpClient != nil {
			status := m.mcpClient.Status()
			if len(status) == 0 {
				m.appendOutput(m.styles.Dim.Render("No MCP servers configured.\n"))
			} else {
				var sb strings.Builder
				sb.WriteString("MCP Servers:\n")
				for name, alive := range status {
					icon := "🟢"
					if !alive {
						icon = "🔴"
					}
					sb.WriteString(fmt.Sprintf("  %s %s\n", icon, name))
				}
				allTools := m.mcpClient.AllTools()
				if len(allTools) > 0 {
					sb.WriteString(fmt.Sprintf("\n%d tools available:\n", len(allTools)))
					for _, t := range allTools {
						sb.WriteString(fmt.Sprintf("  • %s/%s — %s\n", t.ServerName, t.Name, t.Description))
					}
				}
				m.appendOutput(m.styles.Status.Render(sb.String()))
			}
		} else {
			m.appendOutput(m.styles.Dim.Render("No MCP servers connected. Add to mcp_servers in config.\n"))
		}
		m.input.SetValue("")
		return m, nil, true

	case strings.HasPrefix(lower, "/ssh "):
		serverName := strings.TrimSpace(input[5:])
		if p := m.agent.Profile(); p != nil {
			if s := p.InfraMap.ResolveServer(serverName); s != nil {
				m.appendOutput(m.styles.Success.Render(fmt.Sprintf("→ %s\n  %s\n", s.Name, s.SSHCommand())))
				m.input.SetValue(s.SSHCommand())
				m.input.CursorEnd()
			} else {
				m.appendOutput(m.styles.Error.Render(fmt.Sprintf("Unknown server: %s\n", serverName)))
			}
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/habits":
		if h := m.agent.Habits(); h != nil {
			m.appendOutput(m.styles.Status.Render(h.Summary() + "\n"))
		} else {
			m.appendOutput(m.styles.Dim.Render("No habits tracked.\n"))
		}
		m.input.SetValue("")
		return m, nil, true

	case lower == "/me":
		if p := m.agent.Profile(); p != nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("👤 %s\n", p.Name))
			sb.WriteString(fmt.Sprintf("📁 %s (%s)\n", p.WorkDir, p.ProjectName))
			sb.WriteString(fmt.Sprintf("🖥  %d servers\n", len(p.InfraMap.Servers)))
			if len(p.RecentTasks) > 0 {
				sb.WriteString(fmt.Sprintf("✅ %d recent tasks\n", len(p.RecentTasks)))
			}
			if len(p.ActiveIssues) > 0 {
				sb.WriteString(fmt.Sprintf("⚠  %d known issues\n", len(p.ActiveIssues)))
			}
			if len(p.Preferences) > 0 {
				sb.WriteString(fmt.Sprintf("⚙  %d preferences\n", len(p.Preferences)))
			}
			if p.LastSessionSummary != "" {
				sb.WriteString(fmt.Sprintf("\nLast session: %s\n", p.LastSessionSummary))
			}
			m.appendOutput(m.styles.Status.Render(sb.String()))
		}
		m.input.SetValue("")
		return m, nil, true
	case lower == "/provider":
		pname := m.providerName
		if pname == "" {
			pname = "anthropic"
		}
		m.appendOutput(m.styles.Status.Render(fmt.Sprintf("Provider: %s\n", pname)))
		m.input.SetValue("")
		return m, nil, true

	case strings.HasPrefix(lower, "/provider "):
		pname := strings.TrimSpace(input[10:])
		m.providerName = pname
		m.appendOutput(m.styles.Success.Render(fmt.Sprintf("\u2713 Provider: %s\n", pname)))
		m.input.SetValue("")
		return m, nil, true
	}

	return m, nil, false
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = ModeNormal
		m.input.Focus()
		return m, textinput.Blink

	case tea.KeyEnter:
		query := m.searchInput.Value()
		if query == "" {
			m.mode = ModeNormal
			m.input.Focus()
			return m, textinput.Blink
		}
		m.searchQuery = query
		m.searchHits = nil
		m.searchIdx = 0

		content := m.viewport.View()
		for i, line := range strings.Split(content, "\n") {
			if strings.Contains(strings.ToLower(line), strings.ToLower(query)) {
				m.searchHits = append(m.searchHits, i)
			}
		}

		if len(m.searchHits) > 0 {
			pct := float64(m.searchHits[0]) / float64(len(strings.Split(content, "\n")))
			m.viewport.SetYOffset(int(pct * float64(m.viewport.TotalLineCount())))
		}

		m.mode = ModeNormal
		m.input.Focus()
		return m, textinput.Blink
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		if m.confirmYes != nil {
			m.confirmYes()
		}
		m.mode = ModeNormal
		m.confirmPrompt = ""
		m.appendOutput(m.styles.Success.Render("  ✓ Approved\n"))
		return m, waitForEvent(m.eventChan, m.runGeneration)
	case "n", "N", "q":
		if m.confirmNo != nil {
			m.confirmNo()
		}
		m.mode = ModeNormal
		m.confirmPrompt = ""
		m.appendOutput(m.styles.Error.Render("  ✗ Rejected\n"))
		return m, waitForEvent(m.eventChan, m.runGeneration)
	}
	return m, nil
}

func (m Model) handleAgentEvent(evt agent.Event) (tea.Model, tea.Cmd) {
	switch evt.Type {
	case agent.EventText:
		m.pendingText.WriteString(evt.Text)
		m.output.WriteString(evt.Text)
		m.dirty = true

	case agent.EventThinking:
		if m.showThinking {
			if !m.thinking {
				m.thinking = true
				m.currentThink.Reset()
				m.appendOutput(m.styles.Think.Render("💭 "))
			}
		}
		m.currentThink.WriteString(evt.Text)

	case agent.EventConfirm:
		if !m.onBashConfirm {
			// Auto-approve if confirmation is disabled
			confirmCh := evt.ConfirmCh()
			go func() { confirmCh <- true }()
			m.appendOutput(m.styles.Dim.Render("  ⚡ auto-approved: "+evt.Text) + "\n")
			return m, waitForEvent(m.eventChan, m.runGeneration)
		}
		m.mode = ModeConfirm
		m.confirmPrompt = evt.Text
		m.appendOutput(m.styles.Error.Render(evt.Text) + "\n")
		confirmCh := evt.ConfirmCh()
		m.confirmYes = func() {
			confirmCh <- true
		}
		m.confirmNo = func() {
			confirmCh <- false
		}
		return m, nil

	case agent.EventToolCall:
		m.flushThinking()
		m.flushPendingText()
		m.toolCalls++
		args := evt.ToolArgs
		if len(args) > 100 {
			args = args[:100] + "..."
		}
		args = strings.ReplaceAll(args, "\n", " ")
		m.appendOutput(m.styles.FormatToolCall(evt.ToolName, args) + "\n")

	case agent.EventToolResult:
		result := evt.ToolResult
		if m.toolVerbose == "compact" {
			lines := strings.Split(result, "\n")
			if len(lines) > 5 {
				result = strings.Join(lines[:5], "\n") + fmt.Sprintf("\n  ... (%d lines)", len(lines))
			}
			if len(result) > 300 {
				result = result[:300] + "..."
			}
		} else {
			// Full mode — still cap at something reasonable
			if len(result) > 2000 {
				result = result[:2000] + "\n  ... (truncated)"
			}
		}
		m.appendOutput(m.styles.FormatToolResult(result) + "\n")

	case agent.EventUsage:
		elapsed := time.Since(m.startTime).Round(time.Millisecond)
		m.cumulativeTokens += evt.Usage.TotalTokens
		m.tokenCount = evt.Usage.TotalTokens // track last turn's token count
		m.lastUsage = fmt.Sprintf("%d in / %d out / %s",
			evt.Usage.PromptTokens, evt.Usage.CompletionTokens, elapsed)

	case agent.EventModel:
		if evt.Text != "" && m.verifiedModel == "" {
			m.verifiedModel = evt.Text
		}

	case agent.EventError:
		m.flushThinking()
		m.streaming = false
		m.appendOutput(m.styles.Error.Render(fmt.Sprintf("\n✗ %s\n\n", evt.Text)))
		return m, nil

	case agent.EventDone:
		m.flushThinking()
		m.flushPendingText()
		m.streaming = false
		if m.dirty {
			m.viewport.SetContent(m.output.String())
			if m.autoScroll {
				m.viewport.GotoBottom()
			}
			m.dirty = false
		}
		if m.lastUsage != "" {
			usageStr := m.lastUsage
			if m.verifiedModel != "" {
				usageStr += " · " + m.verifiedModel
			}
			m.appendOutput("\n" + m.styles.Usage.Render("["+usageStr+"]") + "\n\n")
		} else {
			m.appendOutput("\n\n")
		}
		// Update context tokens from agent
		m.contextTokens = m.agent.TokenCount()
		return m, nil
	}

	return m, waitForEvent(m.eventChan, m.runGeneration)
}

func (m *Model) flushThinking() {
	if m.thinking {
		m.thinking = false
		thinkLen := m.currentThink.Len()
		if thinkLen > 0 {
			m.appendOutput(m.styles.Think.Render(fmt.Sprintf("(%d chars)\n", thinkLen)))
		}
		m.currentThink.Reset()
	}
}

func (m *Model) flushPendingText() {
	if m.pendingText.Len() > 0 {
		m.pendingText.Reset()
	}
}

// appendOutput adds styled text and immediately refreshes the viewport.
func (m *Model) appendOutput(text string) {
	m.output.WriteString(text)
	m.viewport.SetContent(m.output.String())
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Overlay modes replace entire view
	if m.mode == ModeSettings {
		return m.settings.Render(m.styles, m.settingsValues)
	}

	if m.mode == ModeDiff {
		return m.diff.Render(m.styles)
	}

	if m.mode == ModeBtw {
		return m.btw.Render(m.styles)
	}

	if m.mode == ModePlan {
		return m.plan.Render(m.styles)
	}

	var sb strings.Builder

	header := m.renderHeader()
	sb.WriteString(header + "\n")
	sb.WriteString(m.viewport.View() + "\n")
	sb.WriteString(m.renderStatusBar() + "\n")
	sb.WriteString(m.renderInput())

	return sb.String()
}

func (m Model) renderHeader() string {
	title := m.styles.Header.Render("⚡ Synapse")
	model := m.styles.Dim.Render(" " + m.modelName)

	right := ""
	if m.gitInfo != "" {
		right = m.styles.Dim.Render(m.gitInfo)
	}

	left := title + model
	padding := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if padding < 1 {
		padding = 1
	}

	return left + strings.Repeat(" ", padding) + right
}

func (m Model) renderStatusBar() string {
	left := ""
	if m.streaming {
		if m.thinking {
			left = m.styles.Think.Render(" 💭 thinking...")
		} else {
			left = m.styles.Status.Render(" ⚡ streaming...")
		}
		if m.toolCalls > 0 {
			left += m.styles.Dim.Render(fmt.Sprintf(" [%d tools]", m.toolCalls))
		}
	} else {
		msgs := m.agent.MessageCount()
		left = m.styles.Dim.Render(fmt.Sprintf(" %d msgs", msgs))
	}

	// Model + reasoning in status bar
	modelInfo := m.modelName
	if m.reasoningLevel != "" && m.reasoningLevel != "off" {
		modelInfo += " [" + m.reasoningLevel + "]"
	}
	left += m.styles.Dim.Render(" │ " + modelInfo)

	// Settings status flags
	settingsStatus := FormatSettingsStatus(m.settingsValues)
	if settingsStatus != "" {
		left += m.styles.Dim.Render(" │ " + settingsStatus)
	}

	right := ""
	if m.contextTokens > 0 {
		// API mode: real context window tracking
		pct := float64(m.contextTokens) / float64(m.contextMax) * 100
		right = m.styles.Dim.Render(fmt.Sprintf(" %dk/%dk ctx (%.0f%%) ",
			m.contextTokens/1000, m.contextMax/1000, pct))
		if m.cumulativeTokens > 0 {
			right += m.styles.Dim.Render(fmt.Sprintf("· %dk total ", m.cumulativeTokens/1000))
		}
	} else if m.lastUsage != "" {
		// CLI mode: show last turn stats + session total
		right = m.styles.Dim.Render(fmt.Sprintf(" %s · %dk session ", m.lastUsage, m.cumulativeTokens/1000))
	}

	scrollPct := m.viewport.ScrollPercent()
	right += m.styles.Dim.Render(fmt.Sprintf("%.0f%% ", scrollPct*100))

	barWidth := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if barWidth < 0 {
		barWidth = 0
	}
	sep := m.styles.Border.Render(strings.Repeat("─", barWidth))

	return left + sep + right
}

func (m Model) renderInput() string {
	switch m.mode {
	case ModeSearch:
		return m.styles.SearchPrompt.Render("🔍 ") + m.searchInput.View()
	case ModeConfirm:
		return m.styles.ToolName.Render(m.confirmPrompt + " [y/n] ")
	default:
		prompt := m.styles.InputPrompt.Render("▸ ")
		return prompt + m.input.View()
	}
}

// handleBtwKey dismisses the btw overlay on space, enter, or esc.
func (m Model) handleBtwKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeySpace, tea.KeyEnter, tea.KeyEsc:
		m.agent.Cancel()
		if m.btwChan != nil {
			go func(ch chan agent.Event) {
				for range ch {
				}
			}(m.btwChan)
		}
		m.mode = ModeNormal
		m.btwChan = nil
		return m, nil
	}
	return m, nil
}

// waitForBtwEvent reads from the btw channel and batches events.
func waitForPlanEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return planEventMsg{events: []agent.Event{{Type: agent.EventDone}}}
		}
		batch := []agent.Event{evt}
		for len(batch) < 32 {
			select {
			case e, ok := <-ch:
				if !ok {
					batch = append(batch, agent.Event{Type: agent.EventDone})
					return planEventMsg{events: batch}
				}
				batch = append(batch, e)
			default:
				return planEventMsg{events: batch}
			}
		}
		return planEventMsg{events: batch}
	}
}

func waitForBtwEvent(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return btwEventMsg{events: []agent.Event{{Type: agent.EventDone}}}
		}
		batch := []agent.Event{evt}
		for len(batch) < 32 {
			select {
			case e, ok := <-ch:
				if !ok {
					batch = append(batch, agent.Event{Type: agent.EventDone})
					return btwEventMsg{events: batch}
				}
				batch = append(batch, e)
			default:
				return btwEventMsg{events: batch}
			}
		}
		return btwEventMsg{events: batch}
	}
}

func waitForEvent(ch <-chan agent.Event, gen int) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return agentEventMsg{events: []agent.Event{{Type: agent.EventDone}}, gen: gen}
		}
		batch := []agent.Event{evt}
		for len(batch) < 64 {
			select {
			case e, ok := <-ch:
				if !ok {
					batch = append(batch, agent.Event{Type: agent.EventDone})
					return agentEventMsg{events: batch, gen: gen}
				}
				batch = append(batch, e)
			default:
				return agentEventMsg{events: batch, gen: gen}
			}
		}
		return agentEventMsg{events: batch, gen: gen}
	}
}

func (m Model) estimateCost() float64 {
	inputTokens := float64(m.cumulativeTokens) * 0.6
	outputTokens := float64(m.cumulativeTokens) * 0.4
	return inputTokens/1_000_000*2.50 + outputTokens/1_000_000*10.00
}
