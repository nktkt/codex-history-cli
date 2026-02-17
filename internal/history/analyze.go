package history

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type AnalyzeOptions struct {
	Provider    string
	Limit       int
	OllamaModel string
}

func AnalyzeConversation(c Conversation, messages []Message) Analysis {
	summary := summarizeHeuristic(messages)
	tags := inferTags(c.Provider, messages)
	score, reasons := detectPromptInjection(messages)
	return Analysis{
		ConversationID:   c.ID,
		Provider:         c.Provider,
		Summary:          summary,
		Tags:             tags,
		InjectionScore:   score,
		InjectionReasons: reasons,
		AnalyzedAt:       nowRFC3339(),
	}
}

func summarizeHeuristic(messages []Message) string {
	if len(messages) == 0 {
		return "(empty conversation)"
	}
	var userText string
	var assistantText string
	for _, m := range messages {
		if userText == "" && m.Role == "user" {
			userText = compressText(m.Content, 180)
		}
		if assistantText == "" && m.Role == "assistant" {
			assistantText = compressText(m.Content, 180)
		}
		if userText != "" && assistantText != "" {
			break
		}
	}
	switch {
	case userText != "" && assistantText != "":
		return fmt.Sprintf("User asked: %s | Assistant replied: %s", userText, assistantText)
	case userText != "":
		return "User asked: " + userText
	default:
		return compressText(messages[0].Content, 220)
	}
}

func compressText(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func inferTags(provider string, messages []Message) []string {
	full := strings.ToLower(provider + " " + concatContents(messages))
	tagKeywords := map[string][]string{
		"coding":   {"bug", "error", "stack trace", "compile", "function", "refactor", "go", "python", "rust", "javascript", "sql", "test"},
		"security": {"prompt injection", "jailbreak", "system prompt", "token", "secret", "password", "xss", "csrf", "sqli", "exploit"},
		"devops":   {"docker", "kubernetes", "k8s", "deploy", "ci", "cd", "cloudflare", "terraform", "nginx", "aws", "gcp"},
		"data":     {"csv", "table", "dataset", "query", "schema", "etl", "analytics", "warehouse", "postgres", "sqlite"},
		"ai":       {"llm", "model", "prompt", "embedding", "rag", "agent", "inference", "ollama", "claude", "gemini", "grok", "codex"},
		"product":  {"roadmap", "requirement", "feature", "ux", "ui", "stakeholder", "spec"},
	}
	tags := []string{"provider:" + provider}
	for tag, kws := range tagKeywords {
		for _, kw := range kws {
			if strings.Contains(full, kw) {
				tags = append(tags, tag)
				break
			}
		}
	}
	if len(tags) == 1 {
		tags = append(tags, "general")
	}
	return uniqueSorted(tags)
}

func concatContents(messages []Message) string {
	parts := make([]string, 0, len(messages))
	for _, m := range messages {
		if t := strings.TrimSpace(m.Content); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}

type injectionRule struct {
	pattern *regexp.Regexp
	reason  string
	weight  int
}

var injectionRules = []injectionRule{
	{regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior)\s+instructions`), "ignore-previous-instructions", 30},
	{regexp.MustCompile(`(?i)reveal\s+.*(system\s+prompt|developer\s+message|hidden\s+prompt)`), "prompt-exfiltration", 28},
	{regexp.MustCompile(`(?i)(jailbreak|do\s+anything\s+now|dan\b)`), "jailbreak-attempt", 24},
	{regexp.MustCompile(`(?i)(override|bypass)\s+(policy|guardrail|safety)`), "policy-bypass", 22},
	{regexp.MustCompile(`(?i)(token|secret|password|api[_-]?key).*?(show|dump|print|exfiltrate)`), "credential-exfiltration", 26},
	{regexp.MustCompile(`(?i)(sudo\s+|rm\s+-rf|curl\s+[^\n]*\|\s*sh)`), "dangerous-shell-pattern", 20},
}

func detectPromptInjection(messages []Message) (int, []string) {
	reasons := map[string]int{}
	for _, m := range messages {
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		for _, rule := range injectionRules {
			if rule.pattern.MatchString(text) {
				reasons[rule.reason] += rule.weight
			}
		}
	}
	if len(reasons) == 0 {
		return 0, nil
	}
	score := 0
	keys := make([]string, 0, len(reasons))
	for k, v := range reasons {
		score += v
		keys = append(keys, k)
	}
	if score > 100 {
		score = 100
	}
	sort.Strings(keys)
	return score, keys
}
