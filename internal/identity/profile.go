package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zanfiel/synapse/internal/engram"
)

// Profile represents everything Synapse knows about its operator.
// Built dynamically from Engram memories, AGENTS.md, and accumulated
// session data. This is injected into the system prompt so the LLM
// has full context on every single message — no cold starts, ever.
type Profile struct {
	// Static identity (from AGENTS.md / config)
	Name           string
	Personality    string   // "gir" or "quantum" mode instructions
	VerifyPhrase   string
	InfraMap       *InfraMap

	// Dynamic context (from Engram, rebuilt each session)
	RecentTasks    []string // Last N completed tasks
	RecentDecisions []string // Last N decisions
	ActiveIssues   []string // Known unresolved issues
	Preferences    []string // Detected preferences
	CurrentState   []string // Infrastructure state

	// Session-specific
	WorkDir        string
	ProjectName    string
	LastSessionSummary string
}

// InfraMap holds the entire server fleet.
type InfraMap struct {
	Servers []Server
}

type Server struct {
	Name      string
	Alias     []string // short names: "web", "db", "prod"
	IP        string
	HeadscaleIP string
	SSHUser   string
	SSHPort   int
	SSHKey    string
	Services  []string
	Notes     string
}

// DefaultInfraMap returns an empty infrastructure map.
// Configure your servers via the fleet_servers config or .synapse.json.
// Example server entry:
//
//	Server{
//	    Name: "web-1", Alias: []string{"web", "prod"},
//	    IP: "10.0.0.1", HeadscaleIP: "100.64.1.1",
//	    SSHUser: "deploy", SSHKey: "~/.ssh/id_rsa",
//	    Services: []string{"nginx", "postgres"},
//	    Notes: "Production web server",
//	}
func DefaultInfraMap() *InfraMap {
	return &InfraMap{}
}

// ResolveServer finds a server by name or alias.
func (m *InfraMap) ResolveServer(query string) *Server {
	query = strings.ToLower(query)
	for i := range m.Servers {
		s := &m.Servers[i]
		if strings.ToLower(s.Name) == query {
			return s
		}
		for _, alias := range s.Alias {
			if strings.ToLower(alias) == query {
				return s
			}
		}
	}
	return nil
}

// SSHCommand builds the ssh command string for a server.
func (s *Server) SSHCommand() string {
	port := ""
	if s.SSHPort != 0 && s.SSHPort != 22 {
		port = fmt.Sprintf("-p %d ", s.SSHPort)
	}
	return fmt.Sprintf("ssh -i %s %s%s@%s", s.SSHKey, port, s.SSHUser, s.IP)
}

// BuildProfile constructs a full Profile from Engram + local config.
// Uses a timeout so startup never blocks more than 5 seconds on Engram.
func BuildProfile(eg *engram.Client, workDir string) *Profile {
	p := &Profile{
		InfraMap: DefaultInfraMap(),
		WorkDir:  workDir,
	}

	// Detect project name
	p.ProjectName = filepath.Base(workDir)
	if data, err := os.ReadFile(filepath.Join(workDir, "go.mod")); err == nil {
		lines := strings.SplitN(string(data), "\n", 2)
		if len(lines) > 0 {
			p.ProjectName = strings.TrimPrefix(lines[0], "module ")
		}
	}

	if eg == nil {
		return p
	}

	// Pull recent context from Engram with a hard 5s timeout
	done := make(chan struct{})
	go func() {
		defer close(done)
		buildProfileFromEngram(p, eg)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Engram too slow, proceed without full context
	}

	return p
}

func buildProfileFromEngram(p *Profile, eg *engram.Client) {
	if tasks, err := eg.List("task", 5); err == nil {
		for _, m := range tasks {
			summary := m.Content
			if len(summary) > 150 {
				summary = summary[:150] + "..."
			}
			p.RecentTasks = append(p.RecentTasks, summary)
		}
	}

	if decisions, err := eg.List("decision", 5); err == nil {
		for _, m := range decisions {
			summary := m.Content
			if len(summary) > 150 {
				summary = summary[:150] + "..."
			}
			p.RecentDecisions = append(p.RecentDecisions, summary)
		}
	}

	if issues, err := eg.List("issue", 5); err == nil {
		for _, m := range issues {
			summary := m.Content
			if len(summary) > 150 {
				summary = summary[:150] + "..."
			}
			p.ActiveIssues = append(p.ActiveIssues, summary)
		}
	}

	if states, err := eg.List("state", 5); err == nil {
		for _, m := range states {
			summary := m.Content
			if len(summary) > 150 {
				summary = summary[:150] + "..."
			}
			p.CurrentState = append(p.CurrentState, summary)
		}
	}

	// Search for preferences
	if prefs, err := eg.Search("prefer always never", 5); err == nil {
		for _, m := range prefs {
			summary := m.Content
			if len(summary) > 150 {
				summary = summary[:150] + "..."
			}
			p.Preferences = append(p.Preferences, summary)
		}
	}

	// Look for last session in this project
	if results, err := eg.Search(p.ProjectName, 3); err == nil {
		for _, m := range results {
			if strings.Contains(strings.ToLower(m.Category), "task") {
				p.LastSessionSummary = m.Content
				if len(p.LastSessionSummary) > 300 {
					p.LastSessionSummary = p.LastSessionSummary[:300] + "..."
				}
				break
			}
		}
	}
}

// SystemPromptContext generates the dynamic context block injected into
// the system prompt. This is what makes Synapse KNOW you.
func (p *Profile) SystemPromptContext() string {
	var sb strings.Builder

	sb.WriteString("\n\n# Operator Context (auto-loaded from Engram)\n")
	sb.WriteString(fmt.Sprintf("Operator: %s\n", p.Name))
	sb.WriteString(fmt.Sprintf("Project: %s\n", p.ProjectName))
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", p.WorkDir))
	sb.WriteString(fmt.Sprintf("Time: %s\n", time.Now().Format("Monday, January 2, 2006 at 3:04 PM MST")))

	if p.LastSessionSummary != "" {
		sb.WriteString("\n## Last Session (this project)\n")
		sb.WriteString(p.LastSessionSummary + "\n")
	}

	if len(p.RecentTasks) > 0 {
		sb.WriteString("\n## Recent Completed Work\n")
		for _, t := range p.RecentTasks {
			sb.WriteString("- " + t + "\n")
		}
	}

	if len(p.RecentDecisions) > 0 {
		sb.WriteString("\n## Standing Decisions\n")
		for _, d := range p.RecentDecisions {
			sb.WriteString("- " + d + "\n")
		}
	}

	if len(p.ActiveIssues) > 0 {
		sb.WriteString("\n## Known Issues\n")
		for _, i := range p.ActiveIssues {
			sb.WriteString("- " + i + "\n")
		}
	}

	if len(p.CurrentState) > 0 {
		sb.WriteString("\n## Infrastructure State\n")
		for _, s := range p.CurrentState {
			sb.WriteString("- " + s + "\n")
		}
	}

	if len(p.Preferences) > 0 {
		sb.WriteString("\n## Operator Preferences\n")
		for _, pref := range p.Preferences {
			sb.WriteString("- " + pref + "\n")
		}
	}

	sb.WriteString("\n## Infrastructure Fleet\n")
	for _, s := range p.InfraMap.Servers {
		services := ""
		if len(s.Services) > 0 {
			services = " [" + strings.Join(s.Services, ", ") + "]"
		}
		sb.WriteString(fmt.Sprintf("- %s (%s)%s\n", s.Name, s.IP, services))
	}

	return sb.String()
}

// ServerList returns a formatted infrastructure overview.
func (p *Profile) ServerList() string {
	var sb strings.Builder
	sb.WriteString("⚡ INFRASTRUCTURE FLEET\n")
	sb.WriteString(strings.Repeat("─", 60) + "\n")

	for _, s := range p.InfraMap.Servers {
		hsIP := s.HeadscaleIP
		if hsIP == "" {
			hsIP = "no-hs"
		}
		sb.WriteString(fmt.Sprintf("  %-20s %s / %s\n", s.Name, s.IP, hsIP))
		if len(s.Services) > 0 {
			sb.WriteString(fmt.Sprintf("  %-20s %s\n", "", strings.Join(s.Services, ", ")))
		}
	}
	return sb.String()
}
