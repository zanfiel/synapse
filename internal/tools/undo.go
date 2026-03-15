package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UndoManager tracks every file modification for rollback.
type UndoManager struct {
	mu      sync.Mutex
	entries []UndoEntry
	dir     string // snapshot storage dir
}

// UndoEntry records a single file change.
type UndoEntry struct {
	ID        int
	Path      string
	Op        string    // "write", "edit", "delete", "create"
	Before    string    // previous content (empty for create)
	After     string    // new content (empty for delete)
	Timestamp time.Time
}

func NewUndoManager(configDir string) *UndoManager {
	dir := filepath.Join(configDir, "undo")
	os.MkdirAll(dir, 0755)
	return &UndoManager{dir: dir}
}

// Track records a file operation for later undo.
func (u *UndoManager) Track(path, op, before, after string) int {
	u.mu.Lock()
	defer u.mu.Unlock()

	id := len(u.entries) + 1
	u.entries = append(u.entries, UndoEntry{
		ID:        id,
		Path:      path,
		Op:        op,
		Before:    before,
		After:     after,
		Timestamp: time.Now(),
	})
	return id
}

// Undo reverts the most recent change (or a specific ID).
func (u *UndoManager) Undo(id int) (string, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if len(u.entries) == 0 {
		return "", fmt.Errorf("nothing to undo")
	}

	var entry *UndoEntry
	var idx int

	if id <= 0 {
		// Undo most recent
		idx = len(u.entries) - 1
		entry = &u.entries[idx]
	} else {
		for i := len(u.entries) - 1; i >= 0; i-- {
			if u.entries[i].ID == id {
				entry = &u.entries[i]
				idx = i
				break
			}
		}
	}

	if entry == nil {
		return "", fmt.Errorf("undo entry #%d not found", id)
	}

	switch entry.Op {
	case "write", "edit":
		if err := os.WriteFile(entry.Path, []byte(entry.Before), 0644); err != nil {
			return "", fmt.Errorf("undo write %s: %w", entry.Path, err)
		}
	case "create":
		if err := os.Remove(entry.Path); err != nil {
			return "", fmt.Errorf("undo create %s: %w", entry.Path, err)
		}
	case "delete":
		dir := filepath.Dir(entry.Path)
		os.MkdirAll(dir, 0755)
		if err := os.WriteFile(entry.Path, []byte(entry.Before), 0644); err != nil {
			return "", fmt.Errorf("undo delete %s: %w", entry.Path, err)
		}
	}

	// Remove the entry
	u.entries = append(u.entries[:idx], u.entries[idx+1:]...)

	return fmt.Sprintf("Reverted %s on %s", entry.Op, entry.Path), nil
}

// List returns recent undo entries.
func (u *UndoManager) List(limit int) []UndoEntry {
	u.mu.Lock()
	defer u.mu.Unlock()

	if limit <= 0 || limit > len(u.entries) {
		limit = len(u.entries)
	}

	start := len(u.entries) - limit
	if start < 0 {
		start = 0
	}

	result := make([]UndoEntry, len(u.entries)-start)
	copy(result, u.entries[start:])
	return result
}

// UndoAll reverts all changes in reverse order.
func (u *UndoManager) UndoAll() (string, error) {
	u.mu.Lock()
	count := len(u.entries)
	u.mu.Unlock()

	if count == 0 {
		return "Nothing to undo.", nil
	}

	var reverted int
	var errors []string

	for i := 0; i < count; i++ {
		msg, err := u.Undo(0) // always undo latest
		if err != nil {
			errors = append(errors, err.Error())
		} else {
			reverted++
			_ = msg
		}
	}

	result := fmt.Sprintf("Reverted %d/%d changes.", reverted, count)
	if len(errors) > 0 {
		result += "\nErrors: " + strings.Join(errors, "; ")
	}
	return result, nil
}

// UndoTool returns the undo tool definition.
func UndoTool(undo *UndoManager) *ToolDef {
	return &ToolDef{
		Name:        "undo",
		Description: "Undo file changes made during this session. Can revert the last change, a specific change by ID, or all changes.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "number",
					"description": "Specific change ID to undo (omit for most recent)",
				},
				"all": map[string]interface{}{
					"type":        "boolean",
					"description": "Undo ALL changes made this session",
				},
				"list": map[string]interface{}{
					"type":        "boolean",
					"description": "List recent changes instead of undoing",
				},
			},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			if getBool(args, "list") {
				entries := undo.List(20)
				if len(entries) == 0 {
					return "No changes tracked.", nil
				}
				var sb strings.Builder
				for _, e := range entries {
					sb.WriteString(fmt.Sprintf("#%d [%s] %s %s\n",
						e.ID, e.Timestamp.Format("15:04:05"), e.Op, e.Path))
				}
				return sb.String(), nil
			}

			if getBool(args, "all") {
				return undo.UndoAll()
			}

			id := getInt(args, "id")
			return undo.Undo(id)
		},
	}
}

func getBool(args map[string]interface{}, key string) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}
