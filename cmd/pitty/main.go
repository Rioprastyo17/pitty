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
	"github.com/pitty/pitty/internal/logger"
	"github.com/pitty/pitty/internal/memory"
	"github.com/pitty/pitty/internal/ollama"
	"github.com/pitty/pitty/internal/tools"
	"github.com/pitty/pitty/internal/ui"
)

const version = "0.2.0"

func main() {
	// ── CLI flags ──────────────────────────────────────────────────────────
	model := flag.String("model", "qwen2.5-coder:1.5b", "Ollama model to use")
	ollamaURL := flag.String("ollama-url", "http://127.0.0.1:11434", "Ollama API URL")
	temp := flag.Float64("temperature", 0.7, "Sampling temperature (0.0–2.0)")
	maxTokens := flag.Int("max-tokens", 8192, "Max tokens to generate")
	sysPromptFile := flag.String("system-prompt", "", "Path to a custom system prompt file")
	noTools := flag.Bool("no-tools", false, "Disable tool calling (simple chat mode)")
	importAGY := flag.Bool("import-agy", false, "Import knowledge from Antigravity CLI transcripts and exit")
	showVersion := flag.Bool("version", false, "Print version and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "pitty v%s — Local AI Coding Assistant\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  pitty [flags]          Start interactive chat\n")
		fmt.Fprintf(os.Stderr, "  pitty models           List available Ollama models\n")
		fmt.Fprintf(os.Stderr, "  pitty version          Show version\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()
	args := flag.Args()

	// ── Logger ─────────────────────────────────────────────────────────────
	logPath, logErr := logger.Init(logger.LevelDebug)
	defer logger.Close()
	// We'll report logErr after the UI is ready (below).
	_ = logPath
	_ = logErr

	// ── Version flag ───────────────────────────────────────────────────────
	if *showVersion {
		fmt.Printf("pitty v%s\n", version)
		return
	}

	client := ollama.NewClient(*ollamaURL)
	t := ui.NewTerminalUI()

	// ── Subcommands ────────────────────────────────────────────────────────
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
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
			flag.Usage()
			os.Exit(1)
		}
	}

	// ── --import-agy ──────────────────────────────────────────────────────
	if *importAGY {
		cmdImportAGY(t)
		return
	}

	// ── Interactive mode ───────────────────────────────────────────────────
	runInteractive(client, t, *model, *ollamaURL, *temp, *maxTokens, *sysPromptFile, *noTools)
}

// cmdModels lists all models available in Ollama.
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

// cmdImportAGY imports knowledge from Antigravity CLI transcripts.
func cmdImportAGY(t *ui.TerminalUI) {
	store, err := memory.NewStore()
	if err != nil {
		t.PrintError(fmt.Errorf("failed to init memory: %w", err))
		return
	}
	t.PrintInfo("Importing knowledge from Antigravity CLI transcripts…")
	count, err := memory.ImportFromAntigravity(store)
	if err != nil {
		t.PrintError(fmt.Errorf("import error: %w", err))
		return
	}
	t.PrintSuccess(fmt.Sprintf("Imported %d knowledge entries from Antigravity CLI", count))
	t.PrintInfo(fmt.Sprintf("Total knowledge: %d entries", store.Count()))
}

// runInteractive is the main REPL loop.
func runInteractive(client *ollama.Client, t *ui.TerminalUI, model, ollamaURL string, temp float64, maxTokens int, sysPromptFile string, noTools bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Show log file path if logger was initialized
	if lp := logger.FilePath(); lp != "" {
		t.PrintInfo(fmt.Sprintf("  📋 Log: %s", lp))
	}

	// ── Check Ollama connectivity ──────────────────────────────────────────
	if err := client.Ping(ctx); err != nil {
		logger.Error("Ollama connectivity check failed: %v", err)
		t.PrintError(err)
		t.PrintInfo("Make sure Ollama is running: ollama serve")
		os.Exit(1)
	}
	logger.Info("Connected to Ollama at %s, model=%s", ollamaURL, model)

	// ── Memory system ──────────────────────────────────────────────────────
	store, err := memory.NewStore()
	if err != nil {
		t.PrintWarning(fmt.Sprintf("Memory system unavailable: %v", err))
	}
	var learner *memory.Learner
	if store != nil {
		learner = memory.NewLearner(store)
		// Auto-import from Antigravity on first run
		if store.Count() == 0 {
			count, _ := memory.ImportFromAntigravity(store)
			if count > 0 {
				t.PrintInfo(fmt.Sprintf("  📚 Auto-imported %d entries from Antigravity CLI", count))
			}
		}
	}

	// ── Tool registry ──────────────────────────────────────────────────────
	registry := tools.NewRegistry()
	if !noTools {
		registry.Register(&tools.ReadFileTool{})
		registry.Register(&tools.WriteFileTool{})
		registry.Register(&tools.EditFileTool{})
		registry.Register(&tools.RunCommandTool{})
		registry.Register(&tools.SearchFilesTool{})
		registry.Register(&tools.ListDirTool{})
		registry.Register(&tools.WebSearchTool{})
	}

	// ── Agent ──────────────────────────────────────────────────────────────
	ag := agent.NewAgent(client, registry, model, temp, maxTokens)
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

	// Tool display callbacks
	ag.OnToolCall(func(name string, args map[string]interface{}) {
		t.StopThinking()
		t.PrintToolCall(name, args)
	})
	ag.OnToolResult(func(name string, result string) {
		t.PrintToolResult(name, result)
		t.StartThinking()
	})

	// ── Welcome banner ─────────────────────────────────────────────────────
	memCount := 0
	if store != nil {
		memCount = store.Count()
	}
	t.PrintWelcome(model, ollamaURL, memCount)

	// ── Signal handling ────────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		t.StopThinking()
		logger.Info("Received interrupt signal — exiting")
		if store != nil {
			t.PrintInfo(fmt.Sprintf("💾 Knowledge saved: %d entries", store.Count()))
		}
		t.PrintInfo("Goodbye! 👋")
		cancel()
		os.Exit(0)
	}()

	// ── REPL ───────────────────────────────────────────────────────────────
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
			handleCommand(ctx, input, ag, t, &model, client, store, learner, noTools)
			continue
		}

		// ── Chat ───────────────────────────────────────────────────────────
		t.StartThinking()
		firstChunk := true

		onChunk := func(chunk string) {
			if firstChunk {
				t.StopThinking()
				t.PrintStreamStart()
				firstChunk = false
			}
			t.PrintStreaming(chunk)
		}

		var chatErr error
		if noTools {
			chatErr = ag.ChatSimple(ctx, input, onChunk)
		} else {
			chatErr = ag.Chat(ctx, input, onChunk)
		}

		t.StopThinking()
		if !firstChunk {
			t.PrintStreamEnd()
		}
		if chatErr != nil {
			t.PrintError(chatErr)
		}
	}
}

// handleCommand processes a slash command entered by the user.
func handleCommand(ctx context.Context, input string, ag *agent.Agent, t *ui.TerminalUI, model *string, client *ollama.Client, store *memory.Store, learner *memory.Learner, noTools bool) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return
	}
	cmd := parts[0]

	switch cmd {
	// ── Exit ────────────────────────────────────────────────────────────────
	case "/exit", "/quit", "/q":
		if store != nil {
			t.PrintInfo(fmt.Sprintf("💾 Knowledge saved: %d entries", store.Count()))
		}
		t.PrintInfo("Goodbye! 👋")
		os.Exit(0)

	// ── Clear conversation ──────────────────────────────────────────────────
	case "/clear", "/reset":
		ag.Reset()
		t.PrintSuccess("Conversation history cleared.")

	// ── Switch model ────────────────────────────────────────────────────────
	case "/model":
		if len(parts) < 2 {
			t.PrintInfo("Current model: " + *model)
			t.PrintInfo("Usage: /model <name>")
			return
		}
		*model = parts[1]
		ag.SetModel(parts[1])
		t.PrintSuccess("Model switched to: " + parts[1])

	// ── List models ─────────────────────────────────────────────────────────
	case "/models":
		models, err := client.ListModels(ctx)
		if err != nil {
			t.PrintError(err)
			return
		}
		t.PrintInfo("Available models:")
		for _, m := range models {
			marker := " "
			if m.Name == *model {
				marker = "●"
			}
			fmt.Printf("  %s %s\n", marker, m.Name)
		}

	// ── Learn a fact ────────────────────────────────────────────────────────
	case "/learn":
		if len(parts) < 2 {
			t.PrintInfo("Usage: /learn <something pitty should remember>")
			t.PrintInfo("Example: /learn selalu gunakan bahasa Indonesia untuk menjawab")
			return
		}
		fact := strings.Join(parts[1:], " ")
		if learner != nil {
			learner.LearnFact(fact, "user taught via /learn", []string{"user", "explicit"})
			t.PrintSuccess("Learned: " + fact)
		} else {
			t.PrintWarning("Memory system not available")
		}

	// ── Memory status / search ──────────────────────────────────────────────
	case "/memory":
		if store == nil {
			t.PrintWarning("Memory system not available")
			return
		}
		if len(parts) >= 3 && parts[1] == "search" {
			query := strings.Join(parts[2:], " ")
			entries := store.Search(query, 10)
			if len(entries) == 0 {
				t.PrintInfo("No matching memories found.")
			} else {
				t.PrintInfo(fmt.Sprintf("Found %d matching entries:", len(entries)))
				for _, e := range entries {
					fmt.Printf("  [%s] %s \033[90m(from %s)\033[0m\n", e.Type, e.Content, e.Source)
				}
			}
			return
		}
		t.PrintInfo(fmt.Sprintf("📚 Knowledge base: %d entries", store.Count()))
		recent := store.GetRecent(5)
		if len(recent) > 0 {
			t.PrintInfo("Recent learnings:")
			for _, e := range recent {
				fmt.Printf("  [%s] %s \033[90m(from %s)\033[0m\n", e.Type, e.Content, e.Source)
			}
		}
		fmt.Println()
		t.PrintInfo("Usage: /memory search <query>")

	// ── Import from Antigravity ─────────────────────────────────────────────
	case "/import":
		if store == nil {
			t.PrintWarning("Memory system not available")
			return
		}
		t.PrintInfo("Importing from Antigravity CLI…")
		count, err := memory.ImportFromAntigravity(store)
		if err != nil {
			t.PrintError(err)
		} else {
			t.PrintSuccess(fmt.Sprintf("Imported %d new entries (total: %d)", count, store.Count()))
		}

	// ── History ─────────────────────────────────────────────────────────────
	case "/history":
		t.PrintInfo("Conversation history (JSON):")
		fmt.Println(ag.HistoryJSON())

	// ── Token estimate ──────────────────────────────────────────────────────
	case "/tokens":
		est := ag.TokenEstimate()
		t.PrintInfo(fmt.Sprintf("Estimated tokens in context: ~%d", est))
		if est > 6000 {
			t.PrintWarning("Context is getting large. Consider /compact or /clear.")
		}

	// ── Compact conversation ────────────────────────────────────────────────
	case "/compact":
		history := ag.GetHistory()
		if len(history) == 0 {
			t.PrintInfo("No conversation to compact.")
			return
		}
		// Build a summary prompt
		var sb strings.Builder
		sb.WriteString("Summarize the following conversation into a concise bullet-point list of key decisions, code changes, and facts. Be brief:\n\n")
		for _, msg := range history {
			if msg.Role == "system" {
				continue
			}
			sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, msg.Content))
		}
		t.PrintInfo("Compacting conversation…")
		t.StartThinking()

		firstChunk := true
		var summary strings.Builder
		onChunk := func(chunk string) {
			if firstChunk {
				t.StopThinking()
				t.PrintStreamStart()
				firstChunk = false
			}
			t.PrintStreaming(chunk)
			summary.WriteString(chunk)
		}

		ag.Reset()
		var chatErr error
		if noTools {
			chatErr = ag.ChatSimple(ctx, sb.String(), onChunk)
		} else {
			chatErr = ag.Chat(ctx, sb.String(), onChunk)
		}
		t.StopThinking()
		if !firstChunk {
			t.PrintStreamEnd()
		}
		if chatErr != nil {
			t.PrintError(chatErr)
			return
		}
		// Reset and inject compact summary as initial context
		ag.Reset()
		if learner != nil && summary.Len() > 0 {
			learner.LearnFact("Conversation summary: "+summary.String(), "compact command", []string{"summary", "compact"})
		}
		t.PrintSuccess("Conversation compacted. History cleared and summary saved to memory.")

	// ── Help ────────────────────────────────────────────────────────────────
	case "/help":
		t.PrintHelp()

	// ── Unknown ─────────────────────────────────────────────────────────────
	default:
		t.PrintInfo(fmt.Sprintf("Unknown command: %s  (type /help for available commands)", cmd))
	}
}
