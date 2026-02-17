package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var supportedProviders = map[string]string{
	"codex":     "codex",
	"ollama":    "ollama",
	"grok":      "grok",
	"claude":    "claude",
	"gemini":    "gemini",
	"anthropic": "claude",
	"google":    "gemini",
	"xai":       "grok",
}

func SupportedProviders() []string {
	uniq := map[string]struct{}{}
	for _, p := range supportedProviders {
		uniq[p] = struct{}{}
	}
	out := make([]string, 0, len(uniq))
	for p := range uniq {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func NormalizeProvider(provider string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(provider))
	norm, ok := supportedProviders[p]
	if !ok {
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
	return norm, nil
}

func ParseProviderFile(provider, path string) ([]Conversation, error) {
	norm, err := NormalizeProvider(provider)
	if err != nil {
		return nil, err
	}
	data, err := loadAnyJSON(path)
	if err != nil {
		return nil, err
	}
	sourceID := sanitizeID(filepath.Base(path))
	conversations, err := extractConversations(norm, data, sourceID)
	if err != nil {
		return nil, err
	}
	for i := range conversations {
		normalizeConversation(&conversations[i], i, norm, sourceID)
	}
	if len(conversations) == 0 {
		return nil, fmt.Errorf("no conversations found in %s", path)
	}
	return conversations, nil
}

func loadAnyJSON(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		return nil, fmt.Errorf("empty file")
	}

	var out any
	if err := json.Unmarshal([]byte(trimmed), &out); err == nil {
		return out, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lines := []any{}
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("jsonl parse error at line %d: %w", lineNo, err)
		}
		lines = append(lines, v)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func extractConversations(provider string, data any, sourceID string) ([]Conversation, error) {
	switch root := data.(type) {
	case []any:
		if looksLikeConversationArray(root) {
			out := make([]Conversation, 0, len(root))
			for i, raw := range root {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				out = append(out, parseConversationObject(provider, m, i, sourceID))
			}
			return out, nil
		}
		return parseEventStream(provider, root, sourceID), nil
	case map[string]any:
		if arr := anySlice(root, "conversations", "chats", "threads", "sessions"); len(arr) > 0 {
			out := make([]Conversation, 0, len(arr))
			for i, raw := range arr {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				out = append(out, parseConversationObject(provider, m, i, sourceID))
			}
			return out, nil
		}
		if arr := anySlice(root, "messages", "turns", "items"); len(arr) > 0 {
			conv := parseConversationObject(provider, root, 0, sourceID)
			return []Conversation{conv}, nil
		}
		return parseEventStream(provider, []any{root}, sourceID), nil
	default:
		return nil, fmt.Errorf("unsupported root JSON type")
	}
}

func looksLikeConversationArray(arr []any) bool {
	if len(arr) == 0 {
		return false
	}
	for _, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if len(anySlice(m, "messages", "turns", "items")) == 0 {
			return false
		}
	}
	return true
}

func parseConversationObject(provider string, obj map[string]any, idx int, sourceID string) Conversation {
	id := firstString(obj, "id", "conversation_id", "chat_id", "thread_id", "session_id")
	if id == "" {
		id = fmt.Sprintf("%s:%s:%d", provider, sourceID, idx+1)
	}
	title := firstString(obj, "title", "name", "topic")
	if title == "" {
		title = fmt.Sprintf("%s-%d", provider, idx+1)
	}
	createdAt := toRFC3339String(firstAny(obj, "created_at", "timestamp", "time", "created"))
	if createdAt == "" {
		createdAt = nowRFC3339()
	}
	updatedAt := toRFC3339String(firstAny(obj, "updated_at", "modified_at", "updated", "last_message_at"))
	if updatedAt == "" {
		updatedAt = createdAt
	}

	messages := parseMessagesFromObject(id, obj)

	return Conversation{
		ID:        id,
		Provider:  provider,
		Title:     title,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		Metadata:  obj,
		Messages:  messages,
	}
}

func parseEventStream(provider string, events []any, sourceID string) []Conversation {
	byConv := map[string]*Conversation{}
	order := []string{}

	for i, raw := range events {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		convID := firstString(m, "conversation_id", "chat_id", "thread_id", "session_id", "id")
		if convID == "" {
			convID = fmt.Sprintf("%s:%s", provider, sourceID)
		}
		conv, exists := byConv[convID]
		if !exists {
			title := firstString(m, "title", "name", "topic")
			if title == "" {
				title = convID
			}
			created := toRFC3339String(firstAny(m, "created_at", "timestamp", "time"))
			if created == "" {
				created = nowRFC3339()
			}
			c := Conversation{
				ID:        convID,
				Provider:  provider,
				Title:     title,
				CreatedAt: created,
				UpdatedAt: created,
				Metadata:  map[string]any{"source": "event_stream"},
				Messages:  []Message{},
			}
			byConv[convID] = &c
			order = append(order, convID)
			conv = &c
		}

		msgs := parseMessagesFromObject(convID, m)
		if len(msgs) == 0 {
			role := normalizeRole(firstString(m, "role", "sender", "type"))
			content := firstNonEmpty(extractText(m["content"]), extractText(m["text"]), extractText(m["message"]))
			if role != "" && content != "" {
				msgs = append(msgs, Message{
					ConversationID: convID,
					Role:           role,
					Content:        content,
					CreatedAt:      toRFC3339String(firstAny(m, "created_at", "timestamp", "time")),
				})
			}
		}

		for _, msg := range msgs {
			if msg.CreatedAt == "" {
				msg.CreatedAt = conv.UpdatedAt
			}
			conv.Messages = append(conv.Messages, msg)
			if msg.CreatedAt > conv.UpdatedAt {
				conv.UpdatedAt = msg.CreatedAt
			}
		}

		_ = i
	}

	out := make([]Conversation, 0, len(order))
	for _, id := range order {
		if c := byConv[id]; c != nil {
			out = append(out, *c)
		}
	}
	return out
}

func parseMessagesFromObject(convID string, obj map[string]any) []Message {
	messagesRaw := anySlice(obj, "messages", "turns", "items")
	out := []Message{}

	if len(messagesRaw) > 0 {
		for i, raw := range messagesRaw {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			role := normalizeRole(firstString(m, "role", "sender", "type"))
			content := firstNonEmpty(
				extractText(m["content"]),
				extractText(m["text"]),
				extractText(m["message"]),
				extractText(m["prompt"]),
				extractText(m["response"]),
			)
			if content == "" {
				continue
			}
			if role == "" {
				if i%2 == 0 {
					role = "user"
				} else {
					role = "assistant"
				}
			}
			created := toRFC3339String(firstAny(m, "created_at", "timestamp", "time"))
			out = append(out, Message{
				ID:             firstString(m, "id", "message_id"),
				ConversationID: convID,
				Role:           role,
				Content:        content,
				CreatedAt:      created,
			})
		}
	}

	prompt := extractText(obj["prompt"])
	response := firstNonEmpty(extractText(obj["response"]), extractText(obj["output"]))
	ts := toRFC3339String(firstAny(obj, "created_at", "timestamp", "time"))
	if prompt != "" {
		out = append(out, Message{ConversationID: convID, Role: "user", Content: prompt, CreatedAt: ts})
	}
	if response != "" {
		out = append(out, Message{ConversationID: convID, Role: "assistant", Content: response, CreatedAt: ts})
	}

	return out
}

func normalizeConversation(c *Conversation, idx int, provider, sourceID string) {
	if strings.TrimSpace(c.ID) == "" {
		c.ID = fmt.Sprintf("%s:%s:%d", provider, sourceID, idx+1)
	}
	if strings.TrimSpace(c.Provider) == "" {
		c.Provider = provider
	}
	if strings.TrimSpace(c.Title) == "" {
		c.Title = c.ID
	}
	if strings.TrimSpace(c.CreatedAt) == "" {
		c.CreatedAt = nowRFC3339()
	}
	if strings.TrimSpace(c.UpdatedAt) == "" {
		c.UpdatedAt = c.CreatedAt
	}
	if c.Metadata == nil {
		c.Metadata = map[string]any{}
	}

	for i := range c.Messages {
		if strings.TrimSpace(c.Messages[i].ConversationID) == "" {
			c.Messages[i].ConversationID = c.ID
		}
		c.Messages[i].Role = normalizeRole(c.Messages[i].Role)
		if c.Messages[i].Role == "" {
			if i%2 == 0 {
				c.Messages[i].Role = "user"
			} else {
				c.Messages[i].Role = "assistant"
			}
		}
		if strings.TrimSpace(c.Messages[i].CreatedAt) == "" {
			c.Messages[i].CreatedAt = c.UpdatedAt
		}
		if strings.TrimSpace(c.Messages[i].ID) == "" {
			c.Messages[i].ID = fmt.Sprintf("%s:m%06d", c.ID, i+1)
		}
		if c.Messages[i].CreatedAt > c.UpdatedAt {
			c.UpdatedAt = c.Messages[i].CreatedAt
		}
	}
}

func anySlice(m map[string]any, keys ...string) []any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if arr, ok := v.([]any); ok {
				return arr
			}
		}
	}
	return nil
}

func firstAny(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			s := strings.TrimSpace(extractText(v))
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			return p
		}
	}
	return ""
}

func extractText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.0f", x)
	case map[string]any:
		if s := firstString(x, "text", "content", "value", "message", "output"); s != "" {
			return s
		}
		return ""
	case []any:
		parts := make([]string, 0, len(x))
		for _, it := range x {
			t := strings.TrimSpace(extractText(it))
			if t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", x)
	}
}

func toRFC3339String(v any) string {
	s := strings.TrimSpace(extractText(v))
	if s == "" {
		return ""
	}
	if len(s) >= len("2006-01-02") && strings.Count(s, "-") >= 2 {
		if strings.Contains(s, "T") {
			return s
		}
		return s + "T00:00:00Z"
	}
	return s
}

func normalizeRole(role string) string {
	r := strings.ToLower(strings.TrimSpace(role))
	switch r {
	case "user", "human", "prompt":
		return "user"
	case "assistant", "ai", "model", "response", "bot":
		return "assistant"
	case "system":
		return "system"
	case "tool", "function":
		return "tool"
	default:
		return r
	}
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-", ".", "-")
	s = replacer.Replace(s)
	s = strings.Trim(s, "-")
	if s == "" {
		return "source"
	}
	return s
}
