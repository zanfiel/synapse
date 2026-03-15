package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/zanfiel/synapse/internal/config"
)

// RegisterCustomTools loads tools from project config.
func RegisterCustomTools(r *Registry, workDir string, defs []config.CustomToolDef) {
	for _, def := range defs {
		r.Register(customTool(workDir, def))
	}
}

func customTool(workDir string, def config.CustomToolDef) *ToolDef {
	params := def.Parameters
	if params == nil {
		params = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	return &ToolDef{
		Name:        def.Name,
		Description: def.Description,
		Parameters:  params,
		Execute: func(args map[string]interface{}) (string, error) {
			// Template the command with args
			tmpl, err := template.New("cmd").Parse(def.Command)
			if err != nil {
				return "", fmt.Errorf("bad command template: %w", err)
			}

			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, args); err != nil {
				return "", fmt.Errorf("template exec: %w", err)
			}

			command := buf.String()

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			var cmd *exec.Cmd
			if runtime.GOOS == "windows" {
				cmd = exec.CommandContext(ctx, "cmd", "/c", command)
			} else {
				cmd = exec.CommandContext(ctx, "bash", "-c", command)
			}
			cmd.Dir = workDir

			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err = cmd.Run()

			var result strings.Builder
			if stdout.Len() > 0 {
				result.WriteString(stdout.String())
			}
			if stderr.Len() > 0 {
				if result.Len() > 0 {
					result.WriteString("\n")
				}
				result.WriteString(stderr.String())
			}

			output := result.String()
			if len(output) > maxOutputBytes {
				output = output[len(output)-maxOutputBytes:]
			}

			if err != nil {
				return fmt.Sprintf("%s\nCommand failed: %s", output, err), nil
			}
			return output, nil
		},
	}
}
