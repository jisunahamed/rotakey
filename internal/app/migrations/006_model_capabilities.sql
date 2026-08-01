ALTER TABLE model_routes
    ADD COLUMN IF NOT EXISTS capability_status TEXT NOT NULL DEFAULT 'unverified',
    ADD COLUMN IF NOT EXISTS capability_profile JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS capabilities_checked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS capability_error TEXT NOT NULL DEFAULT '';
