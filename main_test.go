package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyncOnceWritesAndDedups(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, "sessions")
	sessionPath := filepath.Join(sessionsRoot, "2026", "02", "17", "rollout-2026-02-17T12-00-00-11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatal(err)
	}

	content := strings.Join([]string{
		`{"timestamp":"2026-02-17T12:00:00Z","type":"session_meta","payload":{"id":"11111111-2222-3333-4444-555555555555"}}`,
		`{"timestamp":"2026-02-17T12:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}`,
		`{"timestamp":"2026-02-17T12:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":"hi"}}`,
		`{"timestamp":"2026-02-17T12:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{}}}`,
	}, "\n") + "\n"

	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(root, "out", "conversation_history.jsonl")

	first, err := syncOnce(SyncOptions{
		SessionsDir: sessionsRoot,
		OutputPath:  outPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Written != 2 {
		t.Fatalf("expected 2 written records, got %d", first.Written)
	}

	second, err := syncOnce(SyncOptions{
		SessionsDir: sessionsRoot,
		OutputPath:  outPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Written != 0 {
		t.Fatalf("expected 0 new records on second sync, got %d", second.Written)
	}
}

func TestSyncOnceWithSinceFilter(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, "sessions")
	sessionPath := filepath.Join(sessionsRoot, "2026", "02", "17", "rollout-2026-02-17T12-00-00-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o755); err != nil {
		t.Fatal(err)
	}

	content := strings.Join([]string{
		`{"timestamp":"2026-02-17T12:00:00Z","type":"session_meta","payload":{"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}}`,
		`{"timestamp":"2026-02-17T12:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"old"}}`,
		`{"timestamp":"2026-02-17T12:00:10Z","type":"event_msg","payload":{"type":"agent_message","message":"new"}}`,
	}, "\n") + "\n"

	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	since, err := time.Parse(time.RFC3339, "2026-02-17T12:00:05Z")
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(root, "out", "conversation_history.jsonl")
	result, err := syncOnce(SyncOptions{
		SessionsDir: sessionsRoot,
		OutputPath:  outPath,
		Since:       since,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Written != 1 {
		t.Fatalf("expected 1 written record with since filter, got %d", result.Written)
	}

	records, err := loadRecords(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Text != "new" {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestSessionIDFromPath(t *testing.T) {
	path := "/tmp/sessions/rollout-2026-02-17T12-00-00-12345678-1234-1234-1234-123456789abc.jsonl"
	got := sessionIDFromPath(path)
	want := "12345678-1234-1234-1234-123456789abc"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
