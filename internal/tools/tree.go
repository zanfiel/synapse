package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TreeTool lists directory contents in a tree format with metadata.
func TreeTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "tree",
		Description: "List directory contents in a tree format. Shows file sizes, types, and structure. Respects .gitignore patterns. Use this to understand project layout.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Directory path (default: current directory)",
				},
				"depth": map[string]interface{}{
					"type":        "number",
					"description": "Max depth to traverse (default 3)",
				},
				"show_hidden": map[string]interface{}{
					"type":        "boolean",
					"description": "Show hidden files (default false)",
				},
				"show_size": map[string]interface{}{
					"type":        "boolean",
					"description": "Show file sizes (default true)",
				},
			},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			dir := getStr(args, "path")
			if dir == "" {
				dir = "."
			}
			dir = resolvePath(workDir, dir)

			depth := getInt(args, "depth")
			if depth <= 0 {
				depth = 3
			}

			showHidden := getBool(args, "show_hidden")
			showSize := true
			if v, ok := args["show_size"]; ok {
				if b, ok := v.(bool); ok {
					showSize = b
				}
			}

			info, err := os.Stat(dir)
			if err != nil {
				return "", fmt.Errorf("stat %s: %w", dir, err)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", dir)
			}

			// Load gitignore patterns
			ignores := loadGitignore(dir)

			var sb strings.Builder
			sb.WriteString(dir + "\n")

			stats := treeStats{}
			buildTree(&sb, dir, "", depth, 0, showHidden, showSize, ignores, &stats)

			sb.WriteString(fmt.Sprintf("\n%d directories, %d files", stats.dirs, stats.files))
			if showSize {
				sb.WriteString(fmt.Sprintf(" (%s total)", humanSize(stats.totalSize)))
			}
			sb.WriteString("\n")

			return sb.String(), nil
		},
	}
}

type treeStats struct {
	dirs      int
	files     int
	totalSize int64
}

func buildTree(sb *strings.Builder, dir, prefix string, maxDepth, currentDepth int, showHidden, showSize bool, ignores []string, stats *treeStats) {
	if currentDepth >= maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Sort: directories first, then alphabetical
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return entries[i].Name() < entries[j].Name()
	})

	// Filter
	var filtered []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if isIgnored(name, e.IsDir(), ignores) {
			continue
		}
		filtered = append(filtered, e)
	}

	for i, entry := range filtered {
		isLast := i == len(filtered)-1
		connector := "├── "
		childPrefix := "│   "
		if isLast {
			connector = "└── "
			childPrefix = "    "
		}

		name := entry.Name()
		info, _ := entry.Info()

		if entry.IsDir() {
			stats.dirs++
			sb.WriteString(prefix + connector + name + "/\n")
			buildTree(sb, filepath.Join(dir, name), prefix+childPrefix, maxDepth, currentDepth+1, showHidden, showSize, ignores, stats)
		} else {
			stats.files++
			size := int64(0)
			if info != nil {
				size = info.Size()
				stats.totalSize += size
			}
			if showSize {
				sb.WriteString(fmt.Sprintf("%s%s%s (%s)\n", prefix, connector, name, humanSize(size)))
			} else {
				sb.WriteString(prefix + connector + name + "\n")
			}
		}
	}
}

func loadGitignore(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return defaultIgnores()
	}

	patterns := defaultIgnores()
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

func defaultIgnores() []string {
	return []string{
		"node_modules", ".git", "__pycache__", ".next", ".nuxt",
		"dist", "build", "target", "vendor", ".venv", "venv",
		".terraform", ".gradle", ".idea", ".vscode",
	}
}

func isIgnored(name string, isDir bool, patterns []string) bool {
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/")
		if matched, _ := filepath.Match(p, name); matched {
			return true
		}
	}
	return false
}

func humanSize(bytes int64) string {
	switch {
	case bytes >= 1024*1024*1024:
		return fmt.Sprintf("%.1fGB", float64(bytes)/(1024*1024*1024))
	case bytes >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	case bytes >= 1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
