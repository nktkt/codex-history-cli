package history

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type ollamaSummary struct {
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

func AnalyzeConversationWithOllama(c Conversation, messages []Message, model string) (Analysis, error) {
	if strings.TrimSpace(model) == "" {
		return Analysis{}, fmt.Errorf("ollama model is required")
	}
	transcript := buildTranscript(messages, 24)
	prompt := fmt.Sprintf("You summarize AI chat logs. Return ONLY strict JSON object {\"summary\":string,\"tags\":string[]} with <= 120-char summary and up to 6 short lowercase tags.\nProvider: %s\nConversation title: %s\nTranscript:\n%s", c.Provider, c.Title, transcript)
	cmd := exec.Command("ollama", "run", model, prompt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Analysis{}, fmt.Errorf("ollama run failed: %w", err)
	}

	parsed, err := parseOllamaJSON(out)
	if err != nil {
		return Analysis{}, err
	}
	score, reasons := detectPromptInjection(messages)
	if len(parsed.Tags) == 0 {
		parsed.Tags = inferTags(c.Provider, messages)
	}
	tags := append([]string{"provider:" + c.Provider}, parsed.Tags...)
	return Analysis{
		ConversationID:   c.ID,
		Provider:         c.Provider,
		Summary:          compressText(parsed.Summary, 220),
		Tags:             uniqueSorted(tags),
		InjectionScore:   score,
		InjectionReasons: reasons,
		AnalyzedAt:       nowRFC3339(),
	}, nil
}

func buildTranscript(messages []Message, max int) string {
	if len(messages) > max {
		messages = messages[len(messages)-max:]
	}
	lines := make([]string, 0, len(messages))
	for _, m := range messages {
		content := compressText(m.Content, 260)
		if content == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s] %s", m.Role, content))
	}
	return strings.Join(lines, "\n")
}

func parseOllamaJSON(out []byte) (ollamaSummary, error) {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return ollamaSummary{}, fmt.Errorf("empty ollama output")
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return ollamaSummary{}, fmt.Errorf("json block not found in ollama output")
	}
	candidate := text[start : end+1]
	var parsed ollamaSummary
	if err := json.Unmarshal([]byte(candidate), &parsed); err != nil {
		return ollamaSummary{}, fmt.Errorf("invalid ollama json: %w", err)
	}
	parsed.Summary = strings.TrimSpace(parsed.Summary)
	parsed.Tags = uniqueSorted(parsed.Tags)
	if parsed.Summary == "" {
		return ollamaSummary{}, fmt.Errorf("missing summary in ollama output")
	}
	return parsed, nil
}
