# Connect Claude Code to Rotakey

Claude Code can use Rotakey as an Anthropic-compatible gateway while Rotakey keeps provider credentials, routing, limits, health checks, and failover on the server. Claude Code only receives the Rotakey gateway URL, one gateway key, and a public model alias.

## What you need

- A reachable Rotakey URL such as `https://ai.example.com` (recommended) or `http://SERVER_IP` on a trusted network.
- The active Rotakey gateway key from **Admin → Access key**. This is not an upstream provider key.
- At least one enabled model route with **Messages** support and one healthy API key.
- Claude Code v2.1.129 or newer for gateway model discovery. Run `claude update` and `claude doctor`.

Use the Rotakey host root for `ANTHROPIC_BASE_URL`. Do not append `/v1`; Claude Code appends `/v1/messages` itself.

## Quick setup

### macOS, Linux, WSL, or Git Bash

```bash
export ROTAKEY_KEY="gw_replace_me"
export ANTHROPIC_BASE_URL="https://ai.example.com"
export ANTHROPIC_AUTH_TOKEN="$ROTAKEY_KEY"
unset ANTHROPIC_API_KEY
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1

claude --model "anthropic/claude-sonnet"
```

`ANTHROPIC_AUTH_TOKEN` is sent as `Authorization: Bearer …`. Rotakey also accepts `ANTHROPIC_API_KEY` through `x-api-key`, but configure only one authentication method to avoid conflicting headers.

### Windows PowerShell

```powershell
$env:ROTAKEY_KEY = "gw_replace_me"
$env:ANTHROPIC_BASE_URL = "https://ai.example.com"
$env:ANTHROPIC_AUTH_TOKEN = $env:ROTAKEY_KEY
Remove-Item Env:ANTHROPIC_API_KEY -ErrorAction SilentlyContinue
$env:CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY = "1"

claude --model "anthropic/claude-sonnet"
```

Native Windows Claude Code requires Git for Windows. WSL is also supported.

## List every enabled Rotakey model

Rotakey's public aliases—not upstream model IDs—are the model names Claude Code must send.

```bash
curl -s "$ANTHROPIC_BASE_URL/v1/models" \
  -H "Authorization: Bearer $ANTHROPIC_AUTH_TOKEN" \
  -H "anthropic-version: 2023-06-01"
```

PowerShell:

```powershell
$headers = @{
  Authorization = "Bearer $env:ANTHROPIC_AUTH_TOKEN"
  "anthropic-version" = "2023-06-01"
}
(Invoke-RestMethod "$env:ANTHROPIC_BASE_URL/v1/models" -Headers $headers).data |
  Select-Object id, display_name
```

Every enabled alias with Messages support can be selected explicitly, including routes backed by an OpenAI-compatible provider. Rotakey translates the supported Messages subset before forwarding it.

The Model route inspector shows whether an alias was catalog-verified or passed a live core probe, plus whether Messages is native or translated. Prefer a `probe verified` native Anthropic route when Claude Code needs thinking, prompt caching, citations, or other Anthropic-only features.

```bash
claude --model "free-model/claude-fable-5"
claude --model "azure/grok-4.3"
claude --model "nvidia/deepseek-v4"
```

Inside a running session, use `/model <public-alias>` to switch for that session, or `/status` to confirm the active model. You can also set the startup default:

```bash
export ANTHROPIC_MODEL="azure/grok-4.3"
claude
```

## Show gateway models in the `/model` picker

Set:

```bash
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
```

Claude Code then queries Rotakey's authenticated `/v1/models` endpoint at startup. Current Claude Code releases only add discovered IDs beginning with `claude` or `anthropic` to the picker. Other valid Rotakey aliases still work with `--model`, `ANTHROPIC_MODEL`, or `/model <alias>`.

For one non-standard alias that must appear in the picker, use:

```bash
export ANTHROPIC_CUSTOM_MODEL_OPTION="azure/grok-4.3"
export ANTHROPIC_CUSTOM_MODEL_OPTION_NAME="Grok 4.3 via Rotakey"
export ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION="Routed and limited by Rotakey"
```

## Map Claude Code's built-in model roles

Claude Code may use separate model roles for normal work, complex planning, fast/background work, and subagents. Point each role to any suitable Rotakey public alias:

```bash
export ANTHROPIC_DEFAULT_OPUS_MODEL="anthropic/claude-opus"
export ANTHROPIC_DEFAULT_SONNET_MODEL="azure/grok-4.3"
export ANTHROPIC_DEFAULT_HAIKU_MODEL="free-model/claude-fable-5"
export CLAUDE_CODE_SUBAGENT_MODEL="nvidia/deepseek-v4"
```

`ANTHROPIC_SMALL_FAST_MODEL` is deprecated in current Claude Code releases; use `ANTHROPIC_DEFAULT_HAIKU_MODEL`.

You can persist non-secret values in `~/.claude/settings.json`:

```json
{
  "model": "azure/grok-4.3",
  "env": {
    "ANTHROPIC_BASE_URL": "https://ai.example.com",
    "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "anthropic/claude-opus",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "azure/grok-4.3",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "free-model/claude-fable-5"
  }
}
```

Keep the gateway key in your shell, secret manager, or OS credential tooling. Never commit it to a project settings file.

## Native versus translated routes

| Rotakey upstream route | Claude Code behavior |
| --- | --- |
| Anthropic-compatible | Native Messages, streaming, tools, thinking, prompt caching, citations, and safe `anthropic-*` headers pass through. |
| OpenAI-compatible | Rotakey translates core text, images, system prompts, client function tools/results, tool choice, stop sequences, and streaming. |

Features without a faithful target-protocol equivalent are never silently discarded. Rotakey returns `400 unsupported_feature` with a request ID. For the fullest Claude Code feature set, choose a native Anthropic-compatible model route. For a translated route, disabling an unsupported Claude Code feature may be necessary—for example `CLAUDE_CODE_DISABLE_THINKING=1` when the target model cannot represent thinking blocks.

## Verify before opening Claude Code

```bash
curl "$ANTHROPIC_BASE_URL/v1/messages" \
  -H "Authorization: Bearer $ANTHROPIC_AUTH_TOKEN" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{"model":"azure/grok-4.3","max_tokens":64,"messages":[{"role":"user","content":"Reply with OK"}]}'
```

Then run:

```bash
claude -p --model "azure/grok-4.3" "Reply with OK"
```

Open **Admin → Request logs** to confirm the public protocol, translated/native path, provider, selected key, attempts, latency, token usage, and any automatic diagnosis.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `401 authentication_error` | Use the current Rotakey gateway key. Remove a stale `ANTHROPIC_API_KEY` if `ANTHROPIC_AUTH_TOKEN` is configured. |
| Claude Code calls `api.anthropic.com` | `ANTHROPIC_BASE_URL` was not exported in the shell that launched Claude Code. It must be the Rotakey host root. |
| Alias not visible in `/model` | Enable discovery and update Claude Code. IDs not beginning with `claude` or `anthropic` must be selected explicitly or added with `ANTHROPIC_CUSTOM_MODEL_OPTION`. |
| `404` model not found | Use the Rotakey public alias exactly as returned by `/v1/models`, including case and `/`. Confirm the route is enabled. |
| `400 unsupported_feature` | The selected route crosses protocols and the request used a feature with no safe translation. Inspect Request logs; use a native Anthropic route or disable that feature. |
| Blank response with HTTP `200` | Current Rotakey repairs a provider that returns a normal JSON Message despite `stream:true`. If the body is empty or malformed, Request logs show `502 upstream_stream_invalid` rather than a false success. |
| `429` | Every eligible API key is at a configured/shared/model limit or upstream cooldown. Inspect the limiting bucket and reset countdown. |
| `503` | Redis/database readiness or safe routing is unavailable. Rotakey fails closed instead of bypassing limits. |
| Model picker shows stale entries | Restart Claude Code. Gateway discovery refreshes at startup and uses `~/.claude/cache/gateway-models.json`. |
| Long request times out | Check provider latency and Rotakey attempts. Claude Code's `API_TIMEOUT_MS` can be increased, but Rotakey's bounded credential failover still applies. |

Official references: [Claude Code LLM gateways](https://code.claude.com/docs/en/llm-gateway), [model configuration](https://code.claude.com/docs/en/model-config), and [environment variables](https://code.claude.com/docs/en/env-vars).
