# Rotakey

[![CI](https://github.com/jisunahamed/rotakey/actions/workflows/ci.yml/badge.svg)](https://github.com/jisunahamed/rotakey/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-147D92.svg)](LICENSE)

Rotakey is a single-owner, self-hosted AI gateway. Providers are configured provider-wise, while applications use one OpenAI-compatible base URL, one gateway key, and a public model alias.

```text
Application ── Bearer gateway key ──> Rotakey /v1
                                         │
                         public alias ────┤
                                         ▼
                         provider + eligible credential
```

The gateway serves:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `GET /health/live`
- `GET /health/ready`

The admin console is served at `/admin/`.

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

## Add a provider

In **Providers → Add provider**:

1. Enter the provider's OpenAI-compatible base URL and authentication header.
2. Add one or more upstream model IDs and give each a globally unique public alias such as `groq/llama-3.3-70b`.
3. Enter API keys in separate fields. Use **Add another API key** for more keys, and optionally mark one key as **Primary**.
4. Set any combination of RPS, RPM, RPD, TPS, TPM, TPD, and TPR. Blank fields are unlimited. An API key's shared limits are consumed by every model under that provider; optional model-specific limits add a narrower limit for that model.
5. Review, create, and run the connection test.

The provider slug is an internal identifier. Rotakey generates it from the provider name and keeps it out of the setup form.

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

## Routing and limits

- The public alias selects exactly one provider route; there is no cross-provider model fallback.
- Without a primary API key, eligible keys are visited with a Redis-backed round-robin cursor.
- If a healthy primary key is configured, Rotakey uses its available capacity first and falls back to the other keys when needed.
- A key's shared limits span every model route under the provider. Shared limits and any extra model-specific limits must all reserve atomically.
- Request and token windows use fixed UTC-aligned second, minute, and day boundaries.
- Input estimate plus the requested output ceiling is reserved before the upstream call. Missing output ceilings use the model default.
- Non-streaming usage is reconciled to actual upstream usage. Streaming without final usage retains the conservative reservation.
- When every key is full and the earliest reset is within the configured wait ceiling, the request waits with cancellation support. Otherwise it returns OpenAI-style `429` and `Retry-After`.
- One safe retry may use a different credential for connection failures, pre-response timeouts, `429`, and selected `5xx` responses. Streaming is never retried after response bytes begin.
- Upstream `401/403` quarantines a credential, `429` starts its `Retry-After` cooldown, and repeated transport/`5xx` failures open a short circuit.
- Redis failure returns `503`; limits are never bypassed.

The model-first capacity rail shows the next round-robin segment, credential health, and the currently limiting request/token headroom.

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
