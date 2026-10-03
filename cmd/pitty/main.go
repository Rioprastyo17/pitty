package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pitty/pitty/internal/agent"
	"github.com/pitty/pitty/internal/llm"
	"github.com/pitty/pitty/internal/llm/anthropic"
	"github.com/pitty/pitty/internal/llm/gemini"
	"github.com/pitty/pitty/internal/llm/llamacpp"
	"github.com/pitty/pitty/internal/llm/openai"
	"github.com/pitty/pitty/internal/logger"
	"github.com/pitty/pitty/internal/memory"
	"github.com/pitty/pitty/internal/tools"
	"github.com/pitty/pitty/internal/ui"
)

const version = "0.3.0"

// ── Provider name constants ───────────────────────────────────────────────────

const (
	providerLlamaCpp  = "llamacpp"
	providerOpenAI    = "openai"
	providerAnthropic = "anthropic"
	providerGemini    = "gemini"
	providerGroq      = "groq"
	providerTogether  = "together"
	providerMistral   = "mistral"
	providerDeepSeek  = "deepseek"
)

func main() {
	// ── CLI flags ──────────────────────────────────────────────────────────
	providerFlag := flag.String("provider", providerLlamaCpp,
		"AI provider: llamacpp | openai | anthropic | gemini | groq | together | mistral | deepseek")
	model := flag.String("model", "",
		"Model to use (default depends on provider)")
	apiURL := flag.String("url", "",
		"API server URL (auto-detected per provider; override for custom endpoints)")
	apiKey := flag.String("api-key", "",
		"API key (falls back to env: OPENAI_API_KEY, ANTHROPIC_API_KEY, GEMINI_API_KEY, GROQ_API_KEY, …)")
	temp := flag.Float64("temperature", 0.7, "Sampling temperature (0.0–2.0)")
	// Default 2048: aman untuk RAM tersisa ~3GB di i7-4600M CPU-only
	maxTokens := flag.Int("max-tokens", 2048, "Max tokens to generate (kurangi jika RAM terbatas)")
	// 4 threads = optimal untuk i7-4600M (2 core / 4 logical thread)
	threads       := flag.Int("threads", 4, "CPU threads untuk llama.cpp inference (0 = biarkan server menentukan)")
	sysPromptFile := flag.String("system-prompt", "", "Path to a custom system prompt file")
	noTools       := flag.Bool("no-tools", false, "Disable tool calling (simple chat mode)")
	importAGY     := flag.Bool("import-agy", false, "Import knowledge from Antigravity CLI transcripts and exit")
	showVersion   := flag.Bool("version", false, "Print version and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "pitty v%s — AI Coding Assistant\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  pitty [flags]          Start interactive chat\n")
		fmt.Fprintf(os.Stderr, "  pitty models           List available models\n")
		fmt.Fprintf(os.Stderr, "  pitty providers        List supported providers\n")
		fmt.Fprintf(os.Stderr, "  pitty version          Show version\n\n")
		fmt.Fprintf(os.Stderr, "Quick start examples:\n")
		fmt.Fprintf(os.Stderr, "  # Lokal (i7-4600M CPU-only, ~3GB RAM tersisa):\n")
		fmt.Fprintf(os.Stderr, "  pitty                                           # llamacpp default\n")
		fmt.Fprintf(os.Stderr, "  pitty -threads 4 -max-tokens 1024              # hemat RAM ekstra\n\n")
		fmt.Fprintf(os.Stderr, "  # Cloud API (bebas spek laptop):\n")
		fmt.Fprintf(os.Stderr, "  pitty -provider openai    -model gpt-4o\n")
		fmt.Fprintf(os.Stderr, "  pitty -provider anthropic -model claude-sonnet-4-5\n")
		fmt.Fprintf(os.Stderr, "  pitty -provider gemini    -model gemini-2.0-flash\n")
		fmt.Fprintf(os.Stderr, "  pitty -provider groq      -model llama-3.3-70b-versatile\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()
	args := flag.Args()

	// ── Logger ─────────────────────────────────────────────────────────────
	logPath, logErr := logger.Init(logger.LevelDebug)
	defer logger.Close()
	_ = logPath
	_ = logErr

	// ── Version flag ───────────────────────────────────────────────────────
	if *showVersion {
		fmt.Printf("pitty v%s\n", version)
		return
	}

	t := ui.NewTerminalUI()

	// ── Build provider ─────────────────────────────────────────────────────
	provider, defaultModel := buildProvider(*providerFlag, *apiURL, *apiKey, *threads)
	if *model == "" {
		*model = defaultModel
	}

	// ── Subcommands ────────────────────────────────────────────────────────
	if len(args) > 0 {
		switch args[0] {
		case "version":
			fmt.Printf("pitty v%s\n", version)
			return
		case "providers":
			cmdProviders(t)
			return
		case "models":
			cmdModels(provider, t, *model)
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
	runInteractive(provider, t, *model, *temp, *maxTokens, *sysPromptFile, *noTools, *apiURL, *threads)
}

// ── buildProvider constructs the correct llm.Provider from flags ──────────────

// buildProvider creates a provider and returns (provider, defaultModel).
func buildProvider(providerName, apiURL, apiKey string, threads int) (llm.Provider, string) {
	switch strings.ToLower(providerName) {
	case providerLlamaCpp:
		url := apiURL
		if url == "" {
			url = "http://127.0.0.1:8080"
		}
		return llamacpp.New(url, "", threads), "default"

	case providerOpenAI:
		key := apiKey
		if key == "" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		cfg := openai.Config{APIKey: key, BaseURL: apiURL}
		return openai.New(cfg), "gpt-4o"

	case providerAnthropic:
		key := apiKey
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		cfg := anthropic.Config{APIKey: key, BaseURL: apiURL}
		return anthropic.New(cfg), "claude-sonnet-4-5"

	case providerGemini:
		key := apiKey
		if key == "" {
			key = os.Getenv("GEMINI_API_KEY")
		}
		cfg := gemini.Config{APIKey: key, BaseURL: apiURL}
		return gemini.New(cfg), "gemini-2.0-flash"

	case providerGroq:
		key := apiKey
		if key == "" {
			key = os.Getenv("GROQ_API_KEY")
		}
		url := apiURL
		if url == "" {
			url = "https://api.groq.com/openai/v1"
		}
		cfg := openai.Config{APIKey: key, BaseURL: url}
		return openai.New(cfg), "llama-3.3-70b-versatile"

	case providerTogether:
		key := apiKey
		if key == "" {
			key = os.Getenv("TOGETHER_API_KEY")
		}
		url := apiURL
		if url == "" {
			url = "https://api.together.xyz/v1"
		}
		cfg := openai.Config{APIKey: key, BaseURL: url}
		return openai.New(cfg), "meta-llama/Llama-3-70b-chat-hf"

	case providerMistral:
		key := apiKey
		if key == "" {
			key = os.Getenv("MISTRAL_API_KEY")
		}
		url := apiURL
		if url == "" {
			url = "https://api.mistral.ai/v1"
		}
		cfg := openai.Config{APIKey: key, BaseURL: url}
		return openai.New(cfg), "mistral-large-latest"

	case providerDeepSeek:
		key := apiKey
		if key == "" {
			key = os.Getenv("DEEPSEEK_API_KEY")
		}
		url := apiURL
		if url == "" {
			url = "https://api.deepseek.com/v1"
		}
		cfg := openai.Config{APIKey: key, BaseURL: url}
		return openai.New(cfg), "deepseek-chat"

	default:
		fmt.Fprintf(os.Stderr, "unknown provider: %s\nRun 'pitty providers' to list supported providers.\n", providerName)
		os.Exit(1)
		return nil, ""
	}
}

// ── Subcommands ───────────────────────────────────────────────────────────────

// cmdProviders lists all supported providers with their environment variables.
func cmdProviders(t *ui.TerminalUI) {
	providers := [][3]string{
		{providerLlamaCpp, "Local llama.cpp server (default)", "—"},
		{providerOpenAI, "OpenAI (GPT-4o, o1, …)", "OPENAI_API_KEY"},
		{providerAnthropic, "Anthropic Claude (claude-sonnet-4-5, …)", "ANTHROPIC_API_KEY"},
		{providerGemini, "Google Gemini (gemini-2.0-flash, …)", "GEMINI_API_KEY"},
		{providerGroq, "Groq (llama-3.3-70b, …)", "GROQ_API_KEY"},
		{providerTogether, "Together AI (Llama, Mixtral, …)", "TOGETHER_API_KEY"},
		{providerMistral, "Mistral AI (mistral-large, …)", "MISTRAL_API_KEY"},
		{providerDeepSeek, "DeepSeek (deepseek-chat, …)", "DEEPSEEK_API_KEY"},
	}
	t.PrintInfo("Supported providers:\n")
	for _, p := range providers {
		set := "not set"
		if p[2] != "—" {
			if os.Getenv(p[2]) != "" {
				set = "✓ set"
			}
		} else {
			set = "—"
		}
		fmt.Printf("  %-12s  %-40s  %s=%s\n", p[0], p[1], p[2], set)
	}
	fmt.Println()
	t.PrintInfo("Usage: pitty -provider <name> -model <model> [-api-key <key>]")
}

// cmdModels lists available models for the current provider.
func cmdModels(provider llm.Provider, t *ui.TerminalUI, currentModel string) {
	ctx := context.Background()
	models, err := provider.ListModels(ctx)
	if err != nil {
		t.PrintError(fmt.Errorf("failed to list models: %w", err))
		os.Exit(1)
	}
	if len(models) == 0 {
		t.PrintInfo("No models returned by provider.")
		return
	}
	t.PrintInfo(fmt.Sprintf("Available models (%s):", provider.Name()))
	for _, m := range models {
		marker := " "
		if m == currentModel {
			marker = "●"
		}
		fmt.Printf("  %s %s\n", marker, m)
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

// ── Interactive REPL ──────────────────────────────────────────────────────────

func runInteractive(provider llm.Provider, t *ui.TerminalUI, model string, temp float64, maxTokens int, sysPromptFile string, noTools bool, apiURL string, threads int) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Show log file path
	if lp := logger.FilePath(); lp != "" {
		t.PrintInfo(fmt.Sprintf("  📋 Log: %s", lp))
	}
	if ep := logger.ErrorFilePath(); ep != "" {
		absPath, err := filepath.Abs(ep)
		if err == nil {
			t.PrintInfo(fmt.Sprintf("  🚨 Error Log: %s", absPath))
		}
	}

	url := apiURL
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	// ── Check provider connectivity ────────────────────────────────────────
	if provider.Name() == "llamacpp" {
		err := llamacpp.AutoStart(ctx, url, threads, func(msg string) {
			t.PrintInfo("  ⚙️  " + msg)
		})
		if err != nil {
			logger.Error("AutoStart failed: %v", err)
			t.PrintError(fmt.Errorf("gagal menyiapkan server lokal otomatis: %w", err))
		}
	}

	if err := provider.Ping(ctx); err != nil {
		logger.Error("Provider connectivity check failed: %v", err)
		t.PrintError(err)
		printProviderHint(provider.Name())
		os.Exit(1)
	}
	logger.Info("Connected to provider=%s, model=%s", provider.Name(), model)

	// ── Memory system ──────────────────────────────────────────────────────
	store, err := memory.NewStore()
	if err != nil {
		t.PrintWarning(fmt.Sprintf("Memory system unavailable: %v", err))
	}
	var learner *memory.Learner
	if store != nil {
		learner = memory.NewLearner(store)
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
	ag := agent.NewAgent(provider, registry, model, temp, maxTokens)
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
	t.PrintWelcome(model, provider.Name(), memCount)

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
			handleCommand(ctx, input, ag, t, &model, provider, store, learner, noTools)
			continue
		}

		// ── Chat ────────────────────────────────────────────────────────────
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

// printProviderHint prints a helpful hint for the given provider when connection fails.
func printProviderHint(providerName string) {
	switch providerName {
	case providerLlamaCpp:
		fmt.Fprintln(os.Stderr, "Hint: Start llama.cpp server with: ./llama-server -m model.gguf --port 8080")
	case providerOpenAI:
		fmt.Fprintln(os.Stderr, "Hint: Set OPENAI_API_KEY or pass -api-key <key>")
	case providerAnthropic:
		fmt.Fprintln(os.Stderr, "Hint: Set ANTHROPIC_API_KEY or pass -api-key <key>")
	case providerGemini:
		fmt.Fprintln(os.Stderr, "Hint: Set GEMINI_API_KEY or pass -api-key <key>")
	case providerGroq:
		fmt.Fprintln(os.Stderr, "Hint: Set GROQ_API_KEY or pass -api-key <key>")
	case providerTogether:
		fmt.Fprintln(os.Stderr, "Hint: Set TOGETHER_API_KEY or pass -api-key <key>")
	case providerMistral:
		fmt.Fprintln(os.Stderr, "Hint: Set MISTRAL_API_KEY or pass -api-key <key>")
	case providerDeepSeek:
		fmt.Fprintln(os.Stderr, "Hint: Set DEEPSEEK_API_KEY or pass -api-key <key>")
	}
}

// ── Command handler ───────────────────────────────────────────────────────────

func handleCommand(ctx context.Context, input string, ag *agent.Agent, t *ui.TerminalUI, model *string, provider llm.Provider, store *memory.Store, learner *memory.Learner, noTools bool) {
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
			t.PrintInfo(fmt.Sprintf("Provider: %s", ag.Provider()))
			t.PrintInfo("Usage: /model <name>")
			return
		}
		*model = parts[1]
		ag.SetModel(parts[1])
		t.PrintSuccess("Model switched to: " + parts[1])

	// ── List models ─────────────────────────────────────────────────────────
	case "/models":
		cmdModels(provider, t, *model)

	// ── Switch provider ──────────────────────────────────────────────────────
	case "/provider":
		if len(parts) < 2 {
			t.PrintInfo(fmt.Sprintf("Current provider: %s", ag.Provider()))
			t.PrintInfo("Usage: /provider <name> [model]")
			t.PrintInfo("Run 'pitty providers' to list all supported providers.")
			return
		}
		newProviderName := parts[1]
		newModel := ""
		if len(parts) >= 3 {
			newModel = parts[2]
		}
		newProvider, defaultModel := buildProvider(newProviderName, "", "", 0)
		if newModel == "" {
			newModel = defaultModel
		}

		// Ping before switching
		pingCtx, pingCancel := context.WithCancel(ctx)
		defer pingCancel()
		if err := newProvider.Ping(pingCtx); err != nil {
			t.PrintError(fmt.Errorf("cannot connect to %s: %w", newProviderName, err))
			printProviderHint(newProviderName)
			return
		}
		ag.SetProvider(newProvider)
		ag.SetModel(newModel)
		*model = newModel
		// Update the outer provider variable (passed by value, so note it's not updated in parent)
		t.PrintSuccess(fmt.Sprintf("Switched to provider=%s model=%s", newProviderName, newModel))

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
