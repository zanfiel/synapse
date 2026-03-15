package provider

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/zanfiel/synapse/internal/types"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestOpenAIChatStreamParsesMultilineSSE(t *testing.T) {
	body := strings.Join([]string{
		`data: {"model":"gpt-4o","choices":[{"delta":{`,
		`data: "content":"hello"},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	p := NewOpenAIProvider("test")
	p.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	out := make(chan types.StreamEvent, 8)
	err := p.ChatStream(&Request{Model: "gpt-4o", Messages: []types.Message{{Role: "user", Content: "hi"}}}, out)
	close(out)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}

	var text string
	for evt := range out {
		text += evt.TextDelta
	}
	if text != "hello" {
		t.Fatalf("text = %q, want %q", text, "hello")
	}
}

func TestOpenAIChatStreamReturnsMalformedSSEError(t *testing.T) {
	p := NewOpenAIProvider("test")
	p.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("data: {bad json}\n\n")),
		}, nil
	})}

	err := p.ChatStream(&Request{Model: "gpt-4o", Messages: []types.Message{{Role: "user", Content: "hi"}}}, make(chan types.StreamEvent, 1))
	if err == nil || !strings.Contains(err.Error(), "parse openai SSE chunk") {
		t.Fatalf("error = %v, want parse error", err)
	}
}
