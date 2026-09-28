package memory

import (
	"fmt"
	"strings"
)

// Learner automatically extracts and stores knowledge from conversations.
type Learner struct {
	store *Store
}

// NewLearner creates a new auto-learner.
func NewLearner(store *Store) *Learner {
	return &Learner{store: store}
}

// LearnFromToolCall records a tool call as knowledge.
func (l *Learner) LearnFromToolCall(toolName string, args map[string]interface{}, result string, userMessage string) {
	switch toolName {
	case "run_command":
		cmd, _ := args["command"].(string)
		if cmd != "" && len(cmd) < 500 && !isNoisy(cmd) {
			content := fmt.Sprintf("Command `%s` was executed successfully", cmd)
			if len(result) > 0 && len(result) < 200 {
				content += fmt.Sprintf(". Output: %s", strings.TrimSpace(result))
			}
			tags := []string{"command", "shell"}
			tags = append(tags, extractCommandTags(cmd)...)
			_ = l.store.Add("command", content, userMessage, "pitty", tags)
		}

	case "write_file":
		path, _ := args["path"].(string)
		if path != "" {
			content := fmt.Sprintf("Created/wrote file `%s`", path)
			_ = l.store.Add("workflow", content, userMessage, "pitty", []string{"file", "write"})
		}

	case "edit_file":
		path, _ := args["path"].(string)
		if path != "" {
			content := fmt.Sprintf("Edited file `%s`", path)
			_ = l.store.Add("workflow", content, userMessage, "pitty", []string{"file", "edit"})
		}

	case "search_files":
		pattern, _ := args["pattern"].(string)
		if pattern != "" {
			content := fmt.Sprintf("Searched for pattern `%s`", pattern)
			_ = l.store.Add("command", content, userMessage, "pitty", []string{"search"})
		}
	}
}

// LearnFromUserMessage records user preferences and corrections.
func (l *Learner) LearnFromUserMessage(message string) {
	lower := strings.ToLower(message)

	// Detect explicit teaching
	if strings.Contains(lower, "ingat ") || strings.Contains(lower, "remember ") ||
		strings.Contains(lower, "selalu ") || strings.Contains(lower, "always ") ||
		strings.Contains(lower, "jangan ") || strings.Contains(lower, "never ") ||
		strings.Contains(lower, "prefer ") || strings.Contains(lower, "saya suka ") {
		_ = l.store.Add("preference", message, "user instruction", "pitty", []string{"preference", "user"})
	}

	// Detect corrections
	if strings.Contains(lower, "bukan ") || strings.Contains(lower, "salah") ||
		strings.Contains(lower, "wrong") || strings.Contains(lower, "seharusnya") ||
		strings.Contains(lower, "should be") || strings.Contains(lower, "instead") {
		_ = l.store.Add("correction", message, "user correction", "pitty", []string{"correction"})
	}
}

// LearnFact records an explicit fact.
func (l *Learner) LearnFact(content, context string, tags []string) {
	_ = l.store.Add("fact", content, context, "pitty", tags)
}

// isNoisy returns true for commands that aren't worth remembering.
func isNoisy(cmd string) bool {
	noisy := []string{"ls", "pwd", "echo", "cat ", "head ", "tail ", "wc ", "clear", "cd "}
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, n := range noisy {
		if strings.HasPrefix(lower, n) {
			return true
		}
	}
	return false
}

// extractCommandTags extracts tags from a shell command.
func extractCommandTags(cmd string) []string {
	var tags []string
	lower := strings.ToLower(cmd)

	keywords := map[string]string{
		"go ":     "go",
		"npm ":    "npm",
		"pip ":    "python",
		"python ": "python",
		"docker ": "docker",
		"git ":    "git",
		"make":    "make",
		"cargo ":  "rust",
		"apt ":    "system",
		"brew ":   "system",
		"curl ":   "network",
		"wget ":   "network",
		"ollama ": "ollama",
		"grep ":   "search",
		"find ":   "search",
		"sed ":    "text",
		"awk ":    "text",
	}

	for prefix, tag := range keywords {
		if strings.Contains(lower, prefix) {
			tags = append(tags, tag)
		}
	}

	return tags
}
