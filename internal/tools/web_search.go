package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// WebSearchTool performs a web search using DuckDuckGo HTML.
type WebSearchTool struct{}

func (t *WebSearchTool) Name() string        { return "web_search" }
func (t *WebSearchTool) Description() string { return "Search the web for information using DuckDuckGo." }
func (t *WebSearchTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"query": {Type: "string", Description: "The search query", Required: true},
	}
}
func (t *WebSearchTool) RequiredParams() []string { return []string{"query"} }

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	html := string(body)
	return extractResults(html), nil
}

// extractResults extracts snippets from the DuckDuckGo HTML response.
func extractResults(html string) string {
	// Very basic regex to extract result snippets from DDG HTML
	// <a class="result__snippet[^>]*>(.*?)</a>
	re := regexp.MustCompile(`(?s)<a class="result__snippet[^>]*>(.*?)</a>`)
	matches := re.FindAllStringSubmatch(html, 5)

	if len(matches) == 0 {
		return "No results found or parsing failed."
	}

	var sb strings.Builder
	for i, match := range matches {
		if len(match) > 1 {
			// clean up HTML tags
			text := cleanHTML(match[1])
			sb.WriteString(fmt.Sprintf("%d. %s\n\n", i+1, text))
		}
	}
	return strings.TrimSpace(sb.String())
}

func cleanHTML(html string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	text := re.ReplaceAllString(html, "")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#x27;", "'")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	return strings.TrimSpace(text)
}
