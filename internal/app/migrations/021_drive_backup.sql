CREATE TABLE drive_backup_settings (
    id integer PRIMARY KEY CHECK (id = 1),
    client_id text NOT NULL DEFAULT '',
    client_secret_cipher bytea,
    refresh_token_cipher bytea,
    account_label text NOT NULL DEFAULT '',
    folder_id text NOT NULL DEFAULT '',
    folder_name text NOT NULL DEFAULT '',
    daily_time text NOT NULL DEFAULT '00:00',
    scheduled boolean NOT NULL DEFAULT false,
    delete_after_upload boolean NOT NULL DEFAULT false,
    include_bodies boolean NOT NULL DEFAULT true,
    include_metadata boolean NOT NULL DEFAULT true,
    first_backup_pending boolean NOT NULL DEFAULT false,
    destination_id text NOT NULL DEFAULT '',
    next_run_at timestamptz,
    revision bigint NOT NULL DEFAULT 0
);
INSERT INTO drive_backup_settings(id) VALUES (1);

CREATE TABLE drive_backup_runs (
    id text PRIMARY KEY,
    destination_id text NOT NULL,
    delete_source boolean NOT NULL,
    include_bodies boolean NOT NULL,
    include_metadata boolean NOT NULL,
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','completed','failed')),
    cutoff timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    uploaded bigint NOT NULL DEFAULT 0,
    deleted bigint NOT NULL DEFAULT 0,
    files integer NOT NULL DEFAULT 0,
    last_file_id text NOT NULL DEFAULT '',
    verified boolean NOT NULL DEFAULT false,
    error_message text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX drive_one_active_run ON drive_backup_runs ((true)) WHERE state IN ('queued','running');
CREATE INDEX drive_runs_recent ON drive_backup_runs(cutoff DESC);

CREATE TABLE drive_backup_receipts (
    destination_id text NOT NULL,
    log_id text NOT NULL REFERENCES request_logs(id) ON DELETE CASCADE,
    selection smallint NOT NULL,
    fingerprint text NOT NULL,
    run_id text NOT NULL,
    PRIMARY KEY (destination_id,selection,log_id)
);
CREATE INDEX drive_receipts_log_idx ON drive_backup_receipts(log_id);
CREATE INDEX drive_receipts_run_idx ON drive_backup_receipts(run_id,log_id);
CREATE INDEX drive_request_cursor ON request_logs(created_at,id);

CREATE TABLE drive_backup_files (
    run_id text NOT NULL REFERENCES drive_backup_runs(id) ON DELETE CASCADE,
    file_id text NOT NULL,
    checksum text NOT NULL,
    bytes bigint NOT NULL,
    PRIMARY KEY (run_id,file_id)
);
