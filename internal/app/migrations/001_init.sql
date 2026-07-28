CREATE TABLE IF NOT EXISTS admins (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS app_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    gateway_key_hash BYTEA,
    gateway_key_prefix TEXT NOT NULL DEFAULT '',
    metadata_retention_days INTEGER NOT NULL DEFAULT 90 CHECK (metadata_retention_days BETWEEN 1 AND 3650),
    body_retention_days INTEGER NOT NULL DEFAULT 30 CHECK (body_retention_days BETWEEN 1 AND 365),
    max_wait_ms INTEGER NOT NULL DEFAULT 5000 CHECK (max_wait_ms BETWEEN 0 AND 30000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO app_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    auth_header TEXT NOT NULL DEFAULT 'Authorization',
    auth_scheme TEXT NOT NULL DEFAULT 'Bearer',
    extra_headers JSONB NOT NULL DEFAULT '{}'::jsonb,
    timeout_seconds INTEGER NOT NULL DEFAULT 120 CHECK (timeout_seconds BETWEEN 1 AND 900),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    allow_private_network BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS model_routes (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    public_alias TEXT NOT NULL UNIQUE,
    upstream_model TEXT NOT NULL,
    supports_chat BOOLEAN NOT NULL DEFAULT TRUE,
    supports_responses BOOLEAN NOT NULL DEFAULT FALSE,
    default_max_output_tokens INTEGER NOT NULL DEFAULT 1024 CHECK (default_max_output_tokens BETWEEN 1 AND 1000000),
    tokenizer TEXT NOT NULL DEFAULT 'heuristic',
    capture_bodies BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS credentials (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    label TEXT NOT NULL,
    secret_cipher BYTEA NOT NULL,
    secret_suffix TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    status TEXT NOT NULL DEFAULT 'healthy' CHECK (status IN ('healthy', 'cooldown', 'quarantined', 'disabled')),
    cooldown_until TIMESTAMPTZ,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider_id, label)
);

CREATE TABLE IF NOT EXISTS rate_policies (
    credential_id TEXT NOT NULL REFERENCES credentials(id) ON DELETE CASCADE,
    scope_key TEXT NOT NULL DEFAULT '*',
    rps BIGINT CHECK (rps > 0),
    rpm BIGINT CHECK (rpm > 0),
    rpd BIGINT CHECK (rpd > 0),
    tps BIGINT CHECK (tps > 0),
    tpm BIGINT CHECK (tpm > 0),
    tpd BIGINT CHECK (tpd > 0),
    tpr BIGINT CHECK (tpr > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (credential_id, scope_key)
);

CREATE TABLE IF NOT EXISTS request_logs (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE,
    model_id TEXT REFERENCES model_routes(id) ON DELETE SET NULL,
    model_alias TEXT NOT NULL,
    provider_id TEXT REFERENCES providers(id) ON DELETE SET NULL,
    provider_name TEXT NOT NULL,
    credential_id TEXT REFERENCES credentials(id) ON DELETE SET NULL,
    credential_label TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    attempts JSONB NOT NULL DEFAULT '[]'::jsonb,
    status_code INTEGER NOT NULL,
    latency_ms BIGINT NOT NULL,
    input_tokens BIGINT NOT NULL DEFAULT 0,
    output_tokens BIGINT NOT NULL DEFAULT 0,
    error_code TEXT,
    request_body_cipher BYTEA,
    response_body_cipher BYTEA,
    body_truncated BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS request_logs_created_at_idx ON request_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS request_logs_model_alias_idx ON request_logs (model_alias, created_at DESC);

CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    admin_id TEXT REFERENCES admins(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT,
    detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS audit_logs_created_at_idx ON audit_logs (created_at DESC);
