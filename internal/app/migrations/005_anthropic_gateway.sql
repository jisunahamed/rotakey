ALTER TABLE providers
    ADD COLUMN IF NOT EXISTS api_format TEXT NOT NULL DEFAULT 'openai',
    ADD COLUMN IF NOT EXISTS anthropic_version TEXT NOT NULL DEFAULT '2023-06-01';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'providers_api_format_check'
    ) THEN
        ALTER TABLE providers ADD CONSTRAINT providers_api_format_check
            CHECK (api_format IN ('openai', 'anthropic'));
    END IF;
END $$;

ALTER TABLE model_routes
    ADD COLUMN IF NOT EXISTS supports_messages BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE app_settings
    ADD COLUMN IF NOT EXISTS default_anthropic_provider_id TEXT
        REFERENCES providers(id) ON DELETE SET NULL;

ALTER TABLE request_logs
    ADD COLUMN IF NOT EXISTS public_protocol TEXT NOT NULL DEFAULT 'openai',
    ADD COLUMN IF NOT EXISTS upstream_protocol TEXT NOT NULL DEFAULT 'openai',
    ADD COLUMN IF NOT EXISTS upstream_request_id TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS anthropic_resources (
    id TEXT PRIMARY KEY,
    resource_type TEXT NOT NULL CHECK (resource_type IN ('file', 'batch')),
    upstream_id TEXT NOT NULL,
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE RESTRICT,
    credential_id TEXT NOT NULL REFERENCES credentials(id) ON DELETE RESTRICT,
    state JSONB NOT NULL DEFAULT '{}'::jsonb,
    model_aliases JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider_id, credential_id, resource_type, upstream_id)
);

CREATE INDEX IF NOT EXISTS anthropic_resources_type_created_idx
    ON anthropic_resources(resource_type, created_at DESC);

CREATE INDEX IF NOT EXISTS anthropic_resources_provider_idx
    ON anthropic_resources(provider_id);

CREATE INDEX IF NOT EXISTS anthropic_resources_credential_idx
    ON anthropic_resources(credential_id);
