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
3. Add a provider and enter its OpenAI-compatible base URL.
4. Add API keys one at a time or in bulk. Primary is optional.
5. Let Rotakey validate the keys and discover the provider's model list.
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

The model list shows traffic, ready keys, the next key, and the model override state. **Shared only** means that model has no extra override and uses the provider key's shared policy. Selecting a model opens the inspector with its effective limiting bucket, reset time, and credential path. Use **Manage model limits** to edit the per-key override.

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

Rotakey can retry once on another key for a connection failure, timeout before response headers, upstream `429`, and selected `5xx` errors. It never retries after response bytes or SSE events have started.

- Upstream `429`: key enters cooldown until `Retry-After`.
- Upstream `401/403`: key is quarantined and shown as invalid.
- Repeated `5xx`: the circuit breaker temporarily removes the key.
- Redis unavailable: gateway returns `503`; limits are never silently bypassed.

## 7. Compatibility and parameter adaptation

Rotakey exposes `GET /v1/models`, `POST /v1/chat/completions`, and `POST /v1/responses`. Chat streaming, tools/function calls, JSON mode, and compatible multimodal bodies pass through.

Providers do not all accept the same optional fields. A model route can strip known unsupported parameters and Rotakey records removed or replaced fields in the request attempt log. For example, one upstream may reject `thinking`, while another requires `max_completion_tokens` instead of `max_tokens`. Configure adaptation narrowly for the affected route; do not silently remove features globally.

Responses API features that cannot be translated faithfully return `400 unsupported_feature` instead of being silently discarded.

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

Request logs contain request ID, alias, provider, credential label, each attempt, status, latency, usage, parameter adaptations, and error code. Search by model alias or request ID. Captured bodies are off by default; when enabled per route they are encrypted, capped, marked if truncated, and retained for 30 days. Metadata is retained for 90 days.

## 9. Common errors

- `400 unsupported_parameter` or `unrecognized_request_argument`: the chosen upstream model rejected a field. Add a route-specific strip or replacement rule after confirming the provider contract.
- `401/403`: upstream key is invalid, expired, or lacks model permission. Rotakey quarantines it.
- `404 DeploymentNotFound`: the upstream model/deployment ID does not exist at that provider or region. Correct the upstream ID; the public alias may stay unchanged.
- `429 rate_limit_exceeded`: all eligible keys are full or upstream reported a limit that is stricter than configured. Inspect the limiting bucket and `Retry-After`.
- `503`: Redis/database/readiness dependency is unavailable, or no safe routing decision can be made.
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

See [Deployment](DEPLOYMENT.md) for Compose, domain, backup/restore, and GitHub CI/CD instructions. See [API examples](../README.md#call-any-configured-model) for client calls.

## 11. Fast troubleshooting checklist

1. Confirm `/health/live` and `/health/ready`.
2. Confirm the public alias appears in `/v1/models` using the gateway key.
3. Open Overview, expand the provider, and inspect the route's next key and limiting bucket.
4. Re-check warnings/invalid keys and test the provider.
5. Search Request logs with the request ID or alias; inspect every attempt and parameter adaptation.
6. Verify shared and model-specific limits against the upstream account's real quota.
7. For dependency errors, inspect only Rotakey app/PostgreSQL/Redis status before restarting anything.
8. Preserve logs and take a PostgreSQL backup before changing production configuration.
