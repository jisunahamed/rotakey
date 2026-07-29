ALTER TABLE credentials
    ADD COLUMN IF NOT EXISTS last_validated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS validation_error TEXT NOT NULL DEFAULT '';

ALTER TABLE model_routes
    ADD COLUMN IF NOT EXISTS strip_parameters TEXT[] NOT NULL DEFAULT '{}';
