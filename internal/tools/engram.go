package tools

import (
	"fmt"
	"strings"

	"github.com/zanfiel/synapse/internal/engram"
)

// RegisterEngramTools adds memory tools to the registry.
func RegisterEngramTools(r *Registry, client *engram.Client) {
	r.Register(memoryStoreTool(client))
	r.Register(memorySearchTool(client))
	r.Register(memoryListTool(client))
	r.Register(memoryUpdateTool(client))
	r.Register(memoryDeleteTool(client))
	r.Register(memoryArchiveTool(client))
}

func memoryStoreTool(client *engram.Client) *ToolDef {
	return &ToolDef{
		Name:        "memory_store",
		Description: "Store a memory in Engram persistent memory. Use to record completed work, decisions, discoveries, state, or issues.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"content": map[string]interface{}{
					"type":        "string",
					"description": "The memory content. Be specific: what was done, where, relevant details.",
				},
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"task", "discovery", "decision", "state", "issue"},
					"description": "Category: task=completed work, discovery=found info, decision=choice made, state=current status, issue=known problem",
				},
			},
			"required": []string{"content", "category"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			content := getStr(args, "content")
			category := getStr(args, "category")
			if content == "" || category == "" {
				return "", fmt.Errorf("content and category are required")
			}

			mem, err := client.Store(content, category)
			if err != nil {
				return "", err
			}

			return fmt.Sprintf("Stored memory #%d (%s): %s", mem.ID, category, truncate(content, 100)), nil
		},
	}
}

func memorySearchTool(client *engram.Client) *ToolDef {
	return &ToolDef{
		Name:        "memory_search",
		Description: "Search Engram for relevant memories using full-text search.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search keywords",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max results (default 10, max 50)",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			query := getStr(args, "query")
			limit := getInt(args, "limit")
			if limit <= 0 {
				limit = 10
			}
			if limit > 50 {
				limit = 50
			}

			results, err := client.Search(query, limit)
			if err != nil {
				return "", err
			}

			if len(results) == 0 {
				return "No memories found.", nil
			}

			var sb strings.Builder
			for _, m := range results {
				sb.WriteString(fmt.Sprintf("[#%d] %s (%s) %s\n", m.ID, m.CreatedAt, m.Category, m.Content))
			}
			return sb.String(), nil
		},
	}
}

func memoryListTool(client *engram.Client) *ToolDef {
	return &ToolDef{
		Name:        "memory_list",
		Description: "List recent memories from Engram, optionally filtered by category.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"task", "discovery", "decision", "state", "issue"},
					"description": "Filter by category (omit for all)",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Number of results (default 15, max 50)",
				},
			},
			"required": []string{},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			category := getStr(args, "category")
			limit := getInt(args, "limit")
			if limit <= 0 {
				limit = 15
			}

			results, err := client.List(category, limit)
			if err != nil {
				return "", err
			}

			if len(results) == 0 {
				return "No memories found.", nil
			}

			var sb strings.Builder
			for _, m := range results {
				sb.WriteString(fmt.Sprintf("[#%d] %s (%s) %s\n", m.ID, m.CreatedAt, m.Category, m.Content))
			}
			return sb.String(), nil
		},
	}
}

func memoryUpdateTool(client *engram.Client) *ToolDef {
	return &ToolDef{
		Name:        "memory_update",
		Description: "Update a memory by ID.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "number",
					"description": "Memory ID to update",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "New content",
				},
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"task", "discovery", "decision", "state", "issue"},
					"description": "Optionally change category",
				},
			},
			"required": []string{"id", "content"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			id := getInt(args, "id")
			content := getStr(args, "content")
			category := getStr(args, "category")

			if err := client.Update(id, content, category); err != nil {
				return "", err
			}
			return fmt.Sprintf("Updated memory #%d", id), nil
		},
	}
}

func memoryDeleteTool(client *engram.Client) *ToolDef {
	return &ToolDef{
		Name:        "memory_delete",
		Description: "Delete a memory by ID.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "number",
					"description": "Memory ID to delete",
				},
			},
			"required": []string{"id"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			id := getInt(args, "id")
			if err := client.Delete(id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted memory #%d", id), nil
		},
	}
}

func memoryArchiveTool(client *engram.Client) *ToolDef {
	return &ToolDef{
		Name:        "memory_archive",
		Description: "Archive a memory (soft-remove from active recall, remains searchable).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "number",
					"description": "Memory ID to archive",
				},
			},
			"required": []string{"id"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			id := getInt(args, "id")
			if err := client.Archive(id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Archived memory #%d", id), nil
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
