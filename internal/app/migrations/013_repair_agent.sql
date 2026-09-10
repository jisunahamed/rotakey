CREATE TABLE repair_policy (
    id integer PRIMARY KEY CHECK (id = 1),
    version bigint NOT NULL DEFAULT 1,
    policy jsonb NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO repair_policy(id) VALUES(1);
CREATE TABLE repair_incidents (
    id text PRIMARY KEY,
    request_id text NOT NULL,
    route_id text NOT NULL,
    status text NOT NULL,
    evidence jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX repair_incidents_request ON repair_incidents(request_id);
CREATE INDEX repair_incidents_created ON repair_incidents(created_at DESC);
