package responsesapi

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zanfiel/synapse/internal/sse"
	"github.com/zanfiel/synapse/internal/types"
)

// UseResponsesAPI returns true for models that require the OpenAI Responses API.
func UseResponsesAPI(model string) bool {
	lower := strings.ToLower(model)
	return strings.HasPrefix(lower, "gpt-5") ||
		strings.HasPrefix(lower, "o1") ||
		strings.HasPrefix(lower, "o3") ||
		strings.HasPrefix(lower, "o4")
}

// BuildRequestBody converts Synapse request data into an OpenAI Responses API request body.
func BuildRequestBody(model string, messages []types.Message, tools []types.Tool, maxOutputTokens int, reasoningEffort string, store bool) (map[string]interface{}, error) {
	input, err := buildInput(messages)
	if err != nil {
		return nil, err
	}

	body := map[string]interface{}{
		"model":  model,
		"input":  input,
		"store":  store,
		"stream": true,
	}
	if maxOutputTokens > 0 {
		body["max_output_tokens"] = maxOutputTokens
	}
	if len(tools) > 0 {
		body["tools"] = buildTools(tools)
		body["parallel_tool_calls"] = true
	}
	if effort := normalizeReasoningEffort(reasoningEffort); effort != "" {
		body["reasoning"] = map[string]interface{}{"effort": effort}
	}
	return body, nil
}

func buildInput(messages []types.Message) ([]interface{}, error) {
	input := make([]interface{}, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			text := stringifyContent(msg.Content)
			if text == "" {
				continue
			}
			input = append(input, map[string]interface{}{
				"role":    "developer",
				"content": text,
			})

		case "user":
			content := buildUserContent(msg.Content)
			if len(content) == 0 {
				text := stringifyContent(msg.Content)
				if text == "" {
					continue
				}
				content = []interface{}{map[string]interface{}{"type": "input_text", "text": text}}
			}
			input = append(input, map[string]interface{}{
				"role":    "user",
				"content": content,
			})

		case "assistant":
			if text := stringifyContent(msg.Content); text != "" {
				input = append(input, map[string]interface{}{
					"role": "assistant",
					"content": []interface{}{
						map[string]interface{}{"type": "output_text", "text": text},
					},
				})
			}
			for _, tc := range msg.ToolCalls {
				input = append(input, map[string]interface{}{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}

		case "tool":
			if msg.ToolCallID == "" {
				continue
			}
			input = append(input, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  stringifyContent(msg.Content),
			})
		}
	}
	return input, nil
}

func buildUserContent(content interface{}) []interface{} {
	switch v := content.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []interface{}{map[string]interface{}{"type": "input_text", "text": v}}
	case []interface{}:
		parts := make([]interface{}, 0, len(v))
		for _, part := range v {
			if mapped := mapContentPart(part); mapped != nil {
				parts = append(parts, mapped)
			}
		}
		return parts
	case []types.ContentPart:
		parts := make([]interface{}, 0, len(v))
		for _, part := range v {
			if mapped := mapTypedContentPart(part); mapped != nil {
				parts = append(parts, mapped)
			}
		}
		return parts
	default:
		return nil
	}
}

func mapContentPart(part interface{}) map[string]interface{} {
	switch p := part.(type) {
	case types.ContentPart:
		return mapTypedContentPart(p)
	case map[string]interface{}:
		cp := types.ContentPart{}
		if t, _ := p["type"].(string); t != "" {
			cp.Type = t
		}
		if text, _ := p["text"].(string); text != "" {
			cp.Text = text
		}
		if imageURL, ok := p["image_url"].(map[string]interface{}); ok {
			if url, _ := imageURL["url"].(string); url != "" {
				cp.ImageURL = &types.ImageURL{URL: url}
			}
		}
		return mapTypedContentPart(cp)
	default:
		return nil
	}
}

func mapTypedContentPart(part types.ContentPart) map[string]interface{} {
	switch part.Type {
	case "text":
		if part.Text == "" {
			return nil
		}
		return map[string]interface{}{"type": "input_text", "text": part.Text}
	case "image_url":
		if part.ImageURL == nil || part.ImageURL.URL == "" {
			return nil
		}
		return map[string]interface{}{"type": "input_image", "image_url": part.ImageURL.URL}
	default:
		return nil
	}
}

func buildTools(tools []types.Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}
		out = append(out, map[string]interface{}{
			"type":        "function",
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		})
	}
	return out
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(effort) {
	case "low", "medium", "high":
		return strings.ToLower(effort)
	case "xhigh":
		return "high"
	default:
		return ""
	}
}

func stringifyContent(content interface{}) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

// ParseStream parses an OpenAI Responses API SSE stream and emits Synapse stream events.
func ParseStream(body io.Reader, out chan<- types.StreamEvent) error {
	type pendingTool struct {
		id   string
		name string
		args strings.Builder
	}
	pending := make(map[int]*pendingTool)
	sawFunctionCall := false

	return sse.ParseData(body, func(data string) error {
		if data == "" || data == "[DONE]" {
			return nil
		}

		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(data), &env); err != nil {
			return fmt.Errorf("parse responses SSE envelope: %w", err)
		}

		switch env.Type {
		case "response.created":
			var evt struct {
				Response struct {
					Model string `json:"model"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return fmt.Errorf("parse response.created: %w", err)
			}
			if evt.Response.Model != "" {
				out <- types.StreamEvent{Model: evt.Response.Model}
			}

		case "response.output_text.delta":
			var evt struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return fmt.Errorf("parse response.output_text.delta: %w", err)
			}
			if evt.Delta != "" {
				out <- types.StreamEvent{TextDelta: evt.Delta}
			}

		case "response.reasoning.delta", "response.reasoning_summary_text.delta":
			var evt struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return fmt.Errorf("parse reasoning delta: %w", err)
			}
			if evt.Delta != "" {
				out <- types.StreamEvent{ThinkingDelta: evt.Delta}
			}

		case "response.output_item.added":
			var evt struct {
				OutputIndex int `json:"output_index"`
				Item        struct {
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"item"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return fmt.Errorf("parse response.output_item.added: %w", err)
			}
			if evt.Item.Type == "function_call" {
				state := pending[evt.OutputIndex]
				if state == nil {
					state = &pendingTool{}
					pending[evt.OutputIndex] = state
				}
				state.id = evt.Item.CallID
				state.name = evt.Item.Name
				if evt.Item.Arguments != "" && state.args.Len() == 0 {
					state.args.WriteString(evt.Item.Arguments)
					out <- types.StreamEvent{ToolCallDelta: &types.ToolCallDelta{
						Index:     evt.OutputIndex,
						ID:        state.id,
						Name:      state.name,
						ArgsDelta: evt.Item.Arguments,
					}}
				}
			}

		case "response.function_call_arguments.delta":
			var evt struct {
				OutputIndex int    `json:"output_index"`
				Delta       string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return fmt.Errorf("parse response.function_call_arguments.delta: %w", err)
			}
			state := pending[evt.OutputIndex]
			if state == nil {
				state = &pendingTool{}
				pending[evt.OutputIndex] = state
			}
			state.args.WriteString(evt.Delta)
			out <- types.StreamEvent{ToolCallDelta: &types.ToolCallDelta{
				Index:     evt.OutputIndex,
				ID:        state.id,
				Name:      state.name,
				ArgsDelta: evt.Delta,
			}}

		case "response.output_item.done":
			var evt struct {
				OutputIndex int `json:"output_index"`
				Item        struct {
					Type      string `json:"type"`
					CallID    string `json:"call_id"`
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"item"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return fmt.Errorf("parse response.output_item.done: %w", err)
			}
			if evt.Item.Type == "function_call" {
				state := pending[evt.OutputIndex]
				if state == nil {
					state = &pendingTool{id: evt.Item.CallID, name: evt.Item.Name}
					pending[evt.OutputIndex] = state
				}
				if state.id == "" {
					state.id = evt.Item.CallID
				}
				if state.name == "" {
					state.name = evt.Item.Name
				}
				current := state.args.String()
				if evt.Item.Arguments != "" && evt.Item.Arguments != current {
					delta := evt.Item.Arguments
					if strings.HasPrefix(evt.Item.Arguments, current) {
						delta = evt.Item.Arguments[len(current):]
					}
					state.args.Reset()
					state.args.WriteString(evt.Item.Arguments)
					out <- types.StreamEvent{ToolCallDelta: &types.ToolCallDelta{
						Index:     evt.OutputIndex,
						ID:        state.id,
						Name:      state.name,
						ArgsDelta: delta,
					}}
				}
				sawFunctionCall = true
			}

		case "response.completed", "response.incomplete":
			var evt struct {
				Response struct {
					Model             string `json:"model"`
					IncompleteDetails *struct {
						Reason string `json:"reason"`
					} `json:"incomplete_details"`
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return fmt.Errorf("parse %s: %w", env.Type, err)
			}
			if evt.Response.Model != "" {
				out <- types.StreamEvent{Model: evt.Response.Model}
			}
			out <- types.StreamEvent{Usage: &types.Usage{
				PromptTokens:     evt.Response.Usage.InputTokens,
				CompletionTokens: evt.Response.Usage.OutputTokens,
				TotalTokens:      evt.Response.Usage.InputTokens + evt.Response.Usage.OutputTokens,
			}}
			finish := "stop"
			if sawFunctionCall {
				finish = "tool_calls"
			} else if env.Type == "response.incomplete" && evt.Response.IncompleteDetails != nil && evt.Response.IncompleteDetails.Reason != "" {
				finish = evt.Response.IncompleteDetails.Reason
			}
			out <- types.StreamEvent{FinishReason: finish}
		}

		return nil
	})
}
