package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ReadFileTool reads file contents.
type ReadFileTool struct{}

func (t *ReadFileTool) Name() string        { return "read_file" }
func (t *ReadFileTool) Description() string { return "Read the contents of a file. Returns file content with line numbers." }
func (t *ReadFileTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"path":       {Type: "string", Description: "Absolute path to the file to read", Required: true},
		"start_line": {Type: "integer", Description: "Start line (1-indexed, optional)"},
		"end_line":   {Type: "integer", Description: "End line (1-indexed, inclusive, optional)"},
	}
}
func (t *ReadFileTool) RequiredParams() []string { return []string{"path"} }

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	startLine := 1
	endLine := len(lines)

	if v, ok := args["start_line"].(float64); ok && v > 0 {
		startLine = int(v)
	}
	if v, ok := args["end_line"].(float64); ok && v > 0 {
		endLine = int(v)
	}

	if startLine < 1 {
		startLine = 1
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > endLine {
		return "", fmt.Errorf("start_line (%d) > end_line (%d)", startLine, endLine)
	}

	// Limit to 500 lines
	maxLines := 500
	truncated := false
	if endLine-startLine+1 > maxLines {
		endLine = startLine + maxLines - 1
		truncated = true
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("File: %s (lines %d-%d of %d)\n", path, startLine, endLine, len(lines)))
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		sb.WriteString(fmt.Sprintf("%4d: %s\n", i+1, lines[i]))
	}

	if truncated {
		sb.WriteString(fmt.Sprintf("\n... truncated (showing %d of %d lines)\n", maxLines, len(lines)))
	}

	return sb.String(), nil
}
