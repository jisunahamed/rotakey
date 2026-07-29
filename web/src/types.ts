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
  default_max_output_tokens: number;
  tokenizer: string;
  capture_bodies: boolean;
  strip_parameters: string[];
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

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
  range: "1h" | "24h" | "7d";
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
    errors: number;
    error_rate: number;
    latency_p50_ms: number;
    latency_p95_ms: number;
    max_wait_ms: number;
    gateway_key_ready: boolean;
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
  attempts: Array<{
    credential_id: string;
    credential_label: string;
    status_code: number;
    error?: string;
    retryable: boolean;
    duration_ms: number;
  }>;
  status_code: number;
  latency_ms: number;
  input_tokens: number;
  output_tokens: number;
  error_code?: string;
  body_captured: boolean;
  body_truncated: boolean;
  created_at: string;
};

export type Settings = {
  gateway_key_prefix: string;
  metadata_retention_days: number;
  body_retention_days: number;
  max_wait_ms: number;
};
