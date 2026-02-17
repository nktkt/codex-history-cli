# codex-history-cli

複数AIプロバイダ（`codex`, `ollama`, `grok`, `claude`, `gemini`）の会話履歴をローカルDBで管理する CLI です。

## Features

- Multi-provider import (`json` / `jsonl`)
- SQLite + FTS5 full-text search
- 自動要約・タグ付け（ヒューリスティック）
- 任意で Ollama ローカルモデル要約（`-ollama-model`）
- プロンプトインジェクション検知（score + reasons）
- トピック推移表示（ASCII）
- 対話式ダッシュボード（`dashboard` / `tui`）

## Build

```bash
go build -o codex-history-cli ./cmd/codex-history-cli
```

## Commands

```bash
./codex-history-cli init
./codex-history-cli providers
./codex-history-cli import -provider codex -file ./export.jsonl
./codex-history-cli search -q "system prompt" -limit 20
./codex-history-cli analyze
./codex-history-cli security -threshold 30
./codex-history-cli topics -days 30
./codex-history-cli dashboard
```

## Import Notes

- `-provider` は `codex|ollama|grok|claude|gemini`
- 入力は `json` または `jsonl`
- 代表的な解釈対象キー:
  - 会話: `id`, `conversation_id`, `chat_id`, `thread_id`
  - メッセージ: `messages`, `turns`, `items`
  - 文字列: `content`, `text`, `message`, `prompt`, `response`

## Optional Local LLM Summary

```bash
./codex-history-cli analyze -ollama-model qwen2.5:3b
```

`import` 時にも `-analyze -ollama-model ...` で即時分析できます。

## Dashboard Commands

- `r`: refresh
- `s <query>`: search
- `days <n>`: lookback days
- `th <n>`: risk threshold
- `h`: help
- `q`: quit

## DB Path

- `-db PATH`
- 環境変数 `CODEX_HISTORY_DB`
- デフォルト: `~/.local/share/codex-history-cli/history.db`
