package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// transcriptStep represents a step in an Antigravity CLI transcript.
type transcriptStep struct {
	Source  string `json:"source"`
	Type   string `json:"type"`
	Content string `json:"content"`
	ToolCalls []struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	} `json:"tool_calls"`
}

// ImportFromAntigravity scans Antigravity CLI transcripts and imports learnings.
func ImportFromAntigravity(store *Store) (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("get home dir: %w", err)
	}

	brainDir := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	if _, err := os.Stat(brainDir); os.IsNotExist(err) {
		return 0, nil
	}

	count := 0

	entries, err := os.ReadDir(brainDir)
	if err != nil {
		return 0, fmt.Errorf("read brain dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		transcriptPath := filepath.Join(brainDir, entry.Name(), ".system_generated", "logs", "transcript.jsonl")
		if _, err := os.Stat(transcriptPath); os.IsNotExist(err) {
			continue
		}

		n, err := importTranscript(store, transcriptPath)
		if err != nil {
			continue
		}
		count += n
	}

	return count, nil
}

func importTranscript(store *Store, path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 2*1024*1024), 2*1024*1024)

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
			lastUserInput = step.Content
			if len(lastUserInput) > 500 {
				lastUserInput = lastUserInput[:500]
			}
		}

		if step.Type == "PLANNER_RESPONSE" && len(step.ToolCalls) > 0 {
			for _, tc := range step.ToolCalls {
				learning := extractLearning(tc.Name, tc.Arguments, lastUserInput)
				if learning != "" {
					tags := extractTags(tc.Name, tc.Arguments)
					err := store.Add("workflow", learning, lastUserInput, "antigravity", tags)
					if err == nil {
						count++
					}
				}
			}
		}
	}

	return count, scanner.Err()
}

func extractLearning(toolName string, args map[string]interface{}, userContext string) string {
	switch toolName {
	case "run_command":
		cmd, _ := args["CommandLine"].(string)
		if cmd == "" {
			cmd, _ = args["command"].(string)
		}
		if cmd != "" && len(cmd) < 300 {
			return fmt.Sprintf("Command `%s` was used", cmd)
		}
	case "replace_file_content", "write_to_file":
		file, _ := args["TargetFile"].(string)
		if file == "" {
			file, _ = args["path"].(string)
		}
		desc, _ := args["Description"].(string)
		if file != "" && desc != "" {
			return fmt.Sprintf("File `%s` was modified: %s", filepath.Base(file), desc)
		}
	case "search_web":
		query, _ := args["query"].(string)
		if query != "" {
			return fmt.Sprintf("Web search for: %s", query)
		}
	}
	return ""
}

func extractTags(toolName string, args map[string]interface{}) []string {
	tags := []string{toolName}

	if file, ok := args["TargetFile"].(string); ok {
		ext := filepath.Ext(file)
		if ext != "" {
			tags = append(tags, strings.TrimPrefix(ext, "."))
		}
	}
	if file, ok := args["path"].(string); ok {
		ext := filepath.Ext(file)
		if ext != "" {
			tags = append(tags, strings.TrimPrefix(ext, "."))
		}
	}

	return tags
}
