package tools

import (
	"os"

	"github.com/zanfiel/synapse/internal/types"
)

// Registry holds all available tools.
type Registry struct {
	tools map[string]*ToolDef
	undo  *UndoManager
	todo  *TodoList
}

// ToolDef defines a tool with its API schema and execution function.
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
	Execute     func(args map[string]interface{}) (string, error)
}

func NewRegistry(workDir string, configDir string) *Registry {
	undo := NewUndoManager(configDir)
	todo := NewTodoList()

	r := &Registry{
		tools: make(map[string]*ToolDef),
		undo:  undo,
		todo:  todo,
	}

	// Core file tools (undo-tracked)
	r.Register(ReadTool(workDir))
	r.Register(r.writeToolTracked(workDir))
	r.Register(r.editToolTracked(workDir))
	r.Register(BashTool(workDir))
	r.Register(PatchTool(workDir))

	// Navigation & search
	r.Register(TreeTool(workDir))
	r.Register(GlobTool(workDir))
	r.Register(GrepTool(workDir))

	// Reasoning & planning
	r.Register(ThinkTool())
	r.Register(TodoTool(todo))

	// Utilities
	r.Register(FetchTool())
	r.Register(UndoTool(undo))

	return r
}

// writeToolTracked wraps WriteTool with undo tracking.
func (r *Registry) writeToolTracked(workDir string) *ToolDef {
	base := WriteTool(workDir)
	originalExec := base.Execute

	base.Execute = func(args map[string]interface{}) (string, error) {
		path := getStr(args, "path")
		resolved := resolvePath(workDir, path)

		// Capture before state
		before := ""
		op := "create"
		if data, err := os.ReadFile(resolved); err == nil {
			before = string(data)
			op = "write"
		}

		result, err := originalExec(args)
		if err != nil {
			return result, err
		}

		// Track for undo
		content := getStr(args, "content")
		r.undo.Track(resolved, op, before, content)

		return result, nil
	}

	return base
}

// editToolTracked wraps EditTool with undo tracking.
func (r *Registry) editToolTracked(workDir string) *ToolDef {
	base := EditTool(workDir)
	originalExec := base.Execute

	base.Execute = func(args map[string]interface{}) (string, error) {
		path := getStr(args, "path")
		resolved := resolvePath(workDir, path)

		// Capture before state
		before := ""
		if data, err := os.ReadFile(resolved); err == nil {
			before = string(data)
		}

		result, err := originalExec(args)
		if err != nil {
			return result, err
		}

		// Capture after state
		after := ""
		if data, err := os.ReadFile(resolved); err == nil {
			after = string(data)
		}

		r.undo.Track(resolved, "edit", before, after)
		return result, nil
	}

	return base
}

func (r *Registry) Register(t *ToolDef) {
	if t != nil {
		r.tools[t.Name] = t
	}
}

func (r *Registry) Get(name string) *ToolDef {
	return r.tools[name]
}

func (r *Registry) APITools() []types.Tool {
	var tools []types.Tool
	for _, t := range r.tools {
		tools = append(tools, types.Tool{
			Type: "function",
			Function: types.ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return tools
}

func (r *Registry) Names() []string {
	var names []string
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

func (r *Registry) Undo() *UndoManager { return r.undo }
func (r *Registry) Todo() *TodoList     { return r.todo }
