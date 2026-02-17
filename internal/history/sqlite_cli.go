package history

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type SQLiteCLI struct {
	DBPath string
}

func NewSQLiteCLI(dbPath string) SQLiteCLI {
	return SQLiteCLI{DBPath: dbPath}
}

func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "history.db"
	}
	return filepath.Join(home, ".local", "share", "codex-history-cli", "history.db")
}

func EnsureDirForFile(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func (s SQLiteCLI) Exec(sql string) error {
	cmd := exec.Command("sqlite3", "-batch", s.DBPath)
	cmd.Stdin = strings.NewReader(sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sqlite exec failed: %w: %s", err, stderr.String())
	}
	return nil
}

func (s SQLiteCLI) QueryJSON(sql string) ([]map[string]any, error) {
	cmd := exec.Command("sqlite3", "-batch", "-json", s.DBPath)
	cmd.Stdin = strings.NewReader(sql)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sqlite query failed: %w: %s", err, stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return []map[string]any{}, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, fmt.Errorf("invalid sqlite json output: %w", err)
	}
	return rows, nil
}

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.0f", x)
	case int:
		return fmt.Sprintf("%d", x)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		if x == "" {
			return 0
		}
		var n int
		_, _ = fmt.Sscanf(x, "%d", &n)
		return n
	default:
		return 0
	}
}
