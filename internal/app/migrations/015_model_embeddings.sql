ALTER TABLE model_routes
    ADD COLUMN supports_embeddings BOOLEAN NOT NULL DEFAULT FALSE;

-- Earlier releases imported every catalog entry as a chat model, then marked
-- embedding models failed when the chat probe rejected them. Restore those
-- recognizable routes to their last trustworthy state so /v1/embeddings can
-- serve them immediately after this migration.
UPDATE model_routes
SET supports_embeddings=TRUE,
    supports_chat=FALSE,
    supports_responses=FALSE,
    supports_messages=FALSE,
    capability_status=CASE WHEN capability_status='failed' THEN 'catalog_verified' ELSE capability_status END,
    capability_profile=capability_profile || '{"embeddings":"native","chat":"off","responses":"off","messages":"off"}'::jsonb,
    capability_error=CASE WHEN capability_status='failed' THEN '' ELSE capability_error END,
    updated_at=NOW()
WHERE LOWER(public_alias) LIKE '%embed%'
   OR LOWER(upstream_model) LIKE '%embed%';
