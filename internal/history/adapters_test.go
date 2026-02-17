package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProviderFileConversationArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.json")
	content := `[
  {
    "id": "conv-1",
    "title": "debug session",
    "messages": [
      {"role": "user", "content": "there is an error"},
      {"role": "assistant", "content": "let's fix it"}
    ]
  }
]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	convs, err := ParseProviderFile("codex", path)
	if err != nil {
		t.Fatalf("parse provider file: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if convs[0].ID != "conv-1" {
		t.Fatalf("unexpected conv id: %s", convs[0].ID)
	}
	if len(convs[0].Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(convs[0].Messages))
	}
	if convs[0].Messages[0].Role != "user" {
		t.Fatalf("unexpected role: %s", convs[0].Messages[0].Role)
	}
}

func TestParseProviderFileJSONLPromptResponse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ollama.jsonl")
	content := "{\"conversation_id\":\"c1\",\"prompt\":\"hello\",\"response\":\"hi\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	convs, err := ParseProviderFile("ollama", path)
	if err != nil {
		t.Fatalf("parse provider file: %v", err)
	}
	if len(convs) != 1 {
		t.Fatalf("expected 1 conversation, got %d", len(convs))
	}
	if len(convs[0].Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(convs[0].Messages))
	}
}
