package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SearchFilesTool searches for patterns in files recursively.
type SearchFilesTool struct{}

func (t *SearchFilesTool) Name() string        { return "search_files" }
func (t *SearchFilesTool) Description() string { return "Search for a regex pattern in files recursively. Returns matching lines." }
func (t *SearchFilesTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"pattern":     {Type: "string", Description: "Regex pattern to search for", Required: true},
		"path":        {Type: "string", Description: "Directory to search in (default: current dir)"},
		"include":     {Type: "string", Description: "File glob pattern to include (e.g. *.go)"},
		"max_results": {Type: "integer", Description: "Maximum results to return (default: 50)"},
	}
}
func (t *SearchFilesTool) RequiredParams() []string { return []string{"pattern"} }

// skipDirs are directories to skip during search.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true,
	".venv": true, "vendor": true, ".idea": true, ".vscode": true,
}

func (t *SearchFilesTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	searchPath := "."
	if v, _ := args["path"].(string); v != "" {
		searchPath = v
	}
	includeGlob, _ := args["include"].(string)
	maxResults := 50
	if v, ok := args["max_results"].(float64); ok && v > 0 {
		maxResults = int(v)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	var results []string
	count := 0

	err = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || count >= maxResults {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary/large files
		if info.Size() > 1024*1024 {
			return nil
		}

		// Apply include glob
		if includeGlob != "" {
			matched, _ := filepath.Match(includeGlob, info.Name())
			if !matched {
				return nil
			}
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				results = append(results, fmt.Sprintf("%s:%d: %s", path, lineNum, strings.TrimSpace(line)))
				count++
				if count >= maxResults {
					break
				}
			}
		}
		return nil
	})

	if err != nil && err != context.Canceled {
		return "", err
	}

	if len(results) == 0 {
		return "No matches found.", nil
	}

	output := strings.Join(results, "\n")
	if count >= maxResults {
		output += fmt.Sprintf("\n... (limited to %d results)", maxResults)
	}
	return output, nil
}
