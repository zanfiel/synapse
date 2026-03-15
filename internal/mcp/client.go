package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Transport represents how to connect to an MCP server.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportSSE   Transport = "sse"
)

// ServerConfig defines an MCP server to connect to.
type ServerConfig struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`          // for stdio: binary to spawn
	Args      []string          `json:"args,omitempty"`   // for stdio: arguments
	Env       map[string]string `json:"env,omitempty"`    // environment variables
	URL       string            `json:"url,omitempty"`    // for SSE: HTTP endpoint
	Transport Transport         `json:"transport"`
}

// Client manages connections to MCP servers and exposes their tools.
type Client struct {
	servers  map[string]*serverConn
	mu       sync.RWMutex
}

// ToolInfo is a tool discovered from an MCP server.
type ToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	ServerName  string                 `json:"-"` // which server provides this
}

// serverConn manages a single MCP server connection.
type serverConn struct {
	config  ServerConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	scanner *bufio.Scanner
	tools   []ToolInfo
	nextID  atomic.Int64
	pending sync.Map // id -> chan *jsonrpcResponse
	mu      sync.Mutex
	alive   bool
	sseConn *sseConn // non-nil for SSE transport
}

// JSON-RPC 2.0 types
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// New creates a new MCP client.
func New() *Client {
	return &Client{
		servers: make(map[string]*serverConn),
	}
}

// Connect initializes a connection to an MCP server.
func (c *Client) Connect(cfg ServerConfig) error {
	switch cfg.Transport {
	case TransportStdio:
		return c.connectStdio(cfg)
	case TransportSSE:
		return c.connectSSE(cfg)
	default:
		return fmt.Errorf("unknown transport: %s", cfg.Transport)
	}
}

func (c *Client) connectStdio(cfg ServerConfig) error {
	cmd := exec.Command(cfg.Command, cfg.Args...)

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	// Discard stderr (MCP servers often log there)
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cfg.Command, err)
	}

	conn := &serverConn{
		config:  cfg,
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		scanner: bufio.NewScanner(stdout),
		alive:   true,
	}
	conn.scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer

	// Start reading responses
	go conn.readLoop()

	// Initialize
	if err := conn.initialize(); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("initialize %s: %w", cfg.Name, err)
	}

	// Discover tools
	tools, err := conn.listTools()
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("list tools from %s: %w", cfg.Name, err)
	}
	conn.tools = tools

	c.mu.Lock()
	c.servers[cfg.Name] = conn
	c.mu.Unlock()

	return nil
}

func (conn *serverConn) readLoop() {
	for conn.scanner.Scan() {
		line := conn.scanner.Text()
		if line == "" {
			continue
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		if ch, ok := conn.pending.LoadAndDelete(resp.ID); ok {
			ch.(chan *jsonrpcResponse) <- &resp
		}
	}
	conn.alive = false
}

func (conn *serverConn) call(method string, params interface{}) (*jsonrpcResponse, error) {
	id := conn.nextID.Add(1)

	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	respCh := make(chan *jsonrpcResponse, 1)
	conn.pending.Store(id, respCh)

	conn.mu.Lock()
	_, err = conn.stdin.Write(append(data, '\n'))
	conn.mu.Unlock()
	if err != nil {
		conn.pending.Delete(id)
		return nil, fmt.Errorf("write: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	case <-ctx.Done():
		conn.pending.Delete(id)
		return nil, fmt.Errorf("timeout waiting for response to %s", method)
	}
}

func (conn *serverConn) initialize() error {
	params := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "synapse",
			"version": "0.8.0",
		},
	}

	_, err := conn.call("initialize", params)
	if err != nil {
		return err
	}

	// Send initialized notification (no response expected)
	notif := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      0,
		Method:  "notifications/initialized",
	}
	data, _ := json.Marshal(notif)
	conn.mu.Lock()
	conn.stdin.Write(append(data, '\n'))
	conn.mu.Unlock()

	return nil
}

func (conn *serverConn) listTools() ([]ToolInfo, error) {
	resp, err := conn.call("tools/list", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}

	tools := make([]ToolInfo, len(result.Tools))
	for i, t := range result.Tools {
		tools[i] = ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			ServerName:  conn.config.Name,
		}
	}
	return tools, nil
}

// CallTool invokes a tool on the appropriate MCP server.
func (c *Client) CallTool(serverName, toolName string, args map[string]interface{}) (string, error) {
	c.mu.RLock()
	conn, ok := c.servers[serverName]
	c.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("unknown MCP server: %s", serverName)
	}

	if !conn.alive {
		return "", fmt.Errorf("MCP server %s is not running", serverName)
	}

	params := map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	}

	var resp *jsonrpcResponse
	var err error

	if conn.sseConn != nil {
		// SSE transport — HTTP POST
		id := conn.nextID.Add(1)
		req := jsonrpcRequest{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: params}
		resp, err = conn.sseConn.post(req)
	} else {
		// Stdio transport — JSON-RPC over stdin/stdout
		resp, err = conn.call("tools/call", params)
	}

	if err != nil {
		return "", err
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return string(resp.Result), nil
	}

	var parts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// AllTools returns all tools from all connected servers.
func (c *Client) AllTools() []ToolInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var all []ToolInfo
	for _, conn := range c.servers {
		all = append(all, conn.tools...)
	}
	return all
}

// ServerNames returns names of all connected servers.
func (c *Client) ServerNames() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var names []string
	for name := range c.servers {
		names = append(names, name)
	}
	return names
}

// Close shuts down all MCP server connections.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, conn := range c.servers {
		if conn.cmd != nil && conn.cmd.Process != nil {
			conn.stdin.Close()
			conn.cmd.Process.Kill()
		}
	}
	c.servers = make(map[string]*serverConn)
}

// Status returns connection status for all servers.
func (c *Client) Status() map[string]bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := make(map[string]bool)
	for name, conn := range c.servers {
		status[name] = conn.alive
	}
	return status
}

// --- SSE Transport ---

// sseConn manages an SSE-based MCP server connection.
type sseConn struct {
	baseURL    string
	httpClient *http.Client
	messagesURL string // POST endpoint discovered from SSE
}

func (c *Client) connectSSE(cfg ServerConfig) error {
	if cfg.URL == "" {
		return fmt.Errorf("SSE transport requires url")
	}

	sc := &sseConn{
		baseURL:    cfg.URL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	// Connect to SSE endpoint to discover the messages URL
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", cfg.URL, nil)
	if err != nil {
		return fmt.Errorf("sse request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := sc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sse connect %s: %w", cfg.URL, err)
	}

	// Read events until we get the endpoint event
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			// MCP SSE sends the messages endpoint as first event
			if strings.HasPrefix(data, "/") || strings.HasPrefix(data, "http") {
				sc.messagesURL = data
				if strings.HasPrefix(data, "/") {
					// Relative URL — combine with base
					base := strings.TrimSuffix(cfg.URL, "/sse")
					base = strings.TrimSuffix(base, "/")
					sc.messagesURL = base + data
				}
				break
			}

			// Try JSON parse for endpoint event
			var evt struct {
				Endpoint string `json:"endpoint"`
				URL      string `json:"url"`
			}
			if json.Unmarshal([]byte(data), &evt) == nil {
				if evt.Endpoint != "" {
					sc.messagesURL = evt.Endpoint
				} else if evt.URL != "" {
					sc.messagesURL = evt.URL
				}
				if sc.messagesURL != "" {
					break
				}
			}
		}
	}
	resp.Body.Close()

	if sc.messagesURL == "" {
		// Fall back to /messages at the base URL
		sc.messagesURL = strings.TrimSuffix(cfg.URL, "/sse") + "/messages"
	}

	// Initialize via POST
	initReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "synapse",
				"version": "0.9.0",
			},
		},
	}

	if _, err := sc.post(initReq); err != nil {
		return fmt.Errorf("sse initialize: %w", err)
	}

	// Send initialized notification
	notif := jsonrpcRequest{JSONRPC: "2.0", ID: 0, Method: "notifications/initialized"}
	sc.post(notif) // ignore error on notification

	// List tools
	toolsReq := jsonrpcRequest{JSONRPC: "2.0", ID: 2, Method: "tools/list"}
	toolsResp, err := sc.post(toolsReq)
	if err != nil {
		return fmt.Errorf("sse list tools: %w", err)
	}

	var toolsResult struct {
		Tools []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			InputSchema map[string]interface{} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp.Result, &toolsResult); err != nil {
		return fmt.Errorf("sse parse tools: %w", err)
	}

	tools := make([]ToolInfo, len(toolsResult.Tools))
	for i, t := range toolsResult.Tools {
		tools[i] = ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
			ServerName:  cfg.Name,
		}
	}

	// Create a serverConn wrapper for SSE
	conn := &serverConn{
		config: cfg,
		tools:  tools,
		alive:  true,
	}

	// Store the SSE conn in the serverConn for callTool routing
	conn.sseConn = sc

	c.mu.Lock()
	c.servers[cfg.Name] = conn
	c.mu.Unlock()

	return nil
}

func (sc *sseConn) post(req jsonrpcRequest) (*jsonrpcResponse, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", sc.messagesURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := sc.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	var jsonResp jsonrpcResponse
	if err := json.Unmarshal(body, &jsonResp); err != nil {
		return nil, fmt.Errorf("parse response: %w (body: %s)", err, body[:min(len(body), 200)])
	}

	if jsonResp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", jsonResp.Error.Code, jsonResp.Error.Message)
	}

	return &jsonResp, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
