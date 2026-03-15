package tools

import (
	"fmt"
	"strings"
)

// SubAgentRunner is set by main to avoid circular import.
// func(task, model string) (string, error)
var SubAgentRunner func(task, model string) (string, error)

func SpawnAgentTool() *ToolDef {
	return &ToolDef{
		Name:        "spawn_agent",
		Description: "Spawn a sub-agent to handle a scoped task independently. Use for parallel or isolated subtasks. Default model is Haiku for cost efficiency.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"task": map[string]interface{}{
					"type":        "string",
					"description": "The task for the sub-agent to complete",
				},
				"model": map[string]interface{}{
					"type":        "string",
					"description": "Model to use (default: claude-haiku-4-5-20251001 for cost efficiency)",
				},
			},
			"required": []string{"task"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			task := getStr(args, "task")
			if task == "" {
				return "", fmt.Errorf("task is required")
			}
			model := getStr(args, "model")
			if model == "" {
				model = "claude-haiku-4-5-20251001"
			}

			if SubAgentRunner == nil {
				return "", fmt.Errorf("sub-agent runner not configured")
			}

			result, err := SubAgentRunner(task, model)
			if err != nil {
				return fmt.Sprintf("sub-agent error: %s", err), nil
			}
			if result == "" {
				result = "(sub-agent completed with no output)"
			}
			return "Sub-agent result:\n" + strings.TrimSpace(result), nil
		},
	}
}
