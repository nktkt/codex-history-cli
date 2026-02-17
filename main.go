package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

const scannerMaxTokenSize = 16 * 1024 * 1024

var sessionIDPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

type Record struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	Timestamp  string `json:"timestamp"`
	Role       string `json:"role"`
	Text       string `json:"text"`
	SourceFile string `json:"source_file,omitempty"`
	SourceLine int    `json:"source_line,omitempty"`
}

type envelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID string `json:"id"`
}

type eventPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type SyncOptions struct {
	SessionsDir string
	OutputPath  string
	Since       time.Time
	DryRun      bool
}

type SyncResult struct {
	Files   int
	Scanned int
	Written int
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "sync":
		err = runSync(os.Args[2:])
	case "watch":
		err = runWatch(os.Args[2:])
	case "show":
		err = runShow(os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		printUsage()
		err = fmt.Errorf("unknown command: %s", os.Args[1])
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`codex-history: record Codex conversations from ~/.codex/sessions

Usage:
  codex-history sync  [--sessions-dir DIR] [--out FILE] [--from RFC3339] [--dry-run]
  codex-history watch [--sessions-dir DIR] [--out FILE] [--from RFC3339] [--interval 5s]
  codex-history show  [--in FILE] [--session ID] [--role user|assistant] [--limit 20] [--json]

Defaults:
  sessions-dir: %s
  out/in file : %s
`, defaultSessionsDir(), defaultOutputFile())
}

func defaultSessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex/sessions"
	}
	return filepath.Join(home, ".codex", "sessions")
}

func defaultOutputFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".codex/conversation_history.jsonl"
	}
	return filepath.Join(home, ".codex", "conversation_history.jsonl")
}

func runSync(args []string) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	sessionsDir := fs.String("sessions-dir", defaultSessionsDir(), "Codex sessions directory")
	outPath := fs.String("out", defaultOutputFile(), "Output JSONL path")
	from := fs.String("from", "", "Only include records at/after this RFC3339 timestamp")
	dryRun := fs.Bool("dry-run", false, "Scan and count records without writing")

	if err := fs.Parse(args); err != nil {
		return err
	}

	since, err := parseFromTime(*from)
	if err != nil {
		return err
	}

	result, err := syncOnce(SyncOptions{
		SessionsDir: *sessionsDir,
		OutputPath:  *outPath,
		Since:       since,
		DryRun:      *dryRun,
	})
	if err != nil {
		return err
	}

	fmt.Printf("files=%d scanned=%d new=%d output=%s\n", result.Files, result.Scanned, result.Written, *outPath)
	return nil
}

func runWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	sessionsDir := fs.String("sessions-dir", defaultSessionsDir(), "Codex sessions directory")
	outPath := fs.String("out", defaultOutputFile(), "Output JSONL path")
	from := fs.String("from", "", "Only include records at/after this RFC3339 timestamp")
	interval := fs.Duration("interval", 5*time.Second, "Sync interval")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *interval <= 0 {
		return errors.New("interval must be > 0")
	}

	since, err := parseFromTime(*from)
	if err != nil {
		return err
	}

	opts := SyncOptions{
		SessionsDir: *sessionsDir,
		OutputPath:  *outPath,
		Since:       since,
		DryRun:      false,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("watching %s -> %s (interval=%s)\n", opts.SessionsDir, opts.OutputPath, interval.String())

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	for {
		result, err := syncOnce(opts)
		if err != nil {
			return err
		}

		if result.Written > 0 {
			fmt.Printf("%s files=%d scanned=%d new=%d\n", time.Now().UTC().Format(time.RFC3339), result.Files, result.Scanned, result.Written)
		}

		select {
		case <-ctx.Done():
			fmt.Println("watch stopped")
			return nil
		case <-ticker.C:
		}
	}
}

func runShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	inputPath := fs.String("in", defaultOutputFile(), "Input JSONL path")
	sessionID := fs.String("session", "", "Filter by session ID")
	role := fs.String("role", "", "Filter by role: user or assistant")
	limit := fs.Int("limit", 20, "Maximum records to print, 0 means all")
	jsonOut := fs.Bool("json", false, "Print as JSONL")
	maxChars := fs.Int("max-chars", 140, "Max chars per message line, 0 means no truncation")

	if err := fs.Parse(args); err != nil {
		return err
	}

	records, err := loadRecords(*inputPath)
	if err != nil {
		return err
	}

	filtered := make([]Record, 0, len(records))
	for _, record := range records {
		if *sessionID != "" && record.SessionID != *sessionID {
			continue
		}
		if *role != "" && record.Role != *role {
			continue
		}
		filtered = append(filtered, record)
	}

	if *limit > 0 && len(filtered) > *limit {
		filtered = filtered[len(filtered)-*limit:]
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		for _, record := range filtered {
			if err := enc.Encode(record); err != nil {
				return err
			}
		}
		return nil
	}

	for _, record := range filtered {
		text := oneLine(record.Text, *maxChars)
		fmt.Printf("%s [%s] %s: %s\n", record.Timestamp, shortSessionID(record.SessionID), record.Role, text)
	}
	return nil
}

func parseFromTime(from string) (time.Time, error) {
	if strings.TrimSpace(from) == "" {
		return time.Time{}, nil
	}

	ts, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --from value %q: expected RFC3339", from)
	}
	return ts.UTC(), nil
}

func syncOnce(opts SyncOptions) (SyncResult, error) {
	files, err := listSessionFiles(opts.SessionsDir)
	if err != nil {
		return SyncResult{}, err
	}

	existing, err := loadExistingIDs(opts.OutputPath)
	if err != nil {
		return SyncResult{}, err
	}

	newRecords := make([]Record, 0, 128)
	result := SyncResult{Files: len(files)}

	for _, path := range files {
		records, err := extractRecords(path, opts.Since)
		if err != nil {
			return SyncResult{}, fmt.Errorf("failed to parse %s: %w", path, err)
		}

		result.Scanned += len(records)
		for _, record := range records {
			if _, exists := existing[record.ID]; exists {
				continue
			}
			existing[record.ID] = struct{}{}
			newRecords = append(newRecords, record)
		}
	}

	result.Written = len(newRecords)

	if opts.DryRun || len(newRecords) == 0 {
		return result, nil
	}

	if err := appendRecords(opts.OutputPath, newRecords); err != nil {
		return SyncResult{}, err
	}

	return result, nil
}

func listSessionFiles(root string) ([]string, error) {
	files := make([]string, 0, 64)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func extractRecords(path string, since time.Time) ([]Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scannerMaxTokenSize)

	sessionID := sessionIDFromPath(path)
	records := make([]Record, 0, 128)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		var item envelope
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum, err)
		}

		switch item.Type {
		case "session_meta":
			var meta sessionMetaPayload
			if err := json.Unmarshal(item.Payload, &meta); err == nil && strings.TrimSpace(meta.ID) != "" {
				sessionID = strings.TrimSpace(meta.ID)
			}
		case "event_msg":
			var ev eventPayload
			if err := json.Unmarshal(item.Payload, &ev); err != nil {
				continue
			}

			role := ""
			switch ev.Type {
			case "user_message":
				role = "user"
			case "agent_message":
				role = "assistant"
			}

			text := strings.TrimSpace(ev.Message)
			if role == "" || text == "" {
				continue
			}

			timestamp := normalizeTimestamp(item.Timestamp)
			if !since.IsZero() {
				if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil && parsed.Before(since) {
					continue
				}
			}

			record := Record{
				ID:         makeRecordID(sessionID, timestamp, role, text),
				SessionID:  sessionID,
				Timestamp:  timestamp,
				Role:       role,
				Text:       text,
				SourceFile: path,
				SourceLine: lineNum,
			}
			records = append(records, record)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func normalizeTimestamp(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return t.UTC().Format(time.RFC3339Nano)
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return t.UTC().Format(time.RFC3339Nano)
	}
	return trimmed
}

func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	matches := sessionIDPattern.FindAllString(base, -1)
	if len(matches) == 0 {
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return matches[len(matches)-1]
}

func makeRecordID(sessionID, timestamp, role, text string) string {
	raw := sessionID + "\n" + timestamp + "\n" + role + "\n" + text
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

func loadExistingIDs(path string) (map[string]struct{}, error) {
	ids := make(map[string]struct{})

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ids, nil
		}
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scannerMaxTokenSize)

	for scanner.Scan() {
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}

		id := strings.TrimSpace(record.ID)
		if id == "" && record.SessionID != "" && record.Timestamp != "" && record.Role != "" && record.Text != "" {
			id = makeRecordID(record.SessionID, record.Timestamp, record.Role, record.Text)
		}
		if id != "" {
			ids[id] = struct{}{}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func appendRecords(path string, records []Record) error {
	if len(records) == 0 {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)

	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}

	return writer.Flush()
}

func loadRecords(path string) ([]Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), scannerMaxTokenSize)

	records := make([]Record, 0, 256)
	for scanner.Scan() {
		var record Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		records = append(records, record)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func shortSessionID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func oneLine(text string, maxChars int) string {
	value := strings.ReplaceAll(strings.TrimSpace(text), "\n", `\n`)
	if maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return string(runes[:maxChars]) + "..."
}
