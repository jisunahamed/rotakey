ALTER TABLE model_routes
    ADD COLUMN IF NOT EXISTS request_cost_usd NUMERIC(16, 6)
        CHECK (request_cost_usd >= 0);
