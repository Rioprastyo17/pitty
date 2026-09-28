package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ListDirTool lists directory contents.
type ListDirTool struct{}

func (t *ListDirTool) Name() string        { return "list_directory" }
func (t *ListDirTool) Description() string { return "List files and directories in a given path." }
func (t *ListDirTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"path":      {Type: "string", Description: "Path to the directory to list", Required: true},
		"recursive": {Type: "boolean", Description: "List recursively (default: false)"},
		"max_depth": {Type: "integer", Description: "Max depth for recursive listing (default: 3)"},
	}
}
func (t *ListDirTool) RequiredParams() []string { return []string{"path"} }

func (t *ListDirTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	dirPath, _ := args["path"].(string)
	if dirPath == "" {
		return "", fmt.Errorf("path is required")
	}

	recursive, _ := args["recursive"].(bool)
	maxDepth := 3
	if v, ok := args["max_depth"].(float64); ok && v > 0 {
		maxDepth = int(v)
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		return "", fmt.Errorf("stat: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dirPath)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Directory: %s\n\n", dirPath))

	if recursive {
		baseDepth := strings.Count(filepath.Clean(dirPath), string(filepath.Separator))
		count := 0
		err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - baseDepth
			if depth > maxDepth {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if path == dirPath {
				return nil
			}
			indent := strings.Repeat("  ", depth-1)
			if info.IsDir() {
				sb.WriteString(fmt.Sprintf("%s📁 %s/\n", indent, info.Name()))
			} else {
				sb.WriteString(fmt.Sprintf("%s📄 %s (%s)\n", indent, info.Name(), formatSize(info.Size())))
			}
			count++
			if count > 500 {
				return fmt.Errorf("too many entries")
			}
			return nil
		})
	} else {
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if entry.IsDir() {
				sb.WriteString(fmt.Sprintf("📁 %s/\n", entry.Name()))
			} else {
				sb.WriteString(fmt.Sprintf("📄 %s (%s)\n", entry.Name(), formatSize(info.Size())))
			}
		}
	}

	return sb.String(), err
}

func formatSize(size int64) string {
	switch {
	case size >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(size)/(1024*1024*1024))
	case size >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	case size >= 1024:
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
