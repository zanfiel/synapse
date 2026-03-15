package types

// Shared types used across Synapse packages.

// Tool represents a tool definition for the API.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a tool's name, description, and parameters.
type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// Message is a chat message (user, assistant, system, or tool).
type Message struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	Name       string      `json:"name,omitempty"`
}

// ContentPart is a multipart content element (text or image).
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL wraps an image URL for multipart content.
type ImageURL struct {
	URL string `json:"url"`
}

// ToolCall represents an assistant's request to invoke a tool.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments JSON.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Usage tracks token consumption for a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamEvent is emitted by providers during streaming.
type StreamEvent struct {
	TextDelta     string
	ThinkingDelta string
	ToolCallDelta *ToolCallDelta
	Usage         *Usage
	FinishReason  string
	Done          bool
	Error         error
	Model         string
}

// ToolCallDelta is a partial tool call update during streaming.
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	ArgsDelta string
}
