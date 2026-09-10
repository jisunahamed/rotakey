ALTER TABLE providers
    ADD COLUMN pinned BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX providers_pinned_order
    ON providers(pinned DESC, created_at, id);
