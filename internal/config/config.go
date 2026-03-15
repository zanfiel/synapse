package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	// Engram
	EngramURL   string `json:"engram_url"`
	EngramToken string `json:"engram_token"`

	// Agent
	Model          string `json:"model"`
	MaxTokens      int    `json:"max_tokens"`
	SystemPrompt   string `json:"system_prompt"`
	ReasoningLevel string `json:"reasoning_level"`
	WorkDir        string `json:"work_dir"`

	// TUI
	Theme          string `json:"theme"`
	// MCP Servers
	MCPServers []MCPServerConfig `json:"mcp_servers,omitempty"`

	// Fleet Pulse
	FleetEnabled  bool           `json:"fleet_enabled,omitempty"`
	FleetInterval int            `json:"fleet_interval,omitempty"` // seconds
	FleetServers  []FleetServer  `json:"fleet_servers,omitempty"`

	// Auto-capture
	AutoCapture bool `json:"auto_capture,omitempty"`

	// Provider settings
	Provider         string `json:"provider"`
	AnthropicAPIKey  string `json:"anthropic_api_key"`
	PlanningModel    string `json:"planning_model"`
	PlanningProvider string `json:"planning_provider"`
	DiffReview       bool   `json:"diff_review"`

	// Auto model selection — routes to haiku/sonnet/opus by complexity
	AutoModel   bool   `json:"auto_model,omitempty"`
	HaikuModel  string `json:"haiku_model,omitempty"`
	SonnetModel string `json:"sonnet_model,omitempty"`
	OpusModel   string `json:"opus_model,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		Model:          "claude-sonnet-4-20250514",
		MaxTokens:      16384,
		ReasoningLevel: "medium",
		WorkDir:        ".",
	}
}

func ConfigDir() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".synapse")
	}
	return filepath.Join(home, ".config", "synapse")
}

func ConfigPath() string {
	return filepath.Join(ConfigDir(), "config.json")
}

func AuthPath() string {
	return filepath.Join(ConfigDir(), "auth.json")
}

func Load() (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

func (c *Config) Save() error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(ConfigPath(), data, 0600)
}

// Auth is separate from config for security (more restrictive)
type Auth struct {
	Type    string `json:"type"`
	Refresh string `json:"refresh"`
	Access  string `json:"access"`
	Expires int64  `json:"expires"`
}


func SaveAuth(auth *Auth) error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(AuthPath(), data, 0600)
}
