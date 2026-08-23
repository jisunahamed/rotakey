# Rotakey Operator Guide

This guide explains the admin console and the decisions Rotakey makes for every request. It is written for operators who do not need to know the Go or Redis internals.

## 1. The mental model

Your application sends one gateway key, one Rotakey base URL, and a public model alias. Rotakey resolves the alias to a provider, selects an eligible API key, reserves its configured request and token capacity, calls the upstream model, and records the result.

```text
Client -> Rotakey /v1 -> public model alias -> provider -> eligible API key -> upstream model
```

- A **provider** owns a base URL, authentication method, models, and one or more upstream API keys.
- A **model route** maps a public alias such as `nvidia/llama-3.3` to the provider's upstream model ID.
- A **credential/API key** is one upstream secret. It can be optional primary, healthy, cooling down, quarantined, disabled, exhausted, or unknown.
- A **shared limit** belongs to an API key and is consumed by every model using that key.
- A **model override** is an additional limit for one model on one API key. Both shared and model-specific limits must have capacity.

## 2. First setup

1. Open `/admin/` and complete the one-time owner setup.
2. Copy the gateway key when it is shown. Rotakey stores only its hash and cannot show the secret again.
3. Add a provider, choose OpenAI-compatible or Anthropic-compatible, and enter its protocol base URL.
4. Add API keys one at a time or in bulk. Primary is optional.
5. Let Rotakey validate the keys and discover the provider's model list. If an Anthropic catalog is unavailable, enter a model ID manually; Rotakey probes Messages before saving it.
6. Select the models to expose, choose globally unique public aliases, and configure limits.
7. Test the provider, enable it, and copy the unified `/v1` base URL.

Use the same gateway key for all enabled public models. Only the request body's `model` value changes.

## 3. Overview metrics

The range selector (`1h`, `24h`, `7d`) changes traffic statistics, error history, and the timeline. Live capacity is always current and refreshes every ten seconds.

| Metric | Meaning |
| --- | --- |
| Routes ready | Enabled model routes that have at least one eligible API key. |
| API keys ready | Enabled, validated keys currently able to receive traffic. |
| Requests | Completed gateway requests in the selected time range. |
| Errors | Non-successful requests and their percentage of all requests. |
| Tokens | Recorded input plus output tokens. Usage can be conservative when a stream ends without upstream usage data. |
| P50 latency | The median: half of requests completed faster and half slower. |
| P95 latency | 95% of requests completed within this time; the slowest 5% took longer. It exposes tail slowdown better than an average. |

A very high P95 with a normal P50 usually means a smaller group of requests is waiting, streaming for a long time, retrying, or hitting a slow upstream.

The signal timeline plots request volume, P95 latency, and buckets containing errors. The attention queue prioritizes invalid keys, disabled routes, exhausted capacity, and low headroom.

## 4. Provider and model capacity

Providers are collapsed by default. Their header shows the total remaining capacity across all ready keys once. Expand a provider to see its models without repeating the entire key pool for every route.

The model list shows traffic, ready keys, the next key, and the model override state. **Shared only** means that model has no extra override and uses the provider key's shared policy. Selecting a model opens its inspector. The API-key path stays collapsed until you need individual key details, so long providers do not repeat the same pool on every row.

Use **Model routes → Check all models** to run a bounded live capability probe across every configured route that has a healthy key. Routes whose provider has no healthy key are shown as **waiting for keys**, not as model failures. The sweep rail shows progress and retains the exact provider failure on each checked row. **Delete failed** lists the genuinely unavailable aliases and requires confirmation before removing only those routes. Provider model catalogs also include a master checkbox: without a search it selects every loaded model; with a search it selects or clears every visible result.

In **Model routes → Model override**, choose **All API keys** to enter a policy once and apply it to the whole provider key pool, or choose one API key for an exception. **Use shared only** removes the selected model override and restores the key's shared provider policy. Shared and model-specific policies are both enforced whenever an override exists.

Provider models and API keys are collapsed sections on the Providers page. Open only the section you need; edit or delete a selected model/key from its editor header. Request Logs follow the same pattern: select one compact row, then expand routing attempts or encrypted bodies only when investigating them.

Two keys at `40 RPM` give the provider an aggregate `80 RPM`. Removing a key lowers it to `40 RPM`. A model-specific `10 RPM` does not replace the shared `40 RPM`: that model must satisfy both limits and is effectively capped at the tighter available bucket.

## 5. Rate-limit glossary

Every field is optional. Blank means unlimited for that dimension.

| Limit | Meaning |
| --- | --- |
| RPS | Requests per second. |
| RPM | Requests per minute. |
| RPD | Requests per day. |
| TPS | Tokens per second. |
| TPM | Tokens per minute. |
| TPD | Tokens per day. |
| TPR | Tokens per request; an immediate ceiling for one request. |

Rotakey estimates input tokens and reserves the requested output ceiling before calling upstream. If no output ceiling is supplied, the model route default is injected. Non-streaming usage is reconciled after the response. A stream without final usage keeps the conservative reservation.

Capacity colors are operational states:

- Green: more than 20% remains.
- Amber: 20% or less remains.
- Red: no capacity remains.
- Unknown: Redis state could not be read; routing fails closed instead of bypassing limits.
- `∞`: no limit is configured for that dimension.

## 6. API-key selection and failure handling

Healthy keys are balanced with an ordered round-robin cursor. A primary key, when configured, is preferred while it has capacity; primary is not required. A key that cannot satisfy every applicable shared and model bucket is skipped.

If all keys are full and the earliest reset is within five seconds, Rotakey waits while respecting client cancellation. Otherwise it returns OpenAI-style `429` with `Retry-After`.

Before response headers/body begin, Rotakey can try every other eligible key for up to 60 seconds after a connection failure, timeout, `401/403`, upstream `429`/`529`, or selected `5xx`. It stops when one key succeeds, every eligible key has failed, the client cancels, or the 60-second failover window closes. Deterministic request-shape `400` errors are not replayed across keys. Rotakey never retries after response bytes or SSE events have started.

- Upstream `429`: key enters cooldown until `Retry-After`.
- Upstream `401/403`: key is quarantined and shown as invalid.
- Repeated `5xx`: the circuit breaker temporarily removes the key.
- Redis unavailable: gateway returns `503`; limits are never silently bypassed.

## 7. Compatibility and parameter adaptation

If a provider returns HTTP 400 and explicitly says that a safe optional field is unsupported or deprecated for the selected model, Rotakey removes that field and retries before returning an error. For example, a response saying `` `temperature` is deprecated for this model `` is repaired automatically. The successful response includes `X-Rotakey-Removed-Parameters`, the failed and repaired attempts remain visible in Request logs, and the model-specific result is cached for 24 hours. Rotakey never auto-removes core semantic fields such as `model`, `messages`, `tools`, or `response_format`.

For cross-protocol streaming, Rotakey normalizes standard Anthropic text/tool events plus common compatible variants such as text carried in `content_block_start`, indented SSE data lines, and `thinking_delta`. A stream that completes without any text or tool output emits `upstream_stream_empty` instead of silently returning an empty success. Streaming usage is reconciled when supplied; otherwise output is conservatively estimated from emitted bytes.

OpenAI clients use the `/v1` base URL. Anthropic SDKs and Claude Code use the host root because they append `/v1/messages` themselves. Both protocols use the same Rotakey gateway key.

| Client contract | Base URL | Authentication | Required version header |
| --- | --- | --- | --- |
| OpenAI | `https://ai.example.com/v1` | `Authorization: Bearer gw_...` | None |
| Anthropic | `https://ai.example.com` | `x-api-key: gw_...` or `Authorization: Bearer gw_...` | `anthropic-version: 2023-06-01` |

If both authentication headers are present they must contain the same key. A mismatch returns an Anthropic-shaped `401 authentication_error`. Anthropic errors use the native `{type:"error", error:{type,message}, request_id}` envelope and the public `request-id` header. Upstream request IDs are kept separately in Request Logs.

### Public endpoints

| Method and path | Purpose |
| --- | --- |
| `GET /v1/models` | OpenAI list shape by default; Anthropic list shape when Anthropic headers are present. |
| `GET /v1/models/{model_alias}` | Retrieve one public alias. Aliases containing `/` are supported. |
| `POST /v1/chat/completions` | OpenAI Chat Completions, including streaming and client tools. |
| `POST /v1/responses` | Native Responses or the documented core translation subset. |
| `POST /v1/messages` | Anthropic Messages, including named SSE events. |
| `POST /v1/messages/count_tokens` | Exact native Anthropic token count. It consumes request-limit capacity. |
| `POST` or `GET /v1/messages/batches` | Create or list Message Batches. |
| `GET` or `DELETE /v1/messages/batches/{id}` | Retrieve or remove a mapped Batch. |
| `POST /v1/messages/batches/{id}/cancel` | Cancel a Batch on its pinned upstream credential. |
| `GET /v1/messages/batches/{id}/results` | Stream JSONL results with public model aliases restored. |
| `POST` or `GET /v1/files` | Upload or list Files. Uploads stream through Rotakey. |
| `GET` or `DELETE /v1/files/{id}` | Retrieve metadata or delete a mapped File. |
| `GET /v1/files/{id}/content` | Stream File content from its pinned upstream credential. |

Every public `model` field is a Rotakey alias, never an upstream model ID.

### Compatibility matrix

| Public request → upstream provider | Behavior |
| --- | --- |
| Anthropic → Anthropic | Native pass-through. Content blocks, thinking, prompt caching, citations, Files, server/client tools, safe `anthropic-*` headers, and unknown SSE event types are preserved. |
| Anthropic → OpenAI | Text, images, system prompts, tool calls/results, tool choice, stop sequences, and streaming are translated. Claude Code metadata plus thinking/context/container/output controls and prompt-cache hints are accepted and omitted. Citations, Files, and server tools return `400 unsupported_feature`. |
| OpenAI Chat/Responses → OpenAI | Existing OpenAI-compatible behavior is unchanged. |
| OpenAI Chat/Responses → Anthropic | Core text, images, JSON function tools, tool results, stop controls, usage, and streaming are translated to Messages. Use `/v1/messages` for the full Anthropic feature surface. |

Provider protocol is selected during onboarding. Use the official quick setup button for OpenAI or Anthropic; Rotakey also normalizes a pasted official root, `/models`, or inference endpoint to the correct `/v1` base URL. Anthropic-compatible providers default to `x-api-key` and version `2023-06-01`. Redirects and proxy instructions remain blocked. For a `305`, `404`, `405`, or non-standard model catalog: verify the base URL, keep redirects disabled, use **Manual model ID**, and save. Rotakey makes a minimal `/messages` probe with the chosen model. A failed probe leaves the route unsaved and shows the provider error.

Rotakey always loads the authenticated `GET /models` catalog first. For official OpenAI and Anthropic endpoints that catalog validates the key directly, so an arbitrary non-chat catalog entry cannot prevent model import. Custom compatibility providers also receive a one-token `POST /chat/completions` or `POST /messages` protocol check to catch a missing `/v1`, an extra path segment, and protocol mismatches. Every selected model can then be verified individually with **Check all models**. Editing connection settings repeats the applicable check with an existing enabled key.

## Versions and updates

The sidebar and `GET /api/version` show the installed Rotakey version and build commit. Rotakey checks the public [GitHub Releases page](https://github.com/jisunahamed/rotakey/releases) at most once per hour. When a newer semantic version is published, every running installation shows an **Update available** notice with a link to its release notes. A failed release check never affects gateway traffic. Operators should review the notes, back up Rotakey PostgreSQL, and then follow the deployment upgrade steps.

### Model capability verification

Every new route receives a server-owned capability profile:

- **Catalog verified** means the configured API key returned the model in its authenticated catalog. This avoids one paid inference probe for every model in a large bulk selection.
- **Probe verified** means a manually entered or individually created model completed a bounded real core request before save. Invalid, inaccessible, retired, or malformed models are rejected.
- The profile distinguishes `native`, `translated`, `gateway_normalized`, `off`, `unknown`, and `native_unverified`. Unknown is intentional: provider catalogs rarely publish reliable per-model tool, JSON, thinking, and streaming capabilities, and Rotakey does not claim support without evidence.
- Anthropic-native routes expose Messages natively and Chat/Responses through translation. OpenAI Chat routes expose Chat natively and the supported Messages/Responses subsets through translation. Responses-only routes are not falsely exposed through Chat.
- The Model route inspector shows verification state and the effective Chat, Responses, Messages, streaming, tools, and thinking path. Use **Recheck model** to run the bounded core probe again for an existing route; a failed probe is stored and shown without deleting the route.

Model probing sends a one-token request and respects the configured provider timeout, capped at 120 seconds. On a connection, authorization, throttling, or upstream-server failure, Rotakey can try up to three healthy credentials before marking the route unavailable. It never stores or displays a probe secret.

Files are pinned to the default Anthropic resource provider and the selected credential. A Message containing a File reference is forced onto that same provider/key; conflicting references return `400 resource_affinity_conflict`. Message Batches must resolve every model alias to one native Anthropic provider and are pinned to one eligible credential. Mixed-provider Batches return `400`. A credential with live Files or Batches cannot be deleted (`409`) until those resources are removed.

Batch reservations count every item against shared request/token buckets and against each item's model-specific buckets in one atomic reservation. Anthropic usage reconciliation includes input, output, cache-create, and cache-read tokens. Streaming without final usage remains conservatively reserved. Upload/download content is streamed, and captured-body logging never stores File binary content. Default request ceilings are 32 MB for Messages/token count, 256 MB for Batches, and 500 MB for Files.

Providers do not all accept the same optional fields. A model route can strip known unsupported parameters and Rotakey records removed or replaced fields in the request attempt log. For example, one upstream may reject `thinking`, while another requires `max_completion_tokens` instead of `max_tokens`. Configure adaptation narrowly for the affected route. Responses or cross-protocol features that cannot be translated faithfully return `400 unsupported_feature` instead of being silently discarded.

For streaming cross-protocol calls, Rotakey also handles providers that ignore `stream:true` and return a regular Anthropic JSON Message: it synthesizes named Anthropic SSE events and then emits the requested public protocol. Empty, malformed, or non-Message HTTP `200` responses return/log `502 upstream_stream_invalid`; they are no longer recorded as successful blank responses.

## 8. Reading statuses and logs

| State | Operator action |
| --- | --- |
| Healthy | No action required. |
| Partial | Some keys are unavailable; inspect the credential path. |
| Exhausted | Wait for reset, add capacity, or adjust a verified limit. |
| Cooldown | Inspect the upstream `429` and `Retry-After`. |
| Quarantined | Replace/fix the key, then re-check it. |
| Disabled | Enable only after configuration and connection tests pass. |
| Unknown | Check Redis readiness and app logs. |

Request logs contain request ID, alias, provider, credential label, each attempt, status, latency, usage, parameter adaptations, and error code. Failed requests also include an automatic diagnosis: every API key skipped by routing, shared/model scope, blocking `RPS/RPM/RPD/TPS/TPM/TPD/TPR` bucket, required and remaining capacity, cooldown/reset time, and the redacted upstream error message when available. Use **All errors** to filter every non-success status, or search by model alias/request ID.

Reset labels count down once per second in the inspector. Capacity values use compact notation such as `1K`, `100K`, and `1M`; hover the value when an exact integer is needed. Captured bodies are off by default; when enabled per route they are encrypted, capped, marked if truncated, and retained for 30 days. Metadata is retained for 90 days.

## 9. Common errors

- `400 unsupported_parameter` or `unrecognized_request_argument`: the chosen upstream model rejected a field. Add a route-specific strip or replacement rule after confirming the provider contract.
- `400 invalid_request_error` mentioning `anthropic-version`: send `anthropic-version: 2023-06-01` on Anthropic endpoints.
- `400 resource_affinity_conflict`: a File or Batch does not belong to the model route's provider/credential path.
- `401/403`: upstream key is invalid, expired, or lacks model permission. Rotakey quarantines it.
- `409` while deleting a key: a live File or Batch is pinned to it. Delete the mapped resources first.
- `404 DeploymentNotFound`: the upstream model/deployment ID does not exist at that provider or region. Correct the upstream ID; the public alias may stay unchanged.
- `429 rate_limit_exceeded`: all eligible keys are full or upstream reported a limit that is stricter than configured. Inspect the limiting bucket and `Retry-After`.
- `503`: Redis/database/readiness dependency is unavailable, or no safe routing decision can be made.
- `502 upstream_stream_invalid`: the provider returned HTTP `200` for a streaming request but supplied neither valid Anthropic SSE nor a valid JSON Message. Test the provider/model and inspect the upstream request ID.
- High latency: compare P50/P95, inspect attempts for waits/retries, and test the provider directly from the console.

Always include the request/trace ID when investigating an error. Do not paste upstream secrets into issues or logs.

## 10. Security and maintenance

- Use HTTPS for a domain deployment. IP access can use HTTP only on a trusted path.
- Rotate the gateway key from Access Key; the old key is revoked immediately.
- Upstream keys are encrypted at rest and displayed only with a masked suffix.
- Do not expose PostgreSQL or Redis publicly.
- Back up both PostgreSQL and the environment secrets needed to decrypt stored credentials.
- Review captured-body settings before sending personal or confidential data.
- Keep the VPS, Docker images, and Rotakey release current after testing backups.

See [Deployment](DEPLOYMENT.md) for Compose, domain, backup/restore, and GitHub CI/CD instructions. See [API examples](../README.md#call-any-configured-model) for client calls and [Claude Code](CLAUDE-CODE.md) for gateway authentication, every-model selection, role mapping, and troubleshooting.

## 11. Fast troubleshooting checklist

1. Confirm `/health/live` and `/health/ready`.
2. Confirm the public alias appears in `/v1/models` using the gateway key.
3. Open Overview, expand the provider, and inspect the route's next key and limiting bucket.
4. Re-check warnings/invalid keys and test the provider.
5. Request logs refresh every two seconds. Use the `Running` filter to watch in-flight requests; a running row switches to its final status and diagnosis automatically.
6. Search Request logs with the request ID or alias; inspect every routing decision, attempt and parameter adaptation.
7. Verify shared and model-specific limits against the upstream account's real quota.
8. For dependency errors, inspect only Rotakey app/PostgreSQL/Redis status before restarting anything.
9. Preserve logs and take a PostgreSQL backup before changing production configuration.

## 12. Google Gemini

Gemini's native REST root (`https://generativelanguage.googleapis.com/v1beta`) is not an OpenAI-compatible base URL. Configure the provider as **OpenAI-compatible** and use `https://generativelanguage.googleapis.com/v1beta/openai/` with `Authorization: Bearer`. Rotakey recognizes the native root and normalizes it to the compatibility endpoint when saving. A successful authenticated OpenAI-format model catalog validates the provider; use the route probe for a capability check of an individual Gemini model.

Gemini model catalogs can expose IDs like `models/gemini-2.5-flash`. You may keep a public alias such as `google/models/gemini-2.5-flash`; Rotakey still sends the upstream request with the Gemini OpenAI-compatible model ID that Google expects.
