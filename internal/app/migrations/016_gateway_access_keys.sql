CREATE TABLE IF NOT EXISTS gateway_access_keys (
    id TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    key_hash BYTEA NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    rpm BIGINT NOT NULL DEFAULT 2000 CHECK (rpm > 0),
    concurrency_limit INTEGER NOT NULL DEFAULT 100 CHECK (concurrency_limit > 0),
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS gateway_access_keys_created_at_idx
    ON gateway_access_keys (created_at DESC);
