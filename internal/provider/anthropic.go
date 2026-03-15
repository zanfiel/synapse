package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zanfiel/synapse/internal/sse"
	"github.com/zanfiel/synapse/internal/types"
)

const anthropicURL = "https://api.anthropic.com/v1/messages"

type AnthropicProvider struct {
	apiKey string
	http   *http.Client
}

func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey: apiKey,
		http:   &http.Client{Timeout: 300 * time.Second},
	}
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	ID           string        `json:"id,omitempty"`
	Name         string        `json:"name,omitempty"`
	Input        interface{}   `json:"input,omitempty"`
	Source       interface{}   `json:"source,omitempty"`
	ToolUseID    string        `json:"tool_use_id,omitempty"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
	Content      interface{}   `json:"content,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type anthropicSystem struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	CacheControl *cacheControl          `json:"cache_control,omitempty"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    []anthropicSystem  `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Thinking  *anthropicThinking `json:"thinking,omitempty"`
	Stream    bool               `json:"stream"`
}

func (p *AnthropicProvider) ChatStream(req *Request, out chan<- types.StreamEvent) error {
	var sysMsgs []anthropicSystem
	var msgs []anthropicMessage

	for i, m := range req.Messages {
		if m.Role == "system" {
			text := ""
			if v, ok := m.Content.(string); ok {
				text = v
			}
			sys := anthropicSystem{Type: "text", Text: text}
			if req.Cache && i == 0 {
				sys.CacheControl = &cacheControl{Type: "ephemeral"}
			}
			sysMsgs = append(sysMsgs, sys)
			continue
		}

		am := anthropicMessage{Role: m.Role}

		switch v := m.Content.(type) {
		case string:
			if m.Role == "tool" {
				am.Role = "user"
				am.Content = []anthropicBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   []map[string]string{{"type": "text", "text": v}},
				}}
			} else {
				am.Content = v
			}
		case []interface{}:
			var blocks []anthropicBlock
			for _, part := range v {
				switch cp := part.(type) {
				case types.ContentPart:
					if cp.Type == "text" {
						blocks = append(blocks, anthropicBlock{Type: "text", Text: cp.Text})
					} else if cp.Type == "image_url" && cp.ImageURL != nil && cp.ImageURL.URL != "" {
						if src := buildAnthropicImageSource(cp.ImageURL.URL); src != nil {
							blocks = append(blocks, anthropicBlock{Type: "image", Source: src})
						}
					}
				case map[string]interface{}:
					if t, _ := cp["type"].(string); t == "text" {
						text, _ := cp["text"].(string)
						blocks = append(blocks, anthropicBlock{Type: "text", Text: text})
					} else if t == "image_url" {
						if imageURL, ok := cp["image_url"].(map[string]interface{}); ok {
							if url, _ := imageURL["url"].(string); url != "" {
								if src := buildAnthropicImageSource(url); src != nil {
									blocks = append(blocks, anthropicBlock{Type: "image", Source: src})
								}
							}
						}
					}
				}
			}
			if len(blocks) > 0 {
				am.Content = blocks
			}
		case []types.ContentPart:
			blocks := make([]anthropicBlock, 0, len(v))
			for _, cp := range v {
				if cp.Type == "text" {
					blocks = append(blocks, anthropicBlock{Type: "text", Text: cp.Text})
				} else if cp.Type == "image_url" && cp.ImageURL != nil && cp.ImageURL.URL != "" {
					if src := buildAnthropicImageSource(cp.ImageURL.URL); src != nil {
						blocks = append(blocks, anthropicBlock{Type: "image", Source: src})
					}
				}
			}
			if len(blocks) > 0 {
				am.Content = blocks
			}
		}

		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var blocks []anthropicBlock
			if textContent, ok := m.Content.(string); ok && textContent != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: textContent})
			}
			for _, tc := range m.ToolCalls {
				var input interface{}
				if tc.Function.Arguments != "" {
					json.Unmarshal([]byte(tc.Function.Arguments), &input)
				}
				if input == nil {
					input = map[string]interface{}{}
				}
				blocks = append(blocks, anthropicBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			am.Content = blocks
		}

		msgs = append(msgs, am)
	}

	var tools []anthropicTool
	for i, t := range req.Tools {
		at := anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
		}
		if t.Function.Parameters != nil {
			if params, ok := t.Function.Parameters.(map[string]interface{}); ok {
				at.InputSchema = params
			} else {
				at.InputSchema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}
		} else {
			at.InputSchema = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		if req.Cache && i == len(req.Tools)-1 {
			at.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		tools = append(tools, at)
	}

	maxTok := req.MaxTokens
	if maxTok == 0 {
		maxTok = 16384
	}

	apiReq := &anthropicRequest{
		Model:     req.Model,
		MaxTokens: maxTok,
		System:    sysMsgs,
		Messages:  msgs,
		Tools:     tools,
		Stream:    true,
	}

	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		apiReq.Thinking = &anthropicThinking{
			Type:         "enabled",
			BudgetTokens: req.Thinking.BudgetTokens,
		}
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(requestContext(req), "POST", anthropicURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14,prompt-caching-2024-07-31")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anthropic API %d: %s", resp.StatusCode, string(body))
	}

	return p.parseSSE(resp.Body, out)
}

type anthropicSSEEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index"`
	ContentBlock *anthropicBlock        `json:"content_block"`
	Delta        *anthropicDelta        `json:"delta"`
	Usage        *anthropicUsage        `json:"usage"`
	Message      *anthropicMessageStart `json:"message"`
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type anthropicMessageStart struct {
	Usage anthropicUsage `json:"usage"`
	Model string         `json:"model"`
}

func (p *AnthropicProvider) parseSSE(body io.Reader, out chan<- types.StreamEvent) error {
	type toolState struct {
		id   string
		name string
		args strings.Builder
	}
	toolsMap := make(map[int]*toolState)

	var finalUsage *anthropicUsage

	return sse.ParseData(body, func(data string) error {
		if data == "" || data == "[DONE]" {
			return nil
		}

		var evt anthropicSSEEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			return fmt.Errorf("parse anthropic SSE event: %w", err)
		}

		switch evt.Type {
		case "message_start":
			if evt.Message != nil {
				if evt.Message.Model != "" {
					out <- types.StreamEvent{Model: evt.Message.Model}
				}
				if evt.Message.Usage.InputTokens > 0 {
					u := evt.Message.Usage
					finalUsage = &u
				}
			}

		case "content_block_start":
			if evt.ContentBlock != nil && evt.ContentBlock.Type == "tool_use" {
				toolsMap[evt.Index] = &toolState{
					id:   evt.ContentBlock.ID,
					name: evt.ContentBlock.Name,
				}
			}

		case "content_block_delta":
			if evt.Delta == nil {
				return nil
			}
			switch evt.Delta.Type {
			case "text_delta":
				out <- types.StreamEvent{TextDelta: evt.Delta.Text}
			case "thinking_delta":
				out <- types.StreamEvent{ThinkingDelta: evt.Delta.Thinking}
			case "input_json_delta":
				if ts, ok := toolsMap[evt.Index]; ok {
					ts.args.WriteString(evt.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			if ts, ok := toolsMap[evt.Index]; ok {
				out <- types.StreamEvent{
					ToolCallDelta: &types.ToolCallDelta{
						Index:     evt.Index,
						ID:        ts.id,
						Name:      ts.name,
						ArgsDelta: ts.args.String(),
					},
					FinishReason: "tool_calls",
				}
				delete(toolsMap, evt.Index)
			}

		case "message_delta":
			if evt.Delta != nil && evt.Delta.StopReason != "" {
				if evt.Delta.StopReason != "tool_use" {
					out <- types.StreamEvent{FinishReason: evt.Delta.StopReason}
				}
			}
			if evt.Usage != nil {
				if finalUsage == nil {
					u := *evt.Usage
					finalUsage = &u
				} else {
					finalUsage.OutputTokens = evt.Usage.OutputTokens
				}
			}

		case "message_stop":
			if finalUsage != nil {
				total := finalUsage.InputTokens + finalUsage.OutputTokens
				out <- types.StreamEvent{
					Usage: &types.Usage{
						PromptTokens:     finalUsage.InputTokens,
						CompletionTokens: finalUsage.OutputTokens,
						TotalTokens:      total,
					},
				}
			}
			out <- types.StreamEvent{Done: true}
		}

		return nil
	})
}

func inferImageMediaType(url string) string {
	lower := strings.ToLower(url)
	switch {
	case strings.HasPrefix(lower, "data:image/"):
		meta := lower[len("data:"):]
		if idx := strings.Index(meta, ";"); idx >= 0 {
			return meta[:idx]
		}
		if idx := strings.Index(meta, ","); idx >= 0 {
			return meta[:idx]
		}
	case strings.Contains(lower, ".png"):
		return "image/png"
	case strings.Contains(lower, ".webp"):
		return "image/webp"
	case strings.Contains(lower, ".gif"):
		return "image/gif"
	default:
		return "image/jpeg"
	}
	return "image/jpeg"
}

func buildAnthropicImageSource(url string) map[string]interface{} {
	if strings.HasPrefix(url, "data:") {
		parts := strings.SplitN(url, ",", 2)
		if len(parts) == 2 {
			meta := strings.TrimPrefix(parts[0], "data:")
			mediaType := strings.TrimSuffix(meta, ";base64")
			return map[string]interface{}{
				"type":       "base64",
				"media_type": mediaType,
				"data":       parts[1],
			}
		}
	}

	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(url)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				body, err := io.ReadAll(resp.Body)
				if err == nil {
					mediaType := resp.Header.Get("Content-Type")
					if mediaType == "" {
						mediaType = inferImageMediaType(url)
					} else if idx := strings.Index(mediaType, ";"); idx >= 0 {
						mediaType = mediaType[:idx]
					}
					return map[string]interface{}{
						"type":       "base64",
						"media_type": mediaType,
						"data":       base64.StdEncoding.EncodeToString(body),
					}
				}
			}
		}
	}

	// Fallback that will probably fail API validation but keeps types aligned
	return map[string]interface{}{
		"type":       "base64",
		"media_type": inferImageMediaType(url),
		"data":       "",
	}
}
