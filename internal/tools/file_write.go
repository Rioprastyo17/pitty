package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileTool writes content to a file.
type WriteFileTool struct{}

func (t *WriteFileTool) Name() string        { return "write_file" }
func (t *WriteFileTool) Description() string { return "Write content to a file. Creates parent directories if needed." }
func (t *WriteFileTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"path":      {Type: "string", Description: "Absolute path to the file to write", Required: true},
		"content":   {Type: "string", Description: "Content to write to the file", Required: true},
		"overwrite": {Type: "boolean", Description: "If true, overwrite existing file (default: false)"},
		"append":    {Type: "boolean", Description: "If true, append to existing file"},
	}
}
func (t *WriteFileTool) RequiredParams() []string { return []string{"path", "content"} }

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	overwrite, _ := args["overwrite"].(bool)
	appendMode, _ := args["append"].(bool)

	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Create parent directories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create directories: %w", err)
	}

	if appendMode {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("open file for append: %w", err)
		}
		defer f.Close()
		if _, err := f.WriteString(content); err != nil {
			return "", fmt.Errorf("append to file: %w", err)
		}
		return fmt.Sprintf("Appended to %s", path), nil
	}

	// Check if file exists and overwrite flag
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("file %s already exists. Set overwrite=true to overwrite", path)
		}
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return fmt.Sprintf("Written to %s (%d bytes)", path, len(content)), nil
}
