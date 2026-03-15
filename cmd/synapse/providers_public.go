//go:build !personal

package main

import (
	"fmt"
	"os"

	"github.com/zanfiel/synapse/internal/config"
	"github.com/zanfiel/synapse/internal/provider"
)

func selectProvider(cfg *config.Config, anthropicKey string) (provider.Provider, string) {
	if cfg.Provider == "anthropic" || (cfg.Provider == "" && anthropicKey != "") {
		if anthropicKey != "" {
			return provider.NewAnthropicProvider(anthropicKey), "Anthropic"
		}
	}
	if cfg.Provider == "openai" {
		openaiKey := os.Getenv("OPENAI_API_KEY")
		if openaiKey == "" {
			fmt.Fprintf(os.Stderr, "✗ openai provider requires OPENAI_API_KEY env var\n")
			os.Exit(1)
		}
		var opts []provider.OpenAIOption
		if baseURL := os.Getenv("OPENAI_BASE_URL"); baseURL != "" {
			opts = append(opts, provider.WithOpenAIBaseURL(baseURL))
		}
		return provider.NewOpenAIProvider(openaiKey, opts...), "OpenAI"
	}
	return nil, ""
}

func handleExtraCommand(_ string) bool { return false }

func providerFatalMsg() string {
	return "no provider available: set ANTHROPIC_API_KEY or OPENAI_API_KEY"
}

func setupUsageTracking(_ provider.Provider, _ func(func() string)) {}

func getExtraHelpText() string { return "" }
