package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// EditFileTool does search-and-replace in a file.
type EditFileTool struct{}

func (t *EditFileTool) Name() string        { return "edit_file" }
func (t *EditFileTool) Description() string { return "Search and replace text in a file. The target must be an exact substring of the file content." }
func (t *EditFileTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"path":        {Type: "string", Description: "Absolute path to the file to edit", Required: true},
		"target":      {Type: "string", Description: "Exact string to find and replace", Required: true},
		"replacement": {Type: "string", Description: "String to replace the target with", Required: true},
	}
}
func (t *EditFileTool) RequiredParams() []string { return []string{"path", "target", "replacement"} }

func (t *EditFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	target, _ := args["target"].(string)
	replacement, _ := args["replacement"].(string)

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
		return "", fmt.Errorf("target string not found in %s", path)
	}

	newContent := strings.Replace(content, target, replacement, 1)

	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Replaced 1 occurrence in %s (total found: %d)", path, count), nil
}
