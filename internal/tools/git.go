package tools

import (
	"fmt"

	gitpkg "github.com/zanfiel/synapse/internal/git"
)

func GitStatusTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "git_status",
		Description: "Show git status for the current repository.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			status, err := gitpkg.ShortStatus(workDir)
			if err != nil {
				return "", fmt.Errorf("not a git repository or git not available")
			}
			return status, nil
		},
	}
}

func GitDiffTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "git_diff",
		Description: "Show git diff of current changes (unstaged + staged).",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			diff, err := gitpkg.CurrentDiff(workDir)
			if err != nil {
				return "", fmt.Errorf("git diff failed: %w", err)
			}
			if diff == "" {
				return "No changes.", nil
			}
			if len(diff) > maxOutputBytes {
				diff = diff[:maxOutputBytes] + "\n... (truncated)"
			}
			return diff, nil
		},
	}
}

func GitCommitTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "git_commit",
		Description: "Stage all changes and commit with a message.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message": map[string]interface{}{
					"type":        "string",
					"description": "Commit message",
				},
			},
			"required": []string{"message"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			msg := getStr(args, "message")
			if msg == "" {
				return "", fmt.Errorf("message is required")
			}
			out, err := gitpkg.CommitAll(workDir, msg)
			if err != nil {
				return "", fmt.Errorf("git commit: %w", err)
			}
			return out, nil
		},
	}
}

func GitLogTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "git_log",
		Description: "Show recent git log (last N commits, default 10).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"count": map[string]interface{}{
					"type":        "number",
					"description": "Number of commits to show (default 10)",
				},
			},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			n := getInt(args, "count")
			if n <= 0 || n > 50 {
				n = 10
			}
			log, err := gitpkg.Log(workDir, n)
			if err != nil {
				return "", fmt.Errorf("git log: %w", err)
			}
			return log, nil
		},
	}
}
