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

// Agent is the main agent that orchestrates chat, tool calling, and streaming.
type Agent struct {
	client        *ollama.Client
	registry      *tools.Registry
	model         string
	temperature   float64
	maxTokens     int
	systemPrompt  string
	history       []ollama.Message
	onToolCall    func(name string, args map[string]interface{})
	onToolResult  func(name string, result string)
	memStore      *memory.Store
	learner       *memory.Learner
	toolsDisabled bool
}

// NewAgent creates a new Agent.
func NewAgent(client *ollama.Client, registry *tools.Registry, model string, temp float64, maxTokens int) *Agent {
	return &Agent{
		client:       client,
		registry:     registry,
		model:        model,
		temperature:  temp,
		maxTokens:    maxTokens,
		systemPrompt: DefaultSystemPrompt,
	}
}

// SetMemory attaches a memory store and learner to the agent.
func (a *Agent) SetMemory(store *memory.Store, learner *memory.Learner) {
	a.memStore = store
	a.learner = learner
}

// SetSystemPrompt overrides the default system prompt.
func (a *Agent) SetSystemPrompt(prompt string) {
	a.systemPrompt = prompt
}

// SetModel changes the model.
func (a *Agent) SetModel(model string) {
	a.model = model
}

// OnToolCall sets a callback for when a tool is called.
func (a *Agent) OnToolCall(fn func(name string, args map[string]interface{})) {
	a.onToolCall = fn
}

// OnToolResult sets a callback for when a tool returns a result.
func (a *Agent) OnToolResult(fn func(name string, result string)) {
	a.onToolResult = fn
}

// buildMessages builds the full message array with system prompt + memory context.
func (a *Agent) buildMessages() []ollama.Message {
	sysPrompt := a.systemPrompt

	// Inject relevant memory context if available
	if a.memStore != nil && len(a.history) > 0 {
		// Use the last user message as query for relevant memories
		lastMsg := ""
		for i := len(a.history) - 1; i >= 0; i-- {
			if a.history[i].Role == "user" {
				lastMsg = a.history[i].Content
				break
			}
		}
		if lastMsg != "" {
			memContext := a.memStore.FormatForPrompt(lastMsg)
			if memContext != "" {
				sysPrompt += "\n" + memContext
			}
		}
	}

	msgs := make([]ollama.Message, 0, len(a.history)+1)
	msgs = append(msgs, ollama.Message{
		Role:    "system",
		Content: sysPrompt,
	})
	msgs = append(msgs, a.history...)
	return msgs
}

// Chat processes a user message through the agent loop with tool calling.
// It streams the final response via the onChunk callback.
func (a *Agent) Chat(ctx context.Context, userMessage string, onChunk func(string)) error {
	// Auto-learn from user message
	if a.learner != nil {
		a.learner.LearnFromUserMessage(userMessage)
	}

	a.history = append(a.history, ollama.Message{
		Role:    "user",
		Content: userMessage,
	})

	ollamaTools := a.registry.ToOllamaTools()

	// If tools were previously disabled for this model, skip tool calling
	if a.toolsDisabled {
		ollamaTools = nil
	}

	for iteration := 0; iteration < 15; iteration++ {
		messages := a.buildMessages()

		// If we have tools, first try a non-streaming call to check for tool calls
		if len(ollamaTools) > 0 {
			req := ollama.ChatRequest{
				Model:    a.model,
				Messages: messages,
				Tools:    ollamaTools,
				Options: &ollama.ChatOptions{
					Temperature: a.temperature,
					NumPredict:  a.maxTokens,
				},
			}

			resp, err := a.client.Chat(ctx, req)
			if err != nil {
				// Auto-detect "does not support tools" and fallback
				if strings.Contains(err.Error(), "does not support tools") ||
					strings.Contains(err.Error(), "status 400") {
					a.toolsDisabled = true
					ollamaTools = nil
					// Fall through to streaming mode below
					goto streamFallback
				}
				return fmt.Errorf("chat request: %w", err)
			}

			// Check if there are tool calls
			if len(resp.Message.ToolCalls) > 0 {
				// Add assistant message with tool calls to history
				a.history = append(a.history, resp.Message)

				// Execute each tool call
				for _, tc := range resp.Message.ToolCalls {
					if a.onToolCall != nil {
						a.onToolCall(tc.Function.Name, tc.Function.Arguments)
					}

					result, err := a.executeTool(ctx, tc.Function.Name, tc.Function.Arguments)
					if err != nil {
						result = fmt.Sprintf("Error: %v", err)
					}

					// Auto-learn from tool calls
					if a.learner != nil && err == nil {
						a.learner.LearnFromToolCall(tc.Function.Name, tc.Function.Arguments, result, userMessage)
					}

					if a.onToolResult != nil {
						displayResult := result
						if len(displayResult) > 200 {
							displayResult = displayResult[:200] + "..."
						}
						a.onToolResult(tc.Function.Name, displayResult)
					}

					// Add tool result to history
					a.history = append(a.history, ollama.Message{
						Role:    "tool",
						Content: result,
					})
				}
				// Loop back to get next response
				continue
			}

			// No tool calls - this is the final text response
			if resp.Message.Content != "" {
				a.history = append(a.history, ollama.Message{
					Role:    "assistant",
					Content: resp.Message.Content,
				})
				onChunk(resp.Message.Content)
				return nil
			}
		}

	streamFallback:
		// No tools or fallback: stream the response
		var fullResponse strings.Builder
		req := ollama.ChatRequest{
			Model:    a.model,
			Messages: messages,
			Options: &ollama.ChatOptions{
				Temperature: a.temperature,
				NumPredict:  a.maxTokens,
			},
		}

		err := a.client.ChatStream(ctx, req, func(chunk ollama.ChatStreamChunk) {
			onChunk(chunk.Message.Content)
			fullResponse.WriteString(chunk.Message.Content)
		})
		if err != nil {
			return fmt.Errorf("stream chat: %w", err)
		}

		a.history = append(a.history, ollama.Message{
			Role:    "assistant",
			Content: fullResponse.String(),
		})
		return nil
	}

	return fmt.Errorf("max tool iterations (15) reached")
}

// executeTool runs a single tool by name.
func (a *Agent) executeTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	handler, ok := a.registry.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return handler.Execute(ctx, args)
}

// ChatSimple processes a user message without tool calling, streaming only.
func (a *Agent) ChatSimple(ctx context.Context, userMessage string, onChunk func(string)) error {
	a.history = append(a.history, ollama.Message{
		Role:    "user",
		Content: userMessage,
	})

	messages := a.buildMessages()
	var fullResponse strings.Builder

	req := ollama.ChatRequest{
		Model:    a.model,
		Messages: messages,
		Options: &ollama.ChatOptions{
			Temperature: a.temperature,
			NumPredict:  a.maxTokens,
		},
	}

	err := a.client.ChatStream(ctx, req, func(chunk ollama.ChatStreamChunk) {
		onChunk(chunk.Message.Content)
		fullResponse.WriteString(chunk.Message.Content)
	})
	if err != nil {
		return err
	}

	a.history = append(a.history, ollama.Message{
		Role:    "assistant",
		Content: fullResponse.String(),
	})
	return nil
}

// Reset clears the conversation history.
func (a *Agent) Reset() {
	a.history = nil
}

// GetHistory returns the conversation history.
func (a *Agent) GetHistory() []ollama.Message {
	return a.history
}

// HistoryJSON returns the conversation history as formatted JSON.
func (a *Agent) HistoryJSON() string {
	data, err := json.MarshalIndent(a.history, "", "  ")
	if err != nil {
		return "error marshaling history"
	}
	return string(data)
}
