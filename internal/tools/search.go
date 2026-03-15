package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GlobTool provides fuzzy file finding.
func GlobTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "glob",
		Description: "Find files matching a glob pattern. Returns matching file paths.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "Glob pattern (e.g. '**/*.go', 'src/**/*.ts')",
				},
			},
			"required": []string{"pattern"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			pattern := getStr(args, "pattern")
			if pattern == "" {
				return "", fmt.Errorf("pattern required")
			}

			var matches []string
			root := workDir

			filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				// Skip hidden dirs and common junk
				name := info.Name()
				if info.IsDir() && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__" || name == "target") {
					return filepath.SkipDir
				}

				rel, _ := filepath.Rel(root, path)
				matched, _ := filepath.Match(pattern, rel)
				if !matched {
					// Try matching just the filename
					matched, _ = filepath.Match(pattern, name)
				}
				if matched {
					matches = append(matches, rel)
				}
				return nil
			})

			if len(matches) == 0 {
				return "No files found.", nil
			}
			sort.Strings(matches)
			if len(matches) > 100 {
				matches = matches[:100]
				return strings.Join(matches, "\n") + fmt.Sprintf("\n... (%d+ results, showing first 100)", len(matches)), nil
			}
			return strings.Join(matches, "\n"), nil
		},
	}
}

// GrepTool searches file contents.
func GrepTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "grep",
		Description: "Search for a pattern in files. Returns matching lines with file paths and line numbers.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "Text pattern to search for",
				},
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File or directory to search in (default: working directory)",
				},
				"include": map[string]interface{}{
					"type":        "string",
					"description": "File extension filter (e.g. '*.go', '*.ts')",
				},
			},
			"required": []string{"pattern"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			pattern := getStr(args, "pattern")
			searchPath := getStr(args, "path")
			include := getStr(args, "include")

			if pattern == "" {
				return "", fmt.Errorf("pattern required")
			}
			if searchPath == "" {
				searchPath = workDir
			} else {
				searchPath = resolvePath(workDir, searchPath)
			}

			var results []string
			maxResults := 50

			filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					if info != nil && info.IsDir() {
						name := info.Name()
						if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
							return filepath.SkipDir
						}
					}
					return nil
				}
				if len(results) >= maxResults {
					return filepath.SkipAll
				}

				// Extension filter
				if include != "" {
					matched, _ := filepath.Match(include, info.Name())
					if !matched {
						return nil
					}
				}

				// Skip binary files
				if info.Size() > 1024*1024 {
					return nil
				}

				data, err := os.ReadFile(path)
				if err != nil {
					return nil
				}

				rel, _ := filepath.Rel(workDir, path)
				lines := strings.Split(string(data), "\n")
				for i, line := range lines {
					if strings.Contains(line, pattern) {
						results = append(results, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
						if len(results) >= maxResults {
							break
						}
					}
				}
				return nil
			})

			if len(results) == 0 {
				return "No matches found.", nil
			}
			out := strings.Join(results, "\n")
			if len(results) >= maxResults {
				out += fmt.Sprintf("\n... (limited to %d results)", maxResults)
			}
			return out, nil
		},
	}
}

// ConversationSearchTool searches past session messages.
func ConversationSearchTool(dbPath string) *ToolDef {
	return &ToolDef{
		Name:        "conversation_search",
		Description: "Search through past conversation messages to find what was discussed or when something was done.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search keywords to find in past conversations",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max results (default 20)",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			return "Conversation search requires active session store.", nil
		},
	}
}

// SessionSearcher is the interface needed for conversation search.
type SessionSearcher interface {
	SearchMessages(query string, limit int) ([]SessionMessageResult, error)
}

// SessionMessageResult mirrors session.MessageResult without import cycle.
type SessionMessageResult struct {
	SessionID string
	Role      string
	Content   string
	CreatedAt string
}

// ConversationSearchToolLive creates a conversation search tool backed by a real session store.
func ConversationSearchToolLive(store SessionSearcher) *ToolDef {
	return &ToolDef{
		Name:        "conversation_search",
		Description: "Search through all past conversation messages. Use this to find what was discussed, when something was done, or to locate specific conversations by content.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search keywords to find in past conversations",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max results (default 20, max 100)",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			query := getStr(args, "query")
			limit := getInt(args, "limit")
			if limit <= 0 {
				limit = 20
			}
			if limit > 100 {
				limit = 100
			}

			results, err := store.SearchMessages(query, limit)
			if err != nil {
				return "", err
			}

			if len(results) == 0 {
				return "No matching messages found.", nil
			}

			var sb strings.Builder
			for _, r := range results {
				content := r.Content
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				content = strings.ReplaceAll(content, "\n", " ")
				sb.WriteString(fmt.Sprintf("[%s] %s (%s): %s\n", r.SessionID, r.CreatedAt, r.Role, content))
			}
			return sb.String(), nil
		},
	}
}
