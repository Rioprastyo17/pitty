package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/pitty/pitty/internal/agent"
	"github.com/pitty/pitty/internal/memory"
	"github.com/pitty/pitty/internal/ollama"
	"github.com/pitty/pitty/internal/tools"
	"github.com/pitty/pitty/internal/ui"
)

const version = "0.1.0"

var memStore *memory.Store

func main() {
	// Flags
	model := flag.String("model", "qwen2.5-coder:1.5b", "Ollama model to use")
	ollamaURL := flag.String("ollama-url", "http://localhost:11434", "Ollama API URL")
	temp := flag.Float64("temperature", 0.7, "Temperature for generation")
	maxTokens := flag.Int("max-tokens", 4096, "Max tokens to generate")
	sysPromptFile := flag.String("system-prompt", "", "Path to custom system prompt file")
	noTools := flag.Bool("no-tools", false, "Disable tool calling (simple chat mode)")
	importAGY := flag.Bool("import-agy", false, "Import knowledge from Antigravity CLI transcripts")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "pitty v%s - Local AI Coding Assistant\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  pitty [flags]              Start interactive chat\n")
		fmt.Fprintf(os.Stderr, "  pitty models               List available Ollama models\n")
		fmt.Fprintf(os.Stderr, "  pitty version              Show version\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()
	args := flag.Args()

	client := ollama.NewClient(*ollamaURL)
	t := ui.NewTerminalUI()

	// Handle subcommands
	if len(args) > 0 {
		switch args[0] {
		case "version":
			fmt.Printf("pitty v%s\n", version)
			return
		case "models":
			cmdModels(client, t)
			return
		case "help":
			flag.Usage()
			return
		}
	}

	// Handle --import-agy flag
	if *importAGY {
		cmdImportAGY(t)
		return
	}

	// Interactive mode
	runInteractive(client, t, *model, *ollamaURL, *temp, *maxTokens, *sysPromptFile, *noTools)
}

func cmdModels(client *ollama.Client, t *ui.TerminalUI) {
	ctx := context.Background()
	models, err := client.ListModels(ctx)
	if err != nil {
		t.PrintError(fmt.Errorf("failed to list models: %w", err))
		os.Exit(1)
	}

	t.PrintInfo("Available Ollama models:")
	t.PrintInfo("")
	for _, m := range models {
		size := ""
		if m.Details.ParameterSize != "" {
			size = fmt.Sprintf(" (%s, %s)", m.Details.ParameterSize, m.Details.QuantizationLevel)
		}
		fmt.Printf("  • %s%s\n", m.Name, size)
	}
}

func cmdImportAGY(t *ui.TerminalUI) {
	store, err := memory.NewStore()
	if err != nil {
		t.PrintError(fmt.Errorf("failed to init memory: %w", err))
		return
	}

	t.PrintInfo("Importing knowledge from Antigravity CLI transcripts...")
	count, err := memory.ImportFromAntigravity(store)
	if err != nil {
		t.PrintError(fmt.Errorf("import error: %w", err))
		return
	}

	t.PrintSuccess(fmt.Sprintf("Imported %d knowledge entries from Antigravity CLI", count))
	t.PrintInfo(fmt.Sprintf("Total knowledge: %d entries", store.Count()))
}

func runInteractive(client *ollama.Client, t *ui.TerminalUI, model, ollamaURL string, temp float64, maxTokens int, sysPromptFile string, noTools bool) {
	// Check Ollama connectivity
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.PrintError(err)
		t.PrintInfo("Make sure Ollama is running: ollama serve")
		os.Exit(1)
	}

	// Initialize memory system
	store, err := memory.NewStore()
	if err != nil {
		t.PrintWarning(fmt.Sprintf("Memory system unavailable: %v", err))
	}
	memStore = store

	var learner *memory.Learner
	if store != nil {
		learner = memory.NewLearner(store)

		// Auto-import from Antigravity on first run (if no existing knowledge)
		if store.Count() == 0 {
			count, _ := memory.ImportFromAntigravity(store)
			if count > 0 {
				t.PrintInfo(fmt.Sprintf("  📚 Imported %d entries from Antigravity CLI", count))
			}
		}
	}

	// Setup tool registry
	registry := tools.NewRegistry()
	if !noTools {
		registry.Register(&tools.ReadFileTool{})
		registry.Register(&tools.WriteFileTool{})
		registry.Register(&tools.EditFileTool{})
		registry.Register(&tools.RunCommandTool{})
		registry.Register(&tools.SearchFilesTool{})
		registry.Register(&tools.ListDirTool{})
	}

	// Create agent
	ag := agent.NewAgent(client, registry, model, temp, maxTokens)

	// Attach memory
	if store != nil && learner != nil {
		ag.SetMemory(store, learner)
	}

	// Custom system prompt
	if sysPromptFile != "" {
		data, err := os.ReadFile(sysPromptFile)
		if err != nil {
			t.PrintError(fmt.Errorf("failed to read system prompt: %w", err))
			os.Exit(1)
		}
		ag.SetSystemPrompt(string(data))
	}

	// Set tool callbacks for display
	ag.OnToolCall(func(name string, args map[string]interface{}) {
		t.StopThinking()
		t.PrintToolCall(name, args)
	})
	ag.OnToolResult(func(name string, result string) {
		t.PrintToolResult(name, result)
		t.StartThinking()
	})

	// Print welcome
	t.PrintWelcome(model, ollamaURL)
	if store != nil && store.Count() > 0 {
		t.PrintInfo(fmt.Sprintf("  📚 Memory: %d learned entries", store.Count()))
		fmt.Println()
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		t.PrintInfo("Interrupted. Goodbye!")
		cancel()
		os.Exit(0)
	}()

	// REPL loop
	for {
		input, err := t.ReadInput()
		if err != nil {
			break
		}

		if input == "" {
			continue
		}

		// Handle slash commands
		if strings.HasPrefix(input, "/") {
			if handleCommand(input, ag, t, &model, client, store, learner) {
				continue
			}
		}

		// Chat with agent
		t.StartThinking()
		firstChunk := true
		streamCallback := func(chunk string) {
			if firstChunk {
				t.StopThinking()
				t.PrintStreamStart()
				firstChunk = false
			}
			t.PrintStreaming(chunk)
		}

		if noTools {
			err = ag.ChatSimple(ctx, input, streamCallback)
		} else {
			err = ag.Chat(ctx, input, streamCallback)
		}
		t.StopThinking()
		if !firstChunk {
			t.PrintStreamEnd()
		}

		if err != nil {
			t.PrintError(err)
		}
	}
}

// handleCommand processes slash commands. Returns true if handled.
func handleCommand(input string, ag *agent.Agent, t *ui.TerminalUI, model *string, client *ollama.Client, store *memory.Store, learner *memory.Learner) bool {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/exit", "/quit", "/q":
		if store != nil {
			t.PrintInfo(fmt.Sprintf("💾 Knowledge saved: %d entries", store.Count()))
		}
		t.PrintInfo("Goodbye! 👋")
		os.Exit(0)

	case "/clear", "/reset":
		ag.Reset()
		t.PrintSuccess("Conversation history cleared.")

	case "/model":
		if len(parts) < 2 {
			t.PrintInfo("Current model: " + *model)
			t.PrintInfo("Usage: /model <name>")
			return true
		}
		newModel := parts[1]
		*model = newModel
		ag.SetModel(newModel)
		t.PrintSuccess("Model switched to: " + newModel)

	case "/models":
		ctx := context.Background()
		models, err := client.ListModels(ctx)
		if err != nil {
			t.PrintError(err)
			return true
		}
		t.PrintInfo("Available models:")
		for _, m := range models {
			fmt.Printf("  • %s\n", m.Name)
		}

	case "/learn":
		if len(parts) < 2 {
			t.PrintInfo("Usage: /learn <something pitty should remember>")
			t.PrintInfo("Example: /learn selalu gunakan bahasa Indonesia untuk menjawab")
			return true
		}
		fact := strings.Join(parts[1:], " ")
		if learner != nil {
			learner.LearnFact(fact, "user taught via /learn", []string{"user", "explicit"})
			t.PrintSuccess("Learned: " + fact)
		} else {
			t.PrintWarning("Memory system not available")
		}

	case "/memory":
		if store == nil {
			t.PrintWarning("Memory system not available")
			return true
		}
		if len(parts) >= 2 && parts[1] == "search" && len(parts) >= 3 {
			query := strings.Join(parts[2:], " ")
			entries := store.Search(query, 10)
			if len(entries) == 0 {
				t.PrintInfo("No matching memories found.")
			} else {
				t.PrintInfo(fmt.Sprintf("Found %d matching entries:", len(entries)))
				for _, e := range entries {
					fmt.Printf("  [%s] %s %s(from %s)%s\n", e.Type, e.Content, "\033[90m", e.Source, "\033[0m")
				}
			}
		} else {
			t.PrintInfo(fmt.Sprintf("📚 Knowledge base: %d entries", store.Count()))
			recent := store.GetRecent(5)
			if len(recent) > 0 {
				t.PrintInfo("Recent learnings:")
				for _, e := range recent {
					fmt.Printf("  [%s] %s %s(from %s)%s\n", e.Type, e.Content, "\033[90m", e.Source, "\033[0m")
				}
			}
			fmt.Println()
			t.PrintInfo("Usage: /memory search <query>")
		}

	case "/import":
		if store == nil {
			t.PrintWarning("Memory system not available")
			return true
		}
		t.PrintInfo("Importing from Antigravity CLI...")
		count, err := memory.ImportFromAntigravity(store)
		if err != nil {
			t.PrintError(err)
		} else {
			t.PrintSuccess(fmt.Sprintf("Imported %d new entries (total: %d)", count, store.Count()))
		}

	case "/history":
		t.PrintInfo("Conversation history:")
		fmt.Println(ag.HistoryJSON())

	case "/help":
		t.PrintInfo("Available commands:")
		t.PrintInfo("  /help              Show this help")
		t.PrintInfo("  /exit, /quit, /q   Exit pitty")
		t.PrintInfo("  /clear, /reset     Clear conversation history")
		t.PrintInfo("  /model <name>      Switch model")
		t.PrintInfo("  /models            List available models")
		t.PrintInfo("  /learn <text>      Teach pitty something to remember")
		t.PrintInfo("  /memory            Show knowledge base status")
		t.PrintInfo("  /memory search <q> Search knowledge base")
		t.PrintInfo("  /import            Import from Antigravity CLI")
		t.PrintInfo("  /history           Show conversation history")
		t.PrintInfo("")
		t.PrintInfo("Tips:")
		t.PrintInfo("  Use \\ at end of line for multiline input")
		t.PrintInfo("  Ctrl+C to interrupt")
		t.PrintInfo("  pitty auto-learns from your interactions 🧠")

	default:
		t.PrintInfo("Unknown command: " + cmd + ". Type /help for available commands.")
	}
	return true
}
