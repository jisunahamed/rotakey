# Provider running limit and connected request queue

In Providers > Edit > Maximum running requests, set the total number of upstream inference calls allowed across a provider's models and API keys. `0` means unlimited and is the default for existing providers. The provider inspector shows Running / Limit / Queued. Requests shows queued and running separately, with queue reason, position and elapsed wait.

A request first chooses its primary provider using the configured rotation, then joins that provider's FIFO queue. Redis atomically checks queue turn, provider slots, credential cooldown and all key quotas before admission. Waiting consumes neither a running slot nor key quota. A busy or rate-limited primary keeps its request queued. Actual provider failure or unavailability can use the configured alternative for the same alias.

Queue wait has no gateway deadline: client disconnect, cancellation or a caller-supplied deadline removes it. Execution timeouts start after admission; streaming retains the existing execution policy. Upstream 429/Retry-After re-enters admission without consuming the failure retry budget. Impossible per-request token requirements fail explicitly instead of waiting forever. Files, batches and token counting retain their separate resource wait ceiling.

Slots remain held until the upstream response body has been consumed or closed. Ownership leases renew every 10 seconds and expire after 60 seconds after a worker crash. Lease loss cancels the upstream call. Coordination errors fail closed. Waiters poll every 250ms alongside notifications and reload provider/key configuration every second. Lowering a limit never cancels calls already running.

Only queue IDs and ownership metadata live in Redis. Bodies stay in connected HTTP handlers and are never replayed after restart. Deployment allows 60 seconds of HTTP drain and Docker allows 75 seconds before forced shutdown. Responses already started are never replayed or failed over. Successful provider replies retain their forwarding behavior, including missing token usage.

The max_concurrent_requests field is included in the provider API and configuration export/import. Omitting it from an update or old import preserves the saved value.

## Verification

CI uses disposable PostgreSQL and Redis services. Queue integration tests cover five calls at limit two, separate workers/models/keys, cancellation and cleanup, FIFO quota reservation, stale leases, limit changes, impossible token sizes, streaming ownership, upstream 429 without backup use, auxiliary inference admission and upstream execution timeout. Run go test -race ./... with TEST_DATABASE_URL and TEST_REDIS_URL pointing only at disposable test services.
