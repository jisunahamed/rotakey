# Rotakey

[![CI](https://github.com/jisunahamed/rotakey/actions/workflows/ci.yml/badge.svg)](https://github.com/jisunahamed/rotakey/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-147D92.svg)](LICENSE)

Rotakey is a single-owner, self-hosted AI gateway. Providers are configured provider-wise as OpenAI-compatible or Anthropic-compatible, while applications use one gateway key and a public model alias through either SDK contract.

```text
Application ── Bearer gateway key ──> Rotakey /v1
                                         │
                         public alias ────┤
                                         ▼
                         provider + eligible credential
```

The gateway serves:

- OpenAI: `GET /v1/models`, `POST /v1/chat/completions`, and `POST /v1/responses`
- Anthropic: Models, `POST /v1/messages`, token counting, Message Batches, and Files
- `GET /health/live`
- `GET /health/ready`

The admin console is served at `/admin/`.

New to gateway operations? Read the [Rotakey Operator Guide](docs/OPERATOR-GUIDE.md) for setup, dashboard metrics, every rate-limit dimension, routing behavior, errors, security, backup, and troubleshooting. For model discovery, role mapping, native/translated routes, Windows setup, and troubleshooting, see [Connect Claude Code to Rotakey](docs/CLAUDE-CODE.md).

## Run locally with any coding agent

Copy the prompt below with the code-block copy button and give it to any coding agent that can use your terminal:

```text
Set up and run Rotakey locally on this computer using command-line tools. Complete the work instead of only giving me instructions.

Repository: https://github.com/jisunahamed/rotakey.git
Target branch: main

Requirements and safety rules:
1. Detect the operating system and available shell first. On Windows, use CMD or PowerShell-compatible commands. On macOS/Linux, use the available POSIX shell.
2. Verify that Git, Docker Engine/Docker Desktop, and Docker Compose are installed and running. If a required tool is missing, explain the exact official installation needed and stop; do not install system software or change the firewall without my approval.
3. Inspect existing files, containers, services, and occupied ports before making changes. Do not stop, delete, rename, or reconfigure anything that already exists.
4. Clone the repository into a new `rotakey` directory only if it does not exist. If it exists, verify its remote and current changes; never discard or overwrite local work.
5. Create `.env` from `.env.example` only when `.env` does not already exist. Generate independent cryptographically secure values for POSTGRES_PASSWORD, APP_MASTER_KEY, and BOOTSTRAP_TOKEN. Never print these values in command logs.
6. Configure local access through `http://localhost`. If ports 80 or 443 are occupied, do not stop the owner process. Create an untracked local Compose file that runs only Rotakey's app, PostgreSQL, and Redis on an unused high port, with separate project-scoped volumes and no Caddy. Do not modify tracked source files just to resolve a local port conflict.
7. Use a unique Compose project name such as `rotakey-local` so no existing Rotakey or other Docker resources are reused.
8. Start the stack with Docker Compose, build the production image, and wait until both `/health/live` and `/health/ready` return HTTP 200. If startup fails, inspect only Rotakey logs, fix the Rotakey-specific problem, and retry.
9. If `/api/setup/status` reports that setup is required, generate a secure local admin password and complete the one-time setup through `/api/setup`. Capture the gateway key exactly once. Save the admin URL, API base URL, username, generated password, and gateway key in `rotakey-local-credentials.txt`. Restrict the file to the current user where the OS supports it, add it to local Git exclusions, and never commit it.
10. Verify admin login and call `/v1/models` with the generated Bearer key. An empty model list is valid before providers are configured.
11. Finish by reporting the local Admin URL, API base URL, health status, credentials-file path, Compose project name, and container status. Do not reveal secret values in the chat unless I explicitly request them.
```

## Quick start on an Ubuntu VPS

Requirements: Docker Engine with the Compose plugin, ports `80/tcp`, `443/tcp`, and `443/udp` available, and at least 2 GB RAM.

```bash
git clone https://github.com/jisunahamed/rotakey.git
cd rotakey
cp .env.example .env
openssl rand -base64 32
openssl rand -hex 32
openssl rand -hex 24
```

Put the three generated values into `.env` as:

- `APP_MASTER_KEY`: the base64 value
- `BOOTSTRAP_TOKEN`: the first hex value
- `POSTGRES_PASSWORD`: the second hex value

For IP-only HTTP, also set:

```dotenv
APP_HOST=:80
PUBLIC_BASE_URL=http://YOUR_VPS_IP
SESSION_COOKIE_SECURE=false
```

Start the stack:

```bash
docker compose up -d --build
docker compose ps
curl -fsS http://YOUR_VPS_IP/health/ready
```

Open `http://YOUR_VPS_IP/admin/`. The first-run screen asks for the bootstrap token, an admin username, and a password of at least 12 characters. It creates the admin and shows the gateway key once. Save that key immediately.

IP mode sends admin credentials over plain HTTP. Use it only for initial setup on a trusted network or through an SSH tunnel; domain mode is the recommended permanent configuration.

## Domain and automatic HTTPS

Point the domain's `A` record—and `AAAA` when used—to the VPS. Copy the values from `.env.domain.example` into `.env` and replace the example domain:

```dotenv
APP_HOST=ai.example.com
PUBLIC_BASE_URL=https://ai.example.com
SESSION_COOKIE_SECURE=true
```

Then apply the change:

```bash
docker compose up -d
curl -fsS https://ai.example.com/health/ready
```

Caddy obtains and renews the certificate automatically. PostgreSQL and Redis have no published host ports.

## Operational console

Overview is the single-owner routing control surface. It refreshes every 10 seconds and can summarize the last `1h`, `24h`, or `7d` without changing public API behavior.

- The status ledger shows ready routes and API keys, requests, errors, tokens, and P95 latency.
- The accessible SVG signal timeline compares traffic, latency, and error buckets without a chart dependency.
- The attention queue prioritizes invalid keys, disabled resources, exhausted capacity, and limits with 20% or less headroom.
- Provider rows aggregate live `RPS`, `RPM`, `RPD`, `TPS`, `TPM`, `TPD`, and `TPR` capacity across every ready key.
- The Route Debugger traces `public model -> provider -> next API key -> limiting bucket -> reset time`. Every credential segment shows health, cursor state, and effective shared plus model-specific headroom.
- Selecting a provider, model, or key opens the contextual inspector. On mobile it opens as a full-screen sheet.
- Overview exposes only safe actions: copy the unified URL, test a provider, recheck a key, or open filtered provider and request-log views.

Providers, Model routes, and Request logs use the same dense resource/inspector navigation. Deep links are supported with `/admin/providers?provider=<id>`, `/admin/models?model=<id>`, and `/admin/logs?q=<model-or-request>&status=<code>`.

## Add a provider

In **Providers → Add provider**:

1. Choose **OpenAI-compatible** or **Anthropic-compatible**, then enter the provider base URL. The Anthropic preset uses `x-api-key` and `anthropic-version: 2023-06-01`.
2. Enter API keys in separate fields. Use **Add another API key** for more keys, and optionally mark one key as **Primary**.
3. Choose **Check keys & load models**. Rotakey validates every key against the protocol-aware upstream `/models` endpoint. If an Anthropic-compatible catalog returns `305`, `404`, `405`, or a non-standard response, add the model ID manually; Rotakey validates manually created routes with a minimal Messages probe.
4. Select the models to expose and edit their globally unique public aliases, such as `groq/llama-3.3-70b`.
5. Set any combination of RPS, RPM, RPD, TPS, TPM, TPD, and TPR. Blank fields are unlimited. An API key's shared limits are consumed by every model under that provider; optional model-specific limits add a narrower limit for that model.
6. Review and create the provider. Keys are validated again before they are encrypted and saved.

The provider slug is an internal identifier. Rotakey generates it from the provider name and keeps it out of the setup form.

The provider inspector shows aggregate capacity across every ready key. For example, two keys configured at `40 RPM` produce `80 RPM` total. Each routed request lowers the live remaining value; adding or deleting a key recalculates the total. A provider rejection during traffic (`401` or `403`) quarantines the key and surfaces a warning in the UI.

HTTPS is required by default. HTTP and private-network destinations need the explicit private-network switch. Loopback, private, link-local, multicast, and metadata destinations remain blocked unless that switch is enabled.

### Call any configured model

Use the same URL and key for every provider. Only `model` changes:

```bash
curl https://ai.example.com/v1/chat/completions \
  -H "Authorization: Bearer $ROTAKEY_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "groq/llama-3.3-70b",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": false
  }'
```

The official OpenAI clients can use the gateway by changing their base URL:

```python
from openai import OpenAI

client = OpenAI(
    api_key="gw_...",
    base_url="https://ai.example.com/v1",
)

result = client.chat.completions.create(
    model="groq/llama-3.3-70b",
    messages=[{"role": "user", "content": "Hello"}],
)
```

### Anthropic SDK and Claude Code

For the complete Claude Code guide—including how to list and select every enabled Rotakey model—see [Connect Claude Code to Rotakey](docs/CLAUDE-CODE.md).

Anthropic clients use the Rotakey host root; the SDK appends `/v1/messages`. The same Rotakey gateway key works as `x-api-key`:

```bash
export ANTHROPIC_BASE_URL="https://ai.example.com"
export ANTHROPIC_API_KEY="gw_..."
```

```python
from anthropic import Anthropic

client = Anthropic(api_key="gw_...", base_url="https://ai.example.com")
message = client.messages.create(
    model="anthropic/claude-sonnet",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello"}],
)
```

Public Anthropic endpoints include Messages, token counting, models, Message Batches, and Files. Native Anthropic routes preserve thinking, prompt caching, citations, client/server tools, beta headers, and unknown future SSE events. Cross-protocol routes translate the lossless text, image, and client-function-tool subset; unsupported features return `400 unsupported_feature` instead of being dropped.

Model onboarding creates a capability profile. Catalog-selected models are marked `catalog verified`; manually entered/single routes must pass a real bounded core inference probe before they are saved. The profile records native versus translated Chat, Responses, Messages, streaming normalization, tools, thinking, and unknown/unverified optional features. Rotakey uses that profile to expose both public protocols without pretending an untested feature is supported.

Files use the **Default Anthropic resource provider** selected in System settings. Rotakey exposes opaque file/batch IDs pinned to their original provider and API key, so later retrieve, content, cancel, results, and delete calls remain consistent. A pinned credential cannot be deleted until its resources are removed.

## Routing and limits

- The public alias selects exactly one provider route; there is no cross-provider model fallback.
- Without a primary API key, eligible keys are visited with a Redis-backed round-robin cursor.
- If a healthy primary key is configured, Rotakey uses its available capacity first and falls back to the other keys when needed.
- A key's shared limits span every model route under the provider. Shared limits and any extra model-specific limits must all reserve atomically.
- Request and token windows use fixed UTC-aligned second, minute, and day boundaries.
- Input estimate plus the requested output ceiling is reserved before the upstream call. Missing output ceilings use the model default.
- Non-streaming usage is reconciled to actual upstream usage. Streaming without final usage retains the conservative reservation.
- When every key is full and the earliest reset is within the configured wait ceiling, the request waits with cancellation support. Otherwise it returns OpenAI-style `429` and `Retry-After`.
- Before response bytes begin, credential-specific failures can fail over across every other eligible key for up to 60 seconds: connection/pre-response timeout, `401/403`, `429`, `529`, and selected `5xx`. Deterministic payload `400` errors are not replayed across keys. Streaming is never retried after response bytes begin.
- If an Anthropic-compatible upstream accepts `stream:true` but returns a normal JSON Message, Rotakey converts it into protocol-correct SSE before translating it to the OpenAI client. An empty or malformed HTTP `200` is reported as `502 upstream_stream_invalid` instead of a false success.
- Upstream `401/403` quarantines a credential, `429` starts its `Retry-After` cooldown, and repeated transport/`5xx` failures open a short circuit.
- Redis failure returns `503`; limits are never bypassed.

Overview keeps providers collapsed by default. Expanding a provider reveals its compact model list; selecting a model opens the contextual inspector with the next round-robin key, credential health, and the currently limiting request/token headroom. Shared key capacity is shown once at provider level instead of being repeated for every model.

### Provider-specific request compatibility

Some OpenAI-compatible providers reject otherwise common top-level request fields. When an upstream explicitly reports an unsupported optional compatibility hint, Rotakey removes the reported field and performs a bounded retry. Learned fields are cached per model route for 24 hours, appear in the request's routing attempts, and are returned in `X-Rotakey-Removed-Parameters`.

Adaptive removal is intentionally limited to safe behavior hints: `thinking`, `reasoning`, `reasoning_effort`, and `verbosity`. Core request fields and arbitrary parameters are never silently removed. For a permanent or provider-specific override, edit a model route and add a field under **Remove unsupported request fields**.

When an upstream explicitly recommends an equivalent output-limit field, Rotakey preserves the value and learns the model-specific replacement—for example, `max_tokens → max_completion_tokens`. Safe replacements appear in routing attempts and `X-Rotakey-Replaced-Parameters`; arbitrary parameter renames are never inferred.

## Responses compatibility

Native upstream Responses requests pass through. For a Chat-only route, Rotakey translates:

- text input and common message/input items
- JSON output formats
- function tools and tool calls
- usage
- text and function-tool SSE events

Features that cannot be translated faithfully return `400 unsupported_feature`, including background mode, hosted tools, conversation IDs, file references, and unsupported item types. They are never silently dropped.

## Security and retention

- Upstream credentials and optional captured bodies use AES-256-GCM with `APP_MASTER_KEY`.
- Admin passwords use Argon2id.
- The gateway key is stored only as a SHA-256 hash and is shown only at create/rotate time.
- Key rotation revokes the previous key immediately.
- Admin sessions are Redis-backed HttpOnly, SameSite cookies with CSRF protection and login throttling.
- Provider connections re-resolve and validate addresses at dial time; redirects and ambient proxy routing are disabled.
- Metadata retention defaults to 90 days. Per-model body capture is off by default and defaults to 30 days with a size cap and truncation marker.
- Audit records intentionally exclude secrets.

Keep `.env` out of backups and source control. Losing `APP_MASTER_KEY` makes encrypted upstream credentials and captured bodies unrecoverable.

## Operations

View health and logs:

```bash
docker compose ps
docker compose logs -f --tail=200 app caddy
curl -fsS https://ai.example.com/health/live
curl -fsS https://ai.example.com/health/ready
```

Update:

```bash
git pull --ff-only
docker compose up -d --build
```

Back up PostgreSQL:

```bash
chmod +x scripts/backup.sh scripts/restore.sh
./scripts/backup.sh
```

Restore replaces database objects contained in the archive:

```bash
./scripts/restore.sh --confirm-replace-database /absolute/path/to/rotakey-TIMESTAMP.sql.gz
docker compose restart app
```

Redis contains live limiter/session/circuit state rather than the source of truth. PostgreSQL and the `.env` encryption key are the critical backup pair.

See [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) for firewall, DNS, validation, resource, and recovery details.

## Development and verification

Backend:

```bash
go test ./...
TEST_REDIS_URL=redis://127.0.0.1:6379/0 go test -count=1 ./...
go vet ./...
```

Admin UI:

```bash
cd web
npm ci
npm run typecheck
npm run build
```

Container packaging:

```bash
docker compose config --quiet
docker build .
```

The Redis-backed suite includes concurrent atomic reservation and the acceptance case where two credentials at `40 RPM` route the first 80 requests exactly `40/40`, then reject request 81 until capacity resets.

## v1 scope

Rotakey v1 is intentionally single-owner. Multi-user accounts, billing, embeddings, image/audio APIs, cross-provider model fallback, and Kubernetes are outside this release.

## Contributing and security

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md), never through a public issue.

Rotakey is available under the [MIT License](LICENSE).
