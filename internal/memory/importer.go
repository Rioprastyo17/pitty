package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// transcriptStep represents one step in an Antigravity CLI transcript.jsonl file.
type transcriptStep struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Content string `json:"content"`
	// AGY tool calls are nested under tool_calls[].arguments (in PLANNER_RESPONSE steps).
	ToolCalls []transcriptToolCall `json:"tool_calls"`
}

// transcriptToolCall matches the actual AGY transcript format.
type transcriptToolCall struct {
	// AGY stores tool calls as { "name": "...", "arguments": { ... } }
	// at the top level of each tool_calls array item.
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
	// Some steps nest them under "function":
	Function *struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"function"`
}

// resolvedName returns the tool name regardless of which nesting format is used.
func (tc *transcriptToolCall) resolvedName() string {
	if tc.Name != "" {
		return tc.Name
	}
	if tc.Function != nil {
		return tc.Function.Name
	}
	return ""
}

// resolvedArgs returns the arguments regardless of nesting.
func (tc *transcriptToolCall) resolvedArgs() map[string]interface{} {
	if len(tc.Arguments) > 0 {
		return tc.Arguments
	}
	if tc.Function != nil {
		return tc.Function.Arguments
	}
	return nil
}

// ImportFromAntigravity scans all Antigravity CLI transcript files and imports learnings.
func ImportFromAntigravity(store *Store) (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("get home dir: %w", err)
	}

	brainDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	if _, err := os.Stat(brainDir); os.IsNotExist(err) {
		return 0, nil
	}

	entries, err := os.ReadDir(brainDir)
	if err != nil {
		return 0, fmt.Errorf("read brain dir: %w", err)
	}

	total := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		transcriptPath := filepath.Join(brainDir, entry.Name(), ".system_generated", "logs", "transcript.jsonl")
		if _, err := os.Stat(transcriptPath); os.IsNotExist(err) {
			continue
		}
		n, _ := importTranscript(store, transcriptPath)
		total += n
	}
	return total, nil
}

func importTranscript(store *Store, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	count := 0
	var lastUserInput string

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var step transcriptStep
		if err := json.Unmarshal(line, &step); err != nil {
			continue
		}

		if step.Type == "USER_INPUT" && step.Content != "" {
			lastUserInput = truncate(step.Content, 500)
		}

		if step.Type == "PLANNER_RESPONSE" && len(step.ToolCalls) > 0 {
			for _, tc := range step.ToolCalls {
				name := tc.resolvedName()
				args := tc.resolvedArgs()
				if name == "" || args == nil {
					continue
				}
				learning := extractLearning(name, args, lastUserInput)
				if learning == "" {
					continue
				}
				tags := extractTags(name, args)
				if err := store.Add("workflow", learning, lastUserInput, "antigravity", tags); err == nil {
					count++
				}
			}
		}
	}
	return count, scanner.Err()
}

func extractLearning(toolName string, args map[string]interface{}, userContext string) string {
	switch toolName {
	case "run_command":
		// AGY uses "CommandLine", pitty uses "command"
		cmd := firstString(args, "CommandLine", "command")
		if cmd != "" && len(cmd) < 300 && !isNoisy(cmd) {
			return fmt.Sprintf("Command `%s` was used for: %s", cmd, truncate(userContext, 80))
		}
	case "replace_file_content", "write_to_file", "write_file", "edit_file":
		file := firstString(args, "TargetFile", "path")
		desc := firstString(args, "Description", "description")
		if file != "" {
			if desc != "" {
				return fmt.Sprintf("File `%s` was modified: %s", filepath.Base(file), truncate(desc, 120))
			}
			return fmt.Sprintf("File `%s` was modified", filepath.Base(file))
		}
	case "search_web":
		query := firstString(args, "query", "Query")
		if query != "" {
			return fmt.Sprintf("Web search: %s", truncate(query, 100))
		}
	case "view_file", "read_file":
		file := firstString(args, "AbsolutePath", "path")
		if file != "" && !strings.Contains(file, ".system_generated") {
			return fmt.Sprintf("Read file `%s`", filepath.Base(file))
		}
	}
	return ""
}

func extractTags(toolName string, args map[string]interface{}) []string {
	tags := []string{toolName}
	file := firstString(args, "TargetFile", "AbsolutePath", "path")
	if file != "" {
		ext := strings.TrimPrefix(filepath.Ext(file), ".")
		if ext != "" {
			tags = append(tags, ext)
		}
	}
	return tags
}

// firstString returns the first non-empty string value among the given keys.
func firstString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
