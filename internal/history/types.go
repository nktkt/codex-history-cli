package history

import "time"

type Message struct {
	ID             string
	ConversationID string
	Role           string
	Content        string
	CreatedAt      string
}

type Conversation struct {
	ID        string
	Provider  string
	Title     string
	CreatedAt string
	UpdatedAt string
	Metadata  map[string]any
	Messages  []Message
}

type ImportSummary struct {
	Provider      string
	File          string
	Conversations int
	Messages      int
}

type SearchResult struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
	Provider       string `json:"provider"`
	Role           string `json:"role"`
	CreatedAt      string `json:"created_at"`
	Snippet        string `json:"snippet"`
}

type Analysis struct {
	ConversationID   string   `json:"conversation_id"`
	Provider         string   `json:"provider"`
	Summary          string   `json:"summary"`
	Tags             []string `json:"tags"`
	InjectionScore   int      `json:"injection_score"`
	InjectionReasons []string `json:"injection_reasons"`
	AnalyzedAt       string   `json:"analyzed_at"`
}

type SecurityFinding struct {
	ConversationID   string `json:"conversation_id"`
	Provider         string `json:"provider"`
	InjectionScore   int    `json:"injection_score"`
	InjectionReasons string `json:"injection_reasons"`
	Summary          string `json:"summary"`
}

type TopicBucket struct {
	Date      string         `json:"date"`
	TagCounts map[string]int `json:"tag_counts"`
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
