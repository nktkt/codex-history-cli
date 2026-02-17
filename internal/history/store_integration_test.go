package history

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStoreImportSearchAnalyzeFlow(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not available")
	}

	dbPath := filepath.Join(t.TempDir(), "history.db")
	store := NewStore(dbPath)
	if err := store.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}

	conversations := []Conversation{
		{
			ID:        "conv-1",
			Provider:  "codex",
			Title:     "debug",
			CreatedAt: "2026-02-17T12:00:00Z",
			UpdatedAt: "2026-02-17T12:00:00Z",
			Messages: []Message{
				{ID: "m1", ConversationID: "conv-1", Role: "user", Content: "there is a go compile error", CreatedAt: "2026-02-17T12:00:00Z"},
				{ID: "m2", ConversationID: "conv-1", Role: "assistant", Content: "check imports and run tests", CreatedAt: "2026-02-17T12:00:01Z"},
			},
		},
	}
	summary, err := store.Import(conversations, "codex", "test.json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if summary.Messages != 2 {
		t.Fatalf("expected 2 messages, got %d", summary.Messages)
	}

	results, err := store.Search("compile error", "codex", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}

	analyses, err := store.AnalyzeWithOptions(AnalyzeOptions{Provider: "codex", Limit: 10})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(analyses) == 0 {
		t.Fatal("expected analysis results")
	}

	findings, err := store.SecurityFindings(1, 10)
	if err != nil {
		t.Fatalf("security findings: %v", err)
	}
	_ = findings

	trend, err := store.TopicTrend(30)
	if err != nil {
		t.Fatalf("topic trend: %v", err)
	}
	if len(trend) == 0 {
		t.Fatal("expected topic trend buckets")
	}
}
