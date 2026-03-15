package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/zanfiel/synapse/internal/responsesapi"
	"github.com/zanfiel/synapse/internal/sse"
	"github.com/zanfiel/synapse/internal/types"
)

// OpenAIProvider calls the OpenAI /chat/completions endpoint directly.
// Works with any OpenAI API key (Plus, Pro, Teams, etc).

type OpenAIProvider struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

type OpenAIOption func(*OpenAIProvider)

func WithOpenAIBaseURL(url string) OpenAIOption {
	return func(p *OpenAIProvider) { p.baseURL = url }
}

func NewOpenAIProvider(apiKey string, opts ...OpenAIOption) *OpenAIProvider {
	p := &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: "https://api.openai.com/v1",
		http:    &http.Client{Timeout: 300 * time.Second},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *OpenAIProvider) Name() string { return "openai" }

func (p *OpenAIProvider) ChatStream(req *Request, out chan<- types.StreamEvent) error {
	if responsesapi.UseResponsesAPI(req.Model) {
		return p.chatStreamResponses(req, out)
	}

	type openaiMsg struct {
		Role       string      `json:"role"`
		Content    interface{} `json:"content"`
		Name       string      `json:"name,omitempty"`
		ToolCalls  interface{} `json:"tool_calls,omitempty"`
		ToolCallID string      `json:"tool_call_id,omitempty"`
	}

	msgs := make([]openaiMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		om := openaiMsg{Role: m.Role, Content: m.Content}
		if m.Name != "" {
			om.Name = m.Name
		}
		if m.ToolCallID != "" {
			om.ToolCallID = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			om.ToolCalls = m.ToolCalls
		}
		msgs = append(msgs, om)
	}

	body := map[string]interface{}{
		"model":    req.Model,
		"messages": msgs,
		"stream":   true,
		"stream_options": map[string]bool{
			"include_usage": true,
		},
	}

	if req.MaxTokens > 0 {
		body["max_completion_tokens"] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.ReasoningEffort != "" && req.ReasoningEffort != "off" {
		effort := req.ReasoningEffort
		if effort == "xhigh" {
			effort = "high"
		}
		body["reasoning_effort"] = effort
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(requestContext(req), "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai %d: %s", resp.StatusCode, respBody)
	}

	return sse.ParseData(resp.Body, func(data string) error {
		if data == "" {
			return nil
		}
		if data == "[DONE]" {
			out <- types.StreamEvent{Done: true}
			return sse.ErrStop
		}

		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content   *string `json:"content"`
					Reasoning *string `json:"reasoning"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *types.Usage `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("parse openai SSE chunk: %w", err)
		}

		if chunk.Model != "" {
			out <- types.StreamEvent{Model: chunk.Model}
		}

		if chunk.Usage != nil {
			out <- types.StreamEvent{Usage: chunk.Usage}
		}

		if len(chunk.Choices) == 0 {
			return nil
		}

		choice := chunk.Choices[0]

		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			out <- types.StreamEvent{TextDelta: *choice.Delta.Content}
		}

		if choice.Delta.Reasoning != nil && *choice.Delta.Reasoning != "" {
			out <- types.StreamEvent{ThinkingDelta: *choice.Delta.Reasoning}
		}

		for _, tc := range choice.Delta.ToolCalls {
			out <- types.StreamEvent{
				ToolCallDelta: &types.ToolCallDelta{
					Index:     tc.Index,
					ID:        tc.ID,
					Name:      tc.Function.Name,
					ArgsDelta: tc.Function.Arguments,
				},
			}
		}

		if choice.FinishReason != nil {
			finish := *choice.FinishReason
			if finish == "tool_calls" || finish == "function_call" {
				finish = "tool_calls"
			}
			out <- types.StreamEvent{FinishReason: finish}
		}

		return nil
	})
}

func (p *OpenAIProvider) chatStreamResponses(req *Request, out chan<- types.StreamEvent) error {
	body, err := responsesapi.BuildRequestBody(req.Model, req.Messages, req.Tools, req.MaxTokens, req.ReasoningEffort, false)
	if err != nil {
		return fmt.Errorf("build responses request: %w", err)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal responses request: %w", err)
	}

	url := p.baseURL + "/responses"
	httpReq, err := http.NewRequestWithContext(requestContext(req), "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("openai responses request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai responses %d: %s", resp.StatusCode, respBody)
	}

	return responsesapi.ParseStream(resp.Body, out)
}

func requestContext(req *Request) context.Context {
	if req != nil && req.Context != nil {
		return req.Context
	}
	return context.Background()
}
