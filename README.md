# Pitty

Pitty is a powerful **Local AI Coding Assistant** designed to run entirely on your machine using Ollama. It features interactive chat, tool calling, and a learning memory system to adapt to your coding style.

## 🚀 Features

* **Local AI via Ollama:** Connects seamlessly to your local Ollama instance (defaults to `qwen2.5:0.5b`).
* **Tool Calling:** Capable of reading/writing files, executing commands, and searching directories to assist you with coding tasks.
* **Persistent Memory & Learning:** Automatically learns facts and preferences during conversation or via explicit commands (`/learn`).
* **Antigravity CLI Integration:** Can import knowledge and memory entries directly from Antigravity CLI transcripts.
* **Customizable:** Support for custom system prompts and adjustable generation parameters (temperature, max tokens).
* **Interactive Terminal UI:** A clean, easy-to-use REPL environment with built-in slash commands.

## 📋 Requirements

* **Go 1.24+** (for building)
* **[Ollama](https://ollama.com/)** running locally.

## 🛠️ Installation

Clone the repository and build the binary:

```bash
git clone https://github.com/pitty/pitty.git
cd pitty
go build -o pitty ./cmd/pitty
```

## 🎮 Usage

Start Pitty in interactive mode simply by running:

```bash
./pitty
```

### Command Line Flags

You can customize Pitty's behavior when starting up:

```bash
  --model string           Ollama model to use (default "qwen2.5:0.5b")
  --ollama-url string      Ollama API URL (default "http://localhost:11434")
  --temperature float      Temperature for generation (default 0.7)
  --max-tokens int         Max tokens to generate (default 4096)
  --system-prompt string   Path to custom system prompt file
  --no-tools               Disable tool calling (simple chat mode)
  --import-agy             Import knowledge from Antigravity CLI transcripts
```

### Slash Commands

While in the interactive chat, you can use these commands:

* `/help` - Show available commands
* `/exit`, `/quit`, `/q` - Exit pitty
* `/clear`, `/reset` - Clear conversation history
* `/model <name>` - Switch model
* `/models` - List available models
* `/learn <text>` - Teach pitty something to remember (e.g., `/learn Always use Go 1.24 formatting`)
* `/memory` - Show knowledge base status
* `/memory search <q>` - Search knowledge base
* `/import` - Import knowledge from Antigravity CLI
* `/history` - Show conversation history

## 🧠 Memory System

Pitty has a built-in memory system that stores learned facts and preferences. It will automatically learn from your interactions, or you can explicitly teach it using the `/learn` command. These memories persist between sessions, making Pitty smarter and more tailored to your workflow over time.

## 📄 License

This project is open-source.
