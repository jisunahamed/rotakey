# Use Codex with Rotakey

Rotakey v0.2.4 exposes the Responses API used by Codex and includes the
`rotakey-codex` companion. The companion creates a separate `rotakey` Codex
profile, protects the gateway key with the operating-system credential store,
and keeps a local model catalog synchronized with verified Rotakey routes.

This integration follows the official Codex custom-provider contract:
`model_providers.<id>.base_url`, `wire_api = "responses"`, command-backed
authentication, profiles, and `model_catalog_json`.

## Before installing

1. Deploy Rotakey behind HTTPS.
2. Add an enabled provider, API key, and model route in the Admin UI.
3. Run the model capability probe. Only routes marked `catalog_verified` or
   `probe_verified` appear in the public model APIs, Codex manifest, and picker.
4. Save the Rotakey gateway key shown during initial setup or key rotation.

The gateway must return `ready` from `/health/ready`. A bare remote HTTP URL is
rejected by the companion; HTTP is allowed only for loopback development.

## Install the companion

Download the binary for your operating system from the GitHub release. Verify
it against `SHA256SUMS` before running it.

Windows PowerShell:

```powershell
$target = "$env:LOCALAPPDATA\Rotakey\rotakey-codex.exe"
New-Item -ItemType Directory -Force (Split-Path $target) | Out-Null
Invoke-WebRequest "https://github.com/jisunahamed/rotakey/releases/latest/download/rotakey-codex_windows_amd64.exe" -OutFile $target
& $target install --url https://ai.example.com
```

Linux amd64:

```bash
install -d -m 700 "$HOME/.local/bin"
curl -fL https://github.com/jisunahamed/rotakey/releases/latest/download/rotakey-codex_linux_amd64 -o "$HOME/.local/bin/rotakey-codex"
chmod 700 "$HOME/.local/bin/rotakey-codex"
rotakey-codex install --url https://ai.example.com
```

macOS Apple Silicon:

```bash
install -d -m 700 "$HOME/.local/bin"
curl -fL https://github.com/jisunahamed/rotakey/releases/latest/download/rotakey-codex_darwin_arm64 -o "$HOME/.local/bin/rotakey-codex"
chmod 700 "$HOME/.local/bin/rotakey-codex"
rotakey-codex install --url https://ai.example.com
```

Use the `arm64` Linux build or `darwin_amd64` macOS build when appropriate.
The installer prompts for the key without putting it in shell history. The
`--key` flag exists for automation but is discouraged; `ROTAKEY_API_KEY` is
safer in CI.

## Start Codex

```bash
rotakey-codex doctor
codex --profile rotakey
```

Select any displayed Rotakey alias with `/model`, or start directly:

```bash
codex --profile rotakey -m provider/model-alias
```

Plain `codex` continues to use the existing native OpenAI profile. This keeps
native OpenAI authentication and the Rotakey gateway key isolated. Current
Codex configuration selects one provider per profile, so native and Rotakey
models are intentionally not combined in one picker.

Codex Desktop reads the same user configuration, but profile selection support
can vary by Desktop release. Use the `rotakey` profile when the app exposes a
profile selector; otherwise use Codex CLI for Rotakey. Restart Codex after an
install or catalog sync.

## Commands

```text
rotakey-codex install --url https://ai.example.com [--model alias]
rotakey-codex sync
rotakey-codex doctor [--smoke-test]
rotakey-codex status
rotakey-codex disable
rotakey-codex rollback
rotakey-codex uninstall
```

- `sync` refreshes only capability-ready routes and updates the default if its
  route was removed.
- `doctor` checks Codex, the protected key, config, gateway readiness,
  authentication, manifest, and catalog. `--smoke-test` makes a billed model
  request; the default doctor does not.
- `disable` removes only the marked Rotakey block and retains state/key.
- `rollback` restores the newest pre-change config backup.
- `uninstall` removes the managed block, state, catalog, and protected key.

Unknown and user-owned Codex settings are preserved. Every config mutation is
backed up and written atomically. An incomplete managed marker causes the tool
to stop instead of guessing.

## Secret storage

- Windows: DPAPI, scoped to the current Windows user.
- macOS: Login Keychain.
- Linux: Secret Service through `secret-tool`.
- Linux fallback: a warning plus a permission-checked `0600` file.

The key is never written to `config.toml`, the model catalog, command arguments,
or normal companion output. Codex obtains it by running `rotakey-codex token` as
the configured bearer-token command.

## Supported Codex loop

Native Responses routes are preferred. Chat-only and Anthropic-compatible
routes use Rotakey translation for text, SSE streaming, client function tools,
multi-round function results, strict schemas, JSON output, and supported image
inputs. The translated stream emits ordered item/content/delta/done events and
fails interrupted or malformed upstream streams.

Rotakey provides `/v1/responses/compact`. Its continuity item is encrypted with
the Rotakey master key and can be replayed only through the same deployment.
It is a bounded continuity snapshot, not OpenAI's proprietary encrypted
compaction payload.

Hosted OpenAI tools, background mode, conversation IDs, file references,
foreign encrypted compaction items, and other features without a safe
cross-provider representation return `400 unsupported_feature` on translated
routes. WebSocket Responses transport is disabled so Codex uses HTTP/SSE.

## Troubleshooting

- `401`: wrong/revoked gateway key. Run `install` again after key rotation.
- `404 model_not_found`: run `rotakey-codex sync`; confirm the alias is enabled.
- Model missing from picker: run its Admin capability probe, then `sync` and
  restart Codex.
- `429`: every eligible provider credential is at capacity or in cooldown.
- `502`: inspect Rotakey Request logs for every upstream attempt and final cause.
- `unsupported_feature`: use a native Responses route or remove the unsupported
  hosted/file/conversation feature.
- Config parse error: run `rotakey-codex rollback`, then `doctor`.

The authoritative Codex configuration fields are documented in the
[official OpenAI configuration reference](https://developers.openai.com/codex/config-reference/).
