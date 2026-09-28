# Pitty

Pitty is a powerful **Local AI Coding Assistant** designed to run entirely on your machine using Ollama. It features interactive chat, tool calling, and a learning memory system to adapt to your coding workflow.

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
* A model pulled into Ollama, such as `qwen2.5:0.5b`.

## 🛠️ Installation

Follow these steps to install and run Pitty on your machine:

1. Install Go 1.24 or newer.
2. Install [Ollama](https://ollama.com/) and make sure the Ollama service is running locally.
3. Pull a supported model:

```bash
ollama pull qwen2.5:0.5b
```

4. Clone the repository:

```bash
git clone https://github.com/Rioprastyo17/pitty.git
cd pitty
```

5. Build the binary:

```bash
go build -o pitty ./cmd/pitty
```

6. Run Pitty:

```bash
./pitty
```

Optional: install it globally so it can be run from anywhere:

```bash
sudo install -m 755 pitty /usr/local/bin/pitty
```

You can then run:

```bash
pitty
```

## 💡 Recommended Setup

Here are my recommendations for the best experience while using Pitty:

* **For low-end machines:** use `qwen2.5:0.5b` or `qwen2.5:1.5b` for low memory usage.
* **For better code quality:** use `qwen2.5:3b` or `qwen2.5:7b` if your machine has enough RAM and CPU power.
* **For coding tasks:** start with a lower temperature such as `0.2` to `0.4` for more stable and consistent output.
* **Keep Ollama running in the background** so Pitty can access the model without restarting the service.
* **Use a dedicated terminal session** when testing features like tool calling and file editing.

Example for a stronger coding setup:

```bash
./pitty --model qwen2.5:3b --temperature 0.3
```

If the model does not load, make sure Ollama is installed and running:

```bash
ollama serve
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

Pitty has a built-in memory system that stores learned facts and preferences. It will automatically learn from your interactions, or you can explicitly teach it using the `/learn` command. These memories help personalize future coding assistance.

## 📄 License

This project is open-source.
