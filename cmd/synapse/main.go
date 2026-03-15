package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/zanfiel/synapse/internal/agent"
	"github.com/zanfiel/synapse/internal/config"
	"github.com/zanfiel/synapse/internal/engram"
	"github.com/zanfiel/synapse/internal/fleet"
	"github.com/zanfiel/synapse/internal/git"
	"github.com/zanfiel/synapse/internal/identity"
	"github.com/zanfiel/synapse/internal/lsp"
	"github.com/zanfiel/synapse/internal/mcp"
	"github.com/zanfiel/synapse/internal/server"
	"github.com/zanfiel/synapse/internal/session"
	"github.com/zanfiel/synapse/internal/skills"
	sshpool "github.com/zanfiel/synapse/internal/ssh"
	"github.com/zanfiel/synapse/internal/token"
	"github.com/zanfiel/synapse/internal/tools"
	"github.com/zanfiel/synapse/internal/tui"
	"github.com/zanfiel/synapse/internal/types"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.9.1"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("synapse v%s\n", version)
			return
		case "help", "--help", "-h":
			printHelp()
			return
		case "serve", "headless":
			doServe()
			return
		case "sessions":
			doListSessions()
			return
		case "resume":
			if len(os.Args) > 2 {
				doResume(os.Args[2])
				return
			}
			fatal("usage: synapse resume <session-id>")
		case "update":
			doUpdate()
			return
		case "export":
			if len(os.Args) > 2 {
				doExport(os.Args[2])
				return
			}
			fatal("usage: synapse export <session-id>")
		case "branch":
			if len(os.Args) > 2 {
				doBranch(os.Args[2])
				return
			}
			fatal("usage: synapse branch <session-id>")
		case "search":
			if len(os.Args) > 2 {
				doSearchSessions(strings.Join(os.Args[2:], " "))
				return
			}
			fatal("usage: synapse search <query>")
		case "debug":
			if len(os.Args) > 2 {
				doDebug(strings.Join(os.Args[2:], " "))
				return
			}
			fatal("usage: synapse debug <message>")
		case "anthropic":
			os.Setenv("SYNAPSE_PROVIDER", "anthropic")
			runInteractive("")
			return
		case "openai":
			os.Setenv("SYNAPSE_PROVIDER", "openai")
			runInteractive("")
			return
		default:
			if handleExtraCommand(os.Args[1]) {
				return
			}
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			printHelp()
			return
		}
	}

	runInteractive("")
}

func runInteractive(resumeID string) {
	start := time.Now()
	fmt.Fprintf(os.Stderr, "⚡ starting...\n")
	cfg, err := config.Load()
	if err != nil {
		fatal("config: %s", err)
	}

	// Work directory
	workDir := cfg.WorkDir
	if workDir == "" || workDir == "." {
		workDir, _ = os.Getwd()
	}

	// Project config (.synapse.json) then env overrides
	projCfg := config.LoadProjectConfig(workDir)
	cfg = config.MergeProjectConfig(cfg, projCfg)
	applyEnvOverrides(cfg)

	fmt.Fprintf(os.Stderr, "⚡ [%s] config: engram=%s model=%s provider=%s\n", time.Since(start).Round(time.Millisecond), cfg.EngramURL, cfg.Model, cfg.Provider)

	// Determine AI provider
	anthropicKey := cfg.AnthropicAPIKey
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	aiProvider, provName := selectProvider(cfg, anthropicKey)
	if aiProvider != nil {
		fmt.Fprintf(os.Stderr, "⚡ Provider: %s\n", provName)
	}
	if aiProvider == nil {
		fatal(providerFatalMsg())
	}

	// Tool registry
	toolReg := tools.NewRegistry(workDir, config.ConfigDir())
	toolReg.Register(tools.SSHTool())
	toolReg.Register(tools.WebSearchTool())
	toolReg.Register(tools.SpawnAgentTool())

	// Git tools if in a repo
	if gitInfo := git.Detect(workDir); gitInfo != nil {
		toolReg.Register(tools.GitStatusTool(workDir))
		toolReg.Register(tools.GitDiffTool(workDir))
		toolReg.Register(tools.GitCommitTool(workDir))
		toolReg.Register(tools.GitLogTool(workDir))
	}

	// LSP integration — auto-detect language, start server if available
	fmt.Fprintf(os.Stderr, "⚡ [%s] checking LSP...\n", time.Since(start).Round(time.Millisecond))
	var lspClient *lsp.Client
	if lang := lsp.DetectLanguage(workDir); lang != "" {
		if lc, err := lsp.NewClient(workDir, lang); err == nil {
			lspClient = lc
			defer lspClient.Close()
			fmt.Fprintf(os.Stderr, "⚡ LSP: %s\n", lang)
			toolReg.Register(tools.DiagnosticsTool(lspClient))
			toolReg.Register(tools.SymbolTool(lspClient))
			toolReg.Register(tools.HoverTool(lspClient))
			toolReg.Register(tools.DefinitionTool(lspClient))
		}
	}

	// Custom tools from project config
	if projCfg != nil && len(projCfg.Tools) > 0 {
		tools.RegisterCustomTools(toolReg, workDir, projCfg.Tools)
	}

	// Engram
	var engramClient *engram.Client
	engramURL := cfg.EngramURL
	if envURL := os.Getenv("ENGRAM_URL"); envURL != "" {
		engramURL = envURL
	}
	engramToken := cfg.EngramToken
	if envToken := os.Getenv("ENGRAM_TOKEN"); envToken != "" {
		engramToken = envToken
	}

	if engramURL != "" {
		fmt.Fprintf(os.Stderr, "⚡ [%s] connecting to Engram at %s...\n", time.Since(start).Round(time.Millisecond), engramURL)
		engramClient = engram.NewClient(engramURL, engramToken, "synapse@"+hostname())
		engramClient.SetModel(cfg.Model)
		if err := engramClient.Health(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ Engram unreachable at %s: %s\n", engramURL, err)
			engramClient = nil
		} else {
			tools.RegisterEngramTools(toolReg, engramClient)
		}
	}

	// Build system prompt with PromptEngine
	var systemPrompt string
	if cfg.SystemPrompt != "" {
		systemPrompt = cfg.SystemPrompt
		if len(systemPrompt) > 0 && systemPrompt[0] == '@' {
			if data, err := os.ReadFile(systemPrompt[1:]); err == nil {
				systemPrompt = string(data)
			}
		}
	} else {
		pe := agent.DetectProject(workDir)

		// Inject git status
		if gitSummary := git.Summary(workDir); gitSummary != "" {
			branch, dirty := git.BranchAndDirty(workDir)
			pe.SetGitStatus(branch, dirty)
		}

		// Inject Engram context
		if engramClient != nil {
			recall := buildEngramContext(engramClient)
			if recall != "" {
				pe.SetEngramContext(recall)
			}
		}

		systemPrompt = pe.Build(cfg.Model, cfg.ReasoningLevel, toolReg.Names())
	}

	// Identity — build profile from Engram + config
	fmt.Fprintf(os.Stderr, "⚡ [%s] building identity...\n", time.Since(start).Round(time.Millisecond))
	profile := identity.BuildProfile(engramClient, workDir)
	habits := identity.NewHabits(config.ConfigDir())

	contextWindow := token.ModelContextWindow(cfg.Model)

	// MCP Client — connect to configured MCP servers
	var mcpClient *mcp.Client
	if len(cfg.MCPServers) > 0 {
		mcpClient = mcp.New()
		for _, srv := range cfg.MCPServers {
			mcpCfg := mcp.ServerConfig{
				Name: srv.Name, Command: srv.Command, Args: srv.Args,
				Env: srv.Env, URL: srv.URL, Transport: mcp.Transport(srv.Transport),
			}
			if err := mcpClient.Connect(mcpCfg); err != nil {
				fmt.Fprintf(os.Stderr, "  MCP %s: %s\n", srv.Name, err)
			} else {
				fmt.Fprintf(os.Stderr, "  MCP %s: connected\n", srv.Name)
			}
		}
		count := tools.RegisterMCPTools(toolReg, mcpClient)
		fmt.Fprintf(os.Stderr, "  [%s] %d MCP tools registered\n", time.Since(start).Round(time.Millisecond), count)
		defer mcpClient.Close()
	}

	// Fleet Pulse — infrastructure health monitoring
	var fleetPulse *fleet.FleetPulse
	if cfg.FleetEnabled && len(cfg.FleetServers) > 0 {
		sshP := sshpool.NewPool(15 * time.Second)
		var servers []fleet.Server
		for _, s := range cfg.FleetServers {
			servers = append(servers, fleet.Server{
				Name: s.Name, Host: s.Host, Port: s.Port,
				User: s.User, KeyPath: s.KeyPath, HSIP: s.HSIP, Tags: s.Tags,
			})
		}
		interval := time.Duration(cfg.FleetInterval) * time.Second
		if interval == 0 {
			interval = 5 * time.Minute
		}
		fleetPulse = fleet.New(servers, engramClient, sshP, interval)
		tools.RegisterFleetTools(toolReg, fleetPulse)
		fleetPulse.Start()
		defer fleetPulse.Stop()
		fmt.Fprintf(os.Stderr, "  [%s] Fleet Pulse: monitoring %d servers\n", time.Since(start).Round(time.Millisecond), len(servers))
	}

	fmt.Fprintf(os.Stderr, "⚡ [%s] creating agent...\n", time.Since(start).Round(time.Millisecond))

	// Agent
	opts := agent.Options{
		Model:            cfg.Model,
		MaxTokens:        cfg.MaxTokens,
		Reasoning:        cfg.ReasoningLevel,
		SystemPrompt:     systemPrompt,
		WorkDir:          workDir,
		AutoCompact:      true,
		CompactPercent:   0.8,
		ContextWindow:    contextWindow,
		ConfirmBash:      true,
		AutoRecall:       engramClient != nil,
		AutoStore:        engramClient != nil,
		PreferenceDetect: engramClient != nil,
		Profile:          profile,
		Habits:           habits,
		AutoCapture:      cfg.AutoCapture && engramClient != nil,
		FleetPulse:       fleetPulse,
		EngramBudget:     2000,
		AutoModel:        cfg.AutoModel,
		ModelTiers: agent.ModelTiers{
			Haiku:  cfg.HaikuModel,
			Sonnet: cfg.SonnetModel,
			Opus:   cfg.OpusModel,
		},
	}

	ag := agent.New(aiProvider, engramClient, toolReg, opts)

	// Load user-defined skills
	skills.LoadUserSkills(config.ConfigDir())

	// Sub-agent runner (avoids circular import: tools -> agent)
	tools.SubAgentRunner = func(task, model string) (string, error) {
		subOpts := agent.Options{
			Model:        model,
			MaxTokens:    4096,
			SystemPrompt: systemPrompt,
			WorkDir:      workDir,
			ConfirmBash:  false,
		}
		subAgent := agent.New(aiProvider, engramClient, toolReg, subOpts)
		evts := make(chan agent.Event, 100)
		go subAgent.Run(task, evts)
		var result strings.Builder
		for evt := range evts {
			if evt.Type == agent.EventText {
				result.WriteString(evt.Text)
			}
		}
		return result.String(), nil
	}

	// Session store
	store, err := session.NewStore(config.ConfigDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠ Session store unavailable: %s\n", err)
	} else {
		defer store.Close()
	}

	// Conversation search tool (needs session store)
	if store != nil {
		toolReg.Register(tools.ConversationSearchToolLive(&sessionSearchAdapter{store}))
	}

	var sessionID string
	if resumeID != "" && store != nil {
		// Resume existing session
		msgs, err := store.LoadMessages(resumeID)
		if err != nil {
			fatal("resume session %s: %s", resumeID, err)
		}
		ag.SetMessages(msgs)
		sessionID = resumeID
		fmt.Fprintf(os.Stderr, "⚡ Resumed session %s (%d messages)\n", resumeID, len(msgs))
	} else if store != nil {
		sessionID, _ = store.Create(cfg.Model, workDir, "")
	}

	// Auto-save messages
	if store != nil && sessionID != "" {
		ag.OnSaveMessage(func(msg types.Message) {
			store.SaveMessage(sessionID, msg)
		})
	}

	// Theme
	themeName := cfg.Theme
	if themeName == "" {
		themeName = "synapse"
	}

	// TUI
	m := tui.NewModelWithConfig(ag, cfg.Model, themeName, sessionID, contextWindow, cfg.ReasoningLevel, cfg)

	// Git info
	if gitSummary := git.Summary(workDir); gitSummary != "" {
		m.SetGitInfo(gitSummary)
	}

	// MCP client for /mcp command
	if mcpClient != nil {
		m.SetMCPClient(&mcpStatusAdapter{mcpClient})
	}

	// Session title callback
	if store != nil && sessionID != "" {
		m.OnSessionTitle(func(title string) {
			store.SetTitle(sessionID, title)
		})
	}

	// Provider-specific setup (e.g. usage tracking)
	setupUsageTracking(aiProvider, m.SetUsageFunc)

	fmt.Fprintf(os.Stderr, "⚡ [%s] launching TUI...\n", time.Since(start).Round(time.Millisecond))
	p := tea.NewProgram(&m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fatal("tui: %s", err)
	}
}

func doServe() {
	cfg, err := config.Load()
	if err != nil {
		fatal("config: %s", err)
	}

	applyEnvOverrides(cfg)

	workDir, _ := os.Getwd()
	toolReg := tools.NewRegistry(workDir, config.ConfigDir())
	toolReg.Register(tools.SSHTool())

	engramURL := cfg.EngramURL
	if envURL := os.Getenv("ENGRAM_URL"); envURL != "" {
		engramURL = envURL
	}
	engramToken := cfg.EngramToken
	if envToken := os.Getenv("ENGRAM_TOKEN"); envToken != "" {
		engramToken = envToken
	}

	var engramClient *engram.Client
	if engramURL != "" {
		engramClient = engram.NewClient(engramURL, engramToken, "synapse-headless@"+hostname())
		engramClient.SetModel(cfg.Model)
		if engramClient.Health() == nil {
			tools.RegisterEngramTools(toolReg, engramClient)
		}
	}

	systemPrompt := defaultSystemPrompt(workDir, cfg.Model, toolReg.Names())

	addr := ":4300"
	if len(os.Args) > 2 {
		addr = os.Args[2]
	}

	opts := agent.Options{
		Model:        cfg.Model,
		MaxTokens:    cfg.MaxTokens,
		Reasoning:    cfg.ReasoningLevel,
		SystemPrompt: systemPrompt,
		WorkDir:      workDir,
		AutoCompact:  true,
		ConfirmBash:  false,
	}

	if engramClient != nil {
		engramClient.SetModel(cfg.Model)
	}

	anthropicKey := cfg.AnthropicAPIKey
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	servProvider, provName := selectProvider(cfg, anthropicKey)
	if servProvider == nil {
		fatal(providerFatalMsg())
	}
	fmt.Fprintf(os.Stderr, "⚡ Provider: %s\n", provName)

	srv := server.New(servProvider, engramClient, toolReg, opts)
	if err := srv.Serve(addr); err != nil {
		fatal("server: %s", err)
	}
}

func doListSessions() {
	store, err := session.NewStore(config.ConfigDir())
	if err != nil {
		fatal("sessions: %s", err)
	}
	defer store.Close()

	sessions, err := store.List(20)
	if err != nil {
		fatal("list: %s", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return
	}

	fmt.Printf("%-10s %-12s %-6s %-50s\n", "ID", "Updated", "Msgs", "Title")
	fmt.Println(strings.Repeat("─", 82))
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		if len(title) > 50 {
			title = title[:47] + "..."
		}
		fmt.Printf("%-10s %-12s %-6d %-50s\n",
			s.ID,
			s.UpdatedAt.Format("Jan 02 15:04"),
			s.Messages,
			title,
		)
	}
}

func doResume(id string) {
	runInteractive(id)
}

func buildEngramContext(client *engram.Client) string {
	// Try the /context endpoint first — it does semantic ranking and budget-aware packing
	ctx, err := client.Context("session startup context", 8000)
	if err == nil && ctx != "" {
		return "\n\n# Memory Context (from Engram)\n" + ctx
	}

	// Fallback: simple list
	recent, err := client.List("", 10)
	if err != nil || len(recent) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n# Recent Memories (from Engram)\n")
	for _, m := range recent {
		cat := m.Category
		if cat == "" {
			cat = "general"
		}
		content := m.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", cat, content))
	}
	return sb.String()
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "unknown"
	}
	return h
}

func applyEnvOverrides(cfg *config.Config) {
	if envProvider := os.Getenv("SYNAPSE_PROVIDER"); envProvider != "" {
		cfg.Provider = envProvider
	}
	if envModel := os.Getenv("SYNAPSE_MODEL"); envModel != "" {
		cfg.Model = envModel
	}
}

func defaultSystemPrompt(workDir string, model string, toolNames []string) string {
	shellNote := "Use bash for file operations like ls, grep, find"
	if runtime.GOOS == "windows" {
		shellNote = "This is WINDOWS. The bash tool runs cmd.exe. Use Windows commands: dir, type, findstr, where, PowerShell. Do NOT use Linux commands (ls, cat, grep, head, tail, wc, sed, awk). For complex ops, use PowerShell."
	}

	// Build dynamic tool list from actual registered tools
	var toolList strings.Builder
	for _, name := range toolNames {
		desc := toolDescriptions[name]
		if desc == "" {
			desc = name
		}
		toolList.WriteString(fmt.Sprintf("- %s: %s\n", name, desc))
	}

	return fmt.Sprintf(`You are an expert coding assistant operating inside Synapse, an AI coding agent. You are running as model %s on %s/%s. You help users by reading files, executing commands, editing code, and writing new files.

Available tools:
%s
Working directory: %s

Guidelines:
- %s
- Use read to examine files before editing. You MUST use read, not cat/type, to view files.
- Use edit for precise changes (oldText must match exactly including whitespace)
- Use write only for new files or complete rewrites
- Use memory_store to record important work, decisions, and state changes
- Be concise and direct
- Show file paths clearly when working with files`, model, runtime.GOOS, runtime.GOARCH, toolList.String(), workDir, shellNote)
}

// toolDescriptions maps tool names to short descriptions for the system prompt.
var toolDescriptions = map[string]string{
	"read":                "Read file contents (supports images for vision). Use offset/limit for large files",
	"write":               "Create or overwrite files. Creates parent directories automatically",
	"edit":                "Surgical find-and-replace edit. oldText must match exactly",
	"bash":                "Execute shell commands. Output truncated to 2000 lines or 50KB",
	"patch":               "Apply multi-edit patch to a file (multiple find/replace pairs)",
	"tree":                "List directory tree structure",
	"glob":                "Find files matching a glob pattern",
	"grep":                "Search file contents with regex",
	"ssh":                 "Execute commands on remote servers via SSH",
	"think":               "Extended thinking/reasoning scratchpad (not sent to user)",
	"todo":                "Task list management (add/complete/list tasks)",
	"fetch":               "HTTP fetch (GET/POST) for web content and APIs",
	"undo":                "Undo file changes made this session",
	"git_status":          "Show git working tree status",
	"git_diff":            "Show uncommitted changes",
	"git_commit":          "Stage all changes and commit",
	"git_log":             "Show recent commit history",
	"memory_store":        "Store persistent memories in Engram",
	"memory_search":       "Search memories by keyword",
	"memory_list":         "List recent memories, optionally by category",
	"memory_update":       "Update a memory by ID",
	"memory_delete":       "Delete a memory by ID",
	"memory_archive":      "Archive a memory (soft-remove from active recall)",
	"conversation_search": "Search past conversation messages",
	"diagnostics":         "Get compiler/linter errors from LSP",
	"symbol":              "Search for symbols across the workspace via LSP",
	"hover":               "Get type info for a symbol at a position via LSP",
	"definition":          "Go to definition of a symbol via LSP",
}

// sessionSearchAdapter bridges session.Store to tools.SessionSearcher interface.
type sessionSearchAdapter struct {
	store *session.Store
}

func (a *sessionSearchAdapter) SearchMessages(query string, limit int) ([]tools.SessionMessageResult, error) {
	results, err := a.store.SearchMessages(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]tools.SessionMessageResult, len(results))
	for i, r := range results {
		out[i] = tools.SessionMessageResult{
			SessionID: r.SessionID,
			Role:      r.Role,
			Content:   r.Content,
			CreatedAt: r.CreatedAt,
		}
	}
	return out, nil
}

// mcpStatusAdapter bridges mcp.Client to tui.MCPStatus interface.
type mcpStatusAdapter struct {
	client *mcp.Client
}

func (a *mcpStatusAdapter) Status() map[string]bool {
	return a.client.Status()
}

func (a *mcpStatusAdapter) AllTools() []tui.MCPToolInfo {
	raw := a.client.AllTools()
	out := make([]tui.MCPToolInfo, len(raw))
	for i, t := range raw {
		out[i] = tui.MCPToolInfo{
			Name:        t.Name,
			Description: t.Description,
			ServerName:  t.ServerName,
		}
	}
	return out
}

func doDebug(message string) {
	fmt.Println("⚡ SYNAPSE DEBUG MODE")
	fmt.Println("====================")
	start := time.Now()

	cfg, err := config.Load()
	if err != nil {
		fatal("config: %s", err)
	}
	fmt.Printf("[%s] config loaded: model=%s engram=%s\n", time.Since(start).Round(time.Millisecond), cfg.Model, cfg.EngramURL)

	workDir, _ := os.Getwd()
	toolReg := tools.NewRegistry(workDir, config.ConfigDir())
	toolReg.Register(tools.SSHTool())
	fmt.Printf("[%s] tools registered: %v\n", time.Since(start).Round(time.Millisecond), toolReg.Names())

	// Engram
	var engramClient *engram.Client
	engramURL := cfg.EngramURL
	if envURL := os.Getenv("ENGRAM_URL"); envURL != "" {
		engramURL = envURL
	}
	engramToken := cfg.EngramToken
	if envToken := os.Getenv("ENGRAM_TOKEN"); envToken != "" {
		engramToken = envToken
	}
	if engramURL != "" {
		engramClient = engram.NewClient(engramURL, engramToken, "synapse-debug@"+hostname())
		engramClient.SetModel(cfg.Model)
		if err := engramClient.Health(); err != nil {
			fmt.Printf("[%s] ⚠ Engram unreachable: %s\n", time.Since(start).Round(time.Millisecond), err)
			engramClient = nil
		} else {
			fmt.Printf("[%s] Engram OK at %s\n", time.Since(start).Round(time.Millisecond), engramURL)
			tools.RegisterEngramTools(toolReg, engramClient)
		}
	}

	applyEnvOverrides(cfg)
	if engramClient != nil {
		engramClient.SetModel(cfg.Model)
	}

	systemPrompt := defaultSystemPrompt(workDir, cfg.Model, toolReg.Names())
	fmt.Printf("[%s] system prompt: %d chars, %d tools\n", time.Since(start).Round(time.Millisecond), len(systemPrompt), len(toolReg.Names()))

	anthropicKey := cfg.AnthropicAPIKey
	if anthropicKey == "" {
		anthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	debugProvider, provName := selectProvider(cfg, anthropicKey)
	if debugProvider == nil {
		fatal(providerFatalMsg())
	}
	fmt.Printf("[%s] provider: %s\n", time.Since(start).Round(time.Millisecond), provName)

	opts := agent.Options{
		Model:        cfg.Model,
		MaxTokens:    cfg.MaxTokens,
		Reasoning:    cfg.ReasoningLevel,
		SystemPrompt: systemPrompt,
		WorkDir:      workDir,
		ConfirmBash:  false,
	}
	ag := agent.New(debugProvider, engramClient, toolReg, opts)

	fmt.Printf("[%s] agent created, sending message: %q\n", time.Since(start).Round(time.Millisecond), message)
	fmt.Println("----")

	events := make(chan agent.Event, 100)
	go ag.Run(message, events)

	toolCallCount := 0
	for evt := range events {
		elapsed := time.Since(start).Round(time.Millisecond)
		switch evt.Type {
		case agent.EventText:
			text := evt.Text
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			fmt.Printf("[%s] TEXT: %s\n", elapsed, strings.ReplaceAll(text, "\n", "\\n"))
		case agent.EventThinking:
			fmt.Printf("[%s] THINK: +%d chars\n", elapsed, len(evt.Text))
		case agent.EventToolCall:
			toolCallCount++
			args := evt.ToolArgs
			if len(args) > 200 {
				args = args[:200] + "..."
			}
			fmt.Printf("[%s] TOOL_CALL #%d: %s (id=%s) args=%s\n", elapsed, toolCallCount, evt.ToolName, evt.ToolCallID, args)
		case agent.EventToolResult:
			result := evt.ToolResult
			if len(result) > 200 {
				result = result[:200] + "..."
			}
			fmt.Printf("[%s] TOOL_RESULT: %s → %s\n", elapsed, evt.ToolName, strings.ReplaceAll(result, "\n", "\\n"))
		case agent.EventUsage:
			if evt.Usage != nil {
				fmt.Printf("[%s] USAGE: %d prompt + %d completion = %d total\n", elapsed, evt.Usage.PromptTokens, evt.Usage.CompletionTokens, evt.Usage.TotalTokens)
			}
		case agent.EventModel:
			fmt.Printf("[%s] MODEL: %s\n", elapsed, evt.Text)
		case agent.EventError:
			fmt.Printf("[%s] ✗ ERROR: %s\n", elapsed, evt.Text)
		case agent.EventDone:
			fmt.Printf("[%s] ✓ DONE (tool calls: %d)\n", elapsed, toolCallCount)
		case agent.EventConfirm:
			fmt.Printf("[%s] CONFIRM: %s (auto-approving in debug)\n", elapsed, evt.Text)
			go func() { evt.ConfirmCh() <- true }()
		}
	}

	fmt.Println("----")
	fmt.Printf("Total: %s\n", time.Since(start).Round(time.Millisecond))
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "✗ "+format+"\n", args...)
	os.Exit(1)
}

func printHelp() {
	fmt.Printf(`⚡ synapse v%s — AI coding agent

Usage:
  synapse                    Start interactive session%s
  synapse serve [addr]       Start headless HTTP server (default :4300)
  synapse sessions           List saved sessions
  synapse resume <id>        Resume a saved session
  synapse branch <id>        Branch a session (fork conversation)
  synapse export <id>        Export session as JSON
  synapse search <query>     Search past sessions
  synapse update             Check for and install updates
  synapse version            Show version
  synapse help               Show this help

Session commands:
  /quit, /exit       Exit Synapse
  /clear             Clear conversation
  /compact           Compress history
  /model [name]      Show/switch model
  /theme [name]      Show/switch theme (synapse, tokyo, dracula, gruvbox, catppuccin, nord)
  /git               Show git status
  /tasks             Show background tasks
  /cost              Show session cost estimate
  /branch            Fork conversation at this point
  /export            Export current session
  /help              Show commands
  Ctrl+F             Search output
  PgUp/PgDn          Scroll output
  Tab                Auto-complete commands

Project config: .synapse.json (per-directory)
Global config:  %s
Sessions DB:    %s/sessions.db

Environment:
  ANTHROPIC_API_KEY  Anthropic API key
  OPENAI_API_KEY     OpenAI API key
  ENGRAM_URL         Engram memory server URL
  ENGRAM_TOKEN       Engram API token
`, version, getExtraHelpText(), config.ConfigPath(), config.ConfigDir())
}

func doUpdate() {
	fmt.Println("⚡ Checking for updates...")
	release, err := config.CheckUpdate(version)
	if err != nil {
		fatal("update check: %s", err)
	}
	if release == nil {
		fmt.Printf("✓ Already on latest version (v%s)\n", version)
		return
	}

	fmt.Printf("New version available: %s\n", release.TagName)
	if release.Body != "" {
		lines := strings.Split(release.Body, "\n")
		for i, line := range lines {
			if i >= 10 {
				break
			}
			fmt.Println("  " + line)
		}
	}

	if err := config.SelfUpdate(release, version); err != nil {
		fatal("update: %s", err)
	}
}

func doExport(id string) {
	store, err := session.NewStore(config.ConfigDir())
	if err != nil {
		fatal("sessions: %s", err)
	}
	defer store.Close()

	export, err := store.Export(id)
	if err != nil {
		fatal("export: %s", err)
	}

	data, _ := json.MarshalIndent(export, "", "  ")
	fmt.Println(string(data))
}

func doBranch(id string) {
	store, err := session.NewStore(config.ConfigDir())
	if err != nil {
		fatal("sessions: %s", err)
	}
	defer store.Close()

	newID, err := store.Branch(id, 0)
	if err != nil {
		fatal("branch: %s", err)
	}

	fmt.Printf("✓ Branched session %s → %s\n", id, newID)
	fmt.Printf("  Resume with: synapse resume %s\n", newID)
}

func doSearchSessions(query string) {
	store, err := session.NewStore(config.ConfigDir())
	if err != nil {
		fatal("sessions: %s", err)
	}
	defer store.Close()

	sessions, err := store.Search(query, 20)
	if err != nil {
		fatal("search: %s", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No matching sessions found.")
		return
	}

	fmt.Printf("%-10s %-12s %-6s %-50s\n", "ID", "Updated", "Msgs", "Title")
	fmt.Println(strings.Repeat("─", 82))
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		if len(title) > 50 {
			title = title[:47] + "..."
		}
		fmt.Printf("%-10s %-12s %-6d %-50s\n",
			s.ID,
			s.UpdatedAt.Format("Jan 02 15:04"),
			s.Messages,
			title,
		)
	}
}
