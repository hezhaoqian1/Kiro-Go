# Kiro-Go

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat&logo=docker)](https://www.docker.com/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Convert Kiro accounts to OpenAI / Anthropic compatible API service.

[English](README.md) | [中文](README_CN.md) | [Tiếng Việt](README_VI.md)

If this project helps you, a Star would mean a lot.

## Features

- Anthropic `/v1/messages`, OpenAI `/v1/chat/completions` & OpenAI `/v1/responses`
- Multi-account pool with round-robin load balancing
- Auto token refresh, SSE streaming, Web admin panel
- Multiple auth: AWS Builder ID, IAM Identity Center (Enterprise SSO), Microsoft Enterprise SSO, SSO Token, local cache, credentials JSON, Kiro API Key
- Usage tracking, account import/export, i18n (CN / EN / VI)
- Support configuring outbound proxy (SOCKS5 / HTTP)

## Quick Start

### Docker Compose (Recommended)

```bash
git clone https://github.com/Quorinex/Kiro-Go.git
cd Kiro-Go
mkdir -p data
docker-compose up -d
```

### Docker Run

```bash
docker run -d \
  --name kiro-go \
  -p 8080:8080 \
  -e ADMIN_PASSWORD=your_secure_password \
  -v /path/to/data:/app/data \
  --restart unless-stopped \
  ghcr.io/quorinex/kiro-go:latest
```

### Build from Source

```bash
git clone https://github.com/Quorinex/Kiro-Go.git
cd Kiro-Go
go build -o kiro-go .
./kiro-go
```

### Deploy on Zeabur

The repo already includes a `Dockerfile`, so it builds and runs on Zeabur out of the box.

**Option 1: Dashboard (one-click)**

1. Fork this repo to your GitHub account.
2. In Zeabur, create a new service and choose **Deploy from GitHub**, then select your fork.
3. Zeabur auto-detects the `Dockerfile` and builds the image.
4. In the **Networking** tab, expose port `8080` and bind a domain.
5. In the **Variables** tab, set at least `ADMIN_PASSWORD` (admin panel password).
6. Mount a Volume at `/app/data` if you want accounts / config to survive redeploys.

**Option 2: CLI**

```bash
npm i -g zeabur
zeabur auth login
zeabur deploy
```

> Run the commands from the project root. The CLI writes `.zeabur/context.json` to remember the target project / service — it contains personal IDs, so don't commit it.

Once the service is up, open `https://<your-domain>/admin` to log in.

Config is auto-created at `data/config.json`. Mount `/app/data` for persistence. The default admin password is `changeme` — override it via the `ADMIN_PASSWORD` env var or change it in the admin panel before going to production.

## Usage

Open `http://localhost:8080/admin`, log in, add accounts, then call the API:

```bash
# Claude
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4.5","max_tokens":1024,"messages":[{"role":"user","content":"Hello!"}]}'

# OpenAI
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer any" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello!"}]}'
```

### Add a Kiro API Key account

In the admin panel, choose **API Key** when adding an account and paste `ksk_...` (or `ksk_...|region`).

You can also import via the credentials API:

```bash
curl -X POST http://localhost:8080/admin/api/auth/credentials \
  -H "Content-Type: application/json" \
  -H "Cookie: <admin-session>" \
  -d '{"kiroApiKey":"ksk_your_key|us-east-1","authMethod":"api_key","nickname":"cli-key"}'
```

API Key accounts call the Kiro CLI runtime (`https://runtime.{region}.kiro.dev/`) with `tokentype: API_KEY`. They skip OAuth refresh and do not use `profileArn`.

## Thinking Mode

Append a suffix (default `-thinking`) to the model name, e.g. `claude-sonnet-4.5-thinking`. Claude-compatible requests that include a top-level `thinking` config such as `{"type":"enabled","budget_tokens":2048}` or `{"type":"adaptive"}` also enable thinking mode automatically. Configure output format in the admin panel under Settings - Thinking Mode.

All three protocols share the capability table in `proxy/thinking_policy.go`. Older models use enabled/budget prompts; Sonnet/Opus 4.6 and later entries in that table default to adaptive/medium effort. Claude `output_config.effort`, Chat Completions `reasoning_effort`, and Responses `reasoning.effort` map to the same policy. Only allowlisted models receive `additionalModelRequestFields.output_config.effort`. Unsupported model/mode/effort combinations return 400; unknown models are not automatically advertised with thinking variants.

Explicit `thinking.type=disabled` overrides the suffix. Enabled mode honors an explicit budget; otherwise it uses 8192, capped at half of `max_tokens` when provided. Adaptive mode uses effort instead of a fixed 200000 budget. Token counting uses the same prompt. This is a Kiro compatibility policy, not proof of native thinking support for every account or a guarantee of latency or exposed reasoning.

## Shared Streaming and Completion Policy

Messages, Chat Completions, and Responses share upstream deadlines, thinking normalization, and integrity checks for both streaming and buffered requests. With the default `compatible` EOF policy, clean EOF with non-whitespace answer text and no open thinking block is retained without regeneration, but conservatively marked Claude `max_tokens`, Chat `length`, or Responses `incomplete`. This means completion could not be verified, not that a token limit was actually reached. It no longer fabricates `end_turn`. Set `KIRO_STREAM_EOF_POLICY=strict` to reject missing completion signals; bounded retries are allowed only before visible output.

Reasoning-only/whitespace responses without completion, corrupt frames, transport failures, unknown stop reasons, and unclosed thinking blocks still fail. Explicit token/context limits may terminate unfinished reasoning as incomplete. Visible text, reasoning, or tool calls are never replayed. Responses uses `response.incomplete` / `response.failed` rather than false `response.completed` events, and exposes reasoning as compatibility summary_text items with stable IDs shared between stream events and the final object.

Only leading `<thinking>`, `<think>`, `<reasoning>`, and `<thought>` blocks are decoded, including fragmented tags. Native and tagged reasoning are deduplicated. Tags after ordinary answer text, quoted tags, and fenced code remain literal. Only valid leading thinking-control preludes are removed; there is no global XML stripping.

Streaming starts with a Claude ping or OpenAI SSE comment, committing HTTP 200 before generation. Subsequent failures use protocol error events: clients must inspect the terminal event, not just HTTP status. Heartbeats and output writes are serialized, disconnect/write failure cancels generation, and no heartbeat follows a terminal event. Native web_search paths also propagate cancellation and use heartbeats/integrity checks, while retaining their existing synthesized search-result format.

The first-event deadline includes generation HTTP response headers; metadata does not count as generation progress. Text/reasoning/tool activity switches to the idle read budget. Activity, heartbeats, and retries do not extend the overall deadline. OAuth refresh/profile discovery still use their existing independent HTTP timeouts and are not immediately interruptible under the generation first-event deadline.

Design references: `vagmr/kiro2api-rs` (MIT, heartbeat/tag boundaries), `mydisha/keirouter` (MIT), `jwadow/kiro-gateway` (AGPL-3, timeout/completion policy), and `justlovemaki/AIClient2API` (GPL-3, effort adaptation). This is an independent Go implementation reusing this repository's parser/retry infrastructure; no source was copied from those projects, preserving this repository's MIT licensing boundary.

## Outbound Proxy

For users in restricted network regions, configure an outbound proxy in the admin panel under **Settings - Outbound Proxy Settings**. Supports SOCKS5 and HTTP proxies.

The setting takes effect immediately without restarting.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `CONFIG_PATH` | Config file path | `data/config.json` |
| `ADMIN_PASSWORD` | Admin panel password (overrides config) | - |
| `KIRO_FIRST_EVENT_TIMEOUT` | First generation event budget | `60s` |
| `KIRO_THINKING_FIRST_EVENT_TIMEOUT` | First event budget with thinking enabled | `180s` |
| `KIRO_STREAM_IDLE_TIMEOUT` | Upstream read inactivity after first event | `90s` |
| `KIRO_STREAM_TOTAL_TIMEOUT` | Overall generation request budget, including retries | `5m` |
| `KIRO_SSE_PING_INTERVAL` | Downstream heartbeat interval | `15s` |
| `KIRO_SSE_WRITE_TIMEOUT` | Downstream write/flush deadline | `15s` |
| `KIRO_STREAM_EOF_POLICY` | `compatible` or `strict` | `compatible` |

Durations accept Go syntax (`90s`, `5m`) or integer seconds, must be positive, and cannot exceed 24 hours. Invalid values warn and fall back to defaults. Set these in the Railway service environment. Timeout errors identify `first_event`, `idle`, or `total` for diagnostics.

CI runs the full ordinary test suite and static analysis, plus repeated race-enabled tests for the new transport, deadline, parser, and Responses output components. The full proxy race suite currently exposes a pre-existing cross-test race between `config.Init` and asynchronous account-stat persistence (`config.Save`); it is not represented as race-clean by this change.

## Contributing

Friendly discussion is welcome. If you run into issues, try asking Claude Code, Codex, or similar tools for help first — most problems can be solved that way. PRs are even better.

## Friend Links

- [LINUX DO](https://linux.do)

## Disclaimer

For educational and research purposes only. Not affiliated with Amazon, AWS, or Kiro. Users are responsible for complying with applicable terms of service and laws. Use at your own risk.

## License

[MIT](LICENSE)
