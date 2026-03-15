package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func WebSearchTool() *ToolDef {
	return &ToolDef{
		Name:        "web_search",
		Description: "Search the web for information. Returns titles, URLs, and snippets.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum results to return (default 5)",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			query := getStr(args, "query")
			if query == "" {
				return "", fmt.Errorf("query is required")
			}
			maxResults := 5
			if v, ok := args["max_results"].(float64); ok && v > 0 {
				maxResults = int(v)
			}

			if key := os.Getenv("BRAVE_API_KEY"); key != "" {
				return braveSearch(query, maxResults, key)
			}
			if key := os.Getenv("SERPER_API_KEY"); key != "" {
				return serperSearch(query, maxResults, key)
			}
			return ddgSearch(query, maxResults)
		},
	}
}

func braveSearch(query string, maxResults int, apiKey string) (string, error) {
	reqURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), maxResults)
	req, _ := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("brave search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("brave parse: %w", err)
	}

	var sb strings.Builder
	for i, r := range result.Web.Results {
		if i >= maxResults {
			break
		}
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Description))
	}
	if sb.Len() == 0 {
		return "No results found.", nil
	}
	return sb.String(), nil
}

func serperSearch(query string, maxResults int, apiKey string) (string, error) {
	payload := fmt.Sprintf(`{"q":%q,"num":%d}`, query, maxResults)
	req, _ := http.NewRequest("POST", "https://google.serper.dev/search",
		strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("serper search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("serper parse: %w", err)
	}

	var sb strings.Builder
	for i, r := range result.Organic {
		if i >= maxResults {
			break
		}
		sb.WriteString(fmt.Sprintf("%d. **%s**\n   %s\n   %s\n\n", i+1, r.Title, r.Link, r.Snippet))
	}
	if sb.Len() == 0 {
		return "No results found.", nil
	}
	return sb.String(), nil
}

func ddgSearch(query string, maxResults int) (string, error) {
	reqURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1",
		url.QueryEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(reqURL)
	if err != nil {
		return "", fmt.Errorf("ddg search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		AbstractText  string `json:"AbstractText"`
		AbstractURL   string `json:"AbstractURL"`
		RelatedTopics []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("ddg parse: %w", err)
	}

	var sb strings.Builder
	if result.AbstractText != "" {
		sb.WriteString(fmt.Sprintf("**Summary**: %s\n%s\n\n", result.AbstractText, result.AbstractURL))
	}
	count := 0
	for _, r := range result.RelatedTopics {
		if count >= maxResults || r.Text == "" {
			break
		}
		sb.WriteString(fmt.Sprintf("* %s\n  %s\n\n", r.Text, r.FirstURL))
		count++
	}
	if sb.Len() == 0 {
		return "No results found (DuckDuckGo fallback — set BRAVE_API_KEY or SERPER_API_KEY for better results).", nil
	}
	return sb.String(), nil
}
