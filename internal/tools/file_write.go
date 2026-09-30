package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFileTool creates or overwrites a file.
type WriteFileTool struct{}

func (t *WriteFileTool) Name() string        { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "Create a new file or completely overwrite an existing one. Parent directories are created automatically."
}
func (t *WriteFileTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"path":      {Type: "string", Description: "Absolute path to the file to write", Required: true},
		"content":   {Type: "string", Description: "Full content to write to the file", Required: true},
		"overwrite": {Type: "boolean", Description: "If false (default), refuses to overwrite an existing file"},
	}
}
func (t *WriteFileTool) RequiredParams() []string { return []string{"path", "content"} }

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	overwrite, _ := args["overwrite"].(bool)

	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("create directories: %w", err)
	}

	// Guard against accidental overwrite
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("file %s already exists — set overwrite=true to replace it", path)
		}
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	lines := strings.Count(content, "\n") + 1
	return fmt.Sprintf("Written %s (%d bytes, %d lines)", path, len(content), lines), nil
}
