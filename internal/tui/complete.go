package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Completer handles tab completion for commands and file paths.
type Completer struct {
	workDir  string
	commands []string
	models   []string
	themes   []string
}

func NewCompleter(workDir string) *Completer {
	return &Completer{
		workDir: workDir,
		commands: []string{
			"/quit", "/exit", "/clear", "/compact", "/model", "/theme",
			"/git", "/help", "/sessions", "/branch", "/tasks", "/export",
			"/update", "/cost", "/usage",
		},
		models: []string{
			"claude-opus-4.6",
			"claude-sonnet-4.6",
			"claude-opus-4.5",
			"claude-sonnet-4.5",
			"claude-opus-41",
			"claude-sonnet-4",
			"claude-haiku-4.5",
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
			"gemini-3.1-pro-preview",
			"gemini-3-pro-preview",
			"gemini-3-flash-preview",
			"gemini-2.5-pro",
			"grok-code-fast-1",
		},
		themes: ThemeNames(),
	}
}

// Complete returns completions for the given input.
func (c *Completer) Complete(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	// Command completion
	if strings.HasPrefix(input, "/") {
		// /model <model> completion
		if strings.HasPrefix(input, "/model ") {
			prefix := strings.TrimPrefix(input, "/model ")
			return c.filterPrefix(c.models, prefix)
		}

		// /theme <theme> completion
		if strings.HasPrefix(input, "/theme ") {
			prefix := strings.TrimPrefix(input, "/theme ")
			return c.filterPrefix(c.themes, prefix)
		}

		// Command name completion
		return c.filterPrefix(c.commands, input)
	}

	return nil
}

// CompletePath returns file path completions.
func (c *Completer) CompletePath(partial string) []string {
	if partial == "" {
		return nil
	}

	dir := filepath.Dir(partial)
	base := filepath.Base(partial)

	searchDir := dir
	if !filepath.IsAbs(dir) {
		searchDir = filepath.Join(c.workDir, dir)
	}

	entries, err := os.ReadDir(searchDir)
	if err != nil {
		return nil
	}

	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(base)) {
			path := filepath.Join(dir, name)
			if entry.IsDir() {
				path += string(filepath.Separator)
			}
			matches = append(matches, path)
		}
	}

	sort.Strings(matches)
	if len(matches) > 20 {
		matches = matches[:20]
	}
	return matches
}

func (c *Completer) filterPrefix(items []string, prefix string) []string {
	prefix = strings.ToLower(prefix)
	var matches []string
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item), prefix) {
			matches = append(matches, item)
		}
	}
	return matches
}
