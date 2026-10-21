CREATE TABLE provider_fallbacks (
    primary_id TEXT PRIMARY KEY REFERENCES providers(id) ON DELETE CASCADE,
    alternative_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    CHECK (primary_id <> alternative_id)
);
CREATE INDEX provider_fallbacks_alternative_idx ON provider_fallbacks(alternative_id);
