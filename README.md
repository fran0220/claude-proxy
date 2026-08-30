# claude-proxy

Claude-only local reverse proxy with Claude Code OAuth/API-key authentication, request logging, token usage statistics, a macOS status bar app, and a small embedded admin dashboard.

## What it does

- Proxies Anthropic-compatible Claude API calls:
  - `POST /v1/messages`
  - `POST /v1/messages/count_tokens`
- Uses Claude Code OAuth credentials from macOS Keychain by default.
- Can fall back to configured Anthropic API keys.
- Records request logs and token usage into SQLite, including a per-request price snapshot.
- Estimates equivalent Anthropic API cost from [models.dev](https://models.dev) list prices (`input` / `output` / `cache_read` / `cache_write`).
- Provides an embedded dashboard for plan limits, 24h/7d/30d/all usage, request logs, model routing, API keys, and token refresh.
- Shows a macOS status bar icon with auth/model/stats/last-request status and quick actions.
- Dynamically discovers available Claude models via Anthropic `GET /v1/models`.
- Requires separate client and dashboard access tokens generated on first run.

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

security:
  access-token: cp_...       # x-api-key or Bearer token for proxy clients
  admin-token: cp_admin_...  # Bearer token entered in the dashboard login

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

pricing:
  timezone: "" # empty uses the host local timezone for daily/hourly buckets
  overrides: {} # optional model id -> {input, output, cache_read, cache_write} USD / 1M tokens
```

The config is written atomically with file mode `0600` because it contains secrets. The proxy
accepts its access token through either header, making it compatible with Anthropic SDKs and
generic bearer-token clients:

```bash
curl http://localhost:9327/v1/messages \
  -H 'x-api-key: cp_...' \
  -H 'content-type: application/json' \
  -d '{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}'
```

Dashboard API routes require the separate admin token. The dashboard asks for it on load and keeps
it in browser session storage until the tab is closed or you log out.

Usage windows are rolling (`24h`, `7d`, `30d`, `all`). Costs are equivalent official API dollars, not Claude Code subscription spend. Local/Keychain traffic is labeled as equivalent. Unpriced models stay `null` instead of `$0`. New requests snapshot the current catalog rate so later price changes do not rewrite history.

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
GET  /api/usage?range=24h
GET  /api/subscription/usage
GET  /api/prices
POST /api/prices/refresh
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
