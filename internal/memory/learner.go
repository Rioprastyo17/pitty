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

// LearnFromToolCall records knowledge extracted from a successful tool call.
func (l *Learner) LearnFromToolCall(toolName string, args map[string]interface{}, result string, userMessage string) {
	switch toolName {
	case "run_command":
		cmd, _ := args["command"].(string)
		if cmd != "" && len(cmd) < 500 && !isNoisy(cmd) {
			content := fmt.Sprintf("Command `%s` was executed successfully", cmd)
			if len(result) > 0 && len(result) < 200 {
				content += fmt.Sprintf(". Output: %s", strings.TrimSpace(result))
			}
			tags := append([]string{"command", "shell"}, extractCommandTags(cmd)...)
			_ = l.store.Add("command", content, userMessage, "pitty", tags)
		}

	case "write_file":
		path, _ := args["path"].(string)
		if path != "" {
			_ = l.store.Add("workflow", fmt.Sprintf("Created/wrote file `%s`", path), userMessage, "pitty", []string{"file", "write"})
		}

	case "edit_file":
		path, _ := args["path"].(string)
		if path != "" {
			_ = l.store.Add("workflow", fmt.Sprintf("Edited file `%s`", path), userMessage, "pitty", []string{"file", "edit"})
		}

	case "search_files":
		pattern, _ := args["pattern"].(string)
		if pattern != "" {
			_ = l.store.Add("command", fmt.Sprintf("Searched for pattern `%s`", pattern), userMessage, "pitty", []string{"search"})
		}

	case "list_directory":
		path, _ := args["path"].(string)
		if path != "" && path != "." {
			_ = l.store.Add("workflow", fmt.Sprintf("Explored directory `%s`", path), userMessage, "pitty", []string{"navigation", "directory"})
		}
	}
}

// LearnFromUserMessage detects and records preferences, corrections, and facts.
func (l *Learner) LearnFromUserMessage(message string) {
	lower := strings.ToLower(message)

	// Detect explicit teaching / preferences
	if containsAny(lower, "ingat ", "remember ", "selalu ", "always ", "jangan ", "never ", "prefer ", "saya suka ", "i like ", "i prefer ") {
		_ = l.store.Add("preference", message, "user instruction", "pitty", []string{"preference", "user"})
		return
	}

	// Detect corrections
	if containsAny(lower, "bukan ", "salah", "wrong", "seharusnya", "should be", "instead", "koreksi", "correction") {
		_ = l.store.Add("correction", message, "user correction", "pitty", []string{"correction"})
	}
}

// LearnFromAssistantResponse records code patterns and useful facts from AI responses.
func (l *Learner) LearnFromAssistantResponse(response string, userMessage string) {
	// Only learn from substantive responses
	if len(response) < 50 || len(response) > 5000 {
		return
	}

	// Detect responses that contain code blocks — record as workflow knowledge
	if strings.Contains(response, "```") {
		lang := extractCodeLang(response)
		content := fmt.Sprintf("Generated %s code for: %s", lang, truncate(userMessage, 100))
		tags := []string{"code", "generated"}
		if lang != "" {
			tags = append(tags, lang)
		}
		_ = l.store.Add("workflow", content, userMessage, "pitty", tags)
	}
}

// LearnFact records an explicit user-provided fact via /learn.
func (l *Learner) LearnFact(content, context string, tags []string) {
	_ = l.store.Add("fact", content, context, "pitty", tags)
}

// ── helpers ──────────────────────────────────────────────────────────────────

// isNoisy returns true for trivial commands not worth remembering.
func isNoisy(cmd string) bool {
	noisy := []string{"ls", "pwd", "echo", "cat ", "head ", "tail ", "wc ", "clear", "cd ", "date", "whoami"}
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, n := range noisy {
		if strings.HasPrefix(lower, n) {
			return true
		}
	}
	return false
}

// extractCommandTags extracts technology tags from a shell command.
func extractCommandTags(cmd string) []string {
	lower := strings.ToLower(cmd)
	keywords := map[string]string{
		"go ": "go", "npm ": "npm", "pip ": "python", "python ": "python",
		"python3 ": "python", "docker ": "docker", "git ": "git",
		"make": "make", "cargo ": "rust", "apt ": "system", "brew ": "system",
		"curl ": "network", "wget ": "network", "ollama ": "ollama",
		"grep ": "search", "find ": "search", "sed ": "text", "awk ": "text",
		"node ": "node", "yarn ": "npm", "pnpm ": "npm", "bun ": "bun",
		"rustc ": "rust", "mvn ": "java", "gradle ": "java", "javac ": "java",
	}
	var tags []string
	for prefix, tag := range keywords {
		if strings.Contains(lower, prefix) {
			tags = append(tags, tag)
		}
	}
	return tags
}

// extractCodeLang detects the primary language in a markdown response.
func extractCodeLang(response string) string {
	langs := []string{"go", "python", "javascript", "typescript", "rust", "java", "bash", "sh", "sql", "html", "css", "json", "yaml", "toml"}
	lower := strings.ToLower(response)
	for _, lang := range langs {
		if strings.Contains(lower, "```"+lang) {
			return lang
		}
	}
	return "code"
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// truncate shortens a string to max length with ellipsis.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
