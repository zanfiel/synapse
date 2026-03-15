package engram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	source     string
	model      string
}

type Memory struct {
	ID        int    `json:"id"`
	Content   string `json:"content"`
	Category  string `json:"category"`
	Source    string `json:"source,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func NewClient(baseURL, token, source string) *Client {
	return &Client{
		baseURL:    baseURL,
		token:      token,
		source:     source,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetModel sets the model identifier for attribution in stored memories.
func (c *Client) SetModel(model string) {
	c.model = model
}

func (c *Client) do(method, path string, body interface{}) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("engram %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("engram %d: %s", resp.StatusCode, respBody)
	}

	return respBody, nil
}

// Store — POST /store (Engram v5)
func (c *Client) Store(content, category string) (*Memory, error) {
	payload := map[string]interface{}{
		"content":  content,
		"category": category,
		"source":   c.source,
	}
	if c.model != "" {
		payload["model"] = c.model
	}

	data, err := c.do("POST", "/store", payload)
	if err != nil {
		return nil, err
	}

	// v5 returns {memory: {...}, ...}
	var wrapper struct {
		Memory Memory `json:"memory"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Memory.ID > 0 {
		return &wrapper.Memory, nil
	}

	var mem Memory
	if err := json.Unmarshal(data, &mem); err != nil {
		return nil, err
	}
	return &mem, nil
}

// Search — POST /search (Engram v5)
func (c *Client) Search(query string, limit int) ([]Memory, error) {
	payload := map[string]interface{}{
		"query": query,
		"limit": limit,
	}

	data, err := c.do("POST", "/search", payload)
	if err != nil {
		return nil, err
	}

	var result struct {
		Results []struct {
			ID        int    `json:"id"`
			Content   string `json:"content"`
			Category  string `json:"category"`
			Source    string `json:"source"`
			CreatedAt string `json:"created_at"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &result); err == nil {
		mems := make([]Memory, len(result.Results))
		for i, r := range result.Results {
			mems[i] = Memory{ID: r.ID, Content: r.Content, Category: r.Category, Source: r.Source, CreatedAt: r.CreatedAt}
		}
		return mems, nil
	}

	var mems []Memory
	if err := json.Unmarshal(data, &mems); err != nil {
		return nil, fmt.Errorf("parse search response: %s", string(data[:min(len(data), 200)]))
	}
	return mems, nil
}

// List — GET /list (Engram v5)
func (c *Client) List(category string, limit int) ([]Memory, error) {
	path := fmt.Sprintf("/list?limit=%d", limit)
	if category != "" {
		path += "&category=" + url.QueryEscape(category)
	}

	data, err := c.do("GET", path, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Memories []Memory `json:"memories"`
		Results  []Memory `json:"results"`
	}
	if err := json.Unmarshal(data, &result); err == nil {
		if len(result.Memories) > 0 {
			return result.Memories, nil
		}
		return result.Results, nil
	}

	var mems []Memory
	if err := json.Unmarshal(data, &mems); err != nil {
		return nil, fmt.Errorf("parse list response: %s", string(data[:min(len(data), 200)]))
	}
	return mems, nil
}

// Update — POST /memory/:id/update (Engram v5)
func (c *Client) Update(id int, content string, category string) error {
	payload := map[string]interface{}{
		"content": content,
	}
	if category != "" {
		payload["category"] = category
	}

	_, err := c.do("POST", fmt.Sprintf("/memory/%d/update", id), payload)
	return err
}

// Delete — DELETE /memory/:id (Engram v5)
func (c *Client) Delete(id int) error {
	_, err := c.do("DELETE", fmt.Sprintf("/memory/%d", id), nil)
	return err
}

// Archive — POST /memory/:id/archive (Engram v5)
func (c *Client) Archive(id int) error {
	_, err := c.do("POST", fmt.Sprintf("/memory/%d/archive", id), nil)
	return err
}

// Health — GET /health
func (c *Client) Health() error {
	_, err := c.do("GET", "/health", nil)
	return err
}

// Context — POST /context (Engram v5 semantic context packing)
func (c *Client) Context(query string, budget int) (string, error) {
	payload := map[string]interface{}{
		"query":      query,
		"max_tokens": budget,
	}

	data, err := c.do("POST", "/context", payload)
	if err != nil {
		return "", err
	}

	// /context returns structured sections — extract the text
	var result struct {
		Context string `json:"context"`
		// v5 may return sections
		Permanent []struct {
			Content string `json:"content"`
		} `json:"permanent_facts"`
		Relevant []struct {
			Content string `json:"content"`
		} `json:"relevant_memories"`
		Recent []struct {
			Content string `json:"content"`
		} `json:"recent_activity"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		trimmed := strings.TrimSpace(string(data))
		if strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "<!") {
			return "", fmt.Errorf("engram returned HTML instead of JSON: %.100s", trimmed)
		}
		return trimmed, nil
	}

	// If there's a pre-packed context field, use it
	if result.Context != "" {
		return result.Context, nil
	}

	// Otherwise build from sections
	var sb strings.Builder
	if len(result.Permanent) > 0 {
		sb.WriteString("## Permanent Facts\n")
		for _, f := range result.Permanent {
			content := f.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			sb.WriteString("- " + content + "\n")
		}
	}
	if len(result.Relevant) > 0 {
		sb.WriteString("\n## Relevant Memories\n")
		for _, m := range result.Relevant {
			content := m.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			sb.WriteString("- " + content + "\n")
		}
	}
	if len(result.Recent) > 0 {
		sb.WriteString("\n## Recent Activity\n")
		for _, m := range result.Recent {
			content := m.Content
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			sb.WriteString("- " + content + "\n")
		}
	}

	return sb.String(), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
