# AI repair agent

The agent administers request compatibility, route settings and published connection operations. It cannot edit source code, run shell commands, deploy code, create credentials or answer on behalf of the caller's selected model.

## Enable and roll out

1. Apply migrations by starting the updated gateway; migration `013_repair_agent.sql` creates the policy and incident tables. Existing installations start with the agent disabled.
2. In Settings → AI repair agent, select an existing enabled model route. This selects both the diagnosis model and its provider connection; existing eligible credentials are selected by the limiter.
3. Enable Observe and select rollout routes. An empty rollout selection covers every enabled route. Observe records proposals without executing agent actions; existing deterministic routing continues.
4. Review Requests → Repair history, then choose Auto repair, Full administration or Custom permissions. Saving authorizes the selected tools without further per-request prompts.
5. Disable the agent to stop new agent actions. Existing deterministic compatibility handling remains available.

Default limits: two synchronous diagnosis rounds, three repair tests, at most twelve synchronous upstream calls including diagnosis and credential checks, ten seconds of total synchronous diagnosis, 2,048 output tokens per diagnosis and 100,000 diagnosis tokens per UTC day. The caller's existing overall deadline remains authoritative. Concurrent diagnoses share a short-lived proposal cache and atomic daily budget reservation. Uncertain model usage keeps its conservative reservation charged.

## Published operations

| Tool | Behavior |
| --- | --- |
| `set_parameter` | Adjust an existing token-limit field or an allowlisted numeric generation option. Token caps are bounded to 1–32,768. |
| `remove_parameter` | Remove temperature, top_p, frequency/presence penalty or seed. |
| `switch_endpoint` | Rebuild from the original request for Chat or Responses; reject a translation that drops additional fields. |
| `set_timeout` | Test a request-local provider timeout; save only after a valid non-streaming response and configuration version comparison. |
| `select_credential` | Select an existing enabled, eligible credential belonging to the failing route's provider; capacity is checked before dispatch. |
| `validate_credential` | Validate the existing credential through the provider before updating health. |
| `reset_cooldown` | Require credential verification; never override an active rate-limit rejection. |
| `refresh_connection` | Close idle connections and rebuild the provider HTTP client. |
| `set_route_enabled` | Schedule an outage route's state change after the request, with an optimistic version check; cancel if that route recovered. |

Auto repair permits only the first three operations. Full administration permits all published operations. Custom explicitly selects tools. The current runtime has no host lifecycle adapter, so process reload/restart is reported as unsupported; no shell-based substitute is provided. Settings already take effect without a process restart.

## Evidence and verification

Diagnosis receives request shape and numeric generation options, bounded error text, provider/model identifiers and capability metadata. It does not receive conversation content, images, tool descriptions, raw credentials, or provider headers. Provider errors are untrusted evidence. Only structured, allowlisted proposals pass to the executor.

The original request is deep-copied before adaptation. New payload rules are scoped by route, wire endpoint and configuration version. They are saved only after a valid non-streaming answer or tool call, expire after 24 hours, and match the original parameter value so a fix for `max_tokens: 1` cannot lower a later caller's larger limit. Failed learned repairs are invalidated. Stream output is never restarted after it has been committed, and streaming success does not establish a reusable payload rule.

Unknown upstream 400/422 responses remain uncommitted while other configured routes for the same model can be tried. Keys on an incompatible route are skipped together. No cross-model fallback is introduced. Provider-wise routing tries its selected route first, then enabled routes with an exactly matching upstream model identifier. Model-wise routing pools providers publishing the same alias. Credential-affine requests remain pinned to their original credential.

After the caller finishes, a bounded background review can diagnose a completed failure or a fast deterministic repair. Background reviews only propose; a future request must still verify the action. There are at most four background reviewers per server. This is in-process best-effort review, not a durable job queue.

## APIs and operations

- `GET /api/admin/repair/policy`: policy, published tools and service lifecycle support.
- `PUT /api/admin/repair/policy`: replace policy with its current `version`; stale updates return 409.
- `GET /api/admin/repair/incidents?request_id=...`: latest 100 incidents, optionally for a caller request.
- `GET /api/admin/repair/metrics`: 24-hour request success and repair metrics plus UTC daily budget consumption.

All endpoints use existing admin authentication and CSRF checks. Incidents retain diagnosis, before/after values, verification and rollback status, diagnosis usage and test usage. They expire under metadata retention. `X-Rotakey-Recovery: applied` indicates an applied payload repair without changing the response protocol.

Run `go test ./...` and `npm --prefix web run check`. Redis integration tests require `TEST_REDIS_URL` pointing to a disposable test instance. Use Observe before enabling write permissions on production traffic. No production configuration is enabled automatically by installing this change.
