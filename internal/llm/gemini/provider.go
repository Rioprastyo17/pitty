// Package gemini implements an llm.Provider backed by Google Gemini API.
//
// Supported models: gemini-2.0-flash, gemini-1.5-pro, gemini-1.5-flash, etc.
//
// Usage:
//
//	p := gemini.New(gemini.Config{
//	    APIKey: "AIza...",
//	    Model:  "gemini-2.0-flash",
//	})
package gemini

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

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Config holds configuration for the Gemini provider.
type Config struct {
	APIKey  string
	BaseURL string        // Defaults to "https://generativelanguage.googleapis.com/v1beta"
	Timeout time.Duration // Defaults to 10 minutes
}

// Provider implements llm.Provider using Google Gemini's generateContent API.
type Provider struct {
	cfg        Config
	httpClient *http.Client
}

// New creates a new Gemini provider.
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
func (p *Provider) Name() string { return "gemini" }

// ── Wire types ────────────────────────────────────────────────────────────────

type geminiContent struct {
	Role  string      `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text         string               `json:"text,omitempty"`
	FunctionCall *geminiFunctionCall  `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunction `json:"functionDeclarations"`
}

type geminiFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type geminiRequest struct {
	Contents         []geminiContent          `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Tools            []geminiTool             `json:"tools,omitempty"`
	GenerationConfig *geminiGenerationConfig  `json:"generationConfig,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ── Converters ────────────────────────────────────────────────────────────────

func toGeminiContents(msgs []llm.Message) (systemInstruction *geminiContent, contents []geminiContent) {
	for _, m := range msgs {
		switch m.Role {
		case "system":
			systemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: m.Content}},
			}
		case "user":
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			})
		case "assistant":
			parts := []geminiPart{}
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Function.Name,
						Args: tc.Function.Arguments,
					},
				})
			}
			contents = append(contents, geminiContent{Role: "model", Parts: parts})
		case "tool":
			// Tool result — map to function_response
			// We need the function name from the preceding assistant tool call
			funcName := ""
			for i := len(contents) - 1; i >= 0; i-- {
				for _, part := range contents[i].Parts {
					if part.FunctionCall != nil {
						funcName = part.FunctionCall.Name
						goto gotName
					}
				}
			}
		gotName:
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFunctionResponse{
						Name:     funcName,
						Response: map[string]interface{}{"result": m.Content},
					},
				}},
			})
		}
	}
	return
}

func toGeminiTools(tools []llm.ToolDefinition) []geminiTool {
	if len(tools) == 0 {
		return nil
	}
	fns := make([]geminiFunction, 0, len(tools))
	for _, t := range tools {
		props := make(map[string]interface{})
		for pName, p := range t.Parameters {
			prop := map[string]interface{}{
				"type":        strings.ToUpper(p.Type), // Gemini uses uppercase types
				"description": p.Description,
			}
			if len(p.Enum) > 0 {
				prop["enum"] = p.Enum
			}
			props[pName] = prop
		}
		fns = append(fns, geminiFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters: map[string]interface{}{
				"type":       "OBJECT",
				"properties": props,
				"required":   t.Required,
			},
		})
	}
	return []geminiTool{{FunctionDeclarations: fns}}
}

func parseGeminiResponse(gr *geminiResponse) *llm.ChatResponse {
	if len(gr.Candidates) == 0 {
		return &llm.ChatResponse{Message: llm.Message{Role: "assistant"}}
	}
	content := gr.Candidates[0].Content
	var text strings.Builder
	var toolCalls []llm.ToolCall

	for _, part := range content.Parts {
		if part.Text != "" {
			text.WriteString(part.Text)
		}
		if part.FunctionCall != nil {
			toolCalls = append(toolCalls, llm.ToolCall{
				Function: llm.ToolFunction{
					Name:      part.FunctionCall.Name,
					Arguments: part.FunctionCall.Args,
				},
			})
		}
	}

	return &llm.ChatResponse{
		Message: llm.Message{
			Role:      "assistant",
			Content:   text.String(),
			ToolCalls: toolCalls,
		},
	}
}

// ── HTTP helper ───────────────────────────────────────────────────────────────

func (p *Provider) url(model, action string, stream bool) string {
	base := fmt.Sprintf("%s/models/%s:%s?key=%s", p.cfg.BaseURL, model, action, p.cfg.APIKey)
	if stream {
		base += "&alt=sse"
	}
	return base
}

// ── Provider methods ──────────────────────────────────────────────────────────

// Chat performs a non-streaming request.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	sysInstr, contents := toGeminiContents(req.Messages)
	wireReq := geminiRequest{
		Contents:          contents,
		SystemInstruction: sysInstr,
		Tools:             toGeminiTools(req.Tools),
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
		},
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url(req.Model, "generateContent", false), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini: status %d: %s", resp.StatusCode, raw)
	}

	var gr geminiResponse
	if err := json.Unmarshal(raw, &gr); err != nil {
		return nil, fmt.Errorf("gemini: decode: %w", err)
	}
	if gr.Error != nil {
		return nil, fmt.Errorf("gemini error: %s", gr.Error.Message)
	}

	return parseGeminiResponse(&gr), nil
}

// ChatStream performs a streaming request using Gemini's SSE streamGenerateContent.
func (p *Provider) ChatStream(ctx context.Context, req llm.ChatRequest, onChunk func(string)) (*llm.ChatResponse, error) {
	sysInstr, contents := toGeminiContents(req.Messages)
	wireReq := geminiRequest{
		Contents:          contents,
		SystemInstruction: sysInstr,
		Tools:             toGeminiTools(req.Tools),
		GenerationConfig: &geminiGenerationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
		},
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal stream: %w", err)
	}

	streamClient := *p.httpClient
	streamClient.Timeout = 0

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url(req.Model, "streamGenerateContent", true), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: stream request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini: stream status %d: %s", resp.StatusCode, raw)
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
		var chunk geminiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return nil, fmt.Errorf("gemini stream error: %s", chunk.Error.Message)
		}
		if len(chunk.Candidates) == 0 {
			continue
		}
		for _, part := range chunk.Candidates[0].Content.Parts {
			if part.Text != "" {
				onChunk(part.Text)
				fullContent.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				toolCalls = append(toolCalls, llm.ToolCall{
					Function: llm.ToolFunction{
						Name:      part.FunctionCall.Name,
						Arguments: part.FunctionCall.Args,
					},
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("gemini: stream scan: %w", err)
	}

	return &llm.ChatResponse{
		Message: llm.Message{
			Role:      "assistant",
			Content:   fullContent.String(),
			ToolCalls: toolCalls,
		},
	}, nil
}

// ListModels returns known Gemini models.
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/models?key=%s", p.cfg.BaseURL, p.cfg.APIKey), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: list models: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"` // e.g., "models/gemini-2.0-flash"
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// Fallback to well-known models if parsing fails
		return []string{"gemini-2.0-flash", "gemini-1.5-pro", "gemini-1.5-flash"}, nil
	}
	names := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		// Strip "models/" prefix
		names = append(names, strings.TrimPrefix(m.Name, "models/"))
	}
	return names, nil
}

// Ping checks connectivity to the Gemini API.
func (p *Provider) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/models?key=%s", p.cfg.BaseURL, p.cfg.APIKey), nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("cannot connect to Gemini API: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("gemini: invalid API key (status %d)", resp.StatusCode)
	}
	return nil
}
