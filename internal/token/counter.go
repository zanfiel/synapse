package token

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	"github.com/zanfiel/synapse/internal/types"
)

var (
	encoder  *tiktoken.Tiktoken
	initOnce sync.Once
)

func getEncoder() *tiktoken.Tiktoken {
	initOnce.Do(func() {
		enc, err := tiktoken.EncodingForModel("gpt-4o")
		if err != nil {
			// Fallback to cl100k_base
			enc, _ = tiktoken.GetEncoding("cl100k_base")
		}
		encoder = enc
	})
	return encoder
}

// Count returns approximate token count for a string.
func Count(text string) int {
	enc := getEncoder()
	if enc == nil {
		// Rough estimate: 1 token per 4 chars
		return len(text) / 4
	}
	return len(enc.Encode(text, nil, nil))
}

// CountMessages returns token count for a message array.
func CountMessages(messages []types.Message) int {
	total := 0
	for _, msg := range messages {
		total += 4 // message overhead (role, separators)

		switch c := msg.Content.(type) {
		case string:
			total += Count(c)
		case []interface{}:
			for _, part := range c {
				if m, ok := part.(map[string]interface{}); ok {
					if text, ok := m["text"].(string); ok {
						total += Count(text)
					}
					if _, ok := m["image_url"]; ok {
						total += 1000 // rough image token estimate
					}
				}
			}
		default:
			if c != nil {
				data, _ := json.Marshal(c)
				total += Count(string(data))
			}
		}

		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				total += Count(tc.Function.Name)
				total += Count(tc.Function.Arguments)
				total += 10 // tool call overhead
			}
		}
	}
	return total
}

// ModelContextWindow returns the max context window for known models.
func ModelContextWindow(model string) int {
	model = strings.ToLower(model)

	switch {
	case strings.Contains(model, "claude-sonnet-4"),
		strings.Contains(model, "claude-opus-4"),
		model == "sonnet", model == "opus":
		return 200000
	case strings.Contains(model, "claude-haiku"),
		model == "haiku":
		return 200000
	case strings.Contains(model, "claude-3.5"),
		strings.Contains(model, "claude-3-5"):
		return 200000
	case strings.Contains(model, "gpt-4o"):
		return 128000
	case strings.Contains(model, "gpt-4-turbo"):
		return 128000
	case strings.Contains(model, "o1"), strings.Contains(model, "o3"),
		strings.Contains(model, "o4"):
		return 200000
	case strings.Contains(model, "gemini"):
		return 1000000
	default:
		return 128000 // safe default
	}
}
