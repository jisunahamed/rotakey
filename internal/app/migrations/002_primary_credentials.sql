ALTER TABLE credentials
    ADD COLUMN IF NOT EXISTS is_primary BOOLEAN NOT NULL DEFAULT FALSE;

CREATE UNIQUE INDEX IF NOT EXISTS credentials_one_primary_per_provider
    ON credentials(provider_id)
    WHERE is_primary = TRUE;
