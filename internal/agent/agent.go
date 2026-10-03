package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/pitty/pitty/internal/llm"
	"github.com/pitty/pitty/internal/memory"
	"github.com/pitty/pitty/internal/tools"
)

// Agent orchestrates chat, tool calling, memory, and streaming.
type Agent struct {
	provider       llm.Provider
	registry       *tools.Registry
	model          string
	temperature    float64
	maxTokens      int
	systemPrompt   string
	history        []llm.Message
	onToolCall     func(name string, args map[string]interface{})
	onToolResult   func(name string, result string)
	memStore       *memory.Store
	learner        *memory.Learner
	toolsSupported bool // tracks whether the current model supports tool calling
}

// NewAgent creates a new Agent with the given provider.
func NewAgent(provider llm.Provider, registry *tools.Registry, model string, temp float64, maxTokens int) *Agent {
	return &Agent{
		provider:       provider,
		registry:       registry,
		model:          model,
		temperature:    temp,
		maxTokens:      maxTokens,
		systemPrompt:   DefaultSystemPrompt,
		toolsSupported: true,
	}
}

// SetMemory attaches a memory store and learner.
func (a *Agent) SetMemory(store *memory.Store, learner *memory.Learner) {
	a.memStore = store
	a.learner = learner
}

// SetSystemPrompt overrides the default system prompt.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.systemPrompt = prompt
}

// SetModel changes the active model and resets tool support detection.
func (a *Agent) SetModel(model string) {
	a.model = model
	a.toolsSupported = true // reset detection for new model
}

// SetProvider swaps the underlying AI provider at runtime.
func (a *Agent) SetProvider(provider llm.Provider) {
	a.provider = provider
	a.toolsSupported = true
}

// Provider returns the current provider name.
func (a *Agent) Provider() string {
	return a.provider.Name()
}

// OnToolCall sets a callback triggered before a tool executes.
func (a *Agent) OnToolCall(fn func(name string, args map[string]interface{})) {
	a.onToolCall = fn
}

// OnToolResult sets a callback triggered after a tool returns.
func (a *Agent) OnToolResult(fn func(name string, result string)) {
	a.onToolResult = fn
}

// buildMessages assembles the full message list: system prompt + memory context + history.
func (a *Agent) buildMessages() []llm.Message {
	sysPrompt := a.systemPrompt

	// Inject relevant memory context based on the most recent user message.
	if a.memStore != nil && len(a.history) > 0 {
		lastUserMsg := ""
		for i := len(a.history) - 1; i >= 0; i-- {
			if a.history[i].Role == "user" {
				lastUserMsg = a.history[i].Content
				break
			}
		}
		if lastUserMsg != "" {
			if ctx := a.memStore.FormatForPrompt(lastUserMsg); ctx != "" {
				sysPrompt += "\n" + ctx
			}
		}
	}

	msgs := make([]llm.Message, 0, len(a.history)+1)
	msgs = append(msgs, llm.Message{Role: "system", Content: sysPrompt})
	msgs = append(msgs, a.history...)
	return msgs
}

// buildToolDefs converts the registry into llm.ToolDefinition slice.
func (a *Agent) buildToolDefs() []llm.ToolDefinition {
	var defs []llm.ToolDefinition
	for _, h := range a.registry.List() {
		params := make(map[string]llm.ToolParam)
		for k, v := range h.Parameters() {
			params[k] = llm.ToolParam{
				Type:        v.Type,
				Description: v.Description,
				Enum:        v.Enum,
			}
		}
		defs = append(defs, llm.ToolDefinition{
			Name:        h.Name(),
			Description: h.Description(),
			Parameters:  params,
			Required:    h.RequiredParams(),
		})
	}
	return defs
}

// Chat processes a user message with the full tool-calling agentic loop.
// onChunk is called for each streamed text chunk of the final response.
func (a *Agent) Chat(ctx context.Context, userMessage string, onChunk func(string)) error {
	// Auto-learn from the user's message.
	if a.learner != nil {
		a.learner.LearnFromUserMessage(userMessage)
	}

	a.history = append(a.history, llm.Message{Role: "user", Content: userMessage})

	const maxIterations = 15
	for i := 0; i < maxIterations; i++ {
		messages := a.buildMessages()

		// --- Tool-calling branch ---
		if a.toolsSupported && a.registry.Len() > 0 {
			req := llm.ChatRequest{
				Model:       a.model,
				Messages:    messages,
				Temperature: a.temperature,
				MaxTokens:   a.maxTokens,
				Tools:       a.buildToolDefs(),
			}

			resp, err := a.provider.ChatStream(ctx, req, onChunk)
			if err != nil {
				if isToolUnsupportedError(err) {
					// Model doesn't support tools — fall back to streaming only.
					a.toolsSupported = false
					return a.streamResponse(ctx, messages, onChunk)
				}
				return fmt.Errorf("chat: %w", err)
			}

			toolCalls := resp.Message.ToolCalls
			finalContent := resp.Message.Content

			// Fallback: extract tool calls from markdown if native tools weren't populated
			if len(toolCalls) == 0 && finalContent != "" {
				toolCalls = extractToolCallsFromText(finalContent, a.registry)
			}

			// If the model returned tool calls, execute them and loop.
			if len(toolCalls) > 0 {
				a.history = append(a.history, llm.Message{
					Role:      "assistant",
					Content:   finalContent,
					ToolCalls: toolCalls,
				})
				if err := a.executeToolCalls(ctx, toolCalls, userMessage); err != nil {
					return err
				}
				continue // back to top of loop for next model turn
			}

			// No tool calls — the model gave a final text response.
			if finalContent != "" {
				a.history = append(a.history, llm.Message{
					Role:    "assistant",
					Content: finalContent,
				})
				// Auto-learn from the assistant's response.
				if a.learner != nil {
					a.learner.LearnFromAssistantResponse(finalContent, userMessage)
				}
				return nil
			}
		}

		// --- Streaming-only branch (no tools or tools not supported) ---
		return a.streamResponse(ctx, messages, onChunk)
	}

	return fmt.Errorf("max tool iterations (%d) reached — possible loop", maxIterations)
}

// streamResponse sends a streaming chat request and pipes chunks to onChunk.
func (a *Agent) streamResponse(ctx context.Context, messages []llm.Message, onChunk func(string)) error {
	req := llm.ChatRequest{
		Model:       a.model,
		Messages:    messages,
		Temperature: a.temperature,
		MaxTokens:   a.maxTokens,
	}

	resp, err := a.provider.ChatStream(ctx, req, onChunk)
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}

	finalContent := resp.Message.Content
	a.history = append(a.history, llm.Message{Role: "assistant", Content: finalContent})

	// Auto-learn from streaming response.
	if a.learner != nil {
		lastUser := ""
		for i := len(a.history) - 2; i >= 0; i-- {
			if a.history[i].Role == "user" {
				lastUser = a.history[i].Content
				break
			}
		}
		if lastUser != "" {
			a.learner.LearnFromAssistantResponse(finalContent, lastUser)
		}
	}
	return nil
}

// executeToolCalls runs each tool call and appends results to history.
func (a *Agent) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCall, userMessage string) error {
	for _, tc := range toolCalls {
		if a.onToolCall != nil {
			a.onToolCall(tc.Function.Name, tc.Function.Arguments)
		}

		result, execErr := a.executeTool(ctx, tc.Function.Name, tc.Function.Arguments)
		if execErr != nil {
			result = fmt.Sprintf("Error: %v", execErr)
		}

		// Auto-learn from successful tool executions.
		if a.learner != nil && execErr == nil {
			a.learner.LearnFromToolCall(tc.Function.Name, tc.Function.Arguments, result, userMessage)
		}

		if a.onToolResult != nil {
			display := result
			if len(display) > 300 {
				display = display[:300] + "…"
			}
			a.onToolResult(tc.Function.Name, display)
		}

		a.history = append(a.history, llm.Message{
			Role:    "tool",
			Content: result,
		})
	}
	return nil
}

// executeTool dispatches a single tool call.
func (a *Agent) executeTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	handler, ok := a.registry.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return handler.Execute(ctx, args)
}

// ChatSimple streams a response without any tool calling.
func (a *Agent) ChatSimple(ctx context.Context, userMessage string, onChunk func(string)) error {
	a.history = append(a.history, llm.Message{Role: "user", Content: userMessage})
	messages := a.buildMessages()
	return a.streamResponse(ctx, messages, onChunk)
}

// Reset clears the conversation history.
func (a *Agent) Reset() {
	a.history = nil
}

// GetHistory returns the raw conversation history.
func (a *Agent) GetHistory() []llm.Message {
	return a.history
}

// HistoryJSON returns the conversation history as indented JSON.
func (a *Agent) HistoryJSON() string {
	data, err := json.MarshalIndent(a.history, "", "  ")
	if err != nil {
		return "error marshaling history"
	}
	return string(data)
}

// TokenEstimate returns a rough estimate of tokens used in history.
func (a *Agent) TokenEstimate() int {
	total := 0
	for _, m := range a.history {
		// ~4 chars per token is a rough heuristic
		total += len(m.Content) / 4
	}
	return total
}

// isToolUnsupportedError returns true when a provider rejects the tools parameter.
func isToolUnsupportedError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "does not support tools") ||
		(strings.Contains(msg, "tool") && strings.Contains(msg, "400")) ||
		strings.Contains(msg, "status 400") ||
		strings.Contains(msg, "tools_not_supported")
}

var toolBlockRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

// extractToolCallsFromText looks for JSON blocks in markdown that match a tool call schema.
// This acts as a fallback for smaller models that fail to use native tool calling.
func extractToolCallsFromText(content string, registry *tools.Registry) []llm.ToolCall {
	var calls []llm.ToolCall
	seen := make(map[string]bool)

	matches := toolBlockRegex.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		var parsed struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(match[1]), &parsed); err == nil {
			// Only extract if it's a known tool
			if _, ok := registry.Get(parsed.Name); ok {
				// Deduplicate identical tool calls (order-independent)
				normalizedBytes, _ := json.Marshal(parsed)
				callFingerprint := string(normalizedBytes)

				if !seen[callFingerprint] {
					seen[callFingerprint] = true
					calls = append(calls, llm.ToolCall{
						Function: llm.ToolFunction{
							Name:      parsed.Name,
							Arguments: parsed.Arguments,
						},
					})
				}
			}
		}
	}
	return calls
}
