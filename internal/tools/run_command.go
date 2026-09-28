package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RunCommandTool executes a shell command.
type RunCommandTool struct{}

func (t *RunCommandTool) Name() string        { return "run_command" }
func (t *RunCommandTool) Description() string { return "Execute a shell command and return its output." }
func (t *RunCommandTool) Parameters() map[string]ToolParam {
	return map[string]ToolParam{
		"command":         {Type: "string", Description: "The shell command to execute", Required: true},
		"cwd":             {Type: "string", Description: "Working directory (optional, defaults to current dir)"},
		"timeout_seconds": {Type: "integer", Description: "Timeout in seconds (default: 30)"},
	}
}
func (t *RunCommandTool) RequiredParams() []string { return []string{"command"} }

func (t *RunCommandTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}

	cwd, _ := args["cwd"].(string)
	timeoutSec := 30.0
	if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
		timeoutSec = v
	}

	timeout := time.Duration(timeoutSec) * time.Second
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", command)
	if cwd != "" {
		cmd.Dir = cwd
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	var sb strings.Builder
	if stdout.Len() > 0 {
		sb.WriteString(stdout.String())
	}
	if stderr.Len() > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("STDERR:\n")
		sb.WriteString(stderr.String())
	}

	output := sb.String()

	// Limit output size
	const maxLen = 10000
	if len(output) > maxLen {
		output = output[:maxLen] + "\n... (output truncated)"
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return output, fmt.Errorf("command timed out after %.0fs", timeoutSec)
		}
		return output + "\nExit code: " + err.Error(), nil
	}

	return output, nil
}
