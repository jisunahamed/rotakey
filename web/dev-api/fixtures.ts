/** A gateway's worth of plausible data, typed against the console's own types.
 *
 *  This is a development fixture, never shipped: nothing under `dev-api/` is
 *  imported by `src/`, so none of it reaches `dist/`. It exists because the
 *  console could otherwise only be looked at on a deployment — there is no
 *  Postgres, no Redis and no gateway on a workstation, so every page rendered as
 *  a login card and layout could only be checked by reading CSS.
 *
 *  The one rule that keeps a fixture honest: it is typed as `Provider`,
 *  `Overview`, `RequestLog` and `Settings` from `src/types.ts`, so a field the
 *  backend adds or renames fails the typecheck here too. A fixture that drifts
 *  from the API is worse than no fixture, because it makes a broken page look
 *  fine.
 *
 *  The estate is chosen to cover states, not to be realistic in size. Every
 *  route state the console can draw is present: verified, catalogued,
 *  unverified, failed, keyless and switched off. Every key state is present:
 *  healthy, in cooldown, quarantined, disabled and out of credit.
 */

import type {
  Credential,
  ModelRoute,
  Overview,
  Provider,
  RatePolicy,
  RequestLog,
  Settings
} from "../src/types";

const START = Date.parse("2026-09-01T09:00:00Z");

/** Fixture timestamps are relative to a fixed instant so the same fixture reads
 *  the same way every morning, but relative to *now* so "3 minutes ago" is true
 *  while the page is open. */
export function ago(seconds: number): string {
  return new Date(Date.now() - seconds * 1000).toISOString();
}

function ahead(seconds: number): string {
  return new Date(Date.now() + seconds * 1000).toISOString();
}

function made(days: number): string {
  return new Date(START - days * 86_400_000).toISOString();
}

function policy(limits: Partial<RatePolicy>): RatePolicy {
  return { rps: null, rpm: null, rpd: null, tps: null, tpm: null, tpd: null, tpr: null, ...limits };
}

function key(
  id: string,
  providerID: string,
  label: string,
  suffix: string,
  status: Credential["status"],
  extra: Partial<Credential> = {}
): Credential {
  return {
    id,
    provider_id: providerID,
    label,
    secret_suffix: suffix,
    is_primary: false,
    enabled: status !== "disabled",
    status,
    limits: policy({}),
    model_limits: {},
    balance_spent_usd: 0,
    created_at: made(40),
    updated_at: made(2),
    ...extra
  };
}

/** The capability profile the gateway would write, mirroring
 *  `modelCapabilityProfile` in internal/app/credential_validation.go.
 *
 *  It is a function and not three literals because the fixture had invented a
 *  value — `{ chat: "ok" }`, which nothing in the Go ever writes — and the
 *  console dutifully rendered it as "Not checked yet" on a route whose own row
 *  said it had been checked an hour ago. A fixture that makes up enum values is
 *  worse than no fixture: it produces exactly the screens the operator will
 *  never see, and hides the ones they will. */
function profile(upstreamProtocol: "openai" | "anthropic", supports: { chat: boolean; responses: boolean }) {
  const common = {
    availability: "verified",
    verification: "probe",
    upstream_protocol: upstreamProtocol,
    streaming: "gateway_normalized",
    json_output: "unknown"
  };
  if (upstreamProtocol === "anthropic") {
    return { ...common, chat: "translated", responses: "translated", messages: "native", tools: "native", thinking: "native_unverified" };
  }
  const reachable = supports.chat || supports.responses;
  return {
    ...common,
    chat: supports.chat ? "native" : supports.responses ? "translated" : "off",
    responses: supports.responses ? "native" : supports.chat ? "translated" : "off",
    messages: reachable ? "translated" : "off",
    tools: reachable ? "native_unverified" : "unknown",
    thinking: "unsupported_cross_protocol"
  };
}

function route(
  id: string,
  providerID: string,
  alias: string,
  upstream: string,
  extra: Partial<ModelRoute> = {}
): ModelRoute {
  return {
    id,
    provider_id: providerID,
    public_alias: alias,
    upstream_model: upstream,
    supports_chat: true,
    supports_responses: false,
    supports_messages: true,
    default_max_output_tokens: 4096,
    input_cost_per_million_usd: 0,
    output_cost_per_million_usd: 0,
    tokenizer: "heuristic",
    capture_bodies: false,
    strip_parameters: [],
    capability_status: "catalog_verified",
    capability_profile: {},
    enabled: true,
    created_at: made(38),
    updated_at: made(1),
    ...extra
  };
}

/* ── Azure Foundry ─────────────────────────────────────────────────────────
   The busy provider: three keys in three different states, four routes in four
   different capability states, and the only route on the estate that has been
   observed switching endpoint. */

const azure: Provider = {
  id: "prv_azure",
  name: "Azure Foundry",
  slug: "azure",
  base_url: "https://rotakey-eastus.services.ai.azure.com/openai/v1",
  auth_header: "Authorization",
  auth_scheme: "Bearer",
  extra_headers: {},
  timeout_seconds: 120,
  enabled: true,
  allow_private_network: false,
  api_format: "openai",
  anthropic_version: "",
  default_key_balance_usd: 200,
  balance_spent_usd: 3.4,
  created_at: made(41),
  updated_at: made(2),
  credentials: [
    key("crd_azure_a", "prv_azure", "eastus · primary", "9f2a", "healthy", {
      is_primary: true,
      limits: policy({ rpm: 600, tpm: 400_000 }),
      model_limits: { mdl_azure_sol: policy({ rpm: 120 }) },
      balance_usd: 200,
      balance_spent_usd: 41.28,
      last_validated_at: ago(940)
    }),
    key("crd_azure_b", "prv_azure", "eastus · overflow", "41c7", "cooldown", {
      limits: policy({ rpm: 600, tpm: 400_000 }),
      cooldown_until: ahead(38),
      balance_usd: 200,
      balance_spent_usd: 188.4,
      last_validated_at: ago(3_120)
    }),
    key("crd_azure_c", "prv_azure", "westus · retired", "0b13", "quarantined", {
      validation_error: "The provider rejected this key: 401 invalid_api_key.",
      balance_usd: 50,
      balance_spent_usd: 50,
      last_validated_at: ago(86_000)
    })
  ],
  models: [
    route("mdl_azure_sol", "prv_azure", "azure/gpt-5.6-sol", "gpt-5.6-sol", {
      supports_responses: true,
      supports_messages: false,
      default_max_output_tokens: 16_384,
      input_cost_per_million_usd: 1.25,
      output_cost_per_million_usd: 10,
      capability_status: "probe_verified",
      capability_profile: profile("openai", { chat: true, responses: true }),
      capabilities_checked_at: ago(5_400),
      capture_bodies: true
    }),
    route("mdl_azure_mini", "prv_azure", "azure/gpt-5.6-sol-mini", "gpt-5.6-sol-mini", {
      supports_messages: false,
      input_cost_per_million_usd: 0.25,
      output_cost_per_million_usd: 2,
      capabilities_checked_at: ago(5_400)
    }),
    route("mdl_azure_reason", "prv_azure", "azure/o5-reason", "o5-reason", {
      supports_chat: false,
      supports_responses: true,
      supports_messages: false,
      default_max_output_tokens: 32_768,
      capability_status: "unverified"
    }),
    route("mdl_azure_embed", "prv_azure", "azure/embed-4", "text-embedding-4", {
      supports_messages: false,
      capability_status: "failed",
      capability_error: "Unknown parameter: 'max_tokens'. This model has no chat endpoint.",
      capabilities_checked_at: ago(240)
    })
  ],
  capacity: {
    total_keys: 3,
    ready_keys: 1,
    limits: {
      rpm: { limit: 600, remaining: 488, unlimited: false, unknown: false },
      tpm: { limit: 400_000, remaining: 312_400, unlimited: false, unknown: false }
    }
  }
};

/* ── Anthropic ─────────────────────────────────────────────────────────────
   The healthy provider, and the only one whose upstream speaks Anthropic — so
   every request the console sends it on the chat protocol is translated. */

const anthropic: Provider = {
  id: "prv_anthropic",
  name: "Anthropic",
  slug: "anthropic",
  base_url: "https://api.anthropic.com",
  auth_header: "x-api-key",
  auth_scheme: "",
  extra_headers: {},
  timeout_seconds: 180,
  enabled: true,
  allow_private_network: false,
  api_format: "anthropic",
  anthropic_version: "2023-06-01",
  default_key_balance_usd: null,
  balance_spent_usd: 0,
  created_at: made(30),
  updated_at: made(4),
  credentials: [
    key("crd_ant_a", "prv_anthropic", "work account", "ac91", "healthy", {
      is_primary: true,
      limits: policy({ rpm: 1_000, tpm: 200_000, tpd: 20_000_000 }),
      last_validated_at: ago(180)
    }),
    key("crd_ant_b", "prv_anthropic", "spare account", "77de", "healthy", {
      limits: policy({ rpm: 1_000, tpm: 200_000 }),
      last_validated_at: ago(220)
    })
  ],
  models: [
    route("mdl_ant_opus", "prv_anthropic", "anthropic/claude-opus-5", "claude-opus-5", {
      default_max_output_tokens: 32_000,
      input_cost_per_million_usd: 5,
      output_cost_per_million_usd: 25,
      capability_status: "probe_verified",
      capability_profile: profile("anthropic", { chat: true, responses: false }),
      capabilities_checked_at: ago(7_100)
    }),
    route("mdl_ant_sonnet", "prv_anthropic", "anthropic/claude-sonnet-5", "claude-sonnet-5", {
      default_max_output_tokens: 16_000,
      input_cost_per_million_usd: 3,
      output_cost_per_million_usd: 15,
      capability_status: "probe_verified",
      capability_profile: profile("anthropic", { chat: true, responses: false }),
      capabilities_checked_at: ago(7_100)
    }),
    route("mdl_ant_haiku", "prv_anthropic", "anthropic/claude-haiku-4-5", "claude-haiku-4-5-20251001", {
      default_max_output_tokens: 8_192,
      input_cost_per_million_usd: 0.8,
      output_cost_per_million_usd: 4,
      capabilities_checked_at: ago(7_100)
    })
  ],
  capacity: {
    total_keys: 2,
    ready_keys: 2,
    limits: {
      rpm: { limit: 2_000, remaining: 1_996, unlimited: false, unknown: false },
      tpm: { limit: 400_000, remaining: 400_000, unlimited: false, unknown: false },
      tpd: { limit: 20_000_000, remaining: 18_240_000, unlimited: false, unknown: false }
    }
  }
};

/* ── Groq ──────────────────────────────────────────────────────────────────
   Switched on, with a route, and not one key that can take a request. This is
   the state the console has always drawn as if it were fine. */

const groq: Provider = {
  id: "prv_groq",
  name: "Groq",
  slug: "groq",
  base_url: "https://api.groq.com/openai/v1",
  auth_header: "Authorization",
  auth_scheme: "Bearer",
  extra_headers: {},
  timeout_seconds: 60,
  enabled: true,
  allow_private_network: false,
  api_format: "openai",
  anthropic_version: "",
  created_at: made(9),
  updated_at: made(9),
  credentials: [
    key("crd_groq_a", "prv_groq", "free tier", "5e60", "quarantined", {
      is_primary: true,
      validation_error: "The provider rejected this key: 429 rate_limit_exceeded, seven times in a row.",
      last_validated_at: ago(600)
    })
  ],
  models: [
    route("mdl_groq_llama", "prv_groq", "groq/llama-4-70b", "llama-4-70b-versatile", {
      supports_messages: false,
      default_max_output_tokens: 8_192,
      capability_status: "probe_verified",
      capabilities_checked_at: ago(600)
    })
  ],
  capacity: {
    total_keys: 1,
    ready_keys: 0,
    limits: { rpm: { limit: 0, remaining: 0, unlimited: false, unknown: true } }
  }
};

/* ── OpenRouter ────────────────────────────────────────────────────────────
   Switched off at the provider, which strands both of its routes. */

const openrouter: Provider = {
  id: "prv_openrouter",
  name: "OpenRouter",
  slug: "openrouter",
  base_url: "https://openrouter.ai/api/v1",
  auth_header: "Authorization",
  auth_scheme: "Bearer",
  extra_headers: { "HTTP-Referer": "https://rotakey.local" },
  timeout_seconds: 120,
  enabled: false,
  allow_private_network: true,
  api_format: "openai",
  anthropic_version: "",
  default_key_balance_usd: 25,
  balance_spent_usd: 0,
  created_at: made(21),
  updated_at: made(6),
  credentials: [
    key("crd_or_a", "prv_openrouter", "trial credit", "b204", "disabled", {
      is_primary: true,
      balance_usd: 25,
      balance_spent_usd: 24.86
    })
  ],
  models: [
    route("mdl_or_deepseek", "prv_openrouter", "openrouter/deepseek-r2", "deepseek/deepseek-r2", {
      supports_messages: false,
      capability_status: "unverified"
    }),
    route("mdl_or_qwen", "prv_openrouter", "openrouter/qwen-3-max", "qwen/qwen-3-max", {
      supports_messages: false,
      enabled: false,
      capability_status: "unverified"
    })
  ],
  capacity: { total_keys: 1, ready_keys: 0, limits: {} }
};

export const providers: Provider[] = [azure, anthropic, groq, openrouter];

export const settings: Settings = {
  // The first 11 characters of the key below, because that is what the gateway
  // stores: `prefix := key[:11]` in handleRotateGatewayKey.
  gateway_key_prefix: "gw_devONLYn",
  metadata_retention_days: 30,
  body_retention_days: 7,
  max_wait_ms: 2_000,
  default_provider_timeout_seconds: 120,
  default_anthropic_provider_id: "prv_anthropic",
  routing_mode: "provider",
  base_url: "https://gateway.rotakey.dev"
};

export const version = {
  current_version: "3.0.1",
  commit: "bca29a46",
  build_time: new Date(START - 86_400_000).toISOString(),
  latest_version: "3.0.1",
  update_available: false,
  release_url: "https://github.com/jisunahamed/rotakey/releases/latest",
  published_at: new Date(START - 86_400_000).toISOString()
};

/* ── Requests ──────────────────────────────────────────────────────────────
   Twelve rows chosen so every panel on the Requests page has something to draw:
   a plain success, a translated success, a retry that succeeded on the second
   key, a request no key could take, the endpoint switch from the 3.0.1 hotfix,
   a stream that died after its first byte, and one still running. */

function log(over: Partial<RequestLog> & Pick<RequestLog, "id" | "request_id">): RequestLog {
  return {
    model_alias: "azure/gpt-5.6-sol",
    provider_name: "Azure Foundry",
    credential_label: "eastus · primary",
    endpoint: "/v1/chat/completions",
    public_protocol: "openai",
    upstream_protocol: "openai",
    attempts: [],
    routing_decisions: [],
    status_code: 200,
    latency_ms: 1_180,
    input_tokens: 412,
    output_tokens: 268,
    body_captured: false,
    body_truncated: false,
    created_at: ago(600),
    ...over
  };
}

export const logs: RequestLog[] = [
  log({
    id: "log_01",
    request_id: "req_9KpQm2XvbnTgHs4Lw",
    created_at: ago(14),
    running: true,
    status_code: 0,
    latency_ms: 0,
    input_tokens: 0,
    output_tokens: 0,
    attempts: [
      {
        credential_id: "crd_azure_a",
        credential_label: "eastus · primary",
        status_code: 0,
        retryable: false,
        duration_ms: 0
      }
    ]
  }),
  log({
    id: "log_02",
    request_id: "req_TrwPgSoxFIVzRX9rB",
    created_at: ago(95),
    model_alias: "azure/gpt-5.6-sol",
    endpoint: "/v1/responses",
    latency_ms: 1_530,
    upstream_request_id: "chatcmpl-A7fQ2Zx",
    body_captured: true,
    attempts: [
      {
        credential_id: "crd_azure_a",
        credential_label: "eastus · primary",
        status_code: 400,
        error: "unknown_parameter",
        error_message: "Provider requires the Responses endpoint for this request; retried at /responses.",
        retryable: true,
        duration_ms: 1_220,
        switched_endpoint: "responses"
      },
      {
        credential_id: "crd_azure_a",
        credential_label: "eastus · primary",
        status_code: 200,
        retryable: false,
        duration_ms: 310,
        replaced_parameters: { max_tokens: "max_output_tokens" }
      }
    ]
  }),
  log({
    id: "log_03",
    request_id: "req_Lm4Nc8ZaqRt1YbUx7",
    created_at: ago(210),
    model_alias: "anthropic/claude-opus-5",
    provider_name: "Anthropic",
    credential_label: "work account",
    endpoint: "/v1/chat/completions",
    upstream_protocol: "anthropic",
    latency_ms: 4_820,
    input_tokens: 1_904,
    output_tokens: 1_212,
    upstream_request_id: "msg_01FkYb3Qa"
  }),
  log({
    id: "log_04",
    request_id: "req_Bv7Hd2KsmWq9Ez3Pt",
    created_at: ago(360),
    model_alias: "anthropic/claude-sonnet-5",
    provider_name: "Anthropic",
    credential_label: "spare account",
    endpoint: "/v1/messages",
    public_protocol: "anthropic",
    upstream_protocol: "anthropic",
    latency_ms: 2_040,
    input_tokens: 640,
    output_tokens: 480,
    attempts: [
      {
        credential_id: "crd_ant_a",
        credential_label: "work account",
        status_code: 429,
        error: "rate_limit_exceeded",
        error_message: "This key is over its requests-per-minute limit.",
        retryable: true,
        duration_ms: 190
      },
      {
        credential_id: "crd_ant_b",
        credential_label: "spare account",
        status_code: 200,
        retryable: false,
        duration_ms: 1_850
      }
    ]
  }),
  log({
    id: "log_05",
    request_id: "req_Xz1Jf6VqnPd8Rc0Am",
    created_at: ago(540),
    model_alias: "azure/gpt-5.6-sol",
    credential_label: "",
    status_code: 429,
    error_code: "rate_limit_exceeded",
    error_message: "No key could take this request.",
    latency_ms: 12,
    input_tokens: 0,
    output_tokens: 0,
    routing_decisions: [
      {
        credential_id: "crd_azure_a",
        credential_label: "eastus · primary",
        reason: "limit_exhausted",
        scope: "model",
        dimension: "rpm",
        limit: 120,
        used: 120,
        remaining: 0,
        required: 1,
        retry_after_ms: 24_000,
        reset_at: ahead(24)
      },
      {
        credential_id: "crd_azure_b",
        credential_label: "eastus · overflow",
        reason: "cooldown",
        retry_after_ms: 38_000,
        reset_at: ahead(38)
      },
      {
        credential_id: "crd_azure_c",
        credential_label: "westus · retired",
        reason: "quarantined"
      }
    ]
  }),
  log({
    id: "log_06",
    request_id: "req_Qd3Ye9TzrLn2Bk5Wv",
    created_at: ago(720),
    model_alias: "anthropic/claude-opus-5",
    provider_name: "Anthropic",
    credential_label: "work account",
    upstream_protocol: "anthropic",
    status_code: 200,
    error_code: "stream_interrupted",
    error_message: "The reply stopped 6.2s after the first byte: connection reset by peer.",
    latency_ms: 6_210,
    input_tokens: 880,
    output_tokens: 0
  }),
  log({
    id: "log_07",
    request_id: "req_Nh8Kv4WbtCm6Fs1Zy",
    created_at: ago(1_020),
    model_alias: "azure/gpt-5.6-sol-mini",
    latency_ms: 640,
    input_tokens: 218,
    output_tokens: 96
  }),
  log({
    id: "log_08",
    request_id: "req_Rj5Ta7XcvHp3Ln9Qe",
    created_at: ago(1_400),
    model_alias: "groq/llama-4-70b",
    provider_name: "Groq",
    credential_label: "",
    status_code: 503,
    error_code: "no_credentials",
    error_message: "No enabled healthy API key is configured for this model.",
    latency_ms: 4,
    input_tokens: 0,
    output_tokens: 0
  }),
  log({
    id: "log_09",
    request_id: "req_Fw2Mg6ZdpBs4Kt7Ru",
    created_at: ago(1_900),
    model_alias: "azure/gpt-5.6-sol",
    body_captured: true,
    body_truncated: true,
    latency_ms: 9_450,
    input_tokens: 12_400,
    output_tokens: 3_180
  }),
  log({
    id: "log_10",
    request_id: "req_Cs9Bq1YnkVr5Hd8Jm",
    created_at: ago(2_600),
    model_alias: "anthropic/claude-haiku-4-5",
    provider_name: "Anthropic",
    credential_label: "spare account",
    upstream_protocol: "anthropic",
    latency_ms: 780,
    input_tokens: 140,
    output_tokens: 62
  }),
  log({
    id: "log_11",
    request_id: "req_Pv6Rk3AcmTj9Xb2Nf",
    created_at: ago(3_400),
    model_alias: "azure/embed-4",
    credential_label: "eastus · primary",
    status_code: 400,
    error_code: "unsupported_parameter",
    error_message: "Unknown parameter: 'max_tokens'.",
    latency_ms: 310,
    input_tokens: 0,
    output_tokens: 0,
    attempts: [
      {
        credential_id: "crd_azure_a",
        credential_label: "eastus · primary",
        status_code: 400,
        error: "unknown_parameter",
        error_message: "Unknown parameter: 'max_tokens'.",
        retryable: false,
        duration_ms: 310,
        removed_parameters: ["stream_options"]
      }
    ]
  }),
  log({
    id: "log_12",
    request_id: "req_Gy4Wd8LqfEn1Vc6Sk",
    created_at: ago(5_100),
    model_alias: "azure/gpt-5.6-sol",
    status_code: 500,
    error_code: "upstream_unavailable",
    error_message: "The upstream connection failed before a response started.",
    latency_ms: 30_020,
    input_tokens: 0,
    output_tokens: 0
  })
];

export const bodies: Record<string, { request: string | null; response: string | null }> = {
  log_02: {
    request: JSON.stringify(
      { model: "gpt-5.6-sol", input: [{ role: "user", content: "Summarise the release notes." }], max_output_tokens: 16384 },
      null,
      2
    ),
    response: JSON.stringify(
      { id: "resp_A7fQ2Zx", output_text: "The 3.0.1 release fixes the endpoint switch.", usage: { input_tokens: 412, output_tokens: 268 } },
      null,
      2
    )
  },
  log_09: {
    request: JSON.stringify({ model: "gpt-5.6-sol", messages: [{ role: "user", content: "…" }] }, null, 2),
    response: null
  }
};

/* ── Overview ──────────────────────────────────────────────────────────────
   Derived from the estate above rather than written out, so a route added to a
   provider cannot go missing from the readiness panel — the exact class of
   inconsistency a hand-written fixture invites. */

const ranges: Record<Overview["range"], { points: number; stepSeconds: number; scale: number }> = {
  "1h": { points: 12, stepSeconds: 300, scale: 1 },
  "24h": { points: 24, stepSeconds: 3_600, scale: 11 },
  "7d": { points: 14, stepSeconds: 43_200, scale: 74 },
  all: { points: 20, stepSeconds: 172_800, scale: 210 }
};

function readyKeys(provider: Provider): number {
  return provider.enabled ? provider.credentials.filter((c) => c.enabled && c.status === "healthy").length : 0;
}

function routeReady(provider: Provider, model: ModelRoute): boolean {
  return provider.enabled && model.enabled && readyKeys(provider) > 0;
}

export function overview(range: Overview["range"]): Overview {
  const shape = ranges[range] ?? ranges["1h"];
  const series = Array.from({ length: shape.points }, (_, index) => {
    // A fixed wave rather than a random one: a chart that redraws differently on
    // every poll makes it impossible to tell a layout change from noise.
    const wave = 0.55 + 0.45 * Math.sin((index / shape.points) * Math.PI * 2);
    const requests = Math.round(wave * 46 * shape.scale);
    return {
      timestamp: new Date(Date.now() - (shape.points - 1 - index) * shape.stepSeconds * 1000).toISOString(),
      requests,
      errors: index % 5 === 2 ? Math.round(requests * 0.12) : Math.round(requests * 0.01),
      tokens: requests * 1_640,
      latency_p95_ms: 1_400 + Math.round(wave * 2_600)
    };
  });

  const totals = series.reduce(
    (sum, point) => ({
      requests: sum.requests + point.requests,
      errors: sum.errors + point.errors,
      tokens: sum.tokens + point.tokens
    }),
    { requests: 0, errors: 0, tokens: 0 }
  );

  const allRoutes = providers.flatMap((provider) => provider.models.map((model) => ({ provider, model })));

  return {
    generated_at: new Date().toISOString(),
    range,
    base_url: settings.base_url,
    summary: {
      providers_total: providers.length,
      providers_ready: providers.filter((p) => p.enabled && readyKeys(p) > 0).length,
      routes_total: allRoutes.length,
      routes_ready: allRoutes.filter(({ provider, model }) => routeReady(provider, model)).length,
      keys_total: providers.reduce((sum, p) => sum + p.credentials.length, 0),
      keys_ready: providers.reduce((sum, p) => sum + readyKeys(p), 0),
      keys_warning: 3,
      requests: totals.requests,
      tokens: totals.tokens,
      estimated_cost_usd: totals.tokens * 0.0000042,
      errors: totals.errors,
      error_rate: totals.requests === 0 ? 0 : totals.errors / totals.requests,
      latency_p50_ms: 940,
      latency_p95_ms: 3_820,
      max_wait_ms: settings.max_wait_ms,
      gateway_key_ready: true,
      credit: {
        tracked_keys: 4,
        balance_usd: 475,
        spent_usd: 304.54,
        remaining_usd: 170.46,
        exhausted_keys: 1,
        unattributed_spent_usd: 3.4
      }
    },
    series,
    providers: providers.map((provider) => ({
      id: provider.id,
      name: provider.name,
      enabled: provider.enabled,
      models_total: provider.models.length,
      models_ready: provider.models.filter((model) => routeReady(provider, model)).length,
      keys_total: provider.credentials.length,
      keys_ready: readyKeys(provider),
      keys_warning: provider.credentials.filter((c) => c.status === "cooldown" || c.status === "quarantined").length,
      validation_warnings: provider.credentials.filter((c) => c.validation_error).length,
      capacity: provider.capacity?.limits ?? {},
      credit: {
        tracked_keys: provider.credentials.filter((c) => typeof c.balance_usd === "number").length,
        balance_usd: provider.credentials.reduce((sum, c) => sum + (c.balance_usd ?? 0), 0),
        spent_usd: provider.credentials.reduce((sum, c) => sum + c.balance_spent_usd, 0),
        remaining_usd: provider.credentials.reduce((sum, c) => sum + ((c.balance_usd ?? 0) - c.balance_spent_usd), 0),
        exhausted_keys: provider.credentials.filter((c) => (c.balance_usd ?? 1) - c.balance_spent_usd <= 0).length
      },
      default_key_balance_usd: provider.default_key_balance_usd ?? null
    })),
    routes: allRoutes.map(({ provider, model }, index) => {
      const healthy = readyKeys(provider);
      const requests = Math.max(0, Math.round(totals.requests / (index + 2)));
      return {
        id: model.id,
        provider_id: provider.id,
        alias: model.public_alias,
        upstream_model: model.upstream_model,
        provider: provider.name,
        enabled: model.enabled && provider.enabled,
        healthy_credentials: healthy,
        unavailable_credentials: provider.credentials.length - healthy,
        total_credentials: provider.credentials.length,
        requests,
        errors: Math.round(requests * (index === 3 ? 0.31 : 0.02)),
        tokens: requests * 1_640,
        estimated_cost_usd: requests * 0.0068,
        input_cost_per_million_usd: model.input_cost_per_million_usd,
        output_cost_per_million_usd: model.output_cost_per_million_usd,
        error_rate: index === 3 ? 0.31 : 0.02,
        latency_p95_ms: 1_200 + index * 480,
        last_request_at: requests > 0 ? ago(120 + index * 90) : undefined,
        next_credential_id: healthy > 0 ? provider.credentials[0].id : undefined,
        next_request_headroom:
          healthy > 0 && provider.capacity?.limits.rpm
            ? {
                dimension: "rpm",
                scope: "shared" as const,
                remaining: provider.capacity.limits.rpm.remaining,
                limit: provider.capacity.limits.rpm.limit,
                reset_at: ahead(41)
              }
            : undefined,
        next_token_headroom:
          healthy > 0 && provider.capacity?.limits.tpm
            ? {
                dimension: "tpm",
                scope: "shared" as const,
                remaining: provider.capacity.limits.tpm.remaining,
                limit: provider.capacity.limits.tpm.limit,
                reset_at: ahead(41)
              }
            : undefined,
        strip_parameters: model.strip_parameters,
        supports_responses: model.supports_responses,
        default_max_output_tokens: model.default_max_output_tokens,
        segments: provider.credentials.map((credential) => ({
          id: credential.id,
          label: credential.label,
          secret_suffix: credential.secret_suffix,
          primary: credential.is_primary,
          status: !provider.enabled || !credential.enabled ? ("disabled" as const) : credential.status,
          cursor: credential.is_primary && credential.status === "healthy",
          unknown: false,
          validation_error: credential.validation_error,
          last_validated_at: credential.last_validated_at,
          cooldown_until: credential.cooldown_until,
          credit:
            typeof credential.balance_usd === "number"
              ? {
                  balance_usd: credential.balance_usd,
                  spent_usd: credential.balance_spent_usd,
                  remaining_usd: credential.balance_usd - credential.balance_spent_usd,
                  exhausted: credential.balance_usd - credential.balance_spent_usd <= 0,
                  requests: Math.round(requests / provider.credentials.length),
                  errors: 2,
                  tokens: Math.round((requests * 1_640) / provider.credentials.length)
                }
              : undefined
        }))
      };
    }),
    alerts: [
      {
        id: "alert_groq",
        severity: "critical",
        resource_type: "provider",
        resource_id: "prv_groq",
        title: "Groq has no key that can take a request",
        detail: "Its only key was quarantined after seven rate-limit rejections in a row. groq/llama-4-70b is off the air."
      },
      {
        id: "alert_azure_cooldown",
        severity: "warning",
        resource_type: "credential",
        resource_id: "crd_azure_b",
        title: "eastus · overflow is out of credit",
        detail: "$188.40 of its $200 balance is spent. Top it up or the pool drops to one key."
      },
      {
        id: "alert_embed",
        severity: "info",
        resource_type: "route",
        resource_id: "mdl_azure_embed",
        title: "azure/embed-4 failed its capability check",
        detail: "The model has no chat endpoint. Delete the route or point it at a model that does."
      }
    ],
    recent_failures: logs
      .filter((row) => row.status_code >= 400 || (row.error_code ?? "") !== "")
      .slice(0, 5)
      .map((row) => ({
        request_id: row.request_id,
        model_alias: row.model_alias,
        provider_name: row.provider_name,
        credential_label: row.credential_label,
        status_code: row.status_code,
        error_code: row.error_code,
        latency_ms: row.latency_ms,
        created_at: row.created_at
      }))
  };
}
