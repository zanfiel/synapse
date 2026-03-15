package provider

import (
	"context"

	"github.com/zanfiel/synapse/internal/types"
)

// Thinking configures extended thinking budget.
type Thinking struct {
	BudgetTokens int
}

// Request is the unified provider request type.
type Request struct {
	Context         context.Context
	Model           string
	Messages        []types.Message
	Tools           []types.Tool
	MaxTokens       int
	SystemPrompt    string
	Thinking        *Thinking // nil = disabled
	ReasoningEffort string    // "low"/"medium"/"high"/"xhigh"
	HasImages       bool
	Cache           bool // enable prompt caching (Anthropic only)
}

// Provider is the interface all AI providers implement.
type Provider interface {
	ChatStream(req *Request, out chan<- types.StreamEvent) error
	Name() string
}
