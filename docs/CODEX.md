# Connect Codex to Rotakey

Rotakey is the remote, self-hosted owner of provider API keys, model aliases,
rate limits, retries, and request logs. Codex Router is the local companion
that makes those aliases available to Codex Desktop and Codex CLI without
placing upstream provider keys on a developer workstation.

## What is supported

Rotakey exposes the OpenAI Responses endpoints Codex needs:

- `GET /v1/models`
- `GET /v1/codex/manifest`
- `POST /v1/responses`
- `POST /v1/responses/compact`

The compact endpoint creates a short, model-generated replacement history. It
uses the selected Rotakey alias and the normal credential pool, so limits,
failover, and logs still apply. It does not use OpenAI's opaque encrypted
compaction payload, because that payload can only be created by OpenAI.

Core text, streaming, and client function tools work through routes that have
Responses support. A native Responses upstream is preferred. Chat-only and
Anthropic routes use Rotakey's existing safe translation layer; features with
no lossless equivalent return `400 unsupported_feature` instead of silently
changing the request.

## Recommended architecture

```text
Codex Desktop / CLI
        |
        v
Codex Router on the developer computer (127.0.0.1)
        |
        | one Rotakey gateway key
        v
Rotakey on your VPS (/v1)
        |
        v
Provider credential pool and public model aliases
```

Do not put a public Rotakey key directly in shared project files or in Codex
configuration. Store it in Codex Router's local credential store or your OS
secret manager. Rotakey should be reachable with HTTPS in normal use.

## Configure the Rotakey adapter

The upstream Codex Router project needs a Rotakey provider adapter. Its
configuration should contain only this information:

```json
{
  "id": "rotakey",
  "displayName": "Rotakey",
  "kind": "openai-compatible",
  "protocol": "openai-responses",
  "baseUrl": "https://ai.example.com/v1",
  "credential": {
    "file": "rotakey.json",
    "environment": ["ROTAKEY_API_KEY"]
  }
}
```

The adapter should fetch `GET /v1/models`, register every enabled public alias
in Codex Router's local catalog, and send each call to Rotakey's Responses
endpoint. The local Router keeps its loopback capability URL and its own
Codex configuration management; Rotakey is never exposed directly as the
Codex local server.

## Verification checklist

1. In Rotakey, add an enabled route with Responses support and a healthy key.
2. Confirm it appears in `GET /v1/models` using the Rotakey gateway key.
3. Run Codex Router's live compatibility checks against the registered alias:
   basic response, SSE streaming, function tool call, and compaction.
4. In Rotakey Admin > Request logs, verify that every request is recorded as
   `responses`, with the expected alias, provider, key label, and attempt.

Only mark an alias as Codex-ready after all four checks pass. A provider can
be reachable and still reject tool schemas, reasoning fields, or stream
formats; Rotakey records the upstream diagnostic when that happens.

## Current limitations

- Hosted OpenAI tools, background mode, conversation IDs, file references, and
  other features without a safe cross-provider representation remain rejected
  on translated routes.
- Native Responses providers give the most complete Codex experience.
- The Codex Router adapter must be maintained in a fork or contributed to the
  upstream project before a one-click setup can be claimed.
