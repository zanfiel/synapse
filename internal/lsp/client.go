package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Client manages a Language Server Protocol connection.
// Connects to any LSP server (gopls, typescript-language-server, pyright, etc.)
// and provides diagnostics, go-to-definition, hover, and symbol information
// to the agent — making it aware of real compiler errors and project structure.
type Client struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	mu      sync.Mutex
	nextID  atomic.Int64
	pending map[int64]chan *Response
	pendMu  sync.Mutex

	rootURI string
	lang    string
	ready   bool

	// Diagnostics from the server
	diagMu      sync.Mutex
	diagnostics map[string][]Diagnostic // URI -> diagnostics

	onDiagnostics func(uri string, diags []Diagnostic)
}

// Diagnostic represents a compiler error/warning from the LSP server.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"` // 1=Error, 2=Warning, 3=Info, 4=Hint
	Message  string `json:"message"`
	Source   string `json:"source"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Location represents a source code location.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// SymbolInfo holds hover/symbol information.
type SymbolInfo struct {
	Name   string
	Kind   string
	Detail string
	Range  Range
}

// DocumentSymbol from LSP.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children"`
}

// JSON-RPC message types
type Request struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *int64           `json:"id,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *ResponseError   `json:"error,omitempty"`
}

type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Language server configurations
var serverConfigs = map[string][]string{
	"go":         {"gopls"},
	"typescript": {"typescript-language-server", "--stdio"},
	"javascript": {"typescript-language-server", "--stdio"},
	"python":     {"pyright-langserver", "--stdio"},
	"rust":       {"rust-analyzer"},
	"c":          {"clangd"},
	"cpp":        {"clangd"},
	"zig":        {"zls"},
	"lua":        {"lua-language-server"},
}

// DetectLanguage determines the primary language from project files.
func DetectLanguage(workDir string) string {
	indicators := map[string][]string{
		"go":         {"go.mod", "go.sum"},
		"typescript": {"tsconfig.json", "package.json"},
		"javascript": {"package.json"},
		"python":     {"pyproject.toml", "setup.py", "requirements.txt", "Pipfile"},
		"rust":       {"Cargo.toml"},
		"c":          {"CMakeLists.txt", "Makefile"},
		"zig":        {"build.zig"},
		"lua":        {".luarc.json"},
	}

	// Check for type-specific files
	for lang, files := range indicators {
		for _, f := range files {
			if _, err := os.Stat(filepath.Join(workDir, f)); err == nil {
				// For package.json, check if typescript
				if f == "package.json" && lang == "javascript" {
					if _, err := os.Stat(filepath.Join(workDir, "tsconfig.json")); err == nil {
						return "typescript"
					}
				}
				return lang
			}
		}
	}

	return ""
}

// NewClient starts an LSP server for the given language and workspace.
func NewClient(workDir string, lang string) (*Client, error) {
	if lang == "" {
		lang = DetectLanguage(workDir)
	}
	if lang == "" {
		return nil, fmt.Errorf("could not detect project language")
	}

	args, ok := serverConfigs[lang]
	if !ok {
		return nil, fmt.Errorf("no LSP server configured for %s", lang)
	}

	// Check if server binary exists
	if _, err := exec.LookPath(args[0]); err != nil {
		return nil, fmt.Errorf("LSP server %q not found in PATH: %w", args[0], err)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = workDir
	cmd.Stderr = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", args[0], err)
	}

	absDir, _ := filepath.Abs(workDir)

	c := &Client{
		cmd:         cmd,
		stdin:       stdin,
		stdout:      bufio.NewReaderSize(stdout, 1024*1024),
		pending:     make(map[int64]chan *Response),
		diagnostics: make(map[string][]Diagnostic),
		rootURI:     "file://" + absDir,
		lang:        lang,
	}

	// Start reading responses
	go c.readLoop()

	// Initialize
	if err := c.initialize(); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	return c, nil
}

func (c *Client) Language() string { return c.lang }
func (c *Client) Ready() bool     { return c.ready }

func (c *Client) initialize() error {
	params := map[string]interface{}{
		"processId": os.Getpid(),
		"rootUri":   c.rootURI,
		"capabilities": map[string]interface{}{
			"textDocument": map[string]interface{}{
				"publishDiagnostics": map[string]interface{}{
					"relatedInformation": true,
				},
				"hover": map[string]interface{}{
					"contentFormat": []string{"plaintext", "markdown"},
				},
				"definition":     map[string]interface{}{},
				"documentSymbol": map[string]interface{}{},
				"completion": map[string]interface{}{
					"completionItem": map[string]interface{}{
						"snippetSupport": false,
					},
				},
			},
			"workspace": map[string]interface{}{
				"workspaceFolders": true,
			},
		},
		"workspaceFolders": []map[string]interface{}{
			{"uri": c.rootURI, "name": filepath.Base(c.rootURI)},
		},
	}

	resp, err := c.call("initialize", params)
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("LSP error: %s", resp.Error.Message)
	}

	// Send initialized notification
	c.notify("initialized", map[string]interface{}{})
	c.ready = true

	return nil
}

// GetDiagnostics returns current diagnostics for a file.
func (c *Client) GetDiagnostics(filePath string) []Diagnostic {
	uri := pathToURI(filePath)
	c.diagMu.Lock()
	defer c.diagMu.Unlock()
	return c.diagnostics[uri]
}

// GetAllDiagnostics returns diagnostics across all files.
func (c *Client) GetAllDiagnostics() map[string][]Diagnostic {
	c.diagMu.Lock()
	defer c.diagMu.Unlock()
	result := make(map[string][]Diagnostic, len(c.diagnostics))
	for k, v := range c.diagnostics {
		result[k] = v
	}
	return result
}

// GetErrors returns only error-level diagnostics across all files.
func (c *Client) GetErrors() map[string][]Diagnostic {
	all := c.GetAllDiagnostics()
	result := make(map[string][]Diagnostic)
	for uri, diags := range all {
		var errors []Diagnostic
		for _, d := range diags {
			if d.Severity == 1 { // Error
				errors = append(errors, d)
			}
		}
		if len(errors) > 0 {
			result[uri] = errors
		}
	}
	return result
}

// OpenFile notifies the server about an opened file.
func (c *Client) OpenFile(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	langID := c.lang
	ext := filepath.Ext(filePath)
	switch ext {
	case ".ts", ".tsx":
		langID = "typescript"
	case ".js", ".jsx":
		langID = "javascript"
	case ".go":
		langID = "go"
	case ".py":
		langID = "python"
	case ".rs":
		langID = "rust"
	case ".c", ".h":
		langID = "c"
	case ".cpp", ".hpp", ".cc":
		langID = "cpp"
	}

	c.notify("textDocument/didOpen", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":        pathToURI(filePath),
			"languageId": langID,
			"version":    1,
			"text":       string(data),
		},
	})

	return nil
}

// DidChange notifies the server about file content changes.
func (c *Client) DidChange(filePath string, content string, version int) {
	c.notify("textDocument/didChange", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     pathToURI(filePath),
			"version": version,
		},
		"contentChanges": []map[string]interface{}{
			{"text": content},
		},
	})
}

// DidSave notifies the server a file was saved.
func (c *Client) DidSave(filePath string) {
	c.notify("textDocument/didSave", map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri": pathToURI(filePath),
		},
	})
}

// Definition gets the definition location for a symbol at position.
func (c *Client) Definition(filePath string, line, col int) ([]Location, error) {
	resp, err := c.call("textDocument/definition", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": pathToURI(filePath)},
		"position":     Position{Line: line, Character: col},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}

	// Can be a single Location or []Location
	var locs []Location
	if err := json.Unmarshal(resp.Result, &locs); err != nil {
		var loc Location
		if err2 := json.Unmarshal(resp.Result, &loc); err2 != nil {
			return nil, fmt.Errorf("parse definition response: %w", err)
		}
		locs = []Location{loc}
	}

	return locs, nil
}

// Hover gets hover information for a position.
func (c *Client) Hover(filePath string, line, col int) (string, error) {
	resp, err := c.call("textDocument/hover", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": pathToURI(filePath)},
		"position":     Position{Line: line, Character: col},
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%s", resp.Error.Message)
	}
	if resp.Result == nil || string(resp.Result) == "null" {
		return "", nil
	}

	var hover struct {
		Contents interface{} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &hover); err != nil {
		return "", err
	}

	return extractMarkupContent(hover.Contents), nil
}

// DocumentSymbols gets all symbols in a file.
func (c *Client) DocumentSymbols(filePath string) ([]DocumentSymbol, error) {
	resp, err := c.call("textDocument/documentSymbol", map[string]interface{}{
		"textDocument": map[string]interface{}{"uri": pathToURI(filePath)},
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}

	var symbols []DocumentSymbol
	if err := json.Unmarshal(resp.Result, &symbols); err != nil {
		return nil, err
	}

	return symbols, nil
}

// WorkspaceSymbols searches for symbols across the workspace.
func (c *Client) WorkspaceSymbols(query string) ([]SymbolInfo, error) {
	resp, err := c.call("workspace/symbol", map[string]interface{}{
		"query": query,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}

	var raw []struct {
		Name     string `json:"name"`
		Kind     int    `json:"kind"`
		Location Location `json:"location"`
	}
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		return nil, err
	}

	var symbols []SymbolInfo
	for _, r := range raw {
		symbols = append(symbols, SymbolInfo{
			Name:  r.Name,
			Kind:  SymbolKindName(r.Kind),
			Range: r.Location.Range,
		})
	}

	return symbols, nil
}

// OnDiagnostics sets a callback for diagnostic updates.
func (c *Client) OnDiagnostics(fn func(uri string, diags []Diagnostic)) {
	c.onDiagnostics = fn
}

// Close shuts down the LSP server.
func (c *Client) Close() error {
	if c.ready {
		c.call("shutdown", nil)
		c.notify("exit", nil)
	}
	c.stdin.Close()
	return c.cmd.Wait()
}

// --- JSON-RPC transport ---

func (c *Client) call(method string, params interface{}) (*Response, error) {
	id := c.nextID.Add(1)

	ch := make(chan *Response, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.send(req); err != nil {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*1000*1000*1000) // 30s
	defer cancel()

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, fmt.Errorf("LSP call %s timed out", method)
	}
}

func (c *Client) notify(method string, params interface{}) {
	notif := Notification{JSONRPC: "2.0", Method: method, Params: params}
	c.send(notif)
}

func (c *Client) send(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	_, err = c.stdin.Write(data)
	return err
}

func (c *Client) readLoop() {
	for {
		msg, err := c.readMessage()
		if err != nil {
			return // Server closed
		}

		var resp Response
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}

		// Check if it's a response (has ID)
		if resp.ID != nil {
			c.pendMu.Lock()
			ch, ok := c.pending[*resp.ID]
			if ok {
				delete(c.pending, *resp.ID)
			}
			c.pendMu.Unlock()
			if ok {
				ch <- &resp
			}
			continue
		}

		// It's a notification — check for diagnostics
		var notif struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(msg, &notif); err != nil {
			continue
		}

		if notif.Method == "textDocument/publishDiagnostics" {
			var params struct {
				URI         string       `json:"uri"`
				Diagnostics []Diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal(notif.Params, &params); err == nil {
				c.diagMu.Lock()
				c.diagnostics[params.URI] = params.Diagnostics
				c.diagMu.Unlock()

				if c.onDiagnostics != nil {
					c.onDiagnostics(params.URI, params.Diagnostics)
				}
			}
		}
	}
}

func (c *Client) readMessage() ([]byte, error) {
	// Read headers
	contentLength := 0
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break // End of headers
		}
		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			contentLength, _ = strconv.Atoi(val)
		}
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	body := make([]byte, contentLength)
	_, err := io.ReadFull(c.stdout, body)
	return body, err
}

// --- Helpers ---

func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return "file://" + abs
}

func uriToPath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

func extractMarkupContent(v interface{}) string {
	switch c := v.(type) {
	case string:
		return c
	case map[string]interface{}:
		if val, ok := c["value"].(string); ok {
			return val
		}
	case []interface{}:
		var parts []string
		for _, item := range c {
			parts = append(parts, extractMarkupContent(item))
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func SymbolKindName(kind int) string {
	names := map[int]string{
		1: "File", 2: "Module", 3: "Namespace", 4: "Package",
		5: "Class", 6: "Method", 7: "Property", 8: "Field",
		9: "Constructor", 10: "Enum", 11: "Interface", 12: "Function",
		13: "Variable", 14: "Constant", 15: "String", 16: "Number",
		17: "Boolean", 18: "Array", 19: "Object", 20: "Key",
		21: "Null", 22: "EnumMember", 23: "Struct", 24: "Event",
		25: "Operator", 26: "TypeParameter",
	}
	if n, ok := names[kind]; ok {
		return n
	}
	return fmt.Sprintf("Kind(%d)", kind)
}
