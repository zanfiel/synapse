package tools

import (
	"fmt"

	"github.com/zanfiel/synapse/internal/mcp"
)

// RegisterMCPTools discovers tools from all connected MCP servers and adds them to the registry.
func RegisterMCPTools(r *Registry, mcpClient *mcp.Client) int {
	allTools := mcpClient.AllTools()
	count := 0

	for _, tool := range allTools {
		// Prefix tool name with server to avoid collisions: "server_toolname"
		name := fmt.Sprintf("mcp_%s_%s", tool.ServerName, tool.Name)

		// Capture for closure
		serverName := tool.ServerName
		originalName := tool.Name

		r.Register(&ToolDef{
			Name:        name,
			Description: fmt.Sprintf("[MCP:%s] %s", tool.ServerName, tool.Description),
			Parameters:  tool.InputSchema,
			Execute: func(args map[string]interface{}) (string, error) {
				result, err := mcpClient.CallTool(serverName, originalName, args)
				if err != nil {
					return "", fmt.Errorf("MCP %s/%s: %w", serverName, originalName, err)
				}
				return result, nil
			},
		})
		count++
	}

	return count
}
