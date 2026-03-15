package engram

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SessionCapture extracts structured memories from agent conversations.
// Inspired by claude-mem's auto-capture approach but uses heuristics first,
// LLM extraction only when heuristics find nothing.
type SessionCapture struct {
	mu        sync.Mutex
	client    *Client
	sessionID string
	project   string
	workDir   string

	// Track what we've already captured to avoid duplicates
	capturedFiles  map[string]bool
	capturedCmds   map[string]bool
	extractedCount int
}

// CapturedFact represents a structured extraction from conversation.
type CapturedFact struct {
	Content  string
	Category string
	Tags     []string
}

func NewSessionCapture(client *Client, sessionID, project, workDir string) *SessionCapture {
	return &SessionCapture{
		client:        client,
		sessionID:     sessionID,
		project:       project,
		workDir:       workDir,
		capturedFiles: make(map[string]bool),
		capturedCmds:  make(map[string]bool),
	}
}

// --- Regex patterns for heuristic extraction ---

var (
	// File operations
	reFileWrite  = regexp.MustCompile(`(?i)(?:wrote|created|saved|generated)\s+(?:file\s+)?[` + "`" + `"']?([^\s` + "`" + `"']+\.\w{1,6})[` + "`" + `"']?`)
	reFileEdit   = regexp.MustCompile(`(?i)(?:edited|modified|updated|changed|fixed)\s+(?:file\s+)?[` + "`" + `"']?([^\s` + "`" + `"']+\.\w{1,6})[` + "`" + `"']?`)
	reFileDelete = regexp.MustCompile(`(?i)(?:deleted|removed)\s+(?:file\s+)?[` + "`" + `"']?([^\s` + "`" + `"']+\.\w{1,6})[` + "`" + `"']?`)

	// Commands and deployments
	reSCP       = regexp.MustCompile(`scp\s+.*?(\S+@\S+:\S+)`)
	reSSH       = regexp.MustCompile(`ssh\s+.*?(\S+@\S+)`)
	reDeployed  = regexp.MustCompile(`(?i)(?:deployed|uploaded|pushed|transferred)\s+(?:to\s+)?(\S+)`)
	reService   = regexp.MustCompile(`(?i)(?:restarted|started|stopped|enabled)\s+(?:service\s+)?(\S+\.service)`)
	reGitCommit = regexp.MustCompile(`(?i)(?:committed|commit)\s+([a-f0-9]{7,40})`)
	reGitPush   = regexp.MustCompile(`(?i)pushed\s+to\s+(\S+)`)

	// Decisions and discoveries
	reDecision  = regexp.MustCompile(`(?i)(?:decided|choosing|went with|using|switched to)\s+(.{10,80})`)
	reDiscovery = regexp.MustCompile(`(?i)(?:found|discovered|turns out|realized|noticed)\s+(?:that\s+)?(.{10,100})`)
	reIssue     = regexp.MustCompile(`(?i)(?:bug|issue|problem|error|broken|failing|crashed)[:.]?\s*(.{10,100})`)
	reFix       = regexp.MustCompile(`(?i)(?:fixed|resolved|patched|workaround)[:.]?\s*(.{10,100})`)

	// Version/port/config
	rePort    = regexp.MustCompile(`(?:port|listening on)\s+(\d{2,5})`)
	reVersion = regexp.MustCompile(`(?i)v(?:ersion)?\s*(\d+\.\d+(?:\.\d+)?)`)
	reURL     = regexp.MustCompile(`https?://[^\s)>"]+`)
)

// ExtractFromToolCalls processes tool call results and extracts memories.
// Called after each agent turn completes.
func (sc *SessionCapture) ExtractFromToolCalls(toolName, toolArgs, toolResult string) []CapturedFact {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	var facts []CapturedFact

	switch toolName {
	case "write":
		path := extractJSONField(toolArgs, "path")
		if path != "" && !sc.capturedFiles[path] {
			sc.capturedFiles[path] = true
			facts = append(facts, CapturedFact{
				Content:  fmt.Sprintf("Created/wrote file: %s", path),
				Category: "task",
				Tags:     []string{"file-write", sc.project},
			})
		}

	case "edit":
		path := extractJSONField(toolArgs, "path")
		if path != "" && !sc.capturedFiles[path] {
			sc.capturedFiles[path] = true
			facts = append(facts, CapturedFact{
				Content:  fmt.Sprintf("Modified file: %s", path),
				Category: "task",
				Tags:     []string{"file-edit", sc.project},
			})
		}

	case "bash":
		cmd := extractJSONField(toolArgs, "command")
		if cmd == "" {
			break
		}

		// SSH commands = remote work
		if strings.Contains(cmd, "ssh ") || strings.Contains(cmd, "scp ") {
			if m := reSSH.FindStringSubmatch(cmd); m != nil {
				target := m[1]
				if !sc.capturedCmds[target] {
					sc.capturedCmds[target] = true
					facts = append(facts, CapturedFact{
						Content:  fmt.Sprintf("SSH session to %s", target),
						Category: "task",
						Tags:     []string{"ssh", sc.project},
					})
				}
			}
			if m := reSCP.FindStringSubmatch(cmd); m != nil {
				facts = append(facts, CapturedFact{
					Content:  fmt.Sprintf("File transfer via SCP to %s", m[1]),
					Category: "task",
					Tags:     []string{"deploy", sc.project},
				})
			}
		}

		// Service management
		if strings.Contains(cmd, "systemctl") {
			if m := reService.FindStringSubmatch(cmd + " " + toolResult); m != nil {
				facts = append(facts, CapturedFact{
					Content:  fmt.Sprintf("Service operation: %s", m[0]),
					Category: "task",
					Tags:     []string{"service", sc.project},
				})
			}
		}

		// Git operations
		if strings.Contains(cmd, "git commit") || strings.Contains(cmd, "git push") {
			if m := reGitCommit.FindStringSubmatch(toolResult); m != nil {
				facts = append(facts, CapturedFact{
					Content:  fmt.Sprintf("Git commit %s in %s", m[1], sc.project),
					Category: "task",
					Tags:     []string{"git", sc.project},
				})
			}
			if m := reGitPush.FindStringSubmatch(cmd + " " + toolResult); m != nil {
				facts = append(facts, CapturedFact{
					Content:  fmt.Sprintf("Pushed to %s", m[1]),
					Category: "task",
					Tags:     []string{"git-push", sc.project},
				})
			}
		}

		// Build/compile detection
		if strings.Contains(cmd, "go build") || strings.Contains(cmd, "bun build") ||
			strings.Contains(cmd, "npm run build") || strings.Contains(cmd, "cargo build") {
			if toolResult != "" && !strings.Contains(strings.ToLower(toolResult), "error") {
				facts = append(facts, CapturedFact{
					Content:  fmt.Sprintf("Successful build in %s", sc.project),
					Category: "task",
					Tags:     []string{"build", sc.project},
				})
			}
		}

	case "memory_store", "memory_update", "memory_correct":
		// Already going to Engram, skip re-capture
		return nil
	}

	return facts
}

// ExtractFromText processes assistant text output for decisions/discoveries.
func (sc *SessionCapture) ExtractFromText(text string) []CapturedFact {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	var facts []CapturedFact

	// Decisions
	if matches := reDecision.FindAllStringSubmatch(text, 3); matches != nil {
		for _, m := range matches {
			content := strings.TrimSpace(m[1])
			if len(content) > 20 { // Skip trivially short
				facts = append(facts, CapturedFact{
					Content:  fmt.Sprintf("[auto-captured] Decision: %s", content),
					Category: "decision",
					Tags:     []string{"auto", sc.project},
				})
			}
		}
	}

	// Issues/bugs
	if matches := reIssue.FindAllStringSubmatch(text, 3); matches != nil {
		for _, m := range matches {
			content := strings.TrimSpace(m[1])
			if len(content) > 15 {
				facts = append(facts, CapturedFact{
					Content:  fmt.Sprintf("[auto-captured] Issue: %s", content),
					Category: "issue",
					Tags:     []string{"auto", sc.project},
				})
			}
		}
	}

	// Fixes
	if matches := reFix.FindAllStringSubmatch(text, 3); matches != nil {
		for _, m := range matches {
			content := strings.TrimSpace(m[1])
			if len(content) > 15 {
				facts = append(facts, CapturedFact{
					Content:  fmt.Sprintf("[auto-captured] Fix: %s", content),
					Category: "task",
					Tags:     []string{"auto", "fix", sc.project},
				})
			}
		}
	}

	return facts
}

// FlushToEngram stores accumulated facts. Called at end of agent turn.
func (sc *SessionCapture) FlushToEngram(facts []CapturedFact) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if len(facts) == 0 {
		return nil
	}

	// Deduplicate
	seen := make(map[string]bool)
	var unique []CapturedFact
	for _, f := range facts {
		key := f.Category + ":" + f.Content
		if !seen[key] {
			seen[key] = true
			unique = append(unique, f)
		}
	}

	// Batch store — combine related facts into one memory if many
	if len(unique) > 5 {
		// Too many small facts — combine into summary
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("[auto-session %s] Work summary (%s):\n",
			sc.sessionID[:8], time.Now().Format("2006-01-02 15:04")))
		for _, f := range unique {
			sb.WriteString(fmt.Sprintf("- %s\n", f.Content))
		}
		_, err := sc.client.Store(sb.String(), "task")
		sc.extractedCount++
		return err
	}

	// Store individually
	for _, f := range unique {
		taggedContent := f.Content
		if sc.sessionID != "" {
			taggedContent += fmt.Sprintf(" [session:%s]", sc.sessionID[:8])
		}
		_, err := sc.client.Store(taggedContent, f.Category)
		if err != nil {
			return err
		}
		sc.extractedCount++
	}

	return nil
}

// SessionSummary generates an end-of-session summary.
func (sc *SessionCapture) SessionSummary(messageCount, toolCalls, tokenCount int, duration time.Duration) string {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Session %s completed", sc.sessionID[:8]))
	sb.WriteString(fmt.Sprintf(" | %d messages, %d tool calls, %dk tokens, %s",
		messageCount, toolCalls, tokenCount/1000, duration.Round(time.Second)))
	if sc.project != "" {
		sb.WriteString(fmt.Sprintf(" | project: %s", sc.project))
	}
	sb.WriteString(fmt.Sprintf(" | %d files touched, %d facts captured",
		len(sc.capturedFiles), sc.extractedCount))
	return sb.String()
}

// StoreSessionSummary stores the end-of-session summary in Engram.
func (sc *SessionCapture) StoreSessionSummary(messageCount, toolCalls, tokenCount int, duration time.Duration) error {
	summary := sc.SessionSummary(messageCount, toolCalls, tokenCount, duration)
	_, err := sc.client.Store(summary, "task")
	return err
}

// --- Helpers ---

func extractJSONField(jsonStr, field string) string {
	// Quick-and-dirty JSON field extraction without full parse
	pattern := regexp.MustCompile(fmt.Sprintf(`"%s"\s*:\s*"([^"]*)"`, field))
	if m := pattern.FindStringSubmatch(jsonStr); m != nil {
		return m[1]
	}
	return ""
}
