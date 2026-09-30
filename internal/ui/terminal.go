package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ANSI color/style codes.
const (
	reset     = "\033[0m"
	bold      = "\033[1m"
	dim       = "\033[2m"
	italic    = "\033[3m"
	red       = "\033[31m"
	green     = "\033[32m"
	yellow    = "\033[33m"
	blue      = "\033[34m"
	magenta   = "\033[35m"
	cyan      = "\033[36m"
	white     = "\033[37m"
	gray      = "\033[90m"
	clearLine = "\033[2K\r"
)

// Spinner frames — Braille dots like AGY.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// TerminalUI handles all terminal I/O in the style of Antigravity CLI.
type TerminalUI struct {
	reader      *bufio.Reader
	spinnerStop chan struct{}
	spinnerDone chan struct{} // closed when spinner goroutine exits
	spinnerMu   sync.Mutex
	spinning    bool
}

// NewTerminalUI creates a new terminal UI.
func NewTerminalUI() *TerminalUI {
	return &TerminalUI{
		reader: bufio.NewReader(os.Stdin),
	}
}

// ── Welcome ───────────────────────────────────────────────────────────────────

// PrintWelcome prints the startup banner (AGY-style).
func (t *TerminalUI) PrintWelcome(model, ollamaURL string, memCount int) {
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}

	fmt.Println()
	fmt.Printf("%s%s ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ %s\n", bold, gray, reset)
	fmt.Printf("%s%s ✦ pitty%s %sv0.2.0%s  %s— local AI coding assistant%s\n", bold, magenta, reset, gray, reset, dim, reset)
	fmt.Printf("%s ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ %s\n", gray, reset)
	fmt.Println()
	fmt.Printf("  %sModel%s      %s%s%s\n", gray, reset, white, model, reset)
	fmt.Printf("  %sProvider%s   %s%s%s\n", gray, reset, white, ollamaURL, reset)
	fmt.Printf("  %sWorkspace%s  %s%s%s\n", gray, reset, white, cwd, reset)
	if memCount > 0 {
		fmt.Printf("  %sMemory%s     %s📚 %d entries%s\n", gray, reset, cyan, memCount, reset)
	}
	fmt.Println()
	fmt.Printf("  %sType %s/help%s%s for commands • %s/exit%s%s to quit%s\n",
		gray, white, gray, reset, white, gray, reset, reset)
	fmt.Printf("%s ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ %s\n", gray, reset)
	fmt.Println()
}

// ── Streaming output ──────────────────────────────────────────────────────────

// PrintStreamStart prints the assistant prefix before streaming.
func (t *TerminalUI) PrintStreamStart() {
	fmt.Printf("\n%s%s✦%s ", bold, magenta, reset)
}

// PrintStreaming writes a streaming chunk directly (no newline).
func (t *TerminalUI) PrintStreaming(chunk string) {
	fmt.Print(chunk)
}

// PrintStreamEnd finalizes streaming output with newlines.
func (t *TerminalUI) PrintStreamEnd() {
	fmt.Println()
	fmt.Println()
}

// PrintAssistant prints a complete (non-streamed) assistant response.
func (t *TerminalUI) PrintAssistant(text string) {
	fmt.Printf("\n%s%s✦%s %s\n\n", bold, magenta, reset, text)
}

// ── Tool display ──────────────────────────────────────────────────────────────

// PrintToolCall displays a tool being invoked (AGY-style).
func (t *TerminalUI) PrintToolCall(name string, args map[string]interface{}) {
	summary := formatToolSummary(name, args)
	icon := toolIcon(name)
	fmt.Printf("\n  %s%s%s %s%s%s", bold, yellow, icon+" "+name, reset, gray, reset)
	if summary != "" {
		fmt.Printf("  %s%s%s", dim, summary, reset)
	}
	fmt.Println()
}

// PrintToolResult displays the result of a tool call.
func (t *TerminalUI) PrintToolResult(name string, result string) {
	lines := strings.Count(result, "\n") + 1
	fmt.Printf("  %s✓%s %s%s — %d lines%s\n", green, reset, gray, name, lines, reset)
}

// ── Status messages ───────────────────────────────────────────────────────────

func (t *TerminalUI) PrintError(err error) {
	fmt.Printf("\n%s%s✗ Error:%s %s%v%s\n\n", bold, red, reset, red, err, reset)
}

func (t *TerminalUI) PrintInfo(text string) {
	fmt.Printf("%s%s%s\n", gray, text, reset)
}

func (t *TerminalUI) PrintSuccess(text string) {
	fmt.Printf("%s%s✓ %s%s\n", bold, green, text, reset)
}

func (t *TerminalUI) PrintWarning(text string) {
	fmt.Printf("%s%s⚠ %s%s\n", bold, yellow, text, reset)
}

func (t *TerminalUI) PrintSeparator() {
	fmt.Printf("%s  ──────────────────────────────────────────────────%s\n", gray, reset)
}

// ── Spinner ───────────────────────────────────────────────────────────────────

// StartThinking starts the animated "Thinking…" spinner.
// Uses a done channel so StopThinking waits for the goroutine to exit cleanly.
func (t *TerminalUI) StartThinking() {
	t.spinnerMu.Lock()
	defer t.spinnerMu.Unlock()
	if t.spinning {
		return
	}
	t.spinning = true
	t.spinnerStop = make(chan struct{})
	t.spinnerDone = make(chan struct{})

	go func() {
		defer close(t.spinnerDone)
		i := 0
		for {
			select {
			case <-t.spinnerStop:
				fmt.Print(clearLine)
				return
			default:
				frame := spinnerFrames[i%len(spinnerFrames)]
				fmt.Printf("%s  %s%s Thinking…%s", clearLine, magenta, frame, reset)
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

// StopThinking stops the spinner and waits for its goroutine to finish.
func (t *TerminalUI) StopThinking() {
	t.spinnerMu.Lock()
	if !t.spinning {
		t.spinnerMu.Unlock()
		return
	}
	close(t.spinnerStop)
	done := t.spinnerDone
	t.spinning = false
	t.spinnerMu.Unlock()
	<-done // wait for goroutine to clear the line
}

// ── Input ─────────────────────────────────────────────────────────────────────

// ReadInput reads user input with multi-line support (trailing \\ continues).
func (t *TerminalUI) ReadInput() (string, error) {
	var input strings.Builder
	first := true
	for {
		if first {
			fmt.Printf("%s%s❯%s ", bold, green, reset)
			first = false
		} else {
			fmt.Printf("%s…%s ", gray, reset)
		}

		line, err := t.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")

		if strings.HasSuffix(line, "\\") {
			input.WriteString(strings.TrimSuffix(line, "\\"))
			input.WriteRune('\n')
			continue
		}
		input.WriteString(line)
		break
	}
	return strings.TrimSpace(input.String()), nil
}

// PrintUser echoes a user message (no-op: already shown via ReadInput prompt).
func (t *TerminalUI) PrintUser(_ string) {}

// ── Helpers ───────────────────────────────────────────────────────────────────

func toolIcon(name string) string {
	icons := map[string]string{
		"read_file":      "📄",
		"write_file":     "✏️",
		"edit_file":      "🔧",
		"run_command":    "⚡",
		"search_files":   "🔍",
		"list_directory": "📁",
		"web_search":     "🌐",
	}
	if icon, ok := icons[name]; ok {
		return icon
	}
	return "🔧"
}

func formatToolSummary(name string, args map[string]interface{}) string {
	switch name {
	case "read_file", "write_file", "edit_file":
		if p, ok := args["path"].(string); ok {
			return shortenPath(p)
		}
	case "run_command":
		if c, ok := args["command"].(string); ok {
			if len(c) > 70 {
				return c[:70] + "…"
			}
			return c
		}
	case "search_files":
		if p, ok := args["pattern"].(string); ok {
			return fmt.Sprintf("/%s/", p)
		}
	case "list_directory":
		if p, ok := args["path"].(string); ok {
			return shortenPath(p)
		}
	case "web_search":
		if q, ok := args["query"].(string); ok {
			return q
		}
	}
	return ""
}

// shortenPath abbreviates long paths for display.
func shortenPath(path string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(path, home) {
		path = "~" + path[len(home):]
	}
	if len(path) > 60 {
		return "…" + path[len(path)-57:]
	}
	return path
}

// PrintHelp displays the help text — defined here so it's near the UI layer.
func (t *TerminalUI) PrintHelp() {
	_ = dim // ensure const is used
	fmt.Printf("\n%s%sAvailable commands:%s\n", bold, white, reset)
	cmds := [][2]string{
		{"/help", "Show this help"},
		{"/exit, /quit, /q", "Exit pitty"},
		{"/clear, /reset", "Clear conversation history"},
		{"/model <name>", "Switch Ollama model"},
		{"/models", "List available models"},
		{"/learn <text>", "Teach pitty something to remember"},
		{"/memory", "Show knowledge base status"},
		{"/memory search <q>", "Search knowledge base"},
		{"/import", "Import knowledge from Antigravity CLI"},
		{"/history", "Show conversation history (JSON)"},
		{"/tokens", "Show estimated token usage"},
		{"/compact", "Summarize and compact conversation history"},
	}
	for _, c := range cmds {
		fmt.Printf("  %s%s%-22s%s  %s%s%s\n", bold, cyan, c[0], reset, gray, c[1], reset)
	}
	fmt.Println()
	fmt.Printf("  %sTips:%s\n", bold, reset)
	fmt.Printf("  %s• Use \\ at end of line for multi-line input%s\n", gray, reset)
	fmt.Printf("  %s• Ctrl+C to interrupt generation%s\n", gray, reset)
	fmt.Printf("  %s• pitty auto-learns from your interactions 🧠%s\n", gray, reset)
	fmt.Println()
}
