// Package llm defines the provider-agnostic interface for all AI backends.
package llm

import "context"

// Message is a single turn in a conversation.
type Message struct {
	Role      string     `json:"role"`    // "system" | "user" | "assistant" | "tool"
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Function ToolFunction `json:"function"`
}

// ToolFunction holds the name and arguments of a tool call.
type ToolFunction struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolDefinition describes a tool the model can call.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]ToolParam
	Required    []string
}

// ToolParam describes a single parameter of a tool.
type ToolParam struct {
	Type        string
	Description string
	Enum        []string
}

// ChatRequest is the provider-agnostic chat request.
type ChatRequest struct {
	Messages    []Message
	Model       string
	Temperature float64
	MaxTokens   int
	Tools       []ToolDefinition
}

// ChatResponse is the provider-agnostic non-streaming response.
type ChatResponse struct {
	Message Message
}

// Provider is the interface every AI backend must implement.
// It supports both streaming text and tool-calling.
type Provider interface {
	// Name returns the provider identifier (e.g. "llamacpp", "openai").
	Name() string

	// Chat performs a non-streaming request, returning the complete response.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// ChatStream sends a streaming request.
	// onChunk is called for each text chunk.
	// The returned ChatResponse contains accumulated content and any tool calls.
	ChatStream(ctx context.Context, req ChatRequest, onChunk func(string)) (*ChatResponse, error)

	// ListModels returns available models (may return empty slice if unsupported).
	ListModels(ctx context.Context) ([]string, error)

	// Ping checks connectivity / health.
	Ping(ctx context.Context) error
}
