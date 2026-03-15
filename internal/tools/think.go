package tools

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Think tool — extended reasoning scratchpad. Costs zero tokens on the tool
// side since the model's own reasoning IS the content. This gives the model
// a place to work through complex problems step by step without committing
// to output. Every serious agent needs this.
func ThinkTool() *ToolDef {
	return &ToolDef{
		Name:        "think",
		Description: "Use this tool to think through complex problems step-by-step. Your thoughts are recorded but not shown to the user. Use this when you need to plan, reason through tradeoffs, break down a task, or organize your approach before acting.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"thought": map[string]interface{}{
					"type":        "string",
					"description": "Your reasoning, planning, or analysis",
				},
			},
			"required": []string{"thought"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			thought := getStr(args, "thought")
			if thought == "" {
				return "Empty thought.", nil
			}
			return fmt.Sprintf("[Thought recorded: %d chars]", len(thought)), nil
		},
	}
}

// TodoItem represents a single task.
type TodoItem struct {
	ID        int
	Text      string
	Done      bool
	CreatedAt time.Time
	DoneAt    *time.Time
}

// TodoList is a persistent in-session task tracker.
type TodoList struct {
	mu    sync.Mutex
	items []TodoItem
	next  int
}

func NewTodoList() *TodoList {
	return &TodoList{next: 1}
}

func (t *TodoList) Add(text string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	id := t.next
	t.next++
	t.items = append(t.items, TodoItem{
		ID: id, Text: text, CreatedAt: time.Now(),
	})
	return id
}

func (t *TodoList) Complete(id int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.items {
		if t.items[i].ID == id {
			t.items[i].Done = true
			now := time.Now()
			t.items[i].DoneAt = &now
			return true
		}
	}
	return false
}

func (t *TodoList) Remove(id int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.items {
		if t.items[i].ID == id {
			t.items = append(t.items[:i], t.items[i+1:]...)
			return true
		}
	}
	return false
}

func (t *TodoList) List() []TodoItem {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]TodoItem, len(t.items))
	copy(result, t.items)
	return result
}

// TodoTool provides a task tracking tool for the agent.
func TodoTool(todo *TodoList) *ToolDef {
	return &ToolDef{
		Name:        "todolist",
		Description: "Manage a task list for tracking work items. Add tasks, mark them done, remove them, or list all tasks. Use this to stay organized during complex multi-step work.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"add", "done", "remove", "list"},
					"description": "Action to perform",
				},
				"text": map[string]interface{}{
					"type":        "string",
					"description": "Task text (for 'add' action)",
				},
				"id": map[string]interface{}{
					"type":        "number",
					"description": "Task ID (for 'done' or 'remove' actions)",
				},
			},
			"required": []string{"action"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			action := getStr(args, "action")

			switch action {
			case "add":
				text := getStr(args, "text")
				if text == "" {
					return "", fmt.Errorf("text is required for add")
				}
				id := todo.Add(text)
				return fmt.Sprintf("Added task #%d: %s", id, text), nil

			case "done":
				id := getInt(args, "id")
				if id <= 0 {
					return "", fmt.Errorf("id is required for done")
				}
				if todo.Complete(id) {
					return fmt.Sprintf("✓ Completed task #%d", id), nil
				}
				return fmt.Sprintf("Task #%d not found", id), nil

			case "remove":
				id := getInt(args, "id")
				if id <= 0 {
					return "", fmt.Errorf("id is required for remove")
				}
				if todo.Remove(id) {
					return fmt.Sprintf("Removed task #%d", id), nil
				}
				return fmt.Sprintf("Task #%d not found", id), nil

			case "list":
				items := todo.List()
				if len(items) == 0 {
					return "No tasks.", nil
				}
				var sb strings.Builder
				pending, done := 0, 0
				for _, item := range items {
					if item.Done {
						done++
						sb.WriteString(fmt.Sprintf("  ✓ #%d %s\n", item.ID, item.Text))
					} else {
						pending++
						sb.WriteString(fmt.Sprintf("  ○ #%d %s\n", item.ID, item.Text))
					}
				}
				sb.WriteString(fmt.Sprintf("\n%d pending, %d done", pending, done))
				return sb.String(), nil

			default:
				return "", fmt.Errorf("unknown action: %s (use add/done/remove/list)", action)
			}
		},
	}
}
