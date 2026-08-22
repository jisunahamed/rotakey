ALTER TABLE app_settings
    ADD COLUMN IF NOT EXISTS default_provider_timeout_seconds INTEGER NOT NULL DEFAULT 120
        CHECK (default_provider_timeout_seconds BETWEEN 1 AND 900);
