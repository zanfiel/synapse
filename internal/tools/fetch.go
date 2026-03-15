package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// FetchTool reads URLs and returns content as text.
func FetchTool() *ToolDef {
	return &ToolDef{
		Name:        "fetch",
		Description: "Fetch a URL and return its content as text. HTML pages are converted to readable text. Useful for reading documentation, API responses, or web pages.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "URL to fetch",
				},
				"raw": map[string]interface{}{
					"type":        "boolean",
					"description": "Return raw content without HTML stripping (default false)",
				},
				"max_bytes": map[string]interface{}{
					"type":        "number",
					"description": "Max bytes to read (default 100KB)",
				},
			},
			"required": []string{"url"},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			url := getStr(args, "url")
			if url == "" {
				return "", fmt.Errorf("url is required")
			}
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				url = "https://" + url
			}

			maxBytes := getInt(args, "max_bytes")
			if maxBytes <= 0 {
				maxBytes = 100 * 1024
			}
			if maxBytes > 1024*1024 {
				maxBytes = 1024 * 1024 // hard cap 1MB
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return "", fmt.Errorf("invalid url: %w", err)
			}
			req.Header.Set("User-Agent", "Synapse/0.4 (coding-agent)")
			req.Header.Set("Accept", "text/html,application/json,text/plain,*/*")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return "", fmt.Errorf("fetch failed: %w", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
			if err != nil {
				return "", fmt.Errorf("read body: %w", err)
			}

			content := string(body)

			// If not valid UTF-8, it's probably binary
			if !utf8.ValidString(content) {
				return fmt.Sprintf("[Binary content, %d bytes, Content-Type: %s]", len(body), resp.Header.Get("Content-Type")), nil
			}

			// Strip HTML if it looks like HTML and raw mode isn't requested
			if !getBool(args, "raw") && isHTML(content) {
				content = htmlToText(content)
			}

			header := fmt.Sprintf("HTTP %d | %s | %d bytes\n---\n", resp.StatusCode, resp.Header.Get("Content-Type"), len(body))

			if len(content) > maxBytes {
				content = content[:maxBytes] + "\n... (truncated)"
			}

			return header + content, nil
		},
	}
}

func isHTML(s string) bool {
	lower := strings.ToLower(s[:min(500, len(s))])
	return strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") || strings.Contains(lower, "<head")
}

var (
	reScript    = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle     = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
	reMultiNL   = regexp.MustCompile(`\n{3,}`)
	reMultiSP   = regexp.MustCompile(`[ \t]{2,}`)
	reEntity    = regexp.MustCompile(`&[a-zA-Z]+;|&#\d+;`)
)

// htmlToText strips HTML to readable plain text.
func htmlToText(html string) string {
	s := html

	// Remove script and style blocks
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")

	// Convert block elements to newlines
	for _, tag := range []string{"p", "div", "br", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "td", "th", "dt", "dd", "blockquote", "pre", "section", "article", "header", "footer", "nav"} {
		s = strings.ReplaceAll(s, "<"+tag, "\n<"+tag)
		s = strings.ReplaceAll(s, "</"+tag+">", "</"+tag+">\n")
	}

	// Strip remaining tags
	s = reTag.ReplaceAllString(s, "")

	// Decode common entities
	entities := map[string]string{
		"&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": `"`,
		"&apos;": "'", "&nbsp;": " ", "&mdash;": "—", "&ndash;": "–",
		"&hellip;": "…", "&copy;": "©", "&reg;": "®",
	}
	for entity, replacement := range entities {
		s = strings.ReplaceAll(s, entity, replacement)
	}
	s = reEntity.ReplaceAllString(s, "")

	// Clean whitespace
	s = reMultiSP.ReplaceAllString(s, " ")
	s = reMultiNL.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)

	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
