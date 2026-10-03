// Package anthropic implements an llm.Provider backed by Anthropic's Claude API.
//
// Supported models: claude-opus-4-5, claude-sonnet-4-5, claude-haiku-3-5, etc.
//
// Usage:
//
//	p := anthropic.New(anthropic.Config{
//	    APIKey: "sk-ant-...",
//	    Model:  "claude-sonnet-4-5",
//	})
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pitty/pitty/internal/llm"
)

const (
	defaultBaseURL    = "https://api.anthropic.com/v1"
	anthropicVersion  = "2023-06-01"
	defaultMaxTokens  = 8192
)

// Config holds configuration for the Anthropic provider.
type Config struct {
	APIKey  string
	BaseURL string        // Defaults to "https://api.anthropic.com/v1"
	Timeout time.Duration // Defaults to 10 minutes
}

// Provider implements llm.Provider using Anthropic's Messages API.
type Provider struct {
	cfg        Config
	httpClient *http.Client
}

// New creates a new Anthropic provider.
func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute
	}
	return &Provider{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "anthropic" }

// ── Wire types ────────────────────────────────────────────────────────────────

type anthropicMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	// For tool_use blocks
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	// For tool_result blocks
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	Content    string          `json:"content,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
	Temperature *float64         `json:"temperature,omitempty"`
}

type anthropicResponse struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	Role         string        `json:"role"`
	Content      []contentPart `json:"content"`
	StopReason   string        `json:"stop_reason"`
	Error        *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ── Converters ────────────────────────────────────────────────────────────────

// toAnthropicMessages converts pitty messages to Anthropic's format.
// System messages are extracted separately; tool messages become tool_result.
func toAnthropicMessages(msgs []llm.Message) (system string, out []anthropicMessage) {
	for _, m := range msgs {
		switch m.Role {
		case "system":
			system = m.Content
		case "user":
			out = append(out, anthropicMessage{
				Role:    "user",
				Content: []contentPart{{Type: "text", Text: m.Content}},
			})
		case "assistant":
			parts := []contentPart{}
			if m.Content != "" {
				parts = append(parts, contentPart{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				rawInput, _ := json.Marshal(tc.Function.Arguments)
				parts = append(parts, contentPart{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: rawInput,
				})
			}
			out = append(out, anthropicMessage{Role: "assistant", Content: parts})
		case "tool":
			// Tool result — attach to the preceding user turn or create a new one
			toolResult := contentPart{
				Type:      "tool_result",
				ToolUseID: "", // We don't track IDs directly here; Anthropic needs them
				Content:   m.Content,
			}
			// Look back for last assistant message's tool call ID
			for i := len(out) - 1; i >= 0; i-- {
				for _, cp := range out[i].Content {
					if cp.Type == "tool_use" {
						toolResult.ToolUseID = cp.ID
						goto foundID
					}
				}
			}
		foundID:
			// Append as a user message with tool_result
			if len(out) > 0 && out[len(out)-1].Role == "user" {
				// Append to last user turn if it already exists
				out[len(out)-1].Content = append(out[len(out)-1].Content, toolResult)
			} else {
				out = append(out, anthropicMessage{
					Role:    "user",
					Content: []contentPart{toolResult},
				})
			}
		}
	}
	return
}

func toAnthropicTools(tools []llm.ToolDefinition) []anthropicTool {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		props := make(map[string]interface{})
		for pName, p := range t.Parameters {
			prop := map[string]interface{}{
				"type":        p.Type,
				"description": p.Description,
			}
			if len(p.Enum) > 0 {
				prop["enum"] = p.Enum
			}
			props[pName] = prop
		}
		out = append(out, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": props,
				"required":   t.Required,
			},
		})
	}
	return out
}

func parseResponse(resp *anthropicResponse) *llm.ChatResponse {
	var content strings.Builder
	var toolCalls []llm.ToolCall

	for _, part := range resp.Content {
		switch part.Type {
		case "text":
			content.WriteString(part.Text)
		case "tool_use":
			var args map[string]interface{}
			_ = json.Unmarshal(part.Input, &args)
			toolCalls = append(toolCalls, llm.ToolCall{
				ID: part.ID,
				Function: llm.ToolFunction{
					Name:      part.Name,
					Arguments: args,
				},
			})
		}
	}

	return &llm.ChatResponse{
		Message: llm.Message{
			Role:      "assistant",
			Content:   content.String(),
			ToolCalls: toolCalls,
		},
	}
}

// ── HTTP helper ───────────────────────────────────────────────────────────────

func (p *Provider) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	return p.httpClient.Do(httpReq)
}

// ── Provider methods ──────────────────────────────────────────────────────────

// Chat performs a non-streaming request.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	system, msgs := toAnthropicMessages(req.Messages)
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	temp := req.Temperature
	wireReq := anthropicRequest{
		Model:       req.Model,
		System:      system,
		Messages:    msgs,
		MaxTokens:   maxTokens,
		Tools:       toAnthropicTools(req.Tools),
		Stream:      false,
		Temperature: &temp,
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal: %w", err)
	}

	resp, err := p.post(ctx, "/messages", body)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, raw)
	}

	var cr anthropicResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("anthropic: decode: %w", err)
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("anthropic error: %s", cr.Error.Message)
	}

	return parseResponse(&cr), nil
}

// ChatStream performs a streaming request using Anthropic's SSE format.
func (p *Provider) ChatStream(ctx context.Context, req llm.ChatRequest, onChunk func(string)) (*llm.ChatResponse, error) {
	system, msgs := toAnthropicMessages(req.Messages)
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	temp := req.Temperature
	wireReq := anthropicRequest{
		Model:       req.Model,
		System:      system,
		Messages:    msgs,
		MaxTokens:   maxTokens,
		Tools:       toAnthropicTools(req.Tools),
		Stream:      true,
		Temperature: &temp,
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal stream: %w", err)
	}

	streamClient := *p.httpClient
	streamClient.Timeout = 0

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic: stream status %d: %s", resp.StatusCode, raw)
	}

	var fullContent strings.Builder
	var toolCalls []llm.ToolCall
	toolInputBuilders := map[int]*strings.Builder{}
	toolCallsByIndex := map[int]*llm.ToolCall{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "content_block_start":
			var ev struct {
				Index        int         `json:"index"`
				ContentBlock contentPart `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			if ev.ContentBlock.Type == "tool_use" {
				toolCallsByIndex[ev.Index] = &llm.ToolCall{
					ID: ev.ContentBlock.ID,
					Function: llm.ToolFunction{
						Name: ev.ContentBlock.Name,
					},
				}
				toolInputBuilders[ev.Index] = &strings.Builder{}
			}

		case "content_block_delta":
			var ev struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				onChunk(ev.Delta.Text)
				fullContent.WriteString(ev.Delta.Text)
			case "input_json_delta":
				if sb, ok := toolInputBuilders[ev.Index]; ok {
					sb.WriteString(ev.Delta.PartialJSON)
				}
			}

		case "content_block_stop":
			var ev struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			if tc, ok := toolCallsByIndex[ev.Index]; ok {
				var args map[string]interface{}
				if sb := toolInputBuilders[ev.Index]; sb != nil {
					_ = json.Unmarshal([]byte(sb.String()), &args)
				}
				tc.Function.Arguments = args
				toolCalls = append(toolCalls, *tc)
			}

		case "error":
			var ev struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				return nil, fmt.Errorf("anthropic stream error: %s", ev.Error.Message)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("anthropic: stream scan: %w", err)
	}

	return &llm.ChatResponse{
		Message: llm.Message{
			Role:      "assistant",
			Content:   fullContent.String(),
			ToolCalls: toolCalls,
		},
	}, nil
}

// ListModels returns Anthropic's current flagship models.
// (Anthropic doesn't have a /models endpoint, so we return known models.)
func (p *Provider) ListModels(_ context.Context) ([]string, error) {
	return []string{
		"claude-opus-4-5",
		"claude-sonnet-4-5",
		"claude-haiku-3-5",
		"claude-3-opus-20240229",
		"claude-3-5-sonnet-20241022",
		"claude-3-haiku-20240307",
	}, nil
}

// Ping verifies the API key works by hitting the models endpoint.
func (p *Provider) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Anthropic has no lightweight ping; send a minimal message
	req := anthropicRequest{
		Model:     "claude-haiku-3-5",
		MaxTokens: 1,
		Messages: []anthropicMessage{
			{Role: "user", Content: []contentPart{{Type: "text", Text: "hi"}}},
		},
	}
	body, _ := json.Marshal(req)
	resp, err := p.post(ctx, "/messages", body)
	if err != nil {
		return fmt.Errorf("cannot connect to Anthropic: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("anthropic: invalid API key")
	}
	return nil
}
