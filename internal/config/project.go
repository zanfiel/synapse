package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ProjectConfig is per-directory config loaded from .synapse.json
type ProjectConfig struct {
	Model        string            `json:"model,omitempty"`
	SystemPrompt string            `json:"system_prompt,omitempty"`
	Theme        string            `json:"theme,omitempty"`
	MaxTokens    int               `json:"max_tokens,omitempty"`
	Reasoning    string            `json:"reasoning,omitempty"`
	EngramURL    string            `json:"engram_url,omitempty"`
	EngramToken  string            `json:"engram_token,omitempty"`
	Tools        []CustomToolDef   `json:"tools,omitempty"`
	Keybindings  map[string]string `json:"keybindings,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	IgnorePaths  []string          `json:"ignore,omitempty"`
}

// CustomToolDef defines a tool loaded from project config.
type CustomToolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Command     string                 `json:"command"` // shell command template
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// LoadProjectConfig searches up from workDir for .synapse.json
func LoadProjectConfig(workDir string) *ProjectConfig {
	dir := workDir
	for {
		path := filepath.Join(dir, ".synapse.json")
		if data, err := os.ReadFile(path); err == nil {
			var pc ProjectConfig
			if json.Unmarshal(data, &pc) == nil {
				return &pc
			}
		}

		// Also check .synapse/config.json
		path = filepath.Join(dir, ".synapse", "config.json")
		if data, err := os.ReadFile(path); err == nil {
			var pc ProjectConfig
			if json.Unmarshal(data, &pc) == nil {
				return &pc
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

// MergeProjectConfig overlays project config onto global config.
func MergeProjectConfig(global *Config, project *ProjectConfig) *Config {
	if project == nil {
		return global
	}

	merged := *global
	if project.Model != "" {
		merged.Model = project.Model
	}
	if project.SystemPrompt != "" {
		merged.SystemPrompt = project.SystemPrompt
	}
	if project.Theme != "" {
		merged.Theme = project.Theme
	}
	if project.MaxTokens > 0 {
		merged.MaxTokens = project.MaxTokens
	}
	if project.Reasoning != "" {
		merged.ReasoningLevel = project.Reasoning
	}
	if project.EngramURL != "" {
		merged.EngramURL = project.EngramURL
	}
	if project.EngramToken != "" {
		merged.EngramToken = project.EngramToken
	}

	// Apply env vars
	for k, v := range project.Env {
		os.Setenv(k, v)
	}

	return &merged
}

// Keybindings with defaults
type Keybindings struct {
	Quit    string `json:"quit"`
	Search  string `json:"search"`
	Clear   string `json:"clear"`
	Compact string `json:"compact"`
	Help    string `json:"help"`
	ScrollUp   string `json:"scroll_up"`
	ScrollDown string `json:"scroll_down"`
}

func DefaultKeybindings() Keybindings {
	return Keybindings{
		Quit:       "ctrl+c",
		Search:     "ctrl+f",
		Clear:      "ctrl+l",
		Compact:    "ctrl+k",
		Help:       "ctrl+?",
		ScrollUp:   "pgup",
		ScrollDown: "pgdn",
	}
}
