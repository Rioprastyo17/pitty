package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Level represents the severity of a log entry.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

var levelNames = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
	LevelFatal: "FATAL",
}

// Logger writes structured log lines to a rotating daily log file.
type Logger struct {
	mu       sync.Mutex
	file     *os.File
	filePath string
	writer   io.Writer
	minLevel Level
	session  string // unique per-run identifier
}

// global default logger (set by Init).
var defaultLogger *Logger

// Init creates (or opens) the log file and sets it as the default logger.
// The log file is placed at: ~/.pitty/logs/pitty-YYYY-MM-DD.log
// It returns the path of the active log file.
func Init(minLevel Level) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	logDir := filepath.Join(home, ".pitty", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}

	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, "pitty-"+dateStr+".log")

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", fmt.Errorf("open log file: %w", err)
	}

	session := fmt.Sprintf("%x", time.Now().UnixNano())[:8]
	defaultLogger = &Logger{
		file:     f,
		filePath: logPath,
		writer:   f,
		minLevel: minLevel,
		session:  session,
	}

	defaultLogger.Info("=== pitty session started (session=%s) ===", session)
	return logPath, nil
}

// Close flushes and closes the log file. Call on exit.
func Close() {
	if defaultLogger != nil {
		defaultLogger.mu.Lock()
		defer defaultLogger.mu.Unlock()
		defaultLogger.writeRaw(LevelInfo, "=== session ended ===")
		_ = defaultLogger.file.Sync()
		_ = defaultLogger.file.Close()
	}
}

// FilePath returns the current log file path.
func FilePath() string {
	if defaultLogger == nil {
		return ""
	}
	return defaultLogger.filePath
}

// ── Level-named package-level helpers ─────────────────────────────────────────

func Debug(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelDebug, 2, format, args...)
	}
}

func Info(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelInfo, 2, format, args...)
	}
}

func Warn(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelWarn, 2, format, args...)
	}
}

func Error(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelError, 2, format, args...)
	}
}

// ErrorErr logs an error value (no-op if err is nil).
func ErrorErr(err error, context string) {
	if defaultLogger != nil && err != nil {
		defaultLogger.log(LevelError, 2, "%s: %v", context, err)
	}
}

func Fatal(format string, args ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.log(LevelFatal, 2, format, args...)
		_ = defaultLogger.file.Sync()
	}
}

// ── Tool call / result helpers ─────────────────────────────────────────────────

// ToolCall logs a tool invocation.
func ToolCall(name string, args map[string]interface{}) {
	if defaultLogger == nil {
		return
	}
	var sb strings.Builder
	sb.WriteString("TOOL_CALL name=")
	sb.WriteString(name)
	sb.WriteString(" args={")
	i := 0
	for k, v := range args {
		if i > 0 {
			sb.WriteString(", ")
		}
		val := fmt.Sprintf("%v", v)
		if len(val) > 80 {
			val = val[:80] + "…"
		}
		sb.WriteString(fmt.Sprintf("%s=%q", k, val))
		i++
	}
	sb.WriteString("}")
	defaultLogger.log(LevelInfo, 2, "%s", sb.String())
}

// ToolResult logs a tool's return value.
func ToolResult(name string, result string, err error) {
	if defaultLogger == nil {
		return
	}
	if err != nil {
		defaultLogger.log(LevelError, 2, "TOOL_RESULT name=%s error=%v", name, err)
		return
	}
	preview := result
	if len(preview) > 200 {
		preview = preview[:200] + "…"
	}
	defaultLogger.log(LevelInfo, 2, "TOOL_RESULT name=%s len=%d preview=%q", name, len(result), preview)
}

// OllamaRequest logs an outgoing Ollama API call.
func OllamaRequest(model string, messageCount int, hasTools bool) {
	if defaultLogger != nil {
		defaultLogger.log(LevelDebug, 2, "OLLAMA_REQ model=%s messages=%d tools=%v", model, messageCount, hasTools)
	}
}

// OllamaResponse logs a completed Ollama API response.
func OllamaResponse(model string, contentLen int, toolCallCount int, durationMs int64) {
	if defaultLogger != nil {
		defaultLogger.log(LevelDebug, 2, "OLLAMA_RESP model=%s content_len=%d tool_calls=%d duration_ms=%d",
			model, contentLen, toolCallCount, durationMs)
	}
}

// ── Recovery helper ────────────────────────────────────────────────────────────

// Recover should be deferred in goroutines to log panics before crashing.
func Recover(context string) {
	if r := recover(); r != nil {
		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		if defaultLogger != nil {
			defaultLogger.log(LevelFatal, 2, "PANIC in %s: %v\n%s", context, r, string(buf[:n]))
			_ = defaultLogger.file.Sync()
		}
		panic(r) // re-panic after logging
	}
}

// ── Internal ───────────────────────────────────────────────────────────────────

func (l *Logger) log(level Level, skip int, format string, args ...interface{}) {
	if level < l.minLevel {
		return
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writeRaw(level, msg)
}

func (l *Logger) writeRaw(level Level, msg string) {
	// Format: 2006-01-02T15:04:05.000Z07:00 [LEVEL] [session] caller: message
	ts := time.Now().Format("2006-01-02T15:04:05.000")
	levelStr := levelNames[level]
	caller := callerInfo(4)
	line := fmt.Sprintf("%s [%-5s] [%s] %s: %s\n", ts, levelStr, l.session, caller, msg)
	_, _ = l.writer.Write([]byte(line))
}

// callerInfo returns "file:line" of the caller at the given stack depth.
func callerInfo(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	// Trim to package-relative path
	if idx := strings.LastIndex(file, "/pitty/"); idx >= 0 {
		file = file[idx+1:]
	} else {
		parts := strings.Split(file, "/")
		if len(parts) > 2 {
			file = strings.Join(parts[len(parts)-2:], "/")
		}
	}
	return fmt.Sprintf("%s:%d", file, line)
}

// ── Logger instance methods (for passing around if needed) ─────────────────────

func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LevelInfo, 2, format, args...)
}

func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LevelError, 2, format, args...)
}
