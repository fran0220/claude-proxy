# claude-proxy

Claude-only local reverse proxy with Claude Code OAuth/API-key authentication, request logging, token usage statistics, a macOS status bar app, and a small embedded admin dashboard.

## What it does

- Proxies Anthropic-compatible Claude API calls:
  - `POST /v1/messages`
  - `POST /v1/messages/count_tokens`
- Uses Claude Code OAuth credentials from macOS Keychain by default.
- Can fall back to configured Anthropic API keys.
- Records request logs and token usage into SQLite.
- Provides an embedded dashboard for overview, logs, stats, models, API keys, redirects, and token refresh.
- Shows a macOS status bar icon with auth/model/stats/last-request status and quick actions.
- Dynamically discovers available Claude models via Anthropic `GET /v1/models`.

It intentionally does **not** include Amp upstream routing, OpenAI/Codex, Gemini, or custom providers.

## Defaults

| Item | Default |
| --- | --- |
| Proxy | `http://localhost:9327` |
| Admin dashboard | `http://localhost:9328` |
| Config | `~/.claude-proxy/config.yaml` |
| Database | `~/.claude-proxy/claude-proxy.db` |

## Build and run

```bash
go test ./...
go build -o claude-proxy .
./claude-proxy
```

On macOS, normal runs show a status bar item. The menu includes current auth status, model counts, request/token stats, the last request, and actions to open the dashboard, open health, reload the Claude token, discover models, or quit.

Run without the status bar icon:

```bash
CLAUDE_PROXY_NO_TRAY=1 ./claude-proxy
```

Build a menu-bar `.app` bundle:

```bash
./build-macos.sh
open ClaudeProxy.app
```

Install it:

```bash
cp -R ClaudeProxy.app /Applications/
```

Open the dashboard:

```text
http://localhost:9328
```

Use a custom config path:

```bash
CLAUDE_PROXY_CONFIG=/path/to/config.yaml ./claude-proxy
```

Enable debug logs:

```bash
CLAUDE_PROXY_LOG=debug ./claude-proxy
```

## Example request

```bash
curl http://localhost:9327/v1/messages \
  -H 'content-type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -d '{
    "model": "claude-sonnet-4-6",
    "max_tokens": 64,
    "messages": [
      {"role": "user", "content": "hello"}
    ]
  }'
```

## Config shape

First run creates a default config. The important fields are:

```yaml
listen: ":9327"
admin-listen: ":9328"
data-dir: ~/.claude-proxy

claude:
  source: keychain # keychain | apikey
  entries:
    - id: example
      label: Anthropic API Key
      api-key: sk-ant-...
      base-url: "" # optional, defaults to https://api.anthropic.com
  models:
    - name: claude-sonnet-4-6
      route: local # local | apikey

model-redirects:
  claude-opus-4-7: claude-opus-4-8

retry:
  max-attempts: 5
  initial-delay: 1s
```

Unknown model IDs are allowed and default to the configured default route. The model list is for dashboard/config convenience, not a hard whitelist.

## Model discovery

The dashboard's **Models → Discover** button calls:

```text
POST /api/models/discover
```

Discovery uses Anthropic:

```text
GET /v1/models?limit=1000
```

Discovery behavior:

- Adds newly discovered models to config.
- Updates display name, token limits, and `last-seen` metadata for existing models.
- Does not delete models that are no longer returned.
- Falls back from local OAuth discovery to API-key discovery when possible.

## Admin API highlights

```text
GET  /api/status
GET  /api/overview
GET  /api/stats
GET  /api/stats/daily?days=30
GET  /api/stats/hourly?hours=24
GET  /api/stats/routes
GET  /api/stats/tokens
GET  /api/logs?limit=100
GET  /api/logs/errors
GET  /api/auth/status
POST /api/token/refresh
POST /api/config/source
GET  /api/models
POST /api/models/discover
POST /api/models/add
POST /api/models/remove
POST /api/models/route
GET  /api/keys
POST /api/keys/add
POST /api/keys/update
POST /api/keys/remove
POST /api/keys/test
GET  /api/redirects
POST /api/redirects/set
```
