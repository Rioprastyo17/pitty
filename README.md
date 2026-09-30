# Pitty v0.2.0

**Pitty** is a powerful **Local AI Coding Assistant** that runs entirely on your machine using Ollama — no cloud, no API keys.

## 🚀 Features

- **100% Local & Offline** — Powered by Ollama. Your code never leaves your machine.
- **GGUF Model Support** — Uses `qwen2.5-coder:1.5b` (GGUF format, ~986MB) by default. Fast and coding-focused.
- **Tool Calling** — Reads/writes files, runs commands, searches codebases, all autonomously.
- **Persistent Memory** — Auto-learns facts, preferences, and workflows from conversations. Persists across sessions.
- **Antigravity CLI Integration** — Imports knowledge from AGY transcripts via `--import-agy`.
- **Conversation Compaction** — `/compact` summarizes a long conversation and resets context.
- **Interactive Terminal UI** — Spinner, ANSI colors, multi-line input, slash commands.
- **Zero Dependencies** — Pure Go standard library. No `go.sum`.

## 📋 Requirements

- **Go 1.24+** (for building)
- **[Ollama](https://ollama.com/)** running locally (`ollama serve`)
- `qwen2.5-coder:1.5b` model (already included if you followed setup)

## 🛠️ Installation

```bash
ollama pull qwen2.5-coder:1.5b
```

4. Clone the repository:

```bash
git clone https://github.com/Rioprastyo17/pitty.git
cd pitty
```

5. Build the binary:

```bash
go build -o pitty ./cmd/pitty

# Optional: install globally
sudo cp pitty /usr/local/bin/pitty
```

## 🤖 Model Setup (GGUF via Ollama)

Pitty defaults to `qwen2.5-coder:1.5b` — a lightweight GGUF coding model (~986 MB):

```bash
ollama pull qwen2.5-coder:1.5b
```

Other lightweight options:

| Model | Size | Best for |
|-------|------|----------|
| `qwen2.5-coder:1.5b` | 986 MB | Code (default) |
| `deepseek-coder:1.3b` | 776 MB | Code, very fast |
| `gemma3:1b` | 815 MB | General |
| `qwen2.5:0.5b` | 397 MB | Ultra-light |

Switch models at runtime: `/model deepseek-coder:1.3b`

## 🎮 Usage

```bash
pitty                             # Start interactive chat
pitty --model gemma3:1b           # Use a different model
pitty --no-tools                  # Disable tool calling (pure chat)
pitty --system-prompt prompt.txt  # Custom system prompt
pitty models                      # List available models
pitty --import-agy                # Import from Antigravity CLI
```

### Command Line Flags

```
--model string           Ollama model (default "qwen2.5-coder:1.5b")
--ollama-url string      Ollama API URL (default "http://127.0.0.1:11434")
--temperature float      Sampling temperature (default 0.7)
--max-tokens int         Max tokens to generate (default 8192)
--system-prompt string   Path to custom system prompt file
--no-tools               Disable tool calling (simple chat mode)
--import-agy             Import knowledge from Antigravity CLI and exit
```

### Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | Show all commands |
| `/exit`, `/quit`, `/q` | Exit pitty |
| `/clear`, `/reset` | Clear conversation history |
| `/model <name>` | Switch model at runtime |
| `/models` | List available models (current model marked with ●) |
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
- **Explicit teaching** → `/learn selalu jawab dalam bahasa Indonesia`

Stored at `~/.pitty/memory/knowledge.jsonl` (up to 2000 entries, JSONL format).

### Antigravity CLI Integration

```bash
pitty --import-agy   # one-time import
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
