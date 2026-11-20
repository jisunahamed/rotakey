# Rotakey: model catalog, capabilities and usage

Give this document to the agent integrating your website.

## Authentication and user isolation

Use your Rotakey base URL (for example `https://api.inmetech.cloud`) and
`Authorization: Bearer YOUR_ROTAKEY_KEY`. Keep that key on your website's backend;
never put it in browser JavaScript. Rotakey currently uses one shared gateway key,
not per-website-user API keys. Your backend must enforce user authentication,
limits, budgets and request ownership. Never let users query arbitrary request IDs.

## Build the model catalog

1. `GET /v1/models` returns enabled model aliases and a `capabilities_url` per model.
2. `GET /v1/models/{model_id}/capabilities` returns detailed capability metadata.
   URL-encode the alias, including slashes in names such as `openai/gpt-oss-20b`.
3. Refresh capabilities periodically and after configuration changes. Do not
   hardcode context windows or assume every model supports every feature.

```js
const headers = { Authorization: `Bearer ${process.env.ROTAKEY_KEY}` };
const base = process.env.ROTAKEY_URL.replace(/\/$/, "");
const catalog = await fetch(`${base}/v1/models`, { headers }).then(r => r.json());
const model = catalog.data[0].id;
const capabilities = await fetch(
  `${base}/v1/models/${encodeURIComponent(model)}/capabilities`, { headers }
).then(r => r.json());
```

The response includes `features` (vision_input, tool_calling, parallel_tool_calls,
thinking, streaming, structured_output, json_output, web_search, audio_input,
audio_output), `limits.context_window`, `limits.max_output_tokens`,
`reasoning_effort_levels`, public `endpoints`, and per-provider `routes` with
source/verification/check time and the stored capability profile.

Feature values are `supported`, `unsupported` or `unknown`. Unknown is not false.
Null limits mean unknown. The configured default reply cap is listed separately
as `default_max_output_tokens` per route; it is not the model's maximum.
Top-level values are conservative across eligible primary/fallback routes:
minimum known limits, common effort levels and endpoints, and capability
intersection. Inspect routes to explain differences to administrators. Route
eligibility can change; these are configuration declarations, not availability
or entitlement guarantees. Catalog-only support differs from a successful probe.

Enable image upload, tool UI, thinking/effort controls or JSON controls only
when the corresponding feature is confirmed. Offer only returned effort levels.
Other stored capability details remain available in each route's profile.

## Make requests normally

Keep using `/v1/chat/completions`, `/v1/responses` or `/v1/messages` and the public
model alias. Audio models use their dedicated `/v1/audio/*` APIs. Capabilities
and usage endpoints do not change successful model responses or cause retries.

Read the `X-Request-ID` header as soon as response headers arrive and save it
alongside your website's user ID and conversation ID. This header is set by
Rotakey, not supplied by the caller. With streaming, retain it before consuming
the SSE body. Cancel the upstream fetch when your user cancels.

## Usage and request timing

Prefer the response's native `usage` for provider-reported accounting. For Chat
Completions streaming request `stream_options: { include_usage: true }` where
supported, then read the final usage chunk. Anthropic streams carry usage in
message events; Responses streams carry it in response events. Do not stop reading
on the first text delta. Provider support and usage detail vary.

For gateway-recorded totals and queue/processing time:

```js
const requestId = response.headers.get("x-request-id");
const usageResponse = await fetch(
  `${base}/v1/requests/${encodeURIComponent(requestId)}/usage`, { headers }
);
const record = await usageResponse.json();
```

- HTTP 200: a stored record, with `final: true`, model, endpoint, HTTP status,
  state, error_code, usage.input_tokens/output_tokens/total_tokens and timing
  (total_ms, queue_wait_ms, processing_ms). No prompt, answer or provider key is exposed.
- HTTP 202: still running/queued; `final: false`. Respect `Retry-After: 1` and
  poll with a bounded retry policy in your own application.
- HTTP 404: not recorded yet, unknown, or removed by retention/verified backup
  cleanup. Immediately after completion a short logging delay is possible.
- HTTP 401: invalid gateway key. HTTP 503: storage/settings temporarily unavailable.

`usage.source = gateway_recorded` and `accuracy = not_verified`: existing request
logs can include an input estimate. A recorded zero output does not prove the
provider generated or billed zero tokens. Do not represent these totals as an
exact provider invoice. Native response usage takes priority when present;
missing native usage must remain unknown/estimated in your website accounting.
Store your own durable per-user ledger: the gateway usage lookup is subject to
existing log retention and backup cleanup. A usage lookup/storage failure must
never replay an already successful paid model request.

## Quick curl check

```sh
curl "$ROTAKEY_URL/v1/models/your-model-alias/capabilities" \
  -H "Authorization: Bearer $ROTAKEY_KEY"
curl "$ROTAKEY_URL/v1/requests/req_example/usage" \
  -H "Authorization: Bearer $ROTAKEY_KEY"
```

These two endpoints are shared public gateway APIs protected by the gateway key,
not admin-cookie APIs. Keep that distinction when integrating a multi-user site.
