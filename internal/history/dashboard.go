package history

import (
	"fmt"
	"sort"
)

type DashboardStats struct {
	Conversations int    `json:"conversations"`
	Messages      int    `json:"messages"`
	Analyses      int    `json:"analyses"`
	HighRisk      int    `json:"high_risk"`
	GeneratedAt   string `json:"generated_at"`
}

type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

type ConversationBrief struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	Title          string `json:"title"`
	UpdatedAt      string `json:"updated_at"`
	InjectionScore int    `json:"injection_score"`
}

type DashboardSnapshot struct {
	Stats    DashboardStats      `json:"stats"`
	TopTags  []TagCount          `json:"top_tags"`
	Findings []SecurityFinding   `json:"findings"`
	Recent   []ConversationBrief `json:"recent"`
}

func (s Store) DashboardSnapshot(days, threshold, topTags, maxFindings, maxRecent int) (DashboardSnapshot, error) {
	if days <= 0 {
		days = 14
	}
	if threshold < 0 {
		threshold = 0
	}
	if topTags <= 0 {
		topTags = 8
	}
	if maxFindings <= 0 {
		maxFindings = 8
	}
	if maxRecent <= 0 {
		maxRecent = 12
	}

	stats, err := s.dashboardStats(threshold)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	top, err := s.topTags(days, topTags)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	findings, err := s.SecurityFindings(threshold, maxFindings)
	if err != nil {
		return DashboardSnapshot{}, err
	}
	recent, err := s.recentConversations(maxRecent)
	if err != nil {
		return DashboardSnapshot{}, err
	}

	return DashboardSnapshot{
		Stats:    stats,
		TopTags:  top,
		Findings: findings,
		Recent:   recent,
	}, nil
}

func (s Store) dashboardStats(threshold int) (DashboardStats, error) {
	sql := fmt.Sprintf(`
SELECT
  (SELECT COUNT(*) FROM conversations) AS conversations,
  (SELECT COUNT(*) FROM messages) AS messages,
  (SELECT COUNT(*) FROM analyses) AS analyses,
  (SELECT COUNT(*) FROM analyses WHERE injection_score >= %d) AS high_risk;
`, threshold)
	rows, err := s.db.QueryJSON(sql)
	if err != nil {
		return DashboardStats{}, err
	}
	if len(rows) == 0 {
		return DashboardStats{GeneratedAt: nowRFC3339()}, nil
	}
	r := rows[0]
	return DashboardStats{
		Conversations: asInt(r["conversations"]),
		Messages:      asInt(r["messages"]),
		Analyses:      asInt(r["analyses"]),
		HighRisk:      asInt(r["high_risk"]),
		GeneratedAt:   nowRFC3339(),
	}, nil
}

func (s Store) topTags(days, limit int) ([]TagCount, error) {
	sql := fmt.Sprintf(`
SELECT tags
FROM analyses
WHERE analyzed_at >= datetime('now', '-%d days');
`, days)
	rows, err := s.db.QueryJSON(sql)
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, row := range rows {
		for _, tag := range splitCSV(asString(row["tags"])) {
			count[tag]++
		}
	}
	arr := make([]TagCount, 0, len(count))
	for tag, c := range count {
		arr = append(arr, TagCount{Tag: tag, Count: c})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].Count != arr[j].Count {
			return arr[i].Count > arr[j].Count
		}
		return arr[i].Tag < arr[j].Tag
	})
	if len(arr) > limit {
		arr = arr[:limit]
	}
	return arr, nil
}

func (s Store) recentConversations(limit int) ([]ConversationBrief, error) {
	sql := fmt.Sprintf(`
SELECT
  c.id AS id,
  c.provider AS provider,
  c.title AS title,
  c.updated_at AS updated_at,
  COALESCE(a.injection_score, 0) AS injection_score
FROM conversations c
LEFT JOIN analyses a ON a.conversation_id = c.id
ORDER BY c.updated_at DESC
LIMIT %d;
`, limit)
	rows, err := s.db.QueryJSON(sql)
	if err != nil {
		return nil, err
	}
	out := make([]ConversationBrief, 0, len(rows))
	for _, row := range rows {
		out = append(out, ConversationBrief{
			ID:             asString(row["id"]),
			Provider:       asString(row["provider"]),
			Title:          asString(row["title"]),
			UpdatedAt:      asString(row["updated_at"]),
			InjectionScore: asInt(row["injection_score"]),
		})
	}
	return out, nil
}
