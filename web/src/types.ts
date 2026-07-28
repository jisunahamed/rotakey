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
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

export type Credential = {
  id: string;
  provider_id: string;
  label: string;
  secret_suffix: string;
  enabled: boolean;
  status: "healthy" | "cooldown" | "quarantined" | "disabled";
  cooldown_until?: string;
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
};

export type Overview = {
  base_url: string;
  providers: number;
  usage: {
    requests_24h: number;
    errors_24h: number;
    tokens_24h: number;
  };
  settings: Settings;
  routes: Array<{
    id: string;
    alias: string;
    provider: string;
    enabled: boolean;
    healthy_credentials: number;
    unavailable_credentials: number;
    total_credentials: number;
    requests_24h: number;
    errors_24h: number;
    segments: Array<{
      id: string;
      label: string;
      status: "healthy" | "cooldown" | "quarantined" | "disabled" | "exhausted" | "unknown";
      cursor: boolean;
      unknown: boolean;
      request_headroom?: {
        dimension: string;
        scope: "shared" | "model";
        remaining: number;
        limit: number;
      };
      token_headroom?: {
        dimension: string;
        scope: "shared" | "model";
        remaining: number;
        limit: number;
      };
    }>;
  }>;
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
