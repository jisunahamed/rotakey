-- A key's balance is optional: NULL means the operator does not track credit on
-- this key, which is a different state from having zero left.
--
-- Spend is accumulated on the row instead of being derived from request_logs
-- because logs are pruned on a retention schedule, and a balance that silently
-- refilled itself when old rows aged out would be worse than no balance at all.
ALTER TABLE credentials
    ADD COLUMN IF NOT EXISTS balance_usd NUMERIC(16, 6)
        CHECK (balance_usd >= 0),
    ADD COLUMN IF NOT EXISTS balance_spent_usd NUMERIC(16, 6) NOT NULL DEFAULT 0
        CHECK (balance_spent_usd >= 0);
