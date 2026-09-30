package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// EditFileTool performs a search-and-replace in a file.
type EditFileTool struct{}

func (t *EditFileTool) Name() string        { return "edit_file" }
func (t *EditFileTool) Description() string {
	return "Search and replace text in a file. The target must exactly match a substring of the file. Use read_file first to get the exact content."
}
func (t *EditFileTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"path":        {Type: "string", Description: "Absolute path to the file to edit", Required: true},
		"target":      {Type: "string", Description: "Exact string to find and replace", Required: true},
		"replacement": {Type: "string", Description: "String to replace the target with", Required: true},
		"all":         {Type: "boolean", Description: "Replace all occurrences (default: false, replaces only first)"},
	}
}
func (t *EditFileTool) RequiredParams() []string { return []string{"path", "target", "replacement"} }

func (t *EditFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	target, _ := args["target"].(string)
	replacement, _ := args["replacement"].(string)
	replaceAll, _ := args["all"].(bool)

	if path == "" || target == "" {
		return "", fmt.Errorf("path and target are required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	count := strings.Count(content, target)
	if count == 0 {
		return "", fmt.Errorf("target string not found in %s — use read_file to verify the exact content first", path)
	}

	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(content, target, replacement)
	} else {
		newContent = strings.Replace(content, target, replacement, 1)
	}

	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	replaced := 1
	if replaceAll {
		replaced = count
	}
	return fmt.Sprintf("Replaced %d of %d occurrence(s) in %s", replaced, count, path), nil
}
