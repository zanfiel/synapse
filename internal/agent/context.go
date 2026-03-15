package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SmartContext analyzes user messages and auto-includes relevant file content.
// This gives the agent understanding of the project without the user having to
// manually specify which files to read — the killer feature nobody else does well.

var (
	// Patterns that reference files
	fileRefPattern     = regexp.MustCompile(`(?:^|\s|["'\x60])([a-zA-Z0-9_./\-]+\.[a-zA-Z0-9]+)(?:["'\x60]|\s|$|[,.:;])`)
	funcRefPattern     = regexp.MustCompile(`\b(func|function|def|class|struct|type|interface)\s+(\w+)`)
	importRefPattern   = regexp.MustCompile(`(?:from|import|require|use)\s+["']?([^"'\s;]+)`)
)

// ExtractFileReferences finds likely file paths mentioned in a message.
func ExtractFileReferences(message string, workDir string) []string {
	var refs []string
	seen := make(map[string]bool)

	for _, match := range fileRefPattern.FindAllStringSubmatch(message, -1) {
		candidate := match[1]
		// Must have a real extension
		ext := filepath.Ext(candidate)
		if ext == "" || len(ext) < 2 || len(ext) > 6 {
			continue
		}
		// Skip URLs
		if strings.Contains(candidate, "://") {
			continue
		}

		resolved := resolvePath(workDir, candidate)
		if _, err := os.Stat(resolved); err == nil {
			if !seen[resolved] {
				seen[resolved] = true
				refs = append(refs, resolved)
			}
		}
	}

	return refs
}

// AutoContext reads referenced files and builds context for the LLM.
func AutoContext(message string, workDir string, maxBytes int) string {
	refs := ExtractFileReferences(message, workDir)
	if len(refs) == 0 {
		return ""
	}

	if maxBytes <= 0 {
		maxBytes = 30 * 1024 // 30KB default
	}

	var parts []string
	totalBytes := 0

	for _, path := range refs {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		content := string(data)
		if len(content)+totalBytes > maxBytes {
			remaining := maxBytes - totalBytes
			if remaining > 500 {
				content = content[:remaining] + "\n... (truncated)"
			} else {
				break
			}
		}

		rel, _ := filepath.Rel(workDir, path)
		if rel == "" {
			rel = path
		}

		parts = append(parts, "### "+rel+"\n```\n"+content+"\n```")
		totalBytes += len(content)
	}

	if len(parts) == 0 {
		return ""
	}

	return "\n\n---\n# Auto-included referenced files\n" + strings.Join(parts, "\n\n")
}

func resolvePath(workDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workDir, path)
}
