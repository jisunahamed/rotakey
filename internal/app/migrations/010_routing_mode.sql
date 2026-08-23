ALTER TABLE app_settings
    ADD COLUMN IF NOT EXISTS routing_mode TEXT NOT NULL DEFAULT 'provider';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'app_settings_routing_mode_check'
    ) THEN
        ALTER TABLE app_settings ADD CONSTRAINT app_settings_routing_mode_check
            CHECK (routing_mode IN ('provider', 'model'));
    END IF;
END $$;

-- Model-wise routing pools every provider that publishes the same public alias,
-- so the alias is only unique per provider instead of globally.
ALTER TABLE model_routes DROP CONSTRAINT IF EXISTS model_routes_public_alias_key;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'model_routes_provider_alias_key'
    ) THEN
        ALTER TABLE model_routes ADD CONSTRAINT model_routes_provider_alias_key
            UNIQUE (provider_id, public_alias);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS model_routes_public_alias_idx ON model_routes (public_alias);
