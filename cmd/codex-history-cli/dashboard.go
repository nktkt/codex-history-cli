package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"codex-history-cli/internal/history"
)

type dashboardConfig struct {
	Days        int
	Threshold   int
	TopTags     int
	MaxFindings int
	MaxRecent   int
	ClearScreen bool
}

func runDashboard(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	dbPath := fs.String("db", dbPathDefault(), "sqlite db path")
	days := fs.Int("days", 14, "lookback days for tags")
	threshold := fs.Int("threshold", 30, "high-risk threshold")
	topTags := fs.Int("top-tags", 8, "top tag count")
	maxFindings := fs.Int("max-findings", 8, "max findings shown")
	maxRecent := fs.Int("max-recent", 12, "max recent conversations shown")
	clear := fs.Bool("clear", true, "clear screen on each refresh")
	mustParse(fs, args)

	store := history.NewStore(*dbPath)
	if err := store.Init(); err != nil {
		fatalf("dashboard init failed: %v", err)
	}

	cfg := dashboardConfig{
		Days:        *days,
		Threshold:   *threshold,
		TopTags:     *topTags,
		MaxFindings: *maxFindings,
		MaxRecent:   *maxRecent,
		ClearScreen: *clear,
	}
	startDashboardLoop(store, cfg)
}

func startDashboardLoop(store history.Store, cfg dashboardConfig) {
	reader := bufio.NewReader(os.Stdin)
	for {
		snap, err := store.DashboardSnapshot(cfg.Days, cfg.Threshold, cfg.TopTags, cfg.MaxFindings, cfg.MaxRecent)
		if err != nil {
			fatalf("dashboard snapshot failed: %v", err)
		}
		renderDashboard(snap, cfg)
		fmt.Print("dashboard> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		cmd := strings.TrimSpace(line)
		if cmd == "" || cmd == "r" || cmd == "refresh" {
			continue
		}
		if cmd == "q" || cmd == "quit" || cmd == "exit" {
			return
		}
		if cmd == "h" || cmd == "help" {
			printDashboardHelp()
			waitEnter(reader)
			continue
		}
		if strings.HasPrefix(cmd, "s ") || strings.HasPrefix(cmd, "search ") {
			query := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(cmd, "s"), "search"))
			dashboardSearch(store, reader, query)
			continue
		}
		if strings.HasPrefix(cmd, "days ") {
			v := strings.TrimSpace(strings.TrimPrefix(cmd, "days "))
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				cfg.Days = n
			}
			continue
		}
		if strings.HasPrefix(cmd, "th ") {
			v := strings.TrimSpace(strings.TrimPrefix(cmd, "th "))
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				cfg.Threshold = n
			}
			continue
		}
		fmt.Println("unknown command. type 'h' for help")
		waitEnter(reader)
	}
}

func renderDashboard(snap history.DashboardSnapshot, cfg dashboardConfig) {
	if cfg.ClearScreen {
		fmt.Print("\033[2J\033[H")
	}
	fmt.Println("=== codex-history-cli dashboard ===")
	fmt.Printf("generated=%s | days=%d | threshold=%d\n", snap.Stats.GeneratedAt, cfg.Days, cfg.Threshold)
	fmt.Printf("conversations=%d messages=%d analyses=%d high_risk=%d\n", snap.Stats.Conversations, snap.Stats.Messages, snap.Stats.Analyses, snap.Stats.HighRisk)
	fmt.Println()

	fmt.Println("[Top Tags]")
	if len(snap.TopTags) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, t := range snap.TopTags {
			fmt.Printf("  - %-18s %s (%d)\n", t.Tag, bar(t.Count), t.Count)
		}
	}
	fmt.Println()

	fmt.Println("[High-Risk Findings]")
	if len(snap.Findings) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, f := range snap.Findings {
			reasons := f.InjectionReasons
			if reasons == "" {
				reasons = "-"
			}
			fmt.Printf("  - score=%d provider=%s conv=%s reasons=%s\n", f.InjectionScore, f.Provider, truncate(f.ConversationID, 48), truncate(reasons, 56))
		}
	}
	fmt.Println()

	fmt.Println("[Recent Conversations]")
	if len(snap.Recent) == 0 {
		fmt.Println("  (none)")
	} else {
		recent := append([]history.ConversationBrief(nil), snap.Recent...)
		sort.Slice(recent, func(i, j int) bool { return recent[i].UpdatedAt > recent[j].UpdatedAt })
		for _, c := range recent {
			fmt.Printf("  - [%s] %s score=%d %s\n", c.Provider, truncate(c.ID, 36), c.InjectionScore, truncate(c.Title, 64))
		}
	}
	fmt.Println()
	fmt.Println("Commands: r(refresh) | s <query>(search) | days <n> | th <n> | h(help) | q(quit)")
}

func dashboardSearch(store history.Store, reader *bufio.Reader, query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		fmt.Println("search query is empty")
		waitEnter(reader)
		return
	}
	results, err := store.Search(query, "", 10)
	if err != nil {
		fmt.Printf("search failed: %v\n", err)
		waitEnter(reader)
		return
	}
	fmt.Printf("\n[Search Results] query=%q\n", query)
	if len(results) == 0 {
		fmt.Println("  (no matches)")
	} else {
		for _, r := range results {
			fmt.Printf("- [%s] %s/%s %s\n", r.Provider, truncate(r.ConversationID, 36), r.Role, truncate(r.CreatedAt, 24))
			fmt.Printf("  %s\n", truncate(r.Snippet, 120))
		}
	}
	waitEnter(reader)
}

func printDashboardHelp() {
	fmt.Println("\n[Dashboard Help]")
	fmt.Println("  r / refresh        refresh dashboard")
	fmt.Println("  s <query>          full-text search")
	fmt.Println("  days <n>           set lookback days for tags")
	fmt.Println("  th <n>             set injection score threshold")
	fmt.Println("  h / help           show this help")
	fmt.Println("  q / quit           exit dashboard")
}

func waitEnter(reader *bufio.Reader) {
	fmt.Print("press ENTER to continue...")
	_, _ = reader.ReadString('\n')
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 3 || len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
