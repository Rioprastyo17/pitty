package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ANSI color/style codes matching Antigravity CLI
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
	bgBlue    = "\033[44m"
	clearLine = "\033[2K\r"
)

// Spinner frames for thinking animation
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// TerminalUI handles all terminal I/O styled like Antigravity CLI.
type TerminalUI struct {
	reader      *bufio.Reader
	spinnerStop chan struct{}
	spinnerMu   sync.Mutex
	spinning    bool
}

// NewTerminalUI creates a new terminal UI.
func NewTerminalUI() *TerminalUI {
	return &TerminalUI{
		reader: bufio.NewReader(os.Stdin),
	}
}

// PrintWelcome prints the welcome banner matching Antigravity CLI style.
func (t *TerminalUI) PrintWelcome(model, ollamaURL string) {
	// Get current working directory for workspace display
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "."
	}

	fmt.Println()
	fmt.Printf("%s%s ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ %s\n", bold, gray, reset)
	fmt.Printf("%s%s ✦ pitty%s %sv0.1.0%s\n", bold, magenta, reset, gray, reset)
	fmt.Printf("%s ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ %s\n", gray, reset)
	fmt.Println()
	fmt.Printf("  %sModel%s      %s%s%s\n", gray, reset, white, model, reset)
	fmt.Printf("  %sProvider%s   %s%s%s\n", gray, reset, white, ollamaURL, reset)
	fmt.Printf("  %sWorkspace%s  %s%s%s\n", gray, reset, white, cwd, reset)
	fmt.Println()
	fmt.Printf("  %sType %s/help%s%s for commands • %s/exit%s%s to quit%s\n", gray, white, gray, reset, white, gray, reset, reset)
	fmt.Printf("%s ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ %s\n", gray, reset)
	fmt.Println()
}

// PrintStreaming prints a streaming chunk (no newline, no prefix).
func (t *TerminalUI) PrintStreaming(chunk string) {
	fmt.Print(chunk)
}

// PrintStreamStart prints the assistant response prefix before streaming begins.
func (t *TerminalUI) PrintStreamStart() {
	fmt.Printf("\n%s%s✦%s ", bold, magenta, reset)
}

// PrintStreamEnd ends a streaming output.
func (t *TerminalUI) PrintStreamEnd() {
	fmt.Println()
	fmt.Println()
}

// PrintToolCall prints info about a tool being called (Antigravity style).
func (t *TerminalUI) PrintToolCall(name string, args map[string]interface{}) {
	// Format: 🔧 ToolName  summary
	summary := t.formatToolSummary(name, args)
	fmt.Printf("\n  %s%s🔧 %s%s", bold, yellow, name, reset)
	if summary != "" {
		fmt.Printf("  %s%s%s", gray, summary, reset)
	}
	fmt.Println()
}

// PrintToolResult prints the result of a tool call.
func (t *TerminalUI) PrintToolResult(name string, result string) {
	fmt.Printf("  %s✓ Done%s %s(%d chars)%s\n", green, reset, gray, len(result), reset)
}

// PrintError prints an error message.
func (t *TerminalUI) PrintError(err error) {
	fmt.Printf("\n%s%s✗ Error:%s %s%v%s\n\n", bold, red, reset, red, err, reset)
}

// PrintInfo prints an info/dim message.
func (t *TerminalUI) PrintInfo(text string) {
	fmt.Printf("%s%s%s\n", gray, text, reset)
}

// PrintSuccess prints a success message.
func (t *TerminalUI) PrintSuccess(text string) {
	fmt.Printf("%s%s✓ %s%s\n", bold, green, text, reset)
}

// PrintWarning prints a warning message.
func (t *TerminalUI) PrintWarning(text string) {
	fmt.Printf("%s%s⚠ %s%s\n", bold, yellow, text, reset)
}

// StartThinking starts the thinking spinner animation.
func (t *TerminalUI) StartThinking() {
	t.spinnerMu.Lock()
	if t.spinning {
		t.spinnerMu.Unlock()
		return
	}
	t.spinning = true
	t.spinnerStop = make(chan struct{})
	t.spinnerMu.Unlock()

	go func() {
		i := 0
		for {
			select {
			case <-t.spinnerStop:
				fmt.Print(clearLine)
				return
			default:
				frame := spinnerFrames[i%len(spinnerFrames)]
				fmt.Printf("%s  %s%s Thinking...%s", clearLine, magenta, frame, reset)
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

// StopThinking stops the thinking spinner animation.
func (t *TerminalUI) StopThinking() {
	t.spinnerMu.Lock()
	defer t.spinnerMu.Unlock()
	if t.spinning {
		close(t.spinnerStop)
		t.spinning = false
		time.Sleep(100 * time.Millisecond) // Let spinner goroutine clean up
	}
}

// ReadInput reads a line of input from the user, supporting \ for continuation.
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
			input.WriteString("\n")
			continue
		}
		input.WriteString(line)
		break
	}
	return strings.TrimSpace(input.String()), nil
}

// PrintSeparator prints a subtle separator line.
func (t *TerminalUI) PrintSeparator() {
	fmt.Printf("%s  ──────────────────────────────────────────────────%s\n", gray, reset)
}

// formatToolSummary creates a brief summary of tool args for display.
func (t *TerminalUI) formatToolSummary(name string, args map[string]interface{}) string {
	switch name {
	case "read_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "write_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "edit_file":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "run_command":
		if c, ok := args["command"].(string); ok {
			if len(c) > 60 {
				return c[:60] + "..."
			}
			return c
		}
	case "search_files":
		if p, ok := args["pattern"].(string); ok {
			return p
		}
	case "list_directory":
		if p, ok := args["path"].(string); ok {
			return p
		}
	}
	return ""
}

// PrintAssistant prints a full assistant response (non-streaming).
func (t *TerminalUI) PrintAssistant(text string) {
	fmt.Printf("\n%s%s✦%s %s\n\n", bold, magenta, reset, text)
}

// PrintUser echoes user input (optional, for logging).
func (t *TerminalUI) PrintUser(text string) {
	// Antigravity CLI doesn't echo user input, it's already shown via ReadInput
}
