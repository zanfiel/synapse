package tools

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxReadLines = 2000
const maxReadBytes = 50 * 1024

// Image extensions and their MIME types
var imageExts = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

func isImage(path string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	mime, ok := imageExts[ext]
	return mime, ok
}

func ReadTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "read",
		Description: "Read the contents of a file. Supports text files and images (jpg, png, gif, webp). Images are returned as base64 for vision analysis. Text output is truncated to 2000 lines or 50KB. Use offset/limit for large files.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the file to read (relative or absolute)",
				},
				"offset": map[string]interface{}{
					"type":        "number",
					"description": "Line number to start reading from (1-indexed)",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Maximum number of lines to read",
				},
			},
			"required": []string{"path"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			path := getStr(args, "path")
			if path == "" {
				return "", fmt.Errorf("path is required")
			}
			path = resolvePath(workDir, path)

			// Check if it's an image
			if mime, ok := isImage(path); ok {
				data, err := os.ReadFile(path)
				if err != nil {
					return "", fmt.Errorf("read %s: %w", path, err)
				}
				// 20MB limit for images
				if len(data) > 20*1024*1024 {
					return "", fmt.Errorf("image too large: %d bytes (max 20MB)", len(data))
				}
				b64 := base64.StdEncoding.EncodeToString(data)
				// Return special format the agent loop can detect
				return fmt.Sprintf("__IMAGE__%s__DATA__%s", mime, b64), nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", path, err)
			}

			lines := strings.Split(string(data), "\n")

			offset := getInt(args, "offset")
			limit := getInt(args, "limit")

			if offset > 0 {
				offset-- // Convert to 0-indexed
				if offset >= len(lines) {
					return "", fmt.Errorf("offset %d beyond file length %d", offset+1, len(lines))
				}
				lines = lines[offset:]
			}

			if limit > 0 && limit < len(lines) {
				lines = lines[:limit]
			}

			if len(lines) > maxReadLines {
				lines = lines[:maxReadLines]
			}

			result := strings.Join(lines, "\n")
			if len(result) > maxReadBytes {
				result = result[:maxReadBytes] + "\n... (truncated)"
			}

			return result, nil
		},
	}
}

func WriteTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "write",
		Description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the file to write",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Content to write to the file",
				},
			},
			"required": []string{"path", "content"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			path := getStr(args, "path")
			content := getStr(args, "content")
			if path == "" {
				return "", fmt.Errorf("path is required")
			}
			path = resolvePath(workDir, path)

			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("mkdir %s: %w", dir, err)
			}

			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return "", fmt.Errorf("write %s: %w", path, err)
			}

			return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path), nil
		},
	}
}

func EditTool(workDir string) *ToolDef {
	return &ToolDef{
		Name:        "edit",
		Description: "Edit a file by replacing exact text. The oldText must match exactly (including whitespace).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to the file to edit",
				},
				"oldText": map[string]interface{}{
					"type":        "string",
					"description": "Exact text to find and replace",
				},
				"newText": map[string]interface{}{
					"type":        "string",
					"description": "New text to replace with",
				},
			},
			"required": []string{"path", "oldText", "newText"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			path := getStr(args, "path")
			oldText := getStr(args, "oldText")
			newText := getStr(args, "newText")

			if path == "" || oldText == "" {
				return "", fmt.Errorf("path and oldText are required")
			}
			path = resolvePath(workDir, path)

			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", path, err)
			}

			content := string(data)
			count := strings.Count(content, oldText)
			if count == 0 {
				// Try with normalized line endings
				normalized := strings.ReplaceAll(content, "\r\n", "\n")
				normalizedOld := strings.ReplaceAll(oldText, "\r\n", "\n")
				count = strings.Count(normalized, normalizedOld)
				if count == 0 {
					return "", fmt.Errorf("oldText not found in %s", path)
				}
				content = strings.Replace(normalized, normalizedOld, newText, 1)
			} else {
				content = strings.Replace(content, oldText, newText, 1)
			}

			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return "", fmt.Errorf("write %s: %w", path, err)
			}

			return fmt.Sprintf("Edited %s (replaced %d occurrence(s))", path, 1), nil
		},
	}
}

// Helper functions
func resolvePath(workDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workDir, path)
}

func getStr(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getInt(args map[string]interface{}, key string) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}
