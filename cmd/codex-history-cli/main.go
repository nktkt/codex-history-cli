package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex-history-cli/internal/history"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := strings.ToLower(strings.TrimSpace(os.Args[1]))
	args := os.Args[2:]

	switch cmd {
	case "init":
		runInit(args)
	case "providers":
		runProviders(args)
	case "import":
		runImport(args)
	case "search":
		runSearch(args)
	case "analyze":
		runAnalyze(args)
	case "security":
		runSecurity(args)
	case "topics":
		runTopics(args)
	case "dashboard", "tui":
		runDashboard(args)
	case "help", "-h", "--help":
		usage()
	default:
		fatalf("unknown command %q", cmd)
	}
}

func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := fs.String("db", dbPathDefault(), "sqlite db path")
	mustParse(fs, args)

	store := history.NewStore(*dbPath)
	if err := store.Init(); err != nil {
		fatalf("init failed: %v", err)
	}
	fmt.Printf("initialized: %s\n", *dbPath)
}

func runProviders(args []string) {
	fs := flag.NewFlagSet("providers", flag.ExitOnError)
	mustParse(fs, args)
	for _, p := range history.SupportedProviders() {
		fmt.Println(p)
	}
}

func runImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	dbPath := fs.String("db", dbPathDefault(), "sqlite db path")
	provider := fs.String("provider", "", "provider name: codex|ollama|grok|claude|gemini")
	file := fs.String("file", "", "input file path (.json or .jsonl)")
	autoAnalyze := fs.Bool("analyze", true, "run analysis after import")
	ollamaModel := fs.String("ollama-model", "", "optional local model for summarize/tags (used with -analyze)")
	mustParse(fs, args)

	if strings.TrimSpace(*provider) == "" || strings.TrimSpace(*file) == "" {
		fatalf("provider and file are required")
	}
	absFile, err := filepath.Abs(*file)
	if err != nil {
		fatalf("invalid file path: %v", err)
	}

	store := history.NewStore(*dbPath)
	if err := store.Init(); err != nil {
		fatalf("init failed: %v", err)
	}

	conversations, err := history.ParseProviderFile(*provider, absFile)
	if err != nil {
		fatalf("parse failed: %v", err)
	}
	normProvider, _ := history.NormalizeProvider(*provider)
	summary, err := store.Import(conversations, normProvider, absFile)
	if err != nil {
		fatalf("import failed: %v", err)
	}
	fmt.Printf("imported provider=%s file=%s conversations=%d messages=%d\n", summary.Provider, summary.File, summary.Conversations, summary.Messages)

	if *autoAnalyze {
		analyses, err := store.AnalyzeWithOptions(history.AnalyzeOptions{Provider: normProvider, Limit: 200, OllamaModel: *ollamaModel})
		if err != nil {
			fatalf("post-import analyze failed: %v", err)
		}
		fmt.Printf("analyzed conversations=%d\n", len(analyses))
	}
}

func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	dbPath := fs.String("db", dbPathDefault(), "sqlite db path")
	query := fs.String("q", "", "full text query")
	provider := fs.String("provider", "", "provider filter")
	limit := fs.Int("limit", 20, "max results")
	jsonOutput := fs.Bool("json", false, "json output")
	mustParse(fs, args)

	store := history.NewStore(*dbPath)
	results, err := store.Search(*query, strings.TrimSpace(*provider), *limit)
	if err != nil {
		fatalf("search failed: %v", err)
	}
	if *jsonOutput {
		writeJSON(results)
		return
	}
	if len(results) == 0 {
		fmt.Println("no matches")
		return
	}
	for _, r := range results {
		fmt.Printf("[%s] %s %s/%s\n", r.CreatedAt, r.Provider, r.ConversationID, r.Role)
		fmt.Printf("  %s\n", r.Snippet)
	}
}

func runAnalyze(args []string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	dbPath := fs.String("db", dbPathDefault(), "sqlite db path")
	provider := fs.String("provider", "", "provider filter")
	limit := fs.Int("limit", 200, "max conversations to analyze")
	ollamaModel := fs.String("ollama-model", "", "optional local model for summarize/tags")
	jsonOutput := fs.Bool("json", false, "json output")
	mustParse(fs, args)

	store := history.NewStore(*dbPath)
	if err := store.Init(); err != nil {
		fatalf("init failed: %v", err)
	}
	analyses, err := store.AnalyzeWithOptions(history.AnalyzeOptions{Provider: strings.TrimSpace(*provider), Limit: *limit, OllamaModel: strings.TrimSpace(*ollamaModel)})
	if err != nil {
		fatalf("analyze failed: %v", err)
	}
	if *jsonOutput {
		writeJSON(analyses)
		return
	}
	fmt.Printf("analyzed %d conversations\n", len(analyses))
	for _, a := range analyses {
		fmt.Printf("- %s provider=%s score=%d tags=%s\n", a.ConversationID, a.Provider, a.InjectionScore, strings.Join(a.Tags, ","))
		fmt.Printf("  summary: %s\n", a.Summary)
	}
}

func runSecurity(args []string) {
	fs := flag.NewFlagSet("security", flag.ExitOnError)
	dbPath := fs.String("db", dbPathDefault(), "sqlite db path")
	threshold := fs.Int("threshold", 30, "minimum injection score")
	limit := fs.Int("limit", 50, "max results")
	jsonOutput := fs.Bool("json", false, "json output")
	mustParse(fs, args)

	store := history.NewStore(*dbPath)
	findings, err := store.SecurityFindings(*threshold, *limit)
	if err != nil {
		fatalf("security failed: %v", err)
	}
	if *jsonOutput {
		writeJSON(findings)
		return
	}
	if len(findings) == 0 {
		fmt.Println("no high-risk conversations")
		return
	}
	for _, f := range findings {
		fmt.Printf("- score=%d provider=%s conv=%s\n", f.InjectionScore, f.Provider, f.ConversationID)
		if f.InjectionReasons != "" {
			fmt.Printf("  reasons: %s\n", f.InjectionReasons)
		}
		fmt.Printf("  summary: %s\n", f.Summary)
	}
}

func runTopics(args []string) {
	fs := flag.NewFlagSet("topics", flag.ExitOnError)
	dbPath := fs.String("db", dbPathDefault(), "sqlite db path")
	days := fs.Int("days", 14, "lookback days")
	top := fs.Int("top", 5, "top N tags")
	jsonOutput := fs.Bool("json", false, "json output")
	mustParse(fs, args)

	store := history.NewStore(*dbPath)
	buckets, err := store.TopicTrend(*days)
	if err != nil {
		fatalf("topics failed: %v", err)
	}
	if *jsonOutput {
		writeJSON(buckets)
		return
	}
	if len(buckets) == 0 {
		fmt.Println("no topic trend data")
		return
	}

	totals := map[string]int{}
	for _, b := range buckets {
		for tag, count := range b.TagCounts {
			totals[tag] += count
		}
	}
	topTags := selectTopTags(totals, *top)

	fmt.Printf("topic trend (last %d days)\n", *days)
	for _, b := range buckets {
		fmt.Printf("%s", b.Date)
		for _, tag := range topTags {
			count := b.TagCounts[tag]
			if count == 0 {
				continue
			}
			fmt.Printf(" | %s %s (%d)", tag, bar(count), count)
		}
		fmt.Println()
	}
}

func selectTopTags(counts map[string]int, top int) []string {
	if top <= 0 {
		top = 5
	}
	type kv struct {
		tag   string
		count int
	}
	arr := make([]kv, 0, len(counts))
	for tag, count := range counts {
		arr = append(arr, kv{tag: tag, count: count})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].count != arr[j].count {
			return arr[i].count > arr[j].count
		}
		return arr[i].tag < arr[j].tag
	})
	if len(arr) > top {
		arr = arr[:top]
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		out = append(out, item.tag)
	}
	return out
}

func bar(n int) string {
	if n <= 0 {
		return ""
	}
	if n > 20 {
		n = 20
	}
	return strings.Repeat("#", n)
}

func dbPathDefault() string {
	if v := strings.TrimSpace(os.Getenv("CODEX_HISTORY_DB")); v != "" {
		return v
	}
	return history.DefaultDBPath()
}

func writeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatalf("json encode failed: %v", err)
	}
}

func mustParse(fs *flag.FlagSet, args []string) {
	if err := fs.Parse(args); err != nil {
		fatalf("parse error: %v", err)
	}
}

func usage() {
	fmt.Println(`codex-history-cli

Usage:
  codex-history-cli init [-db PATH]
  codex-history-cli providers
  codex-history-cli import -provider codex -file export.jsonl [-db PATH] [-analyze] [-ollama-model MODEL]
  codex-history-cli search -q QUERY [-provider NAME] [-limit N] [-db PATH] [-json]
  codex-history-cli analyze [-provider NAME] [-limit N] [-ollama-model MODEL] [-db PATH] [-json]
  codex-history-cli security [-threshold N] [-limit N] [-db PATH] [-json]
  codex-history-cli topics [-days N] [-top N] [-db PATH] [-json]
  codex-history-cli dashboard [-days N] [-threshold N] [-top-tags N] [-max-findings N] [-max-recent N] [-db PATH]
  codex-history-cli tui (alias of dashboard)

Providers:
  codex, ollama, grok, claude, gemini

Env:
  CODEX_HISTORY_DB sets default DB path`)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
