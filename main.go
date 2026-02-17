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

type RecordFilter struct {
	SessionID string
	Role      string
	Contains  string
	From      time.Time
	To        time.Time
}

type HistoryStats struct {
	Total          int    `json:"total"`
	User           int    `json:"user"`
	Assistant      int    `json:"assistant"`
	Other          int    `json:"other"`
	SessionCount   int    `json:"session_count"`
	FirstTimestamp string `json:"first_timestamp,omitempty"`
	LastTimestamp  string `json:"last_timestamp,omitempty"`
}

type SessionSummary struct {
	SessionID      string `json:"session_id"`
	Total          int    `json:"total"`
	User           int    `json:"user"`
	Assistant      int    `json:"assistant"`
	Other          int    `json:"other"`
	FirstTimestamp string `json:"first_timestamp,omitempty"`
	LastTimestamp  string `json:"last_timestamp,omitempty"`
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
	case "stats":
		err = runStats(os.Args[2:])
	case "sessions":
		err = runSessions(os.Args[2:])
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
  codex-history sync     [--sessions-dir DIR] [--out FILE] [--from RFC3339] [--dry-run]
  codex-history watch    [--sessions-dir DIR] [--out FILE] [--from RFC3339] [--interval 5s]
  codex-history show     [--in FILE] [--session ID] [--role user|assistant] [--from RFC3339] [--to RFC3339] [--contains WORD] [--limit 20] [--desc] [--json]
  codex-history stats    [--in FILE] [--session ID] [--role user|assistant] [--from RFC3339] [--to RFC3339] [--contains WORD] [--json]
  codex-history sessions [--in FILE] [--from RFC3339] [--to RFC3339] [--contains WORD] [--limit 20] [--json]

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

	since, err := parseBoundTime(*from, "--from")
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

	since, err := parseBoundTime(*from, "--from")
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
	from := fs.String("from", "", "Filter records at/after this RFC3339 timestamp")
	to := fs.String("to", "", "Filter records at/before this RFC3339 timestamp")
	contains := fs.String("contains", "", "Case-insensitive substring filter for text")
	limit := fs.Int("limit", 20, "Maximum records to print, 0 means all")
	desc := fs.Bool("desc", false, "Show newest records first")
	jsonOut := fs.Bool("json", false, "Print as JSONL")
	maxChars := fs.Int("max-chars", 140, "Max chars per message line, 0 means no truncation")

	if err := fs.Parse(args); err != nil {
		return err
	}

	fromTime, err := parseBoundTime(*from, "--from")
	if err != nil {
		return err
	}
	toTime, err := parseBoundTime(*to, "--to")
	if err != nil {
		return err
	}
	if err := validateTimeRange(fromTime, toTime); err != nil {
		return err
	}

	records, err := loadRecords(*inputPath)
	if err != nil {
		return err
	}

	filtered := filterRecords(records, RecordFilter{
		SessionID: strings.TrimSpace(*sessionID),
		Role:      strings.TrimSpace(*role),
		Contains:  strings.TrimSpace(*contains),
		From:      fromTime,
		To:        toTime,
	})

	sortRecordsChronological(filtered)
	if *desc {
		reverseRecords(filtered)
	}

	if *limit > 0 && len(filtered) > *limit {
		if *desc {
			filtered = filtered[:*limit]
		} else {
			filtered = filtered[len(filtered)-*limit:]
		}
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

func runStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	inputPath := fs.String("in", defaultOutputFile(), "Input JSONL path")
	sessionID := fs.String("session", "", "Filter by session ID")
	role := fs.String("role", "", "Filter by role: user or assistant")
	from := fs.String("from", "", "Filter records at/after this RFC3339 timestamp")
	to := fs.String("to", "", "Filter records at/before this RFC3339 timestamp")
	contains := fs.String("contains", "", "Case-insensitive substring filter for text")
	jsonOut := fs.Bool("json", false, "Print as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	fromTime, err := parseBoundTime(*from, "--from")
	if err != nil {
		return err
	}
	toTime, err := parseBoundTime(*to, "--to")
	if err != nil {
		return err
	}
	if err := validateTimeRange(fromTime, toTime); err != nil {
		return err
	}

	records, err := loadRecords(*inputPath)
	if err != nil {
		return err
	}

	filtered := filterRecords(records, RecordFilter{
		SessionID: strings.TrimSpace(*sessionID),
		Role:      strings.TrimSpace(*role),
		Contains:  strings.TrimSpace(*contains),
		From:      fromTime,
		To:        toTime,
	})

	stats := computeStats(filtered)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(stats)
	}

	fmt.Printf("total=%d\n", stats.Total)
	fmt.Printf("user=%d\n", stats.User)
	fmt.Printf("assistant=%d\n", stats.Assistant)
	fmt.Printf("other=%d\n", stats.Other)
	fmt.Printf("session_count=%d\n", stats.SessionCount)
	fmt.Printf("first_timestamp=%s\n", stats.FirstTimestamp)
	fmt.Printf("last_timestamp=%s\n", stats.LastTimestamp)
	return nil
}

func runSessions(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	inputPath := fs.String("in", defaultOutputFile(), "Input JSONL path")
	from := fs.String("from", "", "Filter records at/after this RFC3339 timestamp")
	to := fs.String("to", "", "Filter records at/before this RFC3339 timestamp")
	contains := fs.String("contains", "", "Case-insensitive substring filter for text")
	limit := fs.Int("limit", 20, "Maximum sessions to print, 0 means all")
	jsonOut := fs.Bool("json", false, "Print as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	fromTime, err := parseBoundTime(*from, "--from")
	if err != nil {
		return err
	}
	toTime, err := parseBoundTime(*to, "--to")
	if err != nil {
		return err
	}
	if err := validateTimeRange(fromTime, toTime); err != nil {
		return err
	}

	records, err := loadRecords(*inputPath)
	if err != nil {
		return err
	}

	filtered := filterRecords(records, RecordFilter{
		Contains: strings.TrimSpace(*contains),
		From:     fromTime,
		To:       toTime,
	})

	summaries := buildSessionSummaries(filtered)
	if *limit > 0 && len(summaries) > *limit {
		summaries = summaries[:*limit]
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(summaries)
	}

	for _, summary := range summaries {
		fmt.Printf("%s total=%d user=%d assistant=%d other=%d first=%s last=%s\n",
			summary.SessionID,
			summary.Total,
			summary.User,
			summary.Assistant,
			summary.Other,
			summary.FirstTimestamp,
			summary.LastTimestamp,
		)
	}
	return nil
}

func parseBoundTime(raw string, flagName string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, nil
	}

	ts, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid %s value %q: expected RFC3339", flagName, raw)
	}
	return ts.UTC(), nil
}

func validateTimeRange(from, to time.Time) error {
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return errors.New("--from must be before or equal to --to")
	}
	return nil
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
				if parsed, ok := parseRecordTime(timestamp); ok && parsed.Before(since) {
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
	if t, ok := parseRecordTime(trimmed); ok {
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

func filterRecords(records []Record, filter RecordFilter) []Record {
	sessionID := strings.TrimSpace(filter.SessionID)
	role := strings.ToLower(strings.TrimSpace(filter.Role))
	contains := strings.ToLower(strings.TrimSpace(filter.Contains))

	filtered := make([]Record, 0, len(records))
	for _, record := range records {
		if sessionID != "" && record.SessionID != sessionID {
			continue
		}
		if role != "" && strings.ToLower(strings.TrimSpace(record.Role)) != role {
			continue
		}
		if contains != "" && !strings.Contains(strings.ToLower(record.Text), contains) {
			continue
		}
		if !filter.From.IsZero() || !filter.To.IsZero() {
			ts, ok := parseRecordTime(record.Timestamp)
			if !ok {
				continue
			}
			if !filter.From.IsZero() && ts.Before(filter.From) {
				continue
			}
			if !filter.To.IsZero() && ts.After(filter.To) {
				continue
			}
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func computeStats(records []Record) HistoryStats {
	stats := HistoryStats{Total: len(records)}
	sessionSet := make(map[string]struct{})

	for _, record := range records {
		sessionSet[record.SessionID] = struct{}{}
		switch strings.ToLower(strings.TrimSpace(record.Role)) {
		case "user":
			stats.User++
		case "assistant":
			stats.Assistant++
		default:
			stats.Other++
		}

		stats.FirstTimestamp = earlierTimestamp(stats.FirstTimestamp, record.Timestamp)
		stats.LastTimestamp = laterTimestamp(stats.LastTimestamp, record.Timestamp)
	}

	stats.SessionCount = len(sessionSet)
	return stats
}

func buildSessionSummaries(records []Record) []SessionSummary {
	summaryBySession := make(map[string]*SessionSummary)

	for _, record := range records {
		sessionID := record.SessionID
		if strings.TrimSpace(sessionID) == "" {
			sessionID = "unknown"
		}

		summary, exists := summaryBySession[sessionID]
		if !exists {
			summary = &SessionSummary{SessionID: sessionID}
			summaryBySession[sessionID] = summary
		}

		summary.Total++
		switch strings.ToLower(strings.TrimSpace(record.Role)) {
		case "user":
			summary.User++
		case "assistant":
			summary.Assistant++
		default:
			summary.Other++
		}

		summary.FirstTimestamp = earlierTimestamp(summary.FirstTimestamp, record.Timestamp)
		summary.LastTimestamp = laterTimestamp(summary.LastTimestamp, record.Timestamp)
	}

	summaries := make([]SessionSummary, 0, len(summaryBySession))
	for _, summary := range summaryBySession {
		summaries = append(summaries, *summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		cmp := compareTimestamp(summaries[i].LastTimestamp, summaries[j].LastTimestamp)
		if cmp != 0 {
			return cmp > 0
		}
		return summaries[i].SessionID < summaries[j].SessionID
	})

	return summaries
}

func parseRecordTime(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}

	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed.UTC(), true
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.UTC(), true
	}
	return time.Time{}, false
}

func compareTimestamp(left, right string) int {
	leftTime, leftOK := parseRecordTime(left)
	rightTime, rightOK := parseRecordTime(right)

	if leftOK && rightOK {
		if leftTime.Before(rightTime) {
			return -1
		}
		if leftTime.After(rightTime) {
			return 1
		}
		return strings.Compare(left, right)
	}

	if leftOK != rightOK {
		if leftOK {
			return 1
		}
		return -1
	}

	return strings.Compare(left, right)
}

func earlierTimestamp(current, candidate string) string {
	if strings.TrimSpace(candidate) == "" {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return candidate
	}
	if compareTimestamp(candidate, current) < 0 {
		return candidate
	}
	return current
}

func laterTimestamp(current, candidate string) string {
	if strings.TrimSpace(candidate) == "" {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return candidate
	}
	if compareTimestamp(candidate, current) > 0 {
		return candidate
	}
	return current
}

func sortRecordsChronological(records []Record) {
	sort.SliceStable(records, func(i, j int) bool {
		return compareTimestamp(records[i].Timestamp, records[j].Timestamp) < 0
	})
}

func reverseRecords(records []Record) {
	for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
		records[left], records[right] = records[right], records[left]
	}
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
