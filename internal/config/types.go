package config

// MCPServerConfig defines an MCP server connection.
type MCPServerConfig struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	URL       string            `json:"url,omitempty"`
	Transport string            `json:"transport"`
}

// FleetServer defines a server to monitor.
type FleetServer struct {
	Name    string   `json:"name"`
	Host    string   `json:"host"`
	Port    int      `json:"port,omitempty"`
	User    string   `json:"user"`
	KeyPath string   `json:"key_path,omitempty"`
	HSIP    string   `json:"headscale_ip,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}
