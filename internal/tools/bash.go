package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	maxOutputBytes = 50 * 1024
	maxOutputLines = 2000
	defaultTimeout = 120 // seconds
)

func BashTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "bash",
		Description: "Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to 2000 lines or 50KB.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Command to execute",
				},
				"timeout": map[string]interface{}{
					"type":        "number",
					"description": "Timeout in seconds (default 120)",
				},
			},
			"required": []string{"command"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			command := getStr(args, "command")
			if command == "" {
				return "", fmt.Errorf("command is required")
			}

			timeout := getInt(args, "timeout")
			if timeout <= 0 {
				timeout = defaultTimeout
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
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

			err := cmd.Run()

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

			// Truncate
			lines := strings.Split(output, "\n")
			if len(lines) > maxOutputLines {
				lines = lines[len(lines)-maxOutputLines:]
				output = "... (truncated, showing last 2000 lines)\n" + strings.Join(lines, "\n")
			}
			if len(output) > maxOutputBytes {
				output = output[len(output)-maxOutputBytes:]
				output = "... (truncated, showing last 50KB)\n" + output
			}

			if err != nil {
				if ctx.Err() == context.DeadlineExceeded {
					return output + "\n\nCommand timed out after " + fmt.Sprintf("%d", timeout) + "s", nil
				}
				exitCode := -1
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				}
				return fmt.Sprintf("%s\n\nCommand exited with code %d", output, exitCode), nil
			}

			return output, nil
		},
	}
}
