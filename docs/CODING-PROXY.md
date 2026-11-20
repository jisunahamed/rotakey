# Per-provider coding proxy

In **Providers → Edit**, enable **Use coding proxy** for an OpenAI-compatible
provider. Disable it to restore its normal direct connection and saved endpoint
policy. The default is off. When on, text inference uses Responses API.

The same proxy path is used for normal client calls, the Models page's **Check
it**, credential/protocol checks and model discovery. Proxy failures never
silently fall back to a direct upstream call. Other providers retain their own
configuration. The coding proxy does not support audio, embeddings or native
Anthropic resources; unsupported endpoints fail explicitly rather than going
direct while the toggle is on.

## VPS setup

The integration uses self-hosted
[CLIProxyAPI v8.0.79](https://github.com/router-for-me/CLIProxyAPI/releases/tag/v8.0.79),
pinned to Docker digest
`sha256:ce02dd2ba9186024762d8b6525c068c5a5fab78435b8d82d9c03f7ce74feeb5b`.

Use `deploy/compose.coding-proxy.yml` with the regular Compose files. Set a
private `CODING_PROXY_KEY` and an absolute `CODING_PROXY_CONFIG_DIR` accessible
only to the app user (UID 100, GID 101). Initialize `config.yaml` in that directory
with the proxy listener, private access key and an empty Codex API-key list.
Mount the directory rather than one file because Rotakey replaces the
configuration atomically. Never expose port 8317 publicly.

The app receives:

- `CODING_PROXY_URL=http://coding-proxy:8317`
- `CODING_PROXY_KEY=<private internal key>`
- `CODING_PROXY_CONFIG_PATH=/coding-proxy/config.yaml`

Rotakey prepares the current provider/key/model configuration before dispatch
and waits for the proxy to load it. The file is written with mode 0600 and
contains upstream secrets needed by the local proxy. It must not be committed,
included in public backups or served over HTTP. Turning the toggle off stops
Rotakey routing through the proxy; configuration entries are refreshed on the
next proxied call. The internal proxy has no publicly reachable listener.

Every selected Rotakey key receives a distinct hashed routing prefix and a proxy
group containing exactly that key. The proxy cannot independently rotate to
another key after Rotakey reserves a slot or quota. Its retries are disabled;
Rotakey owns retries, queues, cancellations and limits. Hosted image-generation
tool injection is set to passthrough, preserving caller tools.

CLIProxyAPI's Codex executor implements the coding-client request contract. It
is not a general network/IP proxy. A provider's model entitlement and billing
checks still apply. Explicit caller instructions are passed through; upstream
coding defaults may apply when the caller supplies none, which can affect input
usage. Model discovery through this proxy lists configured models, not proof
that the upstream account can run each model. For a new unsaved provider, add a
model manually before expecting a configured proxy catalog.

## Live test results, 20 November 2026

Provider: `xingchiapi`, `https://api.xingchiapi.com/v1`.

| Model | Key | Proxy result |
| --- | --- | --- |
| `gpt-6-astra` | 1 | 403: insufficient dedicated balance |
| `gpt-6-astra` | Key 2 | 404: model not supported in this group |
| `gpt-6.1-sol` | 1 | 403: insufficient dedicated balance |
| `gpt-6.1-sol` | Key 2 | 200: completed response with Python function output |
| `gpt-6.1-sol` streaming | Key 2 | 200: output deltas and response.completed |

Direct calls returned `client_not_allowed`. These results do not mean the proxy
can provide access to unentitled models or fund a group balance.
