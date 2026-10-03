# Pitty v0.3.0

**Pitty** is a powerful **AI Coding Assistant** that can run either fully local on your machine (via llama.cpp) or connect to various cloud AI providers (OpenAI, Anthropic, Gemini, Groq, etc.).

## 🚀 Features

- **Multi-Provider Support** — Connect to `llamacpp`, `openai`, `anthropic`, `gemini`, `groq`, `together`, `mistral`, or `deepseek`.
- **Lightweight Local Setup** — Uses `llamacpp` as the default local provider, optimized for older machines (e.g., dual-core CPUs with 3GB free RAM).
- **Tool Calling** — Reads/writes files, runs commands, and searches codebases autonomously, with native and fallback parsing.
- **Persistent Memory** — Auto-learns facts, preferences, and workflows from conversations. Persists across sessions.
- **Antigravity CLI Integration** — Imports knowledge from AGY transcripts via `--import-agy`.
- **Interactive Terminal UI** — Spinner, ANSI colors, multi-line input, and built-in slash commands.
- **Error Logging** — Keeps track of important issues in a local `error.log`.
- **Zero Dependencies** — Pure Go standard library. No `go.sum`.

## 📋 Requirements

- **Go 1.24+** (for building)
- Local inference requires a running [llama.cpp](https://github.com/ggerganov/llama.cpp) server (default endpoint: `http://127.0.0.1:8080`)
- Cloud inference requires the respective API key set as an environment variable (e.g., `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, etc.)

## 🛠️ Installation

1. Clone the repository:

```bash
git clone https://github.com/Rioprastyo17/pitty.git
cd pitty
```

2. Build the binary:

```bash
go build -o pitty ./cmd/pitty

# Optional: install globally
sudo cp pitty /usr/local/bin/pitty
```

## 🤖 Usage & Setup

Start Pitty in interactive mode or pass specific flags to configure the provider and model:

### Local (llama.cpp)
```bash
# Uses llama.cpp default endpoint (http://127.0.0.1:8080)
pitty 

# Optimize for low RAM / older CPUs
pitty -threads 4 -max-tokens 1024 
```

### Cloud Providers
You can switch to any cloud provider and model. Pitty automatically picks up standard API key environment variables (e.g., `OPENAI_API_KEY`, `GEMINI_API_KEY`).

```bash
pitty -provider openai -model gpt-4o
pitty -provider anthropic -model claude-sonnet-4-5
pitty -provider gemini -model gemini-2.0-flash
pitty -provider groq -model llama-3.3-70b-versatile
```

### Useful Commands
```bash
pitty providers                   # List all supported providers
pitty models                      # List available models for the current provider
pitty --no-tools                  # Disable tool calling (pure chat)
pitty --system-prompt prompt.txt  # Custom system prompt
pitty --import-agy                # Import from Antigravity CLI
pitty version                     # Show version
```

### Command Line Flags

```text
  -api-key string        API key (falls back to env: OPENAI_API_KEY, ANTHROPIC_API_KEY, ...)
  -import-agy            Import knowledge from Antigravity CLI transcripts and exit
  -max-tokens int        Max tokens to generate (default 2048 - reduce if RAM is limited)
  -model string          Model to use (default depends on provider)
  -no-tools              Disable tool calling (simple chat mode)
  -provider string       AI provider: llamacpp | openai | anthropic | gemini | groq | together | mistral | deepseek (default "llamacpp")
  -system-prompt string  Path to a custom system prompt file
  -temperature float     Sampling temperature (0.0-2.0) (default 0.7)
  -threads int           CPU threads untuk llama.cpp inference (0 = let server decide) (default 4)
  -url string            API server URL (auto-detected per provider; override for custom endpoints)
  -version               Print version and exit
```

## 💬 Slash Commands

Inside the interactive chat, you can use these commands:

| Command | Description |
|---------|-------------|
| `/help` | Show all commands |
| `/exit`, `/quit`, `/q` | Exit pitty |
| `/clear`, `/reset` | Clear conversation history |
| `/model <name>` | Switch model at runtime |
| `/models` | List available models for current provider |
| `/provider <name> [model]`| Switch AI provider (llamacpp\|openai\|anthropic\|gemini\|groq\|…) |
| `/learn <text>` | Teach pitty something to remember |
| `/memory` | Show knowledge base status |
| `/memory search <q>` | Search knowledge base |
| `/import` | Import from Antigravity CLI |
| `/history` | Show conversation history (JSON) |
| `/tokens` | Show estimated token usage |
| `/compact` | Summarize and compact conversation |

## 🧠 Memory System

Pitty automatically learns from every session:

- **Tool calls** → remembers commands and file operations
- **User instructions** → detects preferences ("always use..."), corrections
- **Code generation** → notes languages and patterns used
- **Explicit teaching** → `/learn always use absolute paths`

Stored at `~/.pitty/memory/knowledge.jsonl` (up to 2000 entries, JSONL format).

### Antigravity CLI Integration

```bash
pitty -import-agy   # one-time import
# or inside pitty:
/import
```

## 🔧 Tools

| Tool | Description |
|------|-------------|
| `read_file` | Read file with optional line range |
| `write_file` | Create/overwrite files (creates parent dirs) |
| `edit_file` | Search-and-replace in files (`all=true` for global replace) |
| `run_command` | Execute shell commands with configurable timeout |
| `search_files` | Regex search across files (like grep) |
| `list_directory` | List directory contents, optionally recursive |

## 📄 License

Open-source. Use freely.
