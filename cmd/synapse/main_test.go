package main

import (
	"testing"

	"github.com/zanfiel/synapse/internal/config"
)

func TestApplyEnvOverrides(t *testing.T) {
	t.Setenv("SYNAPSE_PROVIDER", "openai")
	t.Setenv("SYNAPSE_MODEL", "gpt-5")

	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4.6"}
	applyEnvOverrides(cfg)

	if cfg.Provider != "openai" {
		t.Fatalf("provider = %q, want %q", cfg.Provider, "openai")
	}
	if cfg.Model != "gpt-5" {
		t.Fatalf("model = %q, want %q", cfg.Model, "gpt-5")
	}
}

func TestApplyEnvOverridesLeavesUnsetValuesUntouched(t *testing.T) {
	t.Setenv("SYNAPSE_PROVIDER", "")
	t.Setenv("SYNAPSE_MODEL", "")

	cfg := &config.Config{Provider: "anthropic", Model: "claude-opus-4.6"}
	applyEnvOverrides(cfg)

	if cfg.Provider != "anthropic" || cfg.Model != "claude-opus-4.6" {
		t.Fatalf("cfg changed unexpectedly: %+v", cfg)
	}
}
