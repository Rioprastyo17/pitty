package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pitty/pitty/internal/memory"
	"github.com/pitty/pitty/internal/ollama"
	"github.com/pitty/pitty/internal/tools"
)

// Agent orchestrates chat, tool calling, memory, and streaming.
type Agent struct {
	client         *ollama.Client
	registry       *tools.Registry
	model          string
	temperature    float64
	maxTokens      int
	systemPrompt   string
	history        []ollama.Message
	onToolCall     func(name string, args map[string]interface{})
	onToolResult   func(name string, result string)
	memStore       *memory.Store
	learner        *memory.Learner
	toolsSupported bool // tracks whether the current model supports tool calling
}

// NewAgent creates a new Agent with tool support enabled by default.
func NewAgent(client *ollama.Client, registry *tools.Registry, model string, temp float64, maxTokens int) *Agent {
	return &Agent{
		client:         client,
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

// OnToolCall sets a callback triggered before a tool executes.
func (a *Agent) OnToolCall(fn func(name string, args map[string]interface{})) {
	a.onToolCall = fn
}

// OnToolResult sets a callback triggered after a tool returns.
func (a *Agent) OnToolResult(fn func(name string, result string)) {
	a.onToolResult = fn
}

// buildMessages assembles the full message list: system prompt + memory context + history.
func (a *Agent) buildMessages() []ollama.Message {
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

	msgs := make([]ollama.Message, 0, len(a.history)+1)
	msgs = append(msgs, ollama.Message{Role: "system", Content: sysPrompt})
	msgs = append(msgs, a.history...)
	return msgs
}

// Chat processes a user message with the full tool-calling agentic loop.
// onChunk is called for each streamed text chunk of the final response.
func (a *Agent) Chat(ctx context.Context, userMessage string, onChunk func(string)) error {
	// Auto-learn from the user's message.
	if a.learner != nil {
		a.learner.LearnFromUserMessage(userMessage)
	}

	a.history = append(a.history, ollama.Message{Role: "user", Content: userMessage})

	const maxIterations = 15
	for i := 0; i < maxIterations; i++ {
		messages := a.buildMessages()

		// --- Tool-calling branch ---
		if a.toolsSupported && a.registry.Len() > 0 {
			var fullContent strings.Builder
			var toolCalls []ollama.ToolCall

			err := a.client.ChatStream(ctx, ollama.ChatRequest{
				Model:    a.model,
				Messages: messages,
				Tools:    a.registry.ToOllamaTools(),
				Options: &ollama.ChatOptions{
					Temperature: a.temperature,
					NumPredict:  a.maxTokens,
				},
			}, func(chunk ollama.ChatStreamChunk) {
				if chunk.Message.Content != "" {
					onChunk(chunk.Message.Content)
					fullContent.WriteString(chunk.Message.Content)
				}
				if len(chunk.Message.ToolCalls) > 0 {
					toolCalls = chunk.Message.ToolCalls
				}
			})

			if err != nil {
				if isToolUnsupportedError(err) {
					// Model doesn't support tools — fall back to streaming forever.
					a.toolsSupported = false
					return a.streamResponse(ctx, messages, onChunk)
				}
				return fmt.Errorf("chat: %w", err)
			}

			finalContent := fullContent.String()

			// If the model returned tool calls, execute them and loop.
			if len(toolCalls) > 0 {
				a.history = append(a.history, ollama.Message{
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
				a.history = append(a.history, ollama.Message{
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
func (a *Agent) streamResponse(ctx context.Context, messages []ollama.Message, onChunk func(string)) error {
	var full strings.Builder

	err := a.client.ChatStream(ctx, ollama.ChatRequest{
		Model:    a.model,
		Messages: messages,
		Options: &ollama.ChatOptions{
			Temperature: a.temperature,
			NumPredict:  a.maxTokens,
		},
	}, func(chunk ollama.ChatStreamChunk) {
		onChunk(chunk.Message.Content)
		full.WriteString(chunk.Message.Content)
	})
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}

	finalContent := full.String()
	a.history = append(a.history, ollama.Message{Role: "assistant", Content: finalContent})
	
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
func (a *Agent) executeToolCalls(ctx context.Context, toolCalls []ollama.ToolCall, userMessage string) error {
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

		a.history = append(a.history, ollama.Message{
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
	a.history = append(a.history, ollama.Message{Role: "user", Content: userMessage})
	messages := a.buildMessages()
	return a.streamResponse(ctx, messages, onChunk)
}

// Reset clears the conversation history.
func (a *Agent) Reset() {
	a.history = nil
}

// GetHistory returns the raw conversation history.
func (a *Agent) GetHistory() []ollama.Message {
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

// isToolUnsupportedError returns true when Ollama rejects the tools parameter.
func isToolUnsupportedError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "does not support tools") ||
		(strings.Contains(msg, "tool") && strings.Contains(msg, "400")) ||
		strings.Contains(msg, "status 400")
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

