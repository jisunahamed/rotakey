ALTER TABLE model_routes
    ADD COLUMN IF NOT EXISTS input_cost_per_million_usd NUMERIC(16, 6) NOT NULL DEFAULT 0 CHECK (input_cost_per_million_usd >= 0),
    ADD COLUMN IF NOT EXISTS output_cost_per_million_usd NUMERIC(16, 6) NOT NULL DEFAULT 0 CHECK (output_cost_per_million_usd >= 0);
