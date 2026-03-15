package tools

import (
	"fmt"
	"strings"

	"github.com/zanfiel/synapse/internal/lsp"
)

// DiagnosticsTool exposes LSP diagnostics (compiler errors) to the agent.
func DiagnosticsTool(lspClient *lsp.Client) *ToolDef {
	return &ToolDef{
		Name:        "diagnostics",
		Description: "Get compiler errors and warnings from the language server. Shows real errors that the compiler/linter found. Use after making code changes to verify correctness.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path to check (omit for all files with errors)",
				},
			},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			path := getStr(args, "path")

			if path != "" {
				diags := lspClient.GetDiagnostics(path)
				if len(diags) == 0 {
					return "No diagnostics for " + path, nil
				}
				return formatDiagnostics(path, diags), nil
			}

			// All errors across workspace
			allDiags := lspClient.GetErrors()
			if len(allDiags) == 0 {
				return "✓ No errors found.", nil
			}

			var sb strings.Builder
			total := 0
			for uri, diags := range allDiags {
				sb.WriteString(formatDiagnostics(uri, diags))
				total += len(diags)
			}
			sb.WriteString(fmt.Sprintf("\n%d error(s) across %d file(s)", total, len(allDiags)))
			return sb.String(), nil
		},
	}
}

// SymbolTool lets the agent search for symbols across the workspace.
func SymbolTool(lspClient *lsp.Client) *ToolDef {
	return &ToolDef{
		Name:        "symbol",
		Description: "Search for code symbols (functions, types, variables) across the project. Use to find where something is defined or to understand project structure.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Symbol name or pattern to search for",
				},
				"file": map[string]interface{}{
					"type":        "string",
					"description": "If set, list all symbols in this file instead of searching",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			file := getStr(args, "file")
			query := getStr(args, "query")

			if file != "" {
				symbols, err := lspClient.DocumentSymbols(file)
				if err != nil {
					return "", err
				}
				if len(symbols) == 0 {
					return "No symbols found in " + file, nil
				}
				var sb strings.Builder
				formatDocSymbols(&sb, symbols, 0)
				return sb.String(), nil
			}

			symbols, err := lspClient.WorkspaceSymbols(query)
			if err != nil {
				return "", err
			}
			if len(symbols) == 0 {
				return "No symbols matching: " + query, nil
			}

			var sb strings.Builder
			for _, s := range symbols {
				sb.WriteString(fmt.Sprintf("%-12s %s (line %d)\n",
					s.Kind, s.Name, s.Range.Start.Line+1))
			}
			return sb.String(), nil
		},
	}
}

// HoverTool gets type/documentation info for a symbol at a position.
func HoverTool(lspClient *lsp.Client) *ToolDef {
	return &ToolDef{
		Name:        "hover",
		Description: "Get type information and documentation for a symbol at a specific position in a file. Like hovering over a symbol in an IDE.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path",
				},
				"line": map[string]interface{}{
					"type":        "number",
					"description": "Line number (1-indexed)",
				},
				"column": map[string]interface{}{
					"type":        "number",
					"description": "Column number (1-indexed)",
				},
			},
			"required": []string{"path", "line", "column"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			path := getStr(args, "path")
			line := getInt(args, "line") - 1   // LSP is 0-indexed
			col := getInt(args, "column") - 1

			if path == "" {
				return "", fmt.Errorf("path is required")
			}

			info, err := lspClient.Hover(path, line, col)
			if err != nil {
				return "", err
			}
			if info == "" {
				return "No information available at this position.", nil
			}
			return info, nil
		},
	}
}

// DefinitionTool finds where a symbol is defined.
func DefinitionTool(lspClient *lsp.Client) *ToolDef {
	return &ToolDef{
		Name:        "definition",
		Description: "Go to the definition of a symbol. Returns the file path and line number where the symbol is defined.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "File path containing the symbol reference",
				},
				"line": map[string]interface{}{
					"type":        "number",
					"description": "Line number (1-indexed)",
				},
				"column": map[string]interface{}{
					"type":        "number",
					"description": "Column number (1-indexed)",
				},
			},
			"required": []string{"path", "line", "column"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			path := getStr(args, "path")
			line := getInt(args, "line") - 1
			col := getInt(args, "column") - 1

			if path == "" {
				return "", fmt.Errorf("path is required")
			}

			locs, err := lspClient.Definition(path, line, col)
			if err != nil {
				return "", err
			}
			if len(locs) == 0 {
				return "No definition found.", nil
			}

			var sb strings.Builder
			for _, loc := range locs {
				sb.WriteString(fmt.Sprintf("%s:%d:%d\n",
					strings.TrimPrefix(loc.URI, "file://"),
					loc.Range.Start.Line+1,
					loc.Range.Start.Character+1))
			}
			return sb.String(), nil
		},
	}
}

func formatDiagnostics(uri string, diags []lsp.Diagnostic) string {
	path := strings.TrimPrefix(uri, "file://")
	var sb strings.Builder
	for _, d := range diags {
		severity := "ERROR"
		switch d.Severity {
		case 2:
			severity = "WARN"
		case 3:
			severity = "INFO"
		case 4:
			severity = "HINT"
		}
		sb.WriteString(fmt.Sprintf("%s:%d:%d [%s] %s\n",
			path, d.Range.Start.Line+1, d.Range.Start.Character+1,
			severity, d.Message))
	}
	return sb.String()
}

func formatDocSymbols(sb *strings.Builder, symbols []lsp.DocumentSymbol, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, s := range symbols {
		kind := lsp.SymbolKindName(s.Kind)
		sb.WriteString(fmt.Sprintf("%s%-12s %s (line %d)\n",
			indent, kind, s.Name, s.Range.Start.Line+1))
		if len(s.Children) > 0 {
			formatDocSymbols(sb, s.Children, depth+1)
		}
	}
}
