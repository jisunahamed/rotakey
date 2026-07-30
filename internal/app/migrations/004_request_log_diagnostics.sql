ALTER TABLE request_logs
    ADD COLUMN IF NOT EXISTS routing_decisions JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '';
