package memory

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry represents a single knowledge entry.
type Entry struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"`      // command, correction, preference, workflow, fact
	Content   string   `json:"content"`   // the actual knowledge
	Context   string   `json:"context"`   // what triggered the learning
	Source    string   `json:"source"`    // "pitty" or "antigravity"
	Tags      []string `json:"tags"`
	CreatedAt string   `json:"created_at"`
	UseCount  int      `json:"use_count"` // how many times this was relevant
}

// Store manages the persistent knowledge base.
type Store struct {
	mu       sync.RWMutex
	entries  []Entry
	filePath string
	baseDir  string
}

// NewStore creates or loads a knowledge store.
func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}

	baseDir := filepath.Join(home, ".pitty", "memory")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create memory dir: %w", err)
	}

	s := &Store{
		filePath: filepath.Join(baseDir, "knowledge.jsonl"),
		baseDir:  baseDir,
	}

	if err := s.load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("load knowledge: %w", err)
		}
	}

	return s, nil
}

// Add adds a new knowledge entry.
func (s *Store) Add(entryType, content, context, source string, tags []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Deduplicate
	contentHash := hashContent(content)
	for _, existing := range s.entries {
		if hashContent(existing.Content) == contentHash {
			return nil
		}
	}

	entry := Entry{
		ID:        fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s-%s-%d", content, source, time.Now().UnixNano()))))[:12],
		Type:      entryType,
		Content:   content,
		Context:   context,
		Source:    source,
		Tags:      tags,
		CreatedAt: time.Now().Format(time.RFC3339),
		UseCount:  0,
	}

	s.entries = append(s.entries, entry)
	return s.appendToFile(entry)
}

// Search finds relevant knowledge entries by keyword.
func (s *Store) Search(query string, maxResults int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query = strings.ToLower(query)
	words := strings.Fields(query)

	type scored struct {
		entry Entry
		score int
	}

	var results []scored
	for _, e := range s.entries {
		score := 0
		lowerContent := strings.ToLower(e.Content)
		lowerContext := strings.ToLower(e.Context)

		for _, w := range words {
			if strings.Contains(lowerContent, w) {
				score += 2
			}
			if strings.Contains(lowerContext, w) {
				score++
			}
			for _, tag := range e.Tags {
				if strings.Contains(strings.ToLower(tag), w) {
					score += 3
				}
			}
		}

		if score > 0 {
			results = append(results, scored{entry: e, score: score + e.UseCount})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	entries := make([]Entry, len(results))
	for i, r := range results {
		entries[i] = r.entry
	}
	return entries
}

// GetRecent returns the most recent N entries.
func (s *Store) GetRecent(n int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if n <= 0 || len(s.entries) == 0 {
		return nil
	}

	start := len(s.entries) - n
	if start < 0 {
		start = 0
	}

	result := make([]Entry, len(s.entries)-start)
	copy(result, s.entries[start:])
	return result
}

// Count returns total number of entries.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// FormatForPrompt formats relevant knowledge as context for the system prompt.
func (s *Store) FormatForPrompt(query string) string {
	entries := s.Search(query, 10)
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Learned Knowledge\n")
	sb.WriteString("The following are things you've learned from previous interactions:\n\n")

	for _, e := range entries {
		switch e.Type {
		case "command":
			sb.WriteString(fmt.Sprintf("- **Command**: %s\n", e.Content))
		case "correction":
			sb.WriteString(fmt.Sprintf("- **Correction**: %s\n", e.Content))
		case "preference":
			sb.WriteString(fmt.Sprintf("- **User prefers**: %s\n", e.Content))
		case "workflow":
			sb.WriteString(fmt.Sprintf("- **Workflow**: %s\n", e.Content))
		case "fact":
			sb.WriteString(fmt.Sprintf("- **Fact**: %s\n", e.Content))
		default:
			sb.WriteString(fmt.Sprintf("- %s\n", e.Content))
		}
		if e.Context != "" {
			sb.WriteString(fmt.Sprintf("  Context: %s\n", e.Context))
		}
	}

	return sb.String()
}

func (s *Store) load() error {
	f, err := os.Open(s.filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		s.entries = append(s.entries, entry)
	}
	return scanner.Err()
}

func (s *Store) appendToFile(entry Entry) error {
	f, err := os.OpenFile(s.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open knowledge file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	_, err = f.Write(append(data, '\n'))
	return err
}

func hashContent(s string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(strings.ToLower(s))))
	return fmt.Sprintf("%x", h)[:16]
}
