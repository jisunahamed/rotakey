-- An operator with dozens of keys behind one provider should not have to type a
-- balance into each of them. The provider carries the figure that every key on it
-- starts with, and each key still keeps its own balance afterwards so a top-up or
-- a one-off allowance stays per key.
--
-- NULL means the provider seeds nothing, which is the existing behaviour: keys
-- created without an explicit balance stay untracked and route forever.
ALTER TABLE providers
    ADD COLUMN IF NOT EXISTS default_key_balance_usd NUMERIC(16, 6)
        CHECK (default_key_balance_usd >= 0);

-- Spend that could not be attributed to one key lands on the provider instead of
-- being dropped. It is counted against the provider's pooled credit so the
-- dashboard's remaining figure stays honest even when a request finished without
-- a recorded credential.
ALTER TABLE providers
    ADD COLUMN IF NOT EXISTS balance_spent_usd NUMERIC(16, 6) NOT NULL DEFAULT 0
        CHECK (balance_spent_usd >= 0);
