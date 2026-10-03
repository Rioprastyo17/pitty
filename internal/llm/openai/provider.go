// Package openai implements an llm.Provider backed by OpenAI-compatible APIs.
//
// Works with:
//   - OpenAI (api.openai.com)
//   - Azure OpenAI
//   - Groq (api.groq.com/openai/v1)
//   - Together AI (api.together.xyz/v1)
//   - Mistral (api.mistral.ai/v1)
//   - DeepSeek (api.deepseek.com/v1)
//   - Any other OpenAI-compatible endpoint
//
// Usage:
//
//	p := openai.New(openai.Config{
//	    APIKey:  "sk-...",
//	    BaseURL: "https://api.openai.com/v1",  // optional
//	    Model:   "gpt-4o",
//	})
package openai

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

// Config holds configuration for the OpenAI-compatible provider.
type Config struct {
	// APIKey is the authentication key (required).
	APIKey string
	// BaseURL overrides the endpoint. Defaults to "https://api.openai.com/v1".
	BaseURL string
	// OrgID is the optional OpenAI organization ID.
	OrgID string
	// Timeout overrides the HTTP client timeout. Defaults to 10 minutes.
	Timeout time.Duration
}

// Provider implements llm.Provider using OpenAI-compatible HTTP API.
type Provider struct {
	cfg        Config
	httpClient *http.Client
}

// New creates a new OpenAI-compatible provider.
func New(cfg Config) *Provider {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.openai.com/v1"
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
func (p *Provider) Name() string {
	host := p.cfg.BaseURL
	if strings.Contains(host, "groq") {
		return "groq"
	}
	if strings.Contains(host, "together") {
		return "together"
	}
	if strings.Contains(host, "mistral") {
		return "mistral"
	}
	if strings.Contains(host, "deepseek") {
		return "deepseek"
	}
	return "openai"
}

// ── Wire types ────────────────────────────────────────────────────────────────

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type toolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function toolFunc `json:"function"`
}

type toolFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON
}

type tool struct {
	Type     string    `json:"type"`
	Function toolInner `json:"function"`
}

type toolInner struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type request struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
	Tools       []tool    `json:"tools,omitempty"`
}

type response struct {
	Choices []struct {
		Message      message `json:"message"`
		Delta        message `json:"delta"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// ── Converters ────────────────────────────────────────────────────────────────

func toMessages(msgs []llm.Message) []message {
	out := make([]message, 0, len(msgs))
	for _, m := range msgs {
		wm := message{
			Role:    m.Role,
			Content: m.Content,
		}
		for _, tc := range m.ToolCalls {
			rawArgs, _ := json.Marshal(tc.Function.Arguments)
			wm.ToolCalls = append(wm.ToolCalls, toolCall{
				ID:   tc.ID,
				Type: "function",
				Function: toolFunc{
					Name:      tc.Function.Name,
					Arguments: string(rawArgs),
				},
			})
		}
		out = append(out, wm)
	}
	return out
}

func toTools(tools []llm.ToolDefinition) []tool {
	out := make([]tool, 0, len(tools))
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
		out = append(out, tool{
			Type: "function",
			Function: toolInner{
				Name:        t.Name,
				Description: t.Description,
				Parameters: map[string]interface{}{
					"type":       "object",
					"properties": props,
					"required":   t.Required,
				},
			},
		})
	}
	return out
}

func parseToolCalls(tcs []toolCall) []llm.ToolCall {
	out := make([]llm.ToolCall, 0, len(tcs))
	for _, tc := range tcs {
		var args map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		out = append(out, llm.ToolCall{
			ID: tc.ID,
			Function: llm.ToolFunction{
				Name:      tc.Function.Name,
				Arguments: args,
			},
		})
	}
	return out
}

// ── HTTP helper ───────────────────────────────────────────────────────────────

func (p *Provider) newRequest(ctx context.Context, path string, body []byte, stream bool) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	if p.cfg.OrgID != "" {
		httpReq.Header.Set("OpenAI-Organization", p.cfg.OrgID)
	}
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	return httpReq, nil
}

// ── Provider methods ──────────────────────────────────────────────────────────

// Chat performs a non-streaming chat request.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	wireReq := request{
		Model:       req.Model,
		Messages:    toMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
		Tools:       toTools(req.Tools),
	}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal: %w", err)
	}
	httpReq, err := p.newRequest(ctx, "/chat/completions", body, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: status %d: %s", resp.StatusCode, raw)
	}

	var cr response
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("openai: decode: %w", err)
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("openai error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("openai: empty response")
	}
	m := cr.Choices[0].Message
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:      m.Role,
			Content:   m.Content,
			ToolCalls: parseToolCalls(m.ToolCalls),
		},
	}, nil
}

// ChatStream performs a streaming chat request using Server-Sent Events.
func (p *Provider) ChatStream(ctx context.Context, req llm.ChatRequest, onChunk func(string)) (*llm.ChatResponse, error) {
	wireReq := request{
		Model:       req.Model,
		Messages:    toMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
		Tools:       toTools(req.Tools),
	}
	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal stream: %w", err)
	}

	streamClient := *p.httpClient
	streamClient.Timeout = 0

	httpReq, err := p.newRequest(ctx, "/chat/completions", body, true)
	if err != nil {
		return nil, err
	}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai: stream status %d: %s", resp.StatusCode, raw)
	}

	var fullContent strings.Builder
	// For streaming tool calls, we accumulate per-index
	toolCallsMap := map[int]*llm.ToolCall{}
	toolArgsMap := map[int]*strings.Builder{}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk response
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return nil, fmt.Errorf("openai stream error: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			onChunk(delta.Content)
			fullContent.WriteString(delta.Content)
		}
		// Accumulate streaming tool calls
		for _, dtc := range delta.ToolCalls {
			// OpenAI streaming sends tool calls with index in ID field as int
			// We key by the tool call ID for simplicity
			key := 0
			if dtc.ID != "" {
				for k, v := range toolCallsMap {
					if v.ID == dtc.ID {
						key = k
						break
					}
				}
				if _, exists := toolCallsMap[key]; !exists || (toolCallsMap[key] != nil && toolCallsMap[key].ID != dtc.ID) {
					key = len(toolCallsMap)
					toolCallsMap[key] = &llm.ToolCall{
						ID: dtc.ID,
						Function: llm.ToolFunction{
							Name: dtc.Function.Name,
						},
					}
					toolArgsMap[key] = &strings.Builder{}
				}
			}
			if tc := toolCallsMap[key]; tc != nil {
				if dtc.Function.Name != "" && tc.Function.Name == "" {
					tc.Function.Name = dtc.Function.Name
				}
				if dtc.Function.Arguments != "" {
					toolArgsMap[key].WriteString(dtc.Function.Arguments)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("openai: stream scan: %w", err)
	}

	// Finalize tool calls
	var toolCalls []llm.ToolCall
	for i := 0; i < len(toolCallsMap); i++ {
		tc := toolCallsMap[i]
		if tc == nil {
			continue
		}
		var args map[string]interface{}
		if sb := toolArgsMap[i]; sb != nil {
			_ = json.Unmarshal([]byte(sb.String()), &args)
		}
		tc.Function.Arguments = args
		toolCalls = append(toolCalls, *tc)
	}

	return &llm.ChatResponse{
		Message: llm.Message{
			Role:      "assistant",
			Content:   fullContent.String(),
			ToolCalls: toolCalls,
		},
	}, nil
}

// ListModels lists available models from the OpenAI-compatible API.
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: list models: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		names = append(names, m.ID)
	}
	return names, nil
}

// Ping checks connectivity to the OpenAI-compatible API.
func (p *Provider) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseURL+"/models", nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("cannot connect to %s: %w", p.cfg.BaseURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("openai: invalid API key (401 Unauthorized)")
	}
	return nil
}
