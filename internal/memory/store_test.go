package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "pitty-mem-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	return &Store{
		filePath: filepath.Join(tempDir, "knowledge.jsonl"),
		baseDir:  tempDir,
		entries:  []Entry{},
	}
}

func TestStore_AddAndSearch(t *testing.T) {
	store := setupTestStore(t)

	// Add some entries
	err := store.Add("preference", "selalu gunakan bahasa Indonesia", "user context", "test", []string{"lang"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	err = store.Add("fact", "go version is 1.24", "context", "test", []string{"go", "version"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Test exact search
	res := store.Search("indonesia", 10)
	if len(res) != 1 {
		t.Errorf("expected 1 result, got %d", len(res))
	}
	if res[0].Type != "preference" {
		t.Errorf("expected preference, got %s", res[0].Type)
	}

	// Test tag search
	res = store.Search("go", 10)
	if len(res) != 1 {
		t.Errorf("expected 1 result for 'go', got %d", len(res))
	}

	// Test deduplication
	err = store.Add("fact", "Go version is 1.24", "different context", "test2", []string{})
	if err != nil {
		t.Fatalf("Add failed on deduplication: %v", err)
	}

	if store.Count() != 2 {
		t.Errorf("expected 2 entries after deduplication, got %d", store.Count())
	}
}

func TestStore_GetRecent(t *testing.T) {
	store := setupTestStore(t)

	for i := 0; i < 5; i++ {
		_ = store.Add("fact", "fact number "+string(rune('A'+i)), "", "", nil)
	}

	recent := store.GetRecent(2)
	if len(recent) != 2 {
		t.Errorf("expected 2 recent entries, got %d", len(recent))
	}
	if recent[1].Content != "fact number E" {
		t.Errorf("expected last entry to be fact E, got %s", recent[1].Content)
	}
}

func TestStore_SizeLimit(t *testing.T) {
	store := setupTestStore(t)

	// Add more than maxEntries
	for i := 0; i < 2010; i++ {
		// Create unique string to avoid deduplication
		_ = store.Add("fact", "unique fact "+fmt.Sprintf("%d", i), "", "", nil)
	}

	if store.Count() != maxEntries {
		t.Errorf("expected maxEntries %d, got %d", maxEntries, store.Count())
	}
}
