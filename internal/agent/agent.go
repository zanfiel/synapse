package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/zanfiel/synapse/internal/engram"
	"github.com/zanfiel/synapse/internal/fleet"
	"github.com/zanfiel/synapse/internal/identity"
	"github.com/zanfiel/synapse/internal/provider"
	"github.com/zanfiel/synapse/internal/token"
	"github.com/zanfiel/synapse/internal/tools"
	"github.com/zanfiel/synapse/internal/types"
)

// Event types emitted by the agent loop.
type EventType int

const (
	EventText EventType = iota
	EventThinking
	EventToolCall
	EventToolResult
	EventUsage
	EventError
	EventDone
	EventConfirm      // request bash confirmation
	EventModel        // actual model name from API
	EventPendingWrite // pauses agent, waits for user accept/reject
)

type Event struct {
	Type       EventType
	Text       string
	ToolName   string
	ToolArgs   string
	ToolResult string
	ToolCallID string
	Usage      *types.Usage
	FilePath   string
	OldContent string
	NewContent string
	confirmCh  chan bool // for bash confirmation (unexported, internal use)
}

// ConfirmCh returns the confirmation channel for EventConfirm events.
func (e Event) ConfirmCh() chan bool { return e.confirmCh }

type Agent struct {
	client       provider.Provider
	engram       *engram.Client
	tools        *tools.Registry
	model        string
	maxTok       int
	reasoning    string
	systemPrompt string
	workDir      string

	messages  []types.Message
	hasImages bool
	mu        sync.Mutex

	// Auto-compaction
	autoCompact   bool
	compactAt     float64 // compact when token usage reaches this % of context
	contextWindow int

	// Engram auto behaviors
	autoRecall       bool
	autoStore        bool
	preferenceDetect bool

	// Bash confirmation
	confirmBash       bool
	dangerousPatterns []*regexp.Regexp

	// Callbacks
	onSaveMessage func(types.Message)

	// Identity
	profile    *identity.Profile
	habits     *identity.Habits
	projectMap *ProjectMap

	// Session capture
	capture        *engram.SessionCapture
	fleetPulse     *fleet.FleetPulse
	thinkingBudget int

	engramBudget int // max tokens for dynamic Engram context per request (0 = disabled)

	// Cancel support
	cancelCh      chan struct{}
	cancelMu      sync.Mutex
	requestCtx    context.Context
	requestCancel context.CancelFunc

	// Private content filter
	privatePatterns []*regexp.Regexp

	// Auto model selection — routes to haiku/sonnet/opus by complexity
	autoModel  bool
	modelTiers ModelTiers
}

// ModelTiers holds the model names for each complexity tier.
type ModelTiers struct {
	Haiku  string
	Sonnet string
	Opus   string
}

type Options struct {
	Model            string
	MaxTokens        int
	Reasoning        string
	SystemPrompt     string
	WorkDir          string
	AutoCompact      bool
	CompactPercent   float64
	ContextWindow    int
	ConfirmBash      bool
	AutoRecall       bool
	AutoStore        bool
	PreferenceDetect bool
	Profile          *identity.Profile
	Habits           *identity.Habits
	AutoCapture      bool
	FleetPulse       *fleet.FleetPulse
	EngramBudget     int // tokens for dynamic Engram context per turn (default 2000)
	AutoModel        bool
	ModelTiers       ModelTiers
}

func New(client provider.Provider, eg *engram.Client, toolReg *tools.Registry, opts Options) *Agent {
	a := &Agent{
		client:           client,
		engram:           eg,
		tools:            toolReg,
		model:            opts.Model,
		maxTok:           opts.MaxTokens,
		reasoning:        opts.Reasoning,
		systemPrompt:     opts.SystemPrompt,
		workDir:          opts.WorkDir,
		messages:         []types.Message{},
		autoCompact:      opts.AutoCompact,
		compactAt:        opts.CompactPercent,
		contextWindow:    opts.ContextWindow,
		confirmBash:      opts.ConfirmBash,
		autoRecall:       opts.AutoRecall,
		autoStore:        opts.AutoStore,
		preferenceDetect: opts.PreferenceDetect,
		profile:          opts.Profile,
		habits:           opts.Habits,
		fleetPulse:       opts.FleetPulse,
		engramBudget:     opts.EngramBudget,
		autoModel:        opts.AutoModel,
		modelTiers:       opts.ModelTiers,
	}

	// Session capture for auto-extraction
	if opts.AutoCapture && eg != nil {
		sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
		project := filepath.Base(opts.WorkDir)
		a.capture = engram.NewSessionCapture(eg, sessionID, project, opts.WorkDir)
	}

	// Inject identity context into system prompt
	if a.profile != nil {
		a.systemPrompt += a.profile.SystemPromptContext()
	}

	// Build project map and inject into system prompt (with timeout)
	if a.workDir != "" {
		done := make(chan *ProjectMap, 1)
		go func() {
			done <- BuildProjectMap(a.workDir)
		}()
		select {
		case pm := <-done:
			if pm != nil && len(pm.Files) > 0 {
				a.projectMap = pm
				a.systemPrompt += pm.Summary()
			}
		case <-time.After(3 * time.Second):
			// Too slow (huge directory), skip
		}
	}

	if a.compactAt == 0 {
		a.compactAt = 0.8
	}
	if a.contextWindow == 0 {
		a.contextWindow = token.ModelContextWindow(opts.Model)
	}

	// Dangerous bash patterns that need confirmation
	a.dangerousPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\brm\s+-rf?\b`),
		regexp.MustCompile(`(?i)\brm\s+/`),
		regexp.MustCompile(`(?i)\bmkfs\b`),
		regexp.MustCompile(`(?i)\bdd\s+if=`),
		regexp.MustCompile(`(?i)\b(systemctl|service)\s+(stop|restart|disable)\b`),
		regexp.MustCompile(`(?i)\breboot\b`),
		regexp.MustCompile(`(?i)\bshutdown\b`),
		regexp.MustCompile(`(?i)\bdropdb\b`),
		regexp.MustCompile(`(?i)\bDROP\s+(TABLE|DATABASE)\b`),
		regexp.MustCompile(`(?i)\bgit\s+push\s+.*--force`),
		regexp.MustCompile(`(?i)\bchmod\s+-R\s+777\b`),
		regexp.MustCompile(`(?i)\bcurl.*\|\s*(ba)?sh\b`),
	}

	// Private content patterns (stripped before sending to LLM)
	a.privatePatterns = []*regexp.Regexp{
		regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`),               // IPs
		regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[=:]\s*\S+`), // credentials
		regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._-]+`),                         // bearer tokens
		regexp.MustCompile(`ghp_[A-Za-z0-9]+|ghu_[A-Za-z0-9]+|gho_[A-Za-z0-9]+`),   // GitHub tokens
	}

	return a
}

func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
	a.contextWindow = token.ModelContextWindow(model)
}

func (a *Agent) Model() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.model
}

func (a *Agent) beginRequest() (chan struct{}, context.Context, func()) {
	cancelCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelMu.Lock()
	a.cancelCh = cancelCh
	a.requestCtx = ctx
	a.requestCancel = cancel
	a.cancelMu.Unlock()
	cleanup := func() {
		a.cancelMu.Lock()
		if a.cancelCh == cancelCh {
			a.cancelCh = nil
			a.requestCtx = nil
			a.requestCancel = nil
		}
		a.cancelMu.Unlock()
		cancel()
	}
	return cancelCh, ctx, cleanup
}

func (a *Agent) ensureRequestContext() (context.Context, func()) {
	a.cancelMu.Lock()
	ctx := a.requestCtx
	a.cancelMu.Unlock()
	if ctx != nil {
		return ctx, nil
	}
	_, ctx, cleanup := a.beginRequest()
	return ctx, cleanup
}

// Cancel stops the current agent run.
func (a *Agent) Cancel() {
	a.cancelMu.Lock()
	defer a.cancelMu.Unlock()
	if a.cancelCh != nil {
		select {
		case <-a.cancelCh:
			// already cancelled
		default:
			close(a.cancelCh)
		}
	}
	if a.requestCancel != nil {
		a.requestCancel()
	}
}

func (a *Agent) isCancelled() bool {
	a.cancelMu.Lock()
	ch := a.cancelCh
	a.cancelMu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (a *Agent) WorkDir() string { return a.workDir }

func (a *Agent) MessageCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.messages)
}

func (a *Agent) TokenCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Count system prompt + estimated Engram context (mirrors buildRequest)
	total := 0
	if a.systemPrompt != "" {
		total += token.Count(a.systemPrompt) + 4 // +4 for message overhead
	}
	// Engram context is injected dynamically each turn — estimate its contribution
	if a.engram != nil && a.engramBudget > 0 {
		total += a.engramBudget // budget is already in tokens
	}
	total += token.CountMessages(a.messages)
	return total
}

func (a *Agent) ContextWindow() int {
	return a.contextWindow
}

func (a *Agent) OnSaveMessage(fn func(types.Message)) {
	a.onSaveMessage = fn
}

func (a *Agent) SetMessages(msgs []types.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = msgs
}

// Run processes a user message through the full agent loop.
func (a *Agent) Run(userMessage string, events chan<- Event) {
	// Set up cancel channel for this run (local capture prevents race with next run)
	cancelCh, reqCtx, cleanup := a.beginRequest()

	isCancelled := func() bool {
		select {
		case <-cancelCh:
			return true
		default:
			return false
		}
	}

	defer func() {
		cleanup()
		if r := recover(); r != nil {
			events <- Event{Type: EventError, Text: fmt.Sprintf("agent panic: %v", r)}
		}
		close(events)
	}()

	// Detect preferences in user message
	if a.preferenceDetect && a.engram != nil {
		a.detectPreferences(userMessage)
	}

	a.mu.Lock()
	msg := types.Message{Role: "user", Content: userMessage}

	// Auto-include referenced files
	if autoCtx := AutoContext(userMessage, a.workDir, 30*1024); autoCtx != "" {
		msg.Content = userMessage + autoCtx
	}

	a.messages = append(a.messages, msg)
	a.mu.Unlock()
	a.saveMsg(msg)

	compacted := false
	if a.autoCompact {
		compacted = a.maybeCompact(events)
	}

	retried := false // track if we've already retried after 400

	// Auto-model: classify complexity and pick tier
	var modelOverride string
	if a.autoModel {
		tier := classifyComplexity(userMessage)
		modelOverride = a.modelForTier(tier)
		if modelOverride != a.model {
			events <- Event{Type: EventText, Text: fmt.Sprintf("\n🧠 %s → %s\n", tier, modelOverride)}
		}
	}

	// Agent loop
	for {
		// Check for cancellation
		if isCancelled() {
			return
		}

		thinkBudget := detectThinkingBudget(userMessage)
		req := a.buildRequest(thinkBudget, modelOverride)
		req.Context = reqCtx

		streamEvents := make(chan types.StreamEvent, 100)
		go func() {
			defer close(streamEvents)
			if err := a.client.ChatStream(req, streamEvents); err != nil {
				streamEvents <- types.StreamEvent{Error: err}
			}
		}()

		assistantMsg, stopReason, err := a.processStream(streamEvents, events, isCancelled)
		if err != nil {
			// Auto-compact on 400/context overflow and retry once
			errStr := err.Error()
			if !retried && (strings.Contains(errStr, "400") || strings.Contains(errStr, "context") ||
				strings.Contains(errStr, "too long") || strings.Contains(errStr, "maximum context")) {
				retried = true
				events <- Event{Type: EventText, Text: "\n⚡ Context too large — auto-compacting...\n"}
				if compErr := a.Compact(events); compErr == nil {
					compacted = true
					continue // retry with compacted context
				}
			}
			events <- Event{Type: EventError, Text: err.Error()}
			return
		}

		if stopReason == "cancelled" || isCancelled() {
			return
		}

		a.mu.Lock()
		a.messages = append(a.messages, *assistantMsg)
		a.mu.Unlock()
		a.saveMsg(*assistantMsg)

		if stopReason != "tool_calls" {
			// Auto-continue once after compaction so the LLM finishes its work
			if compacted {
				compacted = false
				events <- Event{Type: EventText, Text: "\n⚡ Auto-continuing after compaction...\n"}
				continue
			}
			// Auto-store significant responses in Engram
			if a.autoStore && a.engram != nil {
				a.maybeAutoStore(assistantMsg)
			}
			// Session capture — extract from final assistant text
			if a.capture != nil && assistantMsg.Content != "" {
				if contentStr, ok := assistantMsg.Content.(string); ok {
					if facts := a.capture.ExtractFromText(contentStr); len(facts) > 0 {
						a.capture.FlushToEngram(facts)
					}
				}
			}
			events <- Event{Type: EventDone}
			return
		}

		// Check for cancellation before tool execution
		if isCancelled() {
			return
		}

		// Execute tool calls (parallel when possible)
		toolResults := a.executeToolsParallel(assistantMsg.ToolCalls, events)

		a.mu.Lock()
		for _, result := range toolResults {
			a.messages = append(a.messages, result)
			a.saveMsg(result)
		}
		a.mu.Unlock()

		// Check compaction INSIDE the tool loop — prevents runaway context
		if a.autoCompact {
			if a.maybeCompact(events) {
				compacted = true
			}
		}
	}
}

func (a *Agent) buildRequest(thinkBudget int, modelOverride string) *provider.Request {
	a.mu.Lock()
	defer a.mu.Unlock()

	model := a.model
	if modelOverride != "" {
		model = modelOverride
	}

	// Build system prompt with dynamic Engram context injected each turn
	systemPrompt := a.systemPrompt
	if a.engram != nil && a.engramBudget > 0 {
		lastMsg := a.lastUserMessageLocked()
		projectName := filepath.Base(a.workDir)
		query := projectName + " " + lastMsg
		if ctx, err := a.engram.Context(query, a.engramBudget); err == nil && ctx != "" {
			// Strip any old engram-context block, then append fresh one
			if i := strings.Index(systemPrompt, "\n\n<engram-context>"); i >= 0 {
				systemPrompt = systemPrompt[:i]
			}
			systemPrompt += "\n\n<engram-context>\n" + ctx + "\n</engram-context>"
		}
	}

	msgs := make([]types.Message, 0, len(a.messages)+1)
	if systemPrompt != "" {
		msgs = append(msgs, types.Message{Role: "system", Content: systemPrompt})
	}
	msgs = append(msgs, a.messages...)

	// Truncate to fit context window (leave 20% headroom for response + tools)
	maxTokens := int(float64(a.contextWindow) * 0.80)
	msgs = truncateToFit(msgs, maxTokens)

	lastMsg := a.lastUserMessageLocked()
	selectedTools := selectTools(lastMsg, a.tools.APITools())

	req := &provider.Request{
		Model:     model,
		Messages:  msgs,
		Tools:     selectedTools,
		MaxTokens: a.maxTok,
		HasImages: a.hasImages,
	}

	if a.reasoning != "" && a.reasoning != "off" {
		req.ReasoningEffort = a.reasoning
	}

	if thinkBudget > 0 {
		req.Thinking = &provider.Thinking{BudgetTokens: thinkBudget}
	}

	return req
}

// truncateToFit drops oldest messages (preserving system prompt) until
// the total token count fits within maxTokens.
func truncateToFit(msgs []types.Message, maxTokens int) []types.Message {
	total := token.CountMessages(msgs)
	if total <= maxTokens || len(msgs) < 3 {
		return msgs
	}

	// msgs[0] is system prompt, drop from index 1 forward
	system := msgs[0]
	rest := msgs[1:]

	for len(rest) > 2 {
		candidate := make([]types.Message, 0, len(rest)+1)
		candidate = append(candidate, system)
		candidate = append(candidate, rest...)
		if token.CountMessages(candidate) <= maxTokens {
			break
		}
		rest = rest[1:]
	}

	result := make([]types.Message, 0, len(rest)+2)
	result = append(result, system)
	result = append(result, types.Message{
		Role:    "user",
		Content: "[Earlier messages were truncated to fit context window]",
	})
	result = append(result, rest...)
	return result
}

func (a *Agent) processStream(streamEvents <-chan types.StreamEvent, out chan<- Event, isCancelled func() bool) (*types.Message, string, error) {
	var textBuf strings.Builder
	var thinkBuf strings.Builder
	stopReason := "stop"

	type pendingTool struct {
		id   string
		name string
		args strings.Builder
	}
	var pendingTools []*pendingTool

	for evt := range streamEvents {
		// Check cancel every event — allows fast abort during streaming
		if isCancelled() {
			go func() {
				for range streamEvents {
				}
			}() // drain remaining
			return nil, "cancelled", nil
		}

		if evt.Error != nil {
			return nil, "", evt.Error
		}

		if evt.Model != "" {
			out <- Event{Type: EventModel, Text: evt.Model}
		}

		if evt.TextDelta != "" {
			textBuf.WriteString(evt.TextDelta)
			out <- Event{Type: EventText, Text: evt.TextDelta}
		}

		if evt.ThinkingDelta != "" {
			thinkBuf.WriteString(evt.ThinkingDelta)
			out <- Event{Type: EventThinking, Text: evt.ThinkingDelta}
		}

		if evt.ToolCallDelta != nil {
			tc := evt.ToolCallDelta
			for len(pendingTools) <= tc.Index {
				pendingTools = append(pendingTools, &pendingTool{})
			}
			pt := pendingTools[tc.Index]
			if tc.ID != "" {
				pt.id = tc.ID
			}
			if tc.Name != "" {
				pt.name = tc.Name
			}
			pt.args.WriteString(tc.ArgsDelta)
			// Debug disabled — was polluting TUI stderr
		}

		if evt.Usage != nil {
			out <- Event{Type: EventUsage, Usage: evt.Usage}
		}

		if evt.FinishReason != "" {
			stopReason = evt.FinishReason
		}
	}

	msg := &types.Message{Role: "assistant"}

	if textBuf.Len() > 0 {
		msg.Content = textBuf.String()
	}

	if len(pendingTools) > 0 {
		for _, pt := range pendingTools {
			// Skip phantom entries created by non-contiguous tool call indices
			// (some APIs send tool calls starting at index > 0)
			if pt.name == "" && pt.id == "" {
				continue
			}

			msg.ToolCalls = append(msg.ToolCalls, types.ToolCall{
				ID:   pt.id,
				Type: "function",
				Function: types.FunctionCall{
					Name:      pt.name,
					Arguments: pt.args.String(),
				},
			})

			prettyArgs := pt.args.String()
			var parsed interface{}
			if json.Unmarshal([]byte(prettyArgs), &parsed) == nil {
				if pretty, err := json.MarshalIndent(parsed, "", "  "); err == nil {
					prettyArgs = string(pretty)
				}
			}
			out <- Event{
				Type: EventToolCall, ToolName: pt.name,
				ToolArgs: prettyArgs, ToolCallID: pt.id,
			}
		}
	}

	return msg, stopReason, nil
}

// executeToolsParallel runs independent tools concurrently.
func (a *Agent) executeToolsParallel(toolCalls []types.ToolCall, out chan<- Event) []types.Message {
	if len(toolCalls) <= 1 || !canParallelizeTools(toolCalls) {
		return a.executeTools(toolCalls, out)
	}

	type result struct {
		idx  int
		msgs []types.Message
	}

	results := make(chan result, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		wg.Add(1)
		go func(idx int, tc types.ToolCall) {
			defer wg.Done()
			msgs := a.executeTools([]types.ToolCall{tc}, out)
			results <- result{idx: idx, msgs: msgs}
		}(i, tc)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results in order
	ordered := make([][]types.Message, len(toolCalls))
	for r := range results {
		ordered[r.idx] = r.msgs
	}

	var all []types.Message
	for _, msgs := range ordered {
		all = append(all, msgs...)
	}
	return all
}

func canParallelizeTools(toolCalls []types.ToolCall) bool {
	unsafeTools := map[string]bool{
		"write":          true,
		"edit":           true,
		"patch":          true,
		"undo":           true,
		"bash":           true,
		"ssh":            true,
		"git_commit":     true,
		"memory_store":   true,
		"memory_update":  true,
		"memory_delete":  true,
		"memory_archive": true,
		"todolist":       true,
		"todo":           true,
		"spawn_agent":    true,
		"fleet_check":    true,
	}

	for _, tc := range toolCalls {
		if unsafeTools[tc.Function.Name] {
			return false
		}
	}
	return true
}

func (a *Agent) executeTools(toolCalls []types.ToolCall, out chan<- Event) []types.Message {
	var results []types.Message

	for _, tc := range toolCalls {
		// Skip malformed tool calls (empty name or missing ID)
		if tc.Function.Name == "" || tc.ID == "" {
			continue
		}

		var args map[string]interface{}
		argStr := tc.Function.Arguments
		if argStr == "" || argStr == "{}" {
			args = map[string]interface{}{}
		} else if err := json.Unmarshal([]byte(argStr), &args); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ tool %s bad args (%d bytes): %q\n", tc.Function.Name, len(argStr), argStr)
			result := fmt.Sprintf("Error parsing arguments: %s", err)
			results = append(results, types.Message{Role: "tool", Content: result, ToolCallID: tc.ID})
			out <- Event{Type: EventToolResult, ToolName: tc.Function.Name, ToolResult: result, ToolCallID: tc.ID}
			continue
		}

		tool := a.tools.Get(tc.Function.Name)
		if tool == nil {
			result := fmt.Sprintf("Unknown tool: %s", tc.Function.Name)
			results = append(results, types.Message{Role: "tool", Content: result, ToolCallID: tc.ID})
			out <- Event{Type: EventToolResult, ToolName: tc.Function.Name, ToolResult: result, ToolCallID: tc.ID}
			continue
		}

		// Bash confirmation for dangerous commands
		if tc.Function.Name == "bash" || tc.Function.Name == "ssh" {
			command, _ := args["command"].(string)
			if a.IsDangerous(command) {
				// Send confirmation event and wait for response
				confirmCh := make(chan bool, 1)
				out <- Event{
					Type:      EventConfirm,
					ToolName:  tc.Function.Name,
					ToolArgs:  command,
					Text:      fmt.Sprintf("⚠ Execute dangerous command?\n  %s", command),
					confirmCh: confirmCh,
				}
				if approved := <-confirmCh; !approved {
					result := "Command rejected by user."
					results = append(results, types.Message{Role: "tool", Content: result, ToolCallID: tc.ID})
					out <- Event{Type: EventToolResult, ToolName: tc.Function.Name, ToolResult: result, ToolCallID: tc.ID}
					continue
				}
			}
		}

		// Generate diff preview for edit operations
		if tc.Function.Name == "edit" {
			if diff := a.generateEditDiff(args); diff != "" {
				out <- Event{Type: EventToolResult, ToolName: "diff", ToolResult: diff, ToolCallID: tc.ID}
			}
		}

		result, err := tool.Execute(args)
		if err != nil {
			result = fmt.Sprintf("Error: %s", err)
		}

		// Handle image results
		if strings.HasPrefix(result, "__IMAGE__") {
			parts := strings.SplitN(result, "__DATA__", 2)
			if len(parts) == 2 {
				mime := strings.TrimPrefix(parts[0], "__IMAGE__")
				b64 := parts[1]
				a.mu.Lock()
				a.hasImages = true
				a.mu.Unlock()

				results = append(results, types.Message{
					Role: "tool", Content: "(see attached image)", ToolCallID: tc.ID,
				})
				results = append(results, types.Message{
					Role: "user",
					Content: []interface{}{
						types.ContentPart{Type: "text", Text: "Attached image from tool result:"},
						types.ContentPart{
							Type:     "image_url",
							ImageURL: &types.ImageURL{URL: fmt.Sprintf("data:%s;base64,%s", mime, b64)},
						},
					},
				})
				out <- Event{Type: EventToolResult, ToolName: tc.Function.Name,
					ToolResult: fmt.Sprintf("📷 Image (%s, %dKB)", mime, len(b64)*3/4/1024), ToolCallID: tc.ID}
				continue
			}
		}

		// Auto-detect infrastructure state changes
		if a.autoStore && a.engram != nil && tc.Function.Name == "bash" {
			a.detectInfraState(args, result)
		}
		if a.autoStore && a.engram != nil && tc.Function.Name == "ssh" {
			a.detectInfraState(args, result)
		}

		// Session capture — extract facts from tool calls
		if a.capture != nil {
			if facts := a.capture.ExtractFromToolCalls(tc.Function.Name, tc.Function.Arguments, result); len(facts) > 0 {
				a.capture.FlushToEngram(facts)
			}
		}

		results = append(results, types.Message{Role: "tool", Content: result, ToolCallID: tc.ID})

		// Track habit
		if a.habits != nil {
			a.habits.Track(tc.Function.Name)
		}

		display := result
		if len(display) > 500 {
			display = display[:500] + fmt.Sprintf("... (%d bytes)", len(result))
		}
		out <- Event{Type: EventToolResult, ToolName: tc.Function.Name, ToolResult: display, ToolCallID: tc.ID}
	}

	return results
}

// generateEditDiff creates a colored diff preview for edit operations.
func (a *Agent) generateEditDiff(args map[string]interface{}) string {
	oldText, _ := args["oldText"].(string)
	newText, _ := args["newText"].(string)
	if oldText == "" || newText == "" {
		return ""
	}

	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldText, newText, false)

	var sb strings.Builder
	sb.WriteString("  📝 diff:\n")
	for _, d := range diffs {
		lines := strings.Split(d.Text, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			switch d.Type {
			case diffmatchpatch.DiffDelete:
				sb.WriteString("  \033[31m- " + line + "\033[0m\n")
			case diffmatchpatch.DiffInsert:
				sb.WriteString("  \033[32m+ " + line + "\033[0m\n")
			}
		}
	}
	return sb.String()
}

// IsDangerous checks if a bash command matches dangerous patterns.
func (a *Agent) IsDangerous(command string) bool {
	if !a.confirmBash {
		return false
	}
	for _, pat := range a.dangerousPatterns {
		if pat.MatchString(command) {
			return true
		}
	}
	return false
}

// maybeCompact auto-compacts if token usage is high. Returns true if compaction occurred.
func (a *Agent) maybeCompact(events chan<- Event) bool {
	a.mu.Lock()
	msgCount := len(a.messages)
	a.mu.Unlock()

	if msgCount < 6 {
		return false
	}

	tokens := a.TokenCount()
	threshold := int(float64(a.contextWindow) * a.compactAt)
	if tokens < threshold {
		return false
	}

	events <- Event{Type: EventText, Text: fmt.Sprintf("\n⚡ Auto-compacting (%d tokens, %.0f%% of context)...\n",
		tokens, float64(tokens)/float64(a.contextWindow)*100)}

	if err := a.Compact(nil); err != nil {
		if events != nil {
			events <- Event{Type: EventError, Text: fmt.Sprintf("auto-compact failed: %v", err)}
		}
		return false
	}
	return true
}

// Compact summarizes the conversation.
func (a *Agent) Compact(events chan<- Event) error {
	reqCtx, cleanup := a.ensureRequestContext()
	if cleanup != nil {
		defer cleanup()
	}

	a.mu.Lock()
	msgCount := len(a.messages)
	if msgCount < 4 {
		a.mu.Unlock()
		return fmt.Errorf("not enough messages to compact")
	}
	msgs := make([]types.Message, len(a.messages))
	copy(msgs, a.messages)
	a.mu.Unlock()

	// Truncate to fit half the context window — prevents the summarization
	// request itself from exceeding API limits
	maxSummaryTokens := a.contextWindow / 2
	for len(msgs) > 4 && token.CountMessages(msgs) > maxSummaryTokens {
		msgs = msgs[1:]
	}

	summaryPrompt := "Summarize this conversation concisely. Include: what was discussed, decisions made, files modified, commands run, current state. This replaces the conversation history."

	allMsgs := make([]types.Message, 0, len(msgs)+2)
	allMsgs = append(allMsgs, types.Message{Role: "system", Content: "You are a precise summarizer."})
	allMsgs = append(allMsgs, msgs...)
	allMsgs = append(allMsgs, types.Message{Role: "user", Content: summaryPrompt})

	req := &provider.Request{Context: reqCtx, Model: a.model, Messages: allMsgs, MaxTokens: 2000}

	streamEvents := make(chan types.StreamEvent, 100)
	go func() {
		defer close(streamEvents)
		if err := a.client.ChatStream(req, streamEvents); err != nil {
			streamEvents <- types.StreamEvent{Error: err}
		}
	}()

	var summary strings.Builder
	var streamErr error
	for evt := range streamEvents {
		if evt.Error != nil {
			streamErr = evt.Error
			continue
		}
		if evt.TextDelta != "" {
			summary.WriteString(evt.TextDelta)
		}
	}

	if streamErr != nil {
		return fmt.Errorf("compact summary stream: %w", streamErr)
	}

	if summary.Len() == 0 {
		return fmt.Errorf("empty summary")
	}

	if a.engram != nil {
		a.engram.Store(summary.String(), "task")
	}

	a.mu.Lock()
	a.messages = []types.Message{
		{Role: "user", Content: "[Previous conversation summary]\n" + summary.String()},
		{Role: "assistant", Content: "I have the full context from our conversation."},
		{Role: "user", Content: "Continue where you left off. Complete any remaining tasks from the summary."},
	}
	a.mu.Unlock()

	if events != nil {
		events <- Event{Type: EventText, Text: fmt.Sprintf("\n[Compacted %d messages into summary]\n", msgCount)}
	}

	return nil
}

// Reset clears conversation history.

// BtwQuery runs an ephemeral side question against the current conversation context.
// The question and answer are NOT added to session history. No tools are available.
func (a *Agent) BtwQuery(question string, events chan<- Event) {
	reqCtx, cleanup := a.ensureRequestContext()
	if cleanup != nil {
		defer cleanup()
	}

	// Snapshot current messages without mutating state
	a.mu.Lock()
	msgs := make([]types.Message, 0, len(a.messages)+2)
	if a.systemPrompt != "" {
		msgs = append(msgs, types.Message{Role: "system", Content: a.systemPrompt})
	}
	msgs = append(msgs, a.messages...)
	a.mu.Unlock()

	// Append the question — never saved back to a.messages
	msgs = append(msgs, types.Message{Role: "user", Content: question})

	req := &provider.Request{
		Context:   reqCtx,
		Model:     a.model,
		Messages:  msgs,
		MaxTokens: 2048,
		// No Tools — btw is context-only, no side effects
	}

	streamEvents := make(chan types.StreamEvent, 100)
	go func() {
		defer close(streamEvents)
		if err := a.client.ChatStream(req, streamEvents); err != nil {
			streamEvents <- types.StreamEvent{Error: err}
		}
	}()

	for evt := range streamEvents {
		if evt.Error != nil {
			events <- Event{Type: EventError, Text: evt.Error.Error()}
			close(events)
			return
		}
		if evt.TextDelta != "" {
			events <- Event{Type: EventText, Text: evt.TextDelta}
		}
	}
	close(events)
}

func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = nil
	a.hasImages = false
}

func (a *Agent) saveMsg(msg types.Message) {
	if a.onSaveMessage != nil {
		a.onSaveMessage(msg)
	}
}

// --- Engram Auto Behaviors ---

var preferencePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(I prefer|always use|never use|never do|don't ever|always do)\b`),
	regexp.MustCompile(`(?i)\b(from now on|going forward|remember that|keep in mind)\b`),
}

func (a *Agent) detectPreferences(text string) {
	for _, pat := range preferencePatterns {
		if pat.MatchString(text) {
			a.engram.Store(text, "decision")
			return
		}
	}
}

var infraPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(systemctl|service)\s+(start|stop|restart|enable|disable)`),
	regexp.MustCompile(`(?i)(docker|podman)\s+(run|stop|rm|compose)`),
	regexp.MustCompile(`(?i)(nginx|caddy|apache)\s+(-s\s+reload|restart)`),
	regexp.MustCompile(`(?i)(scp|rsync)\s+`),
	regexp.MustCompile(`(?i)git\s+push\b`),
	regexp.MustCompile(`(?i)(certbot|letsencrypt)\b`),
	regexp.MustCompile(`(?i)(ufw|iptables|firewall-cmd)\b`),
}

func (a *Agent) detectInfraState(args map[string]interface{}, result string) {
	command, _ := args["command"].(string)
	if command == "" {
		return
	}
	for _, pat := range infraPatterns {
		if pat.MatchString(command) {
			// Only store on success (no error in result)
			if !strings.Contains(strings.ToLower(result), "error") &&
				!strings.Contains(result, "failed") {
				summary := fmt.Sprintf("Command: %s", command)
				if len(summary) > 200 {
					summary = summary[:200]
				}
				a.engram.Store(summary, "state")
			}
			return
		}
	}
}

func (a *Agent) maybeAutoStore(msg *types.Message) {
	text, ok := msg.Content.(string)
	if !ok || len(text) < 100 {
		return
	}
	// Only store if the response mentions significant actions
	significantPatterns := []string{
		"deployed", "created", "configured", "installed",
		"fixed", "resolved", "migrated", "completed",
	}
	lower := strings.ToLower(text)
	for _, p := range significantPatterns {
		if strings.Contains(lower, p) {
			// Store a condensed version
			summary := text
			if len(summary) > 500 {
				summary = summary[:500]
			}
			a.engram.Store("[auto] "+summary, "task")
			return
		}
	}
}

// FilterPrivate strips sensitive content from text.
func (a *Agent) FilterPrivate(text string) string {
	result := text
	for _, pat := range a.privatePatterns {
		result = pat.ReplaceAllString(result, "[REDACTED]")
	}
	return result
}

// UndoLast reverts the most recent file change.
func (a *Agent) UndoLast() (string, error) {
	return a.tools.Undo().Undo(0)
}

// UndoAll reverts all file changes made this session.
func (a *Agent) UndoAll() (string, error) {
	return a.tools.Undo().UndoAll()
}

// ListChanges returns a formatted list of tracked file changes.
func (a *Agent) ListChanges() string {
	entries := a.tools.Undo().List(30)
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("#%d [%s] %s %s\n",
			e.ID, e.Timestamp.Format("15:04:05"), e.Op, e.Path))
	}
	return sb.String()
}

// ListTodos returns the current todo list.
func (a *Agent) ListTodos() string {
	items := a.tools.Todo().List()
	if len(items) == 0 {
		return "No tasks."
	}
	var sb strings.Builder
	for _, item := range items {
		if item.Done {
			sb.WriteString(fmt.Sprintf("  ✓ #%d %s\n", item.ID, item.Text))
		} else {
			sb.WriteString(fmt.Sprintf("  ○ #%d %s\n", item.ID, item.Text))
		}
	}
	return sb.String()
}

// Profile returns the loaded identity profile.
func (a *Agent) Profile() *identity.Profile { return a.profile }

// Habits returns the habit tracker.
func (a *Agent) Habits() *identity.Habits { return a.habits }

// FleetPulse returns the fleet monitor (may be nil).
func (a *Agent) FleetPulse() *fleet.FleetPulse { return a.fleetPulse }

// Capture returns the session capture engine (may be nil).
func (a *Agent) Capture() *engram.SessionCapture { return a.capture }

// BuildAutoContext reads project files and returns context string.
func BuildAutoContext(workDir string) string {
	var parts []string

	// Read AGENTS.md or CLAUDE.md or similar
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "INSTRUCTIONS.md", ".cursorrules", ".github/copilot-instructions.md"} {
		path := filepath.Join(workDir, name)
		if data, err := os.ReadFile(path); err == nil {
			parts = append(parts, fmt.Sprintf("# Project Instructions (%s)\n%s", name, string(data)))
			break
		}
	}

	// Read package files for project type detection
	for _, name := range []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "pom.xml", "Gemfile"} {
		path := filepath.Join(workDir, name)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			if len(content) > 2000 {
				content = content[:2000] + "\n..."
			}
			parts = append(parts, fmt.Sprintf("# %s\n```\n%s\n```", name, content))
			break
		}
	}

	// Read README (first 2000 chars)
	for _, name := range []string{"README.md", "README.txt", "README"} {
		path := filepath.Join(workDir, name)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			if len(content) > 2000 {
				content = content[:2000] + "\n..."
			}
			parts = append(parts, fmt.Sprintf("# %s\n%s", name, content))
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return "\n\n---\n# Project Context\n" + strings.Join(parts, "\n\n")
}

func (a *Agent) SetReasoning(level string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reasoning = level
}

func (a *Agent) Reasoning() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reasoning
}

// detectThinkingBudget maps keywords in user message to thinking token budgets.
func detectThinkingBudget(msg string) int {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "ultrathink"):
		return 31999
	case strings.Contains(lower, "think harder"),
		strings.Contains(lower, "think really hard"),
		strings.Contains(lower, "think a lot"):
		return 15000
	case strings.Contains(lower, "think hard"),
		strings.Contains(lower, "think more"):
		return 10000
	case strings.Contains(lower, "think"):
		return 5000
	}
	return 0
}

// classifyComplexity analyzes a user message and returns "haiku", "sonnet", or "opus".
func classifyComplexity(msg string) string {
	lower := strings.ToLower(msg)
	words := strings.Fields(lower)
	wordCount := len(words)

	// --- Haiku: trivial/conversational ---
	if wordCount <= 3 {
		// Ultra-short messages — quick answers
		trivial := []string{"yes", "no", "ok", "thanks", "thank you", "sure", "yep", "nope",
			"got it", "right", "cool", "nice", "done", "good", "great", "hi", "hello", "hey"}
		for _, t := range trivial {
			if lower == t || strings.TrimSpace(lower) == t {
				return "haiku"
			}
		}
	}

	// Simple questions / quick lookups
	haikuPatterns := []string{
		"what is ", "what does ", "what's ", "how do i ", "how to ",
		"explain ", "describe ", "show me ", "where is ", "where does ",
		"which file", "which function", "list ", "status", "check ",
		"fix typo", "add comment", "rename ", "what happened",
	}
	if wordCount < 20 {
		for _, p := range haikuPatterns {
			if strings.HasPrefix(lower, p) || strings.Contains(lower, p) {
				// But not if it also has complex qualifiers
				if !containsAny(lower, "refactor", "rewrite", "redesign", "architect", "migrate", "implement", "entire", "whole", "all files") {
					return "haiku"
				}
			}
		}
	}

	// --- Opus: complex tasks ---
	opusKeywords := []string{
		"refactor", "redesign", "architect", "rewrite", "overhaul",
		"migrate", "port this", "convert this",
		"implement ", "build a ", "build the ", "create a system", "create a service",
		"design ", "plan ", "strategy",
		"debug this", "investigate", "root cause",
		"review the entire", "review the whole", "review the full", "review all",
		"analyze the codebase", "analyze the architecture", "analyze the system",
		"optimize the", "performance",
		"security audit", "vulnerability",
		"multi-file", "across all files", "across the codebase",
		"complex", "difficult", "tricky", "sophisticated",
	}
	for _, k := range opusKeywords {
		if strings.Contains(lower, k) {
			return "opus"
		}
	}

	// Long messages with technical content suggest complexity
	if wordCount > 100 {
		return "opus"
	}

	// Multiple numbered items or bullet points → complex multi-step
	bullets := strings.Count(lower, "\n- ") + strings.Count(lower, "\n* ")
	numbered := 0
	for i := 1; i <= 9; i++ {
		if strings.Contains(lower, fmt.Sprintf("\n%d.", i)) || strings.Contains(lower, fmt.Sprintf("\n%d)", i)) {
			numbered++
		}
	}
	if bullets >= 3 || numbered >= 3 {
		return "opus"
	}

	// Multiple file references suggest multi-file work
	fileExts := []string{".go", ".ts", ".js", ".py", ".rs", ".tsx", ".jsx", ".css", ".html", ".yaml", ".yml", ".json", ".toml"}
	fileRefs := 0
	for _, ext := range fileExts {
		fileRefs += strings.Count(lower, ext)
	}
	if fileRefs >= 3 {
		return "opus"
	}

	// Default
	return "sonnet"
}

// modelForTier returns the model name for a given complexity tier.
func (a *Agent) modelForTier(tier string) string {
	switch tier {
	case "haiku":
		if a.modelTiers.Haiku != "" {
			return a.modelTiers.Haiku
		}
	case "opus":
		if a.modelTiers.Opus != "" {
			return a.modelTiers.Opus
		}
	default:
		if a.modelTiers.Sonnet != "" {
			return a.modelTiers.Sonnet
		}
	}
	return a.model // fallback to configured model
}

// lastUserMessageLocked returns the last user message text. Must be called with mu held.
func (a *Agent) lastUserMessageLocked() string {
	for i := len(a.messages) - 1; i >= 0; i-- {
		if a.messages[i].Role == "user" {
			if s, ok := a.messages[i].Content.(string); ok {
				return s
			}
		}
	}
	return ""
}

// selectTools filters tools based on message content to reduce token count.
func selectTools(msg string, allTools []types.Tool) []types.Tool {
	lower := strings.ToLower(msg)

	hasFile := strings.ContainsAny(lower, "./") ||
		containsAny(lower, "file", "read", "write", "edit", "create", "delete", "code", "function", "class", "import")
	hasGit := containsAny(lower, "git", "commit", "diff", "branch", "merge", "push", "pull", "stage")
	hasSearch := containsAny(lower, "search", "grep", "find", "glob", "where", "which")
	hasBash := containsAny(lower, "run", "execute", "bash", "command", "install", "build", "test", "script")
	hasMemory := containsAny(lower, "memory", "remember", "engram", "recall", "store")
	hasSSH := containsAny(lower, "ssh", "server", "remote", "deploy")
	hasWeb := containsAny(lower, "search", "google", "web", "internet", "fetch", "url", "http")
	hasAgent := containsAny(lower, "parallel", "agent", "spawn", "delegate", "subtask")

	// If message seems general/conversational, send all tools
	if !hasFile && !hasGit && !hasSearch && !hasBash && !hasMemory && !hasSSH && !hasWeb && !hasAgent {
		return allTools
	}

	fileTools := map[string]bool{
		"read": true, "write": true, "edit": true, "patch": true, "tree": true,
		"think": true, "todo": true, "undo": true,
	}
	gitTools := map[string]bool{
		"git_status": true, "git_diff": true, "git_commit": true, "git_log": true,
	}
	searchTools := map[string]bool{
		"glob": true, "grep": true, "tree": true, "conversation_search": true,
	}
	bashTools := map[string]bool{
		"bash": true,
	}
	memoryTools := map[string]bool{
		"memory_store": true, "memory_search": true, "memory_list": true,
		"memory_update": true, "memory_delete": true, "memory_archive": true,
	}
	sshTools := map[string]bool{
		"ssh": true,
	}
	webTools := map[string]bool{
		"web_search": true, "fetch": true,
	}
	agentTools := map[string]bool{
		"spawn_agent": true,
	}
	alwaysInclude := map[string]bool{
		"think": true, "todo": true,
	}

	allowed := map[string]bool{}
	for k := range alwaysInclude {
		allowed[k] = true
	}
	if hasFile {
		for k := range fileTools {
			allowed[k] = true
		}
	}
	if hasGit {
		for k := range gitTools {
			allowed[k] = true
		}
	}
	if hasSearch {
		for k := range searchTools {
			allowed[k] = true
		}
	}
	if hasBash {
		for k := range bashTools {
			allowed[k] = true
		}
	}
	if hasMemory {
		for k := range memoryTools {
			allowed[k] = true
		}
	}
	if hasSSH {
		for k := range sshTools {
			allowed[k] = true
		}
	}
	if hasWeb {
		for k := range webTools {
			allowed[k] = true
		}
	}
	if hasAgent {
		for k := range agentTools {
			allowed[k] = true
		}
	}

	// Always include file+search basics when doing code tasks (bash implies file ops)
	if hasBash || hasGit {
		for k := range fileTools {
			allowed[k] = true
		}
		for k := range searchTools {
			allowed[k] = true
		}
	}

	var filtered []types.Tool
	for _, t := range allTools {
		if allowed[t.Function.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
