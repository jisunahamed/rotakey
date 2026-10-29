ALTER TABLE providers ADD COLUMN max_concurrent_requests INTEGER NOT NULL DEFAULT 0 CHECK (max_concurrent_requests >= 0);
ALTER TABLE request_logs ADD COLUMN queue_wait_ms BIGINT NOT NULL DEFAULT 0;
