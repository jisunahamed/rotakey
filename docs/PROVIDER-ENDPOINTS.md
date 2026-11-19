# Provider-wide text API selection

In **Providers → Edit**, select **API protocol** and then **Upstream text API**.

| Setting | Upstream text inference |
| --- | --- |
| OpenAI-compatible + Use model settings | Existing per-model endpoint flags and automatic compatibility handling |
| OpenAI-compatible + Chat Completions only | All text models use `/chat/completions` |
| OpenAI-compatible + Responses API only | All text models use `/responses` |
| Anthropic-compatible | Native `/messages` |

The base URL remains the provider's compatibility root, for example
`https://provider.example/v1`. The gateway appends the selected endpoint.

The provider policy applies to normal inference, credential protocol checks and
model capability checks. Public Chat, Responses and Anthropic callers are
translated to the selected upstream API. Existing translation code preserves
the caller's supported tools and reasoning controls. Audio and embedding models
retain their dedicated endpoints.

Explicit Chat/Responses selections override old model flags and cached endpoint
learning. A fixed Responses policy will not silently retry at Chat after a 404;
the administrator must correct the base URL or selection. The automatic repair
agent cannot switch a fixed provider policy. Alternative providers keep their
own policy.

The new database field is `providers.inference_endpoint`:
`auto`, `chat` or `responses`. Existing providers default to `auto`. Missing
fields in older update/import payloads preserve a saved policy. Configuration
export/import includes the field. Saving the endpoint choice alone does not
require an upstream probe; use the model check to verify access afterward.

## The reported Chinese rejection

The screenshot says the upstream only supports programming clients such as
Codex, Claude Code and OpenCode, and rejects SDK calls and other third-party
chat clients. It does **not** explicitly say “use the Responses API”.

Two separate conditions must be checked:

1. **Endpoint contract:** if the upstream only implements Responses, choose
   Responses API only and rerun the model check. Previously the OpenAI-format
   provider check and chat-enabled model check started at Chat Completions.
2. **Client/account policy:** a correct Responses call can still be rejected by
   the provider's access policy, model entitlement or required integration.
   The provider must document or enable the permitted gateway integration.

The screenshot does not expose the provider URL or request headers, so it cannot
establish which client-policy signal triggered that upstream rejection. Endpoint
selection fixes the routing mismatch; it does not guarantee that a separate
provider client restriction will disappear.

## Verification

Regression coverage includes Chat, Responses and Anthropic input translated
under both fixed policies; provider and model probes using Responses; correct
recognition of a Responses response envelope; unchanged audio/embedding routing;
saved policy persistence with older API payloads; export; and no automatic
Chat fallback after a Responses-only provider returns 404.
