# Five-hour request error audit — 30 October 2026

The audited window is **07:35:05–12:35:05 Bangladesh time** (01:35:05–06:35:05 UTC). Rotakey rows are selected by their recorded completion timestamp; Torve API rows by request receipt timestamp. Counts from these two databases cannot be matched one-to-one or divided to calculate a provider success rate. No customer prompts, API-key secrets or customer identities were queried.

## Findings

Rotakey recorded **1,497 requests**: 945 without a recorded error and **552 with an error or cancellation**.

| Result | Requests | Evidence and ownership |
| --- | ---: | --- |
| Cancelled/disconnected | 432 | 389 HTTP 499; 43 after HTTP 200 headers. Seven were cancelled in the queue without an upstream attempt. The remaining 425 have at least one recorded upstream attempt. |
| Provider error inside HTTP 200 stream | 33 | The upstream SSE observer saw an error envelope before protocol translation. HTTP 200 means headers were accepted, not that generation completed successfully. Old rows retain only a generic message, so the exact historical provider reason cannot be recovered. |
| Conversation too long | 28 | Provider HTTP 400 explicitly requested context compression or a new session. |
| Provider unavailable | 21 | HTTP 502, with actual upstream attempts and the message “Upstream service temporarily unavailable”. |
| Tool/parser rejection | 20 | Provider HTTP 400: missing tool-call parser, disabled automatic tool choice, or provider-generated invalid tool JSON. |
| No usable provider/key | 17 | Router selection error. Fourteen have no upstream attempt; three follow an attempted failure. Routing decisions show both primary credentials unavailable, but do not retain the precise historical cooldown cause. |
| Provider prompt-template failure | 1 | Provider HTTP 500 repeated identically across **10 credentials**. The provider failure was real; nine identical same-route retries were avoidable. |

There were **1,551 recorded upstream attempts** across 1,476 requests with an attempt, including 75 additional attempts on 37 retried requests. Some retries are legitimate failover. These are metadata counts, not a statement of provider charges. Earlier capacity retries were not retained in attempt arrays, so those totals are not a complete billing ledger.

### Cancellation sources

- **150 cancellations ended around 20 seconds**, all with fewer than 200 estimated input tokens and about five repetitions per affected model across five hours. This closely matches the automatic hourly probe's 20-second budget and small “Reply hello” payload. These should not all be presented as customer generation failures. The historical router did not store the caller request ID, so exact attribution of each row is unavailable.
- **76 cancellations ended around 120 seconds**, with median estimated input around 107,834 tokens. Torve's live policy enforced a 120-second generation-header floor; its own database recorded **73 pre-body upstream timeouts** with about 123 seconds total latency including local processing. The timing and model patterns support a website timeout cause for this group; existing IDs do not prove a one-to-one join.
- Torve also recorded **28 stream timeouts after body bytes started**. Nine ended around 30 seconds after the first body byte. The endpoint's actual persisted inter-chunk budget was 30 seconds, despite a 45-second environment default. The persisted endpoint value wins.
- Torve recorded **182 explicit client disconnects** in the same receipt-time window: 169 before body bytes and 13 after body bytes. A client closing a connection must still cancel upstream work. Continuing or replaying that work would create more avoidable spend.

### Internal problems confirmed in code

1. A cancelled caller was labelled as a retryable provider `connection_error` and passed into credential-failure handling. Cancelled contexts normally prevent the subsequent Redis/database state writes, so this audit does **not** prove those cancellations caused the 17 unavailable-key responses. The classification and repair path were nevertheless wrong: 382 repair incidents correspond to cancelled requests.
2. Streaming protocol detection held initial provider keepalive comments until the first data event and delayed public response headers. A provider could be active while the next gateway's header or idle timer expired.
3. A deterministic model/template HTTP 500 could rotate across every credential on the same route even though changing the key cannot repair a model's template.
4. NVIDIA's `ResourceExhausted: Worker local total request limit reached (…/32)` HTTP 503 was treated as a generic failure. Rotating more keys immediately against the same worker increased pressure. Several requests tried four to ten keys.
5. Post-header provider errors were reduced to a generic stream-error label. Original bounded provider reasons and a caller/router ID mapping were missing.

## Corrections

- Caller cancellation is recorded as `client_cancelled` at the attempt level, with no credential strike, automatic replay or new repair diagnosis.
- Provider SSE headers are flushed when the upstream accepts the stream; safe keepalive comments are forwarded while protocol detection waits for actual data. Forwarded comments are not duplicated or discarded, including with body capture enabled.
- Known template/parser errors skip sibling keys on the same model route. Other providers serving the same alias remain eligible; request content and tools are preserved.
- Proven worker-capacity HTTP 503 responses create a shared provider backoff and re-enter its FIFO. A key rotation cannot bypass that backoff. Capacity retries keep the failure retry budget intact and retain up to 128 capacity attempt records per connected request.
- New attempt metadata includes caller request ID, streaming flag, completion and failure phase. Provider stream-error reasons are bounded and credential-redacted in logs; successful/raw response forwarding is preserved.
- The Torve `rotakey` endpoint's header wait is increased to 280 seconds and idle wait to 120 seconds, within its existing 280-second total budget. This does not override a caller's own shorter deadline, and does not remove execution timeouts or queue cancellation.

## Limits of the conclusion

Zero recorded output before response headers is not evidence that the provider used zero tokens. Conversely, recorded output does not prove the client received the entire answer. Existing body capture was off for the sampled cancellations; historical rows cannot be safely relabelled as success. Provider invoices/usage records were not available in this audit, so the actual monetary loss is unknown. Existing request, quota and financial history is retained.

Invalid credentials, plan/model access denials, exhausted user quotas, oversized contexts and genuine provider failures continue to return appropriate errors. The fix prevents avoidable cancellation/retry behavior; it cannot promise every provider request succeeds.

Verification includes unit and disposable-database integration tests for early streaming headers/keepalives, byte preservation, cancellation without repair/key health changes, a single template-failure attempt, shared worker-capacity waiting with recorded attempts, and redacted provider error reasons. Existing provider queue, cancellation, fallback and successful-response tests remain required. Deployment uses source/database/image backups and validates embedded migration checksums before replacing the app.

## Follow-up audit: 08:44–13:44 Bangladesh time

A fresh completion-time snapshot at **07:44:12 UTC / 13:44:12 Bangladesh time** contains **1,527 Rotakey requests**, of which **430** have an error/cancellation: 320 cancellations, 48 HTTP 400 rejections, 37 provider stream errors carried inside HTTP 200, 11 HTTP 500 failures, nine HTTP 502 failures and five unavailable-provider responses. Eight cancellations have no upstream attempt. Windows overlap the initial audit; these totals must not be added together.

The first fixes were confirmed live at commit `b4367e6`, with the main instance starting at 07:11:54 UTC. Excluding calls that began before that deployment, the snapshot contains 276 requests, including 22 cancellations. Twelve pre-header cancellations are explicitly `client_cancelled`, with no replay. Two cancelled requests have repair incidents from an earlier genuine provider 502 in the same request; that does not mean cancellation bought a new repair.

Caller IDs now allow an exact join with Torve's request records. Of the 22 post-deployment cancellations, 18 matched: **15 client disconnects, two Torve timeouts and one Torve stream failure**. Four remain unmatched, including direct callers without the website ID. The timeout cases include a 120-second stream-idle expiration and a 280-second total execution expiration. One total-timeout request delivered **1,539,044 bytes** to Torve while Rotakey recorded zero output tokens. Missing usage is not evidence of a missing response, and the captured bytes do not prove the entire answer reached the end user.

The endpoint settings are confirmed as 280 seconds total, 280 seconds for headers and 120 seconds idle. Caller deadlines remain authoritative. Pre-header cancellation is not enough to establish whether the provider already consumed tokens; actual charges still require provider billing records.

### Additional confirmed waste and corrections

- Ten post-deployment NVIDIA requests received the same generic HTTP 500 across ten credentials each: 100 attempts. The earlier template-specific protection could not recognize this generic error. The new guard stops sibling-key rotation after two identical 5xx responses for the same model route, endpoint and encoded payload. That would remove 80 attempts from this observed pattern; it is not a measured currency saving. A successful second attempt remains valid, repaired payloads have separate budgets, and other providers remain eligible.
- Four observed HTTP 200 streams contain the provider's own “Concurrency limit exceeded for user” error. Those streams were forwarded once; they are not gateway-created 502s. This exact capacity signal now sets shared provider backpressure for later requests, including when reported via HTTP 429. Started streams are never replayed. Capacity-only incidents do not buy an AI diagnosis.
- New attempt metadata records `response_bytes` accepted by the downstream writer even without usage or body capture. HTTP 4xx/5xx rejection phases are explicitly `upstream_rejection`, rather than being labelled as an active stream merely because the client requested streaming.

Follow-up verification covers ten-key identical 500s, a successful second attempt, payload/route budget isolation, shared user-concurrency waiting across keys, byte-for-byte forwarding of capacity SSE without replay, delivered-byte metadata and diagnosis suppression. The entire application suite and existing queue/fallback tests remain part of verification.

### Stream-contract and release packaging follow-up

An exact ID join also found three completed Rotakey OpenAI streams for `claude-opus-5` which Torve marked `upstream_unavailable` after receiving 3,420, 3,979 and 13,611 bytes. All three used an Anthropic upstream and had provider output usage. Their bodies were not captured, so the precise historical first/last event is unknown. A reproducible gateway defect was identified: an Anthropic `ping` as the first data event bypassed protocol translation, leaking an Anthropic stream to an OpenAI caller. Anthropic ping/delta/stop events are now recognized before choosing the translator. Regression tests verify a valid OpenAI terminal marker and preserved answer, and byte preservation for an Anthropic caller.

The manual `3cbbe16` build accidentally embedded the tracked development UI placeholder. API health checks passed while the admin console was unavailable. The replacement release uses `scripts/build-release.ps1` to compile production frontend assets before building the gateway from an isolated committed checkout. Deployment verification must fetch `/admin/` and its JavaScript/CSS assets in addition to API readiness. The earlier API fixes remain included.
