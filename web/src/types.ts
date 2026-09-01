export type RatePolicy = {
  rps: number | null;
  rpm: number | null;
  rpd: number | null;
  tps: number | null;
  tpm: number | null;
  tpd: number | null;
  tpr: number | null;
};

export const emptyPolicy = (): RatePolicy => ({
  rps: null,
  rpm: null,
  rpd: null,
  tps: null,
  tpm: null,
  tpd: null,
  tpr: null
});

export type ModelRoute = {
  id: string;
  provider_id: string;
  public_alias: string;
  upstream_model: string;
  supports_chat: boolean;
  supports_responses: boolean;
  supports_messages: boolean;
  default_max_output_tokens: number;
  input_cost_per_million_usd: number;
  output_cost_per_million_usd: number;
  request_cost_usd?: number;
  tokenizer: string;
  capture_bodies: boolean;
  strip_parameters: string[];
  capability_status: "unverified" | "catalog_verified" | "probe_verified" | "failed";
  capability_profile: Record<string, string>;
  capabilities_checked_at?: string;
  capability_error?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

/** One thing the gateway worked out from a provider's own answer and has been
 * acting on ever since, without anyone asking it to.
 *
 * Each one is a repair: an endpoint switched because the provider named a
 * different one, a field dropped or renamed because the provider rejected it. All
 * of it is right, and all of it was invisible — the console showed a route's own
 * configuration while the gateway sent something else, for a day, with nothing on
 * any screen saying so. None of it is configuration: it lives in Redis, every fact
 * expires within a day, and a restart forgets all of it. So it is shown as facts
 * with an expiry, never as settings with a value. */
export type LearnedFact = {
  kind: "prefer_responses" | "no_responses" | "strip_parameters" | "rename_parameters" | "strip_item_fields";
  /** Which endpoint the fact applies to, on the two that are learned per
   * endpoint. Absent on the two that apply to the route as a whole. */
  endpoint?: string;
  parameters?: string[];
  /** `[sent, accepted]` pairs, the same shape an attempt's evidence uses. */
  renames?: Array<[string, string]>;
  /** When Redis drops this and the gateway plans from the route's own
   * configuration again. Absolute, so nothing here needs to agree with the
   * server's clock about what "in 20 hours" means. */
  expires_at: string;
};

/** A tracked API key's credit. Absent when the operator does not track a balance
 * on that key, which is a different state from having spent it all. */
export type CreditRemaining = {
  balance_usd: number;
  spent_usd: number;
  remaining_usd: number;
  exhausted: boolean;
  /** Traffic this key served in the selected range, which is what answers
   * "which key is burning the credit". */
  requests?: number;
  errors?: number;
  tokens?: number;
};

/** Credit summed over the keys that track a balance. tracked_keys separates
 * "nobody tracks a balance" from "every tracked key is empty". */
export type CreditTotals = {
  tracked_keys: number;
  balance_usd: number;
  spent_usd: number;
  remaining_usd: number;
  exhausted_keys: number;
  /** Spend charged to the provider because the request ended without a recorded
   * key. Already counted in spent_usd; reported separately so the console can
   * explain a gap between the keys' own figures and the provider's. */
  unattributed_spent_usd?: number;
};

export const emptyCreditTotals = (): CreditTotals => ({
  tracked_keys: 0,
  balance_usd: 0,
  spent_usd: 0,
  remaining_usd: 0,
  exhausted_keys: 0,
  unattributed_spent_usd: 0
});

export type Credential = {
  id: string;
  provider_id: string;
  label: string;
  secret_suffix: string;
  is_primary: boolean;
  enabled: boolean;
  status: "healthy" | "cooldown" | "quarantined" | "disabled";
  cooldown_until?: string;
  last_validated_at?: string;
  validation_error?: string;
  limits: RatePolicy;
  model_limits: Record<string, RatePolicy>;
  /** null or absent means this key's balance is not tracked. */
  balance_usd?: number | null;
  balance_spent_usd: number;
  created_at: string;
  updated_at: string;
};

export type Provider = {
  id: string;
  name: string;
  slug: string;
  base_url: string;
  auth_header: string;
  auth_scheme: string;
  extra_headers: Record<string, string>;
  timeout_seconds: number;
  enabled: boolean;
  allow_private_network: boolean;
  api_format: "openai" | "anthropic";
  anthropic_version: string;
  /** The credit every new key on this provider starts with. null or absent means
   * new keys are created untracked. */
  default_key_balance_usd?: number | null;
  /** Spend the gateway could not pin on one key. Already subtracted from the
   * provider's remaining credit. */
  balance_spent_usd?: number;
  created_at: string;
  updated_at: string;
  models: ModelRoute[];
  credentials: Credential[];
  capacity?: {
    total_keys: number;
    ready_keys: number;
    limits: Record<string, {
      limit: number;
      remaining: number;
      unlimited: boolean;
      unknown: boolean;
    }>;
  };
};

export type DiscoveredModel = {
  id: string;
  owned_by?: string;
};

export type Overview = {
  generated_at: string;
  range: "1h" | "24h" | "7d" | "all";
  base_url: string;
  summary: {
    providers_total: number;
    providers_ready: number;
    routes_total: number;
    routes_ready: number;
    keys_total: number;
    keys_ready: number;
    keys_warning: number;
    requests: number;
    tokens: number;
    estimated_cost_usd: number;
    errors: number;
    error_rate: number;
    latency_p50_ms: number;
    latency_p95_ms: number;
    max_wait_ms: number;
    gateway_key_ready: boolean;
    credit: CreditTotals;
  };
  series: Array<{
    timestamp: string;
    requests: number;
    errors: number;
    tokens: number;
    latency_p95_ms: number;
  }>;
  providers: Array<{
    id: string;
    name: string;
    enabled: boolean;
    models_total: number;
    models_ready: number;
    keys_total: number;
    keys_ready: number;
    keys_warning: number;
    validation_warnings: number;
    capacity: Record<string, OverviewLimit>;
    credit: CreditTotals;
    /** What each new key on this provider starts with, so an operator can see an
     * account's per-key figure without opening every key. */
    default_key_balance_usd?: number | null;
  }>;
  routes: Array<{
    id: string;
    provider_id: string;
    alias: string;
    upstream_model: string;
    provider: string;
    enabled: boolean;
    healthy_credentials: number;
    unavailable_credentials: number;
    total_credentials: number;
    requests: number;
    errors: number;
    tokens: number;
    estimated_cost_usd: number;
    input_cost_per_million_usd: number;
    output_cost_per_million_usd: number;
    error_rate: number;
    latency_p95_ms: number;
    last_request_at?: string;
    next_credential_id?: string;
    next_request_headroom?: OverviewHeadroom;
    next_token_headroom?: OverviewHeadroom;
    strip_parameters: string[];
    supports_responses: boolean;
    default_max_output_tokens: number;
    segments: Array<{
      id: string;
      label: string;
      secret_suffix: string;
      primary: boolean;
      status: "healthy" | "cooldown" | "quarantined" | "disabled" | "exhausted" | "unknown";
      cursor: boolean;
      unknown: boolean;
      validation_error?: string;
      last_validated_at?: string;
      cooldown_until?: string;
      request_headroom?: OverviewHeadroom;
      token_headroom?: OverviewHeadroom;
      credit?: CreditRemaining;
    }>;
  }>;
  alerts: Array<{
    id: string;
    severity: "critical" | "warning" | "info";
    resource_type: "provider" | "route" | "credential";
    resource_id: string;
    title: string;
    detail: string;
  }>;
  recent_failures: Array<{
    request_id: string;
    model_alias: string;
    provider_name: string;
    credential_label: string;
    status_code: number;
    error_code?: string;
    latency_ms: number;
    created_at: string;
  }>;
};

export type OverviewLimit = {
  limit: number;
  remaining: number;
  unlimited: boolean;
  unknown: boolean;
  reset_at?: string;
};

export type OverviewHeadroom = {
  dimension: string;
  scope: "shared" | "model";
  remaining: number;
  limit: number;
  reset_at?: string;
};

export type RequestLog = {
  id: string;
  request_id: string;
  model_alias: string;
  provider_name: string;
  credential_label: string;
  endpoint: string;
  public_protocol: "openai" | "anthropic";
  upstream_protocol: "openai" | "anthropic";
  upstream_request_id?: string;
  attempts: Array<{
    credential_id: string;
    credential_label: string;
    status_code: number;
    error?: string;
    error_message?: string;
    retryable: boolean;
    duration_ms: number;
    removed_parameters?: string[];
    replaced_parameters?: Record<string, string>;
    switched_endpoint?: string;
  }>;
  routing_decisions: Array<{
    credential_id?: string;
    credential_label?: string;
    reason: "cooldown" | "tpr_exceeded" | "limit_exhausted" | "disabled" | "quarantined" | string;
    scope?: "shared" | "model";
    dimension?: string;
    limit?: number;
    used?: number;
    remaining?: number;
    required?: number;
    retry_after_ms?: number;
    reset_at?: string;
  }>;
  status_code: number;
  latency_ms: number;
  input_tokens: number;
  output_tokens: number;
  error_code?: string;
  error_message?: string;
  running?: boolean;
  body_captured: boolean;
  body_truncated: boolean;
  created_at: string;
};

export type RoutingMode = 'provider' | 'model';

export type Settings = {
  gateway_key_prefix: string;
  metadata_retention_days: number;
  body_retention_days: number;
  max_wait_ms: number;
  default_provider_timeout_seconds: number;
  default_anthropic_provider_id: string;
  routing_mode: RoutingMode;
  base_url: string;
};

/** Returned by PUT /api/admin/settings, which reports the alias renames the
 * routing-mode switch performed. */
export type SettingsUpdateResult = {
  routing_mode: RoutingMode;
  aliases_rewritten: number;
  alias_conflicts: string[];
  /** How many providers had their own timeout replaced by the default. Zero unless
   * the save carried `apply_timeout_to_all_providers`. */
  providers_retimed: number;
};

/** Returned by POST /api/admin/config/import, which reports what the bundle
 * created or updated so the operator can see the whole setup landed. */
export type ImportResult = {
  routing_mode: RoutingMode;
  providers_created: number;
  providers_updated: number;
  models_created: number;
  models_updated: number;
  credentials_created: number;
  credentials_updated: number;
  credentials_skipped: number;
  credentials_unverified: number;
  warnings: string[];
};

/** Returned by PUT /api/admin/providers/{id}/state. When a provider is turned
 * off, aliases_stranded names the public aliases no other enabled provider can
 * serve, which is the difference between draining one pool member and taking a
 * model off the gateway. */
export type ProviderStateResult = {
  enabled: boolean;
  aliases_stranded: string[];
  warnings: string[];
};
