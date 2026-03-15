package tools

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// PatchTool applies unified diff patches to files. More powerful than edit
// for multi-hunk changes — send one patch instead of multiple edits.
func PatchTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "patch",
		Description: "Apply a unified diff patch to a file. More powerful than edit for multiple changes — send all hunks in one call. The patch should be in standard unified diff format.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the file to patch",
				},
				"diff": map[string]interface{}{
					"type":        "string",
					"description": "Unified diff content (lines starting with - are removed, + are added, space is context)",
				},
			},
			"required": []string{"path", "diff"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			path := getStr(args, "path")
			diff := getStr(args, "diff")
			if path == "" || diff == "" {
				return "", fmt.Errorf("path and diff are required")
			}
			path = resolvePath(workDir, path)

			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", path, err)
			}

			lines := strings.Split(string(data), "\n")
			hunks, err := parseHunks(diff)
			if err != nil {
				return "", err
			}

			// Apply hunks in reverse order to preserve line numbers
			for i := len(hunks) - 1; i >= 0; i-- {
				lines, err = applyHunk(lines, hunks[i])
				if err != nil {
					return "", fmt.Errorf("hunk %d: %w", i+1, err)
				}
			}

			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
				return "", fmt.Errorf("write %s: %w", path, err)
			}

			return fmt.Sprintf("Applied %d hunk(s) to %s", len(hunks), path), nil
		},
	}
}

type diffHunk struct {
	oldStart int
	oldCount int
	newStart int
	newCount int
	lines    []diffLine
}

type diffLine struct {
	op   byte   // ' ', '+', '-'
	text string
}

var hunkHeaderRe = regexp.MustCompile(`^@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)

func parseHunks(diff string) ([]diffHunk, error) {
	rawLines := strings.Split(diff, "\n")
	var hunks []diffHunk
	var current *diffHunk

	for _, line := range rawLines {
		// Skip file headers
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			continue
		}

		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				hunks = append(hunks, *current)
			}
			current = &diffHunk{
				oldStart: mustAtoi(m[1]),
				oldCount: mustAtoiDefault(m[2], 1),
				newStart: mustAtoi(m[3]),
				newCount: mustAtoiDefault(m[4], 1),
			}
			continue
		}

		if current == nil {
			// Lines before first hunk header — try to parse as simple +/- format
			if len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' ') {
				if current == nil {
					current = &diffHunk{oldStart: 1, oldCount: 0, newStart: 1, newCount: 0}
				}
			} else {
				continue
			}
		}

		if len(line) == 0 {
			current.lines = append(current.lines, diffLine{op: ' ', text: ""})
			continue
		}

		switch line[0] {
		case '+':
			current.lines = append(current.lines, diffLine{op: '+', text: line[1:]})
		case '-':
			current.lines = append(current.lines, diffLine{op: '-', text: line[1:]})
		case ' ':
			current.lines = append(current.lines, diffLine{op: ' ', text: line[1:]})
		default:
			// Treat as context
			current.lines = append(current.lines, diffLine{op: ' ', text: line})
		}
	}

	if current != nil {
		hunks = append(hunks, *current)
	}

	if len(hunks) == 0 {
		return nil, fmt.Errorf("no valid hunks found in diff")
	}

	return hunks, nil
}

func applyHunk(lines []string, h diffHunk) ([]string, error) {
	start := h.oldStart - 1 // 0-indexed
	if start < 0 {
		start = 0
	}

	// Find the best match location with fuzzy offset
	bestOffset := findBestMatch(lines, h, start)
	if bestOffset < 0 {
		return nil, fmt.Errorf("could not find match for hunk at line %d", h.oldStart)
	}

	pos := bestOffset
	var result []string
	result = append(result, lines[:pos]...)

	for _, dl := range h.lines {
		switch dl.op {
		case ' ':
			if pos < len(lines) {
				result = append(result, lines[pos])
				pos++
			}
		case '-':
			if pos < len(lines) {
				pos++ // skip the deleted line
			}
		case '+':
			result = append(result, dl.text)
		}
	}

	result = append(result, lines[pos:]...)
	return result, nil
}

// findBestMatch looks for the context lines near the expected position.
func findBestMatch(lines []string, h diffHunk, expected int) int {
	// Extract context/delete lines from hunk (these must exist in file)
	var oldLines []string
	for _, dl := range h.lines {
		if dl.op == ' ' || dl.op == '-' {
			oldLines = append(oldLines, dl.text)
		}
	}

	if len(oldLines) == 0 {
		// Pure insertion — use expected position
		if expected > len(lines) {
			return len(lines)
		}
		return expected
	}

	// Search near expected position first, then expand
	for radius := 0; radius < len(lines); radius++ {
		for _, offset := range []int{expected + radius, expected - radius} {
			if offset < 0 || offset+len(oldLines) > len(lines) {
				continue
			}
			match := true
			for i, ol := range oldLines {
				if strings.TrimRight(lines[offset+i], " \t\r") != strings.TrimRight(ol, " \t\r") {
					match = false
					break
				}
			}
			if match {
				return offset
			}
		}
	}

	return -1
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func mustAtoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
