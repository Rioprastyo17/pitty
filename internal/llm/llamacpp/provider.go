// Package llamacpp implements an llm.Provider backed by a running llama.cpp server.
//
// llama.cpp exposes an OpenAI-compatible HTTP API at /v1/chat/completions
// when launched with: ./llama-server -m model.gguf --port 8080
//
// Usage:
//
//	p := llamacpp.New("http://127.0.0.1:8080", "llama3")
package llamacpp

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

// Provider connects to a llama.cpp HTTP server using its OpenAI-compatible API.
type Provider struct {
	baseURL    string
	model      string
	threads    int // 0 = let the server decide
	httpClient *http.Client
}

// New creates a new llama.cpp provider.
// baseURL is the server address, e.g. "http://127.0.0.1:8080".
// model is the model name to use (llama.cpp often ignores it but we send it anyway).
// threads controls how many CPU threads the server uses (0 = server default).
func New(baseURL, model string, threads int) *Provider {
	return &Provider{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		threads: threads,
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "llamacpp" }

// ── OpenAI-compatible wire types ─────────────────────────────────────────────

type chatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
	Tools       []toolDef     `json:"tools,omitempty"`
	// llama.cpp-specific: number of CPU threads (0 = server default)
	NThreads    int           `json:"n_threads,omitempty"`
}

type toolDef struct {
	Type     string        `json:"type"`
	Function toolDefInner  `json:"function"`
}

type toolDefInner struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		Delta        chatMessage `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ── Conversion helpers ────────────────────────────────────────────────────────

func toWireMessages(msgs []llm.Message) []chatMessage {
	out := make([]chatMessage, 0, len(msgs))
	for _, m := range msgs {
		wm := chatMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.Role == "tool" {
			// llama.cpp expects tool results as role=tool with tool_call_id
			wm.Role = "tool"
		}
		for _, tc := range m.ToolCalls {
			rawArgs, _ := json.Marshal(tc.Function.Arguments)
			wm.ToolCalls = append(wm.ToolCalls, toolCall{
				ID:   tc.ID,
				Type: "function",
				Function: toolFunction{
					Name:      tc.Function.Name,
					Arguments: string(rawArgs),
				},
			})
		}
		out = append(out, wm)
	}
	return out
}

func toWireTools(tools []llm.ToolDefinition) []toolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]toolDef, 0, len(tools))
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
		out = append(out, toolDef{
			Type: "function",
			Function: toolDefInner{
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

func parseToolCalls(wcs []toolCall) []llm.ToolCall {
	out := make([]llm.ToolCall, 0, len(wcs))
	for _, wc := range wcs {
		var args map[string]interface{}
		_ = json.Unmarshal([]byte(wc.Function.Arguments), &args)
		out = append(out, llm.ToolCall{
			ID: wc.ID,
			Function: llm.ToolFunction{
				Name:      wc.Function.Name,
				Arguments: args,
			},
		})
	}
	return out
}

// ── Provider methods ──────────────────────────────────────────────────────────

// Chat sends a non-streaming request.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	wireReq := chatRequest{
		Model:       req.Model,
		Messages:    toWireMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
		Tools:       toWireTools(req.Tools),
		NThreads:    p.threads,
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("llamacpp: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llamacpp: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llamacpp: do request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llamacpp: server error (status %d): %s", resp.StatusCode, raw)
	}

	var cr chatResponse
	if err := json.Unmarshal(raw, &cr); err != nil {
		return nil, fmt.Errorf("llamacpp: decode response: %w", err)
	}
	if cr.Error != nil {
		return nil, fmt.Errorf("llamacpp error: %s", cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("llamacpp: empty choices")
	}

	choice := cr.Choices[0]
	return &llm.ChatResponse{
		Message: llm.Message{
			Role:      choice.Message.Role,
			Content:   choice.Message.Content,
			ToolCalls: parseToolCalls(choice.Message.ToolCalls),
		},
	}, nil
}

// ChatStream sends a streaming request, calling onChunk for each text delta.
func (p *Provider) ChatStream(ctx context.Context, req llm.ChatRequest, onChunk func(string)) (*llm.ChatResponse, error) {
	wireReq := chatRequest{
		Model:       req.Model,
		Messages:    toWireMessages(req.Messages),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      true,
		Tools:       toWireTools(req.Tools),
		NThreads:    p.threads,
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("llamacpp: marshal stream request: %w", err)
	}

	streamClient := *p.httpClient
	streamClient.Timeout = 0

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llamacpp: create stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llamacpp: do stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llamacpp: stream error (status %d): %s", resp.StatusCode, raw)
	}

	var fullContent strings.Builder
	var toolCalls []llm.ToolCall

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
		var chunk chatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return nil, fmt.Errorf("llamacpp stream error: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			onChunk(delta.Content)
			fullContent.WriteString(delta.Content)
		}
		if len(delta.ToolCalls) > 0 {
			toolCalls = append(toolCalls, parseToolCalls(delta.ToolCalls)...)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("llamacpp: stream scan: %w", err)
	}

	return &llm.ChatResponse{
		Message: llm.Message{
			Role:      "assistant",
			Content:   fullContent.String(),
			ToolCalls: toolCalls,
		},
	}, nil
}

// ListModels lists models available on the llama.cpp server.
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llamacpp: list models: %w", err)
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

// Ping checks if the llama.cpp server is reachable.
func (p *Provider) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("cannot connect to llama.cpp server at %s: %w", p.baseURL, err)
	}
	resp.Body.Close()
	return nil
}
