package history

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Store struct {
	db SQLiteCLI
}

func NewStore(dbPath string) Store {
	return Store{db: NewSQLiteCLI(dbPath)}
}

func (s Store) Init() error {
	if err := EnsureDirForFile(s.db.DBPath); err != nil {
		return err
	}
	schema := `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS conversations (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  title TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  meta_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE VIRTUAL TABLE IF NOT EXISTS message_fts USING fts5(
  message_id UNINDEXED,
  content
);

CREATE TABLE IF NOT EXISTS analyses (
  conversation_id TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  summary TEXT NOT NULL,
  tags TEXT NOT NULL,
  injection_score INTEGER NOT NULL,
  injection_reasons TEXT NOT NULL,
  analyzed_at TEXT NOT NULL,
  FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);
CREATE INDEX IF NOT EXISTS idx_conversations_provider ON conversations(provider);
CREATE INDEX IF NOT EXISTS idx_analyses_score ON analyses(injection_score DESC);
`
	return s.db.Exec(schema)
}

func (s Store) UpsertConversation(c Conversation) error {
	meta := c.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	sql := fmt.Sprintf(`
INSERT INTO conversations(id, provider, title, created_at, updated_at, meta_json)
VALUES(%s, %s, %s, %s, %s, %s)
ON CONFLICT(id) DO UPDATE SET
  provider=excluded.provider,
  title=excluded.title,
  updated_at=excluded.updated_at,
  meta_json=excluded.meta_json;
`, sqlQuote(c.ID), sqlQuote(c.Provider), sqlQuote(c.Title), sqlQuote(c.CreatedAt), sqlQuote(c.UpdatedAt), sqlQuote(string(metaJSON)))
	return s.db.Exec(sql)
}

func (s Store) InsertMessage(m Message, provider string) error {
	sql := fmt.Sprintf(`
INSERT OR IGNORE INTO messages(id, conversation_id, provider, role, content, created_at)
VALUES(%s, %s, %s, %s, %s, %s);
INSERT OR IGNORE INTO message_fts(message_id, content)
VALUES(%s, %s);
`, sqlQuote(m.ID), sqlQuote(m.ConversationID), sqlQuote(provider), sqlQuote(m.Role), sqlQuote(m.Content), sqlQuote(m.CreatedAt), sqlQuote(m.ID), sqlQuote(m.Content))
	return s.db.Exec(sql)
}

func (s Store) Import(conversations []Conversation, provider string, file string) (ImportSummary, error) {
	summary := ImportSummary{Provider: provider, File: file}
	for _, c := range conversations {
		if err := s.UpsertConversation(c); err != nil {
			return summary, err
		}
		summary.Conversations++
		for _, m := range c.Messages {
			if err := s.InsertMessage(m, provider); err != nil {
				return summary, err
			}
			summary.Messages++
		}
	}
	return summary, nil
}

func (s Store) Search(query, provider string, limit int) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = 20
	}
	match := buildMatchExpr(query)
	whereProvider := ""
	if provider != "" {
		whereProvider = " AND c.provider = " + sqlQuote(provider)
	}
	sql := fmt.Sprintf(`
SELECT
  m.id AS message_id,
  m.conversation_id AS conversation_id,
  c.provider AS provider,
  m.role AS role,
  m.created_at AS created_at,
  snippet(message_fts, 1, '[', ']', '...', 18) AS snippet
FROM message_fts
JOIN messages m ON m.id = message_fts.message_id
JOIN conversations c ON c.id = m.conversation_id
WHERE message_fts MATCH %s%s
ORDER BY bm25(message_fts), m.created_at DESC
LIMIT %d;
`, sqlQuote(match), whereProvider, limit)
	rows, err := s.db.QueryJSON(sql)
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, SearchResult{
			MessageID:      asString(row["message_id"]),
			ConversationID: asString(row["conversation_id"]),
			Provider:       asString(row["provider"]),
			Role:           asString(row["role"]),
			CreatedAt:      asString(row["created_at"]),
			Snippet:        asString(row["snippet"]),
		})
	}
	return results, nil
}

func buildMatchExpr(query string) string {
	parts := strings.Fields(strings.TrimSpace(query))
	if len(parts) == 0 {
		return ""
	}
	for i := range parts {
		parts[i] = strings.Trim(parts[i], `"'`)
		if parts[i] == "" {
			parts[i] = "*"
		}
	}
	return strings.Join(parts, " AND ")
}

func (s Store) listConversations(provider string, limit int) ([]Conversation, error) {
	if limit <= 0 {
		limit = 100
	}
	whereProvider := ""
	if provider != "" {
		whereProvider = "WHERE provider = " + sqlQuote(provider)
	}
	sql := fmt.Sprintf(`
SELECT id, provider, title, created_at, updated_at, meta_json
FROM conversations
%s
ORDER BY updated_at DESC
LIMIT %d;
`, whereProvider, limit)
	rows, err := s.db.QueryJSON(sql)
	if err != nil {
		return nil, err
	}
	out := make([]Conversation, 0, len(rows))
	for _, row := range rows {
		conv := Conversation{
			ID:        asString(row["id"]),
			Provider:  asString(row["provider"]),
			Title:     asString(row["title"]),
			CreatedAt: asString(row["created_at"]),
			UpdatedAt: asString(row["updated_at"]),
			Metadata:  map[string]any{},
		}
		_ = json.Unmarshal([]byte(asString(row["meta_json"])), &conv.Metadata)
		out = append(out, conv)
	}
	return out, nil
}

func (s Store) listMessagesByConversation(conversationID string) ([]Message, error) {
	sql := fmt.Sprintf(`
SELECT id, conversation_id, role, content, created_at
FROM messages
WHERE conversation_id = %s
ORDER BY created_at ASC, id ASC;
`, sqlQuote(conversationID))
	rows, err := s.db.QueryJSON(sql)
	if err != nil {
		return nil, err
	}
	msgs := make([]Message, 0, len(rows))
	for _, row := range rows {
		msgs = append(msgs, Message{
			ID:             asString(row["id"]),
			ConversationID: asString(row["conversation_id"]),
			Role:           asString(row["role"]),
			Content:        asString(row["content"]),
			CreatedAt:      asString(row["created_at"]),
		})
	}
	return msgs, nil
}

func (s Store) UpsertAnalysis(a Analysis) error {
	tags := strings.Join(uniqueSorted(a.Tags), ",")
	reasons := strings.Join(uniqueSorted(a.InjectionReasons), ",")
	sql := fmt.Sprintf(`
INSERT INTO analyses(conversation_id, provider, summary, tags, injection_score, injection_reasons, analyzed_at)
VALUES(%s, %s, %s, %s, %d, %s, %s)
ON CONFLICT(conversation_id) DO UPDATE SET
  provider=excluded.provider,
  summary=excluded.summary,
  tags=excluded.tags,
  injection_score=excluded.injection_score,
  injection_reasons=excluded.injection_reasons,
  analyzed_at=excluded.analyzed_at;
`, sqlQuote(a.ConversationID), sqlQuote(a.Provider), sqlQuote(a.Summary), sqlQuote(tags), a.InjectionScore, sqlQuote(reasons), sqlQuote(a.AnalyzedAt))
	return s.db.Exec(sql)
}

func (s Store) Analyze(provider string, limit int) ([]Analysis, error) {
	return s.AnalyzeWithOptions(AnalyzeOptions{Provider: provider, Limit: limit})
}

func (s Store) AnalyzeWithOptions(opts AnalyzeOptions) ([]Analysis, error) {
	convs, err := s.listConversations(opts.Provider, opts.Limit)
	if err != nil {
		return nil, err
	}
	results := make([]Analysis, 0, len(convs))
	for _, c := range convs {
		msgs, err := s.listMessagesByConversation(c.ID)
		if err != nil {
			return nil, err
		}
		var a Analysis
		if strings.TrimSpace(opts.OllamaModel) != "" {
			llmAnalysis, err := AnalyzeConversationWithOllama(c, msgs, opts.OllamaModel)
			if err == nil {
				a = llmAnalysis
			} else {
				a = AnalyzeConversation(c, msgs)
			}
		} else {
			a = AnalyzeConversation(c, msgs)
		}
		if err := s.UpsertAnalysis(a); err != nil {
			return nil, err
		}
		results = append(results, a)
	}
	return results, nil
}

func (s Store) SecurityFindings(threshold int, limit int) ([]SecurityFinding, error) {
	if threshold < 0 {
		threshold = 0
	}
	if limit <= 0 {
		limit = 50
	}
	sql := fmt.Sprintf(`
SELECT conversation_id, provider, injection_score, injection_reasons, summary
FROM analyses
WHERE injection_score >= %d
ORDER BY injection_score DESC, analyzed_at DESC
LIMIT %d;
`, threshold, limit)
	rows, err := s.db.QueryJSON(sql)
	if err != nil {
		return nil, err
	}
	out := make([]SecurityFinding, 0, len(rows))
	for _, row := range rows {
		out = append(out, SecurityFinding{
			ConversationID:   asString(row["conversation_id"]),
			Provider:         asString(row["provider"]),
			InjectionScore:   asInt(row["injection_score"]),
			InjectionReasons: asString(row["injection_reasons"]),
			Summary:          asString(row["summary"]),
		})
	}
	return out, nil
}

func (s Store) TopicTrend(days int) ([]TopicBucket, error) {
	if days <= 0 {
		days = 14
	}
	sql := fmt.Sprintf(`
SELECT analyzed_at, tags
FROM analyses
WHERE analyzed_at >= datetime('now', '-%d days')
ORDER BY analyzed_at ASC;
`, days)
	rows, err := s.db.QueryJSON(sql)
	if err != nil {
		return nil, err
	}
	byDate := map[string]map[string]int{}
	for _, row := range rows {
		date := asString(row["analyzed_at"])
		if len(date) >= 10 {
			date = date[:10]
		}
		if date == "" {
			continue
		}
		if byDate[date] == nil {
			byDate[date] = map[string]int{}
		}
		tags := splitCSV(asString(row["tags"]))
		for _, tag := range tags {
			byDate[date][tag]++
		}
	}
	dates := make([]string, 0, len(byDate))
	for d := range byDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	out := make([]TopicBucket, 0, len(dates))
	for _, d := range dates {
		out = append(out, TopicBucket{Date: d, TagCounts: byDate[d]})
	}
	return out, nil
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func uniqueSorted(items []string) []string {
	m := map[string]struct{}{}
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		m[it] = struct{}{}
	}
	out := make([]string, 0, len(m))
	for it := range m {
		out = append(out, it)
	}
	sort.Strings(out)
	return out
}
