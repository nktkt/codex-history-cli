# codex-history-cli

Small Go CLI to record Codex conversation content from local session logs.

It reads:
- `~/.codex/sessions/**/*.jsonl`

It extracts only:
- `user_message` as `role=user`
- `agent_message` as `role=assistant`

And appends normalized JSONL records to:
- `~/.codex/conversation_history.jsonl` (default)

## Build

```bash
go build -o codex-history .
```

## Commands

### Sync once

```bash
./codex-history sync
```

Useful flags:

```bash
./codex-history sync \
  --sessions-dir ~/.codex/sessions \
  --out ~/.codex/conversation_history.jsonl \
  --from 2026-02-17T00:00:00Z \
  --dry-run
```

### Watch continuously

```bash
./codex-history watch --interval 5s
```

### Show records

```bash
./codex-history show --limit 20
./codex-history show --session <session-id> --role user
./codex-history show --contains github --from 2026-02-17T00:00:00Z --to 2026-02-17T23:59:59Z
./codex-history show --desc --json
```

### Show aggregate stats

```bash
./codex-history stats
./codex-history stats --session <session-id> --contains error --json
```

### List session summaries

```bash
./codex-history sessions
./codex-history sessions --contains github --limit 10
./codex-history sessions --json
```

### Export records (new)

```bash
# markdown to stdout
./codex-history export --format markdown

# csv to file
./codex-history export \
  --format csv \
  --out /tmp/codex-history.csv \
  --from 2026-02-17T00:00:00Z \
  --to 2026-02-17T23:59:59Z \
  --contains github

# jsonl subset
./codex-history export --format jsonl --session <session-id> --limit 100 --desc
```

## Output format

Each line is a JSON object:

```json
{
  "id": "a2b6f0f9f8fdb7611c4eae45fcfd2947",
  "session_id": "019c6699-d4b6-79d2-8143-fff160d7add6",
  "timestamp": "2026-02-17T11:56:22.113Z",
  "role": "user",
  "text": "Please create a Go CLI that records Codex conversation history.",
  "source_file": "/Users/x/.codex/sessions/2026/02/16/rollout-2026-02-16T22-18-03-019c6699-d4b6-79d2-8143-fff160d7add6.jsonl",
  "source_line": 116
}
```

`id` is deterministic (hash of session/timestamp/role/text), so re-running `sync` does not duplicate existing records.

## Multi-provider Mode (new)

This repository now also includes a multi-provider history manager command:

```bash
go build -o codex-history-cli ./cmd/codex-history-cli
```

Supports:

- `codex`, `ollama`, `grok`, `claude`, `gemini`
- SQLite + FTS search
- summary/tagging (heuristic, optional local Ollama)
- prompt injection scoring
- dashboard/TUI

See `cmd/codex-history-cli/README.md` for details.
