package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const requestArchiveSchema = `
CREATE SCHEMA IF NOT EXISTS rotakey_archive;
REVOKE ALL ON SCHEMA rotakey_archive FROM PUBLIC;
CREATE TABLE IF NOT EXISTS rotakey_archive.requests (
 instance_id text NOT NULL, request_id text NOT NULL, model_alias text NOT NULL,
 provider_name text NOT NULL, created_at timestamptz NOT NULL,
 metadata jsonb NOT NULL, request_body text, response_body text,
 source_fingerprint text NOT NULL, archived_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(instance_id,request_id)
);
ALTER TABLE rotakey_archive.requests ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON rotakey_archive.requests FROM PUBLIC;
CREATE INDEX IF NOT EXISTS archive_requests_created_idx ON rotakey_archive.requests(created_at DESC);
CREATE INDEX IF NOT EXISTS archive_requests_model_idx ON rotakey_archive.requests(model_alias,created_at DESC);
CREATE TABLE IF NOT EXISTS rotakey_archive.runs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, instance_id text NOT NULL,
 started_at timestamptz NOT NULL DEFAULT now(), completed_at timestamptz,
 copied bigint NOT NULL DEFAULT 0, deleted bigint NOT NULL DEFAULT 0,
 status text NOT NULL DEFAULT 'running'
);
ALTER TABLE rotakey_archive.runs ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON rotakey_archive.runs FROM PUBLIC;
`

type ArchiveResult struct{ Copied, Deleted int64 }
type archiveRow struct {
	id, requestID, model, provider, fingerprint string
	created                                     time.Time
	metadata                                    []byte
	request, response                           *string
}

func archivePool(ctx context.Context, raw string, remote bool) (*pgxpool.Pool, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("invalid archive database configuration")
	}
	if remote {
		q := u.Query()
		q.Set("sslmode", "verify-full")
		u.RawQuery = q.Encode()
	}
	cfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, errors.New("invalid archive database configuration")
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.ConnectTimeout = 20 * time.Second
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return pgxpool.NewWithConfig(ctx, cfg)
}

// ArchiveRequests copies and verifies each batch in a committed remote
// transaction before conditional local deletion. A failed or changed row stays
// local. Remote records are idempotent across crashes and repeated runs.
func ArchiveRequests(ctx context.Context, sourceURL, destinationURL, instance string, masterKey []byte, progress func(ArchiveResult)) (result ArchiveResult, err error) {
	if instance == "" || sourceURL == "" || destinationURL == "" || sourceURL == destinationURL {
		return result, errors.New("archive needs distinct databases and an instance identifier")
	}
	v, err := newVault(masterKey)
	if err != nil {
		return result, err
	}
	source, err := archivePool(ctx, sourceURL, false)
	if err != nil {
		return result, err
	}
	defer source.Close()
	remote, err := archivePool(ctx, destinationURL, true)
	if err != nil {
		return result, err
	}
	defer remote.Close()
	return archiveWithPools(ctx, source, remote, instance, v, progress)
}

func archiveWithPools(ctx context.Context, source, remote *pgxpool.Pool, instance string, v *vault, progress func(ArchiveResult)) (result ArchiveResult, err error) {
	owner, err := source.Acquire(ctx)
	if err != nil {
		return result, errors.New("source archive connection unavailable")
	}
	defer owner.Release()
	var locked bool
	if err = owner.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext('rotakey-request-archive'))`).Scan(&locked); err != nil || !locked {
		return result, errors.New("another request archive is active")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = owner.Exec(c, `SELECT pg_advisory_unlock(hashtext('rotakey-request-archive'))`)
	}()
	if _, err = remote.Exec(ctx, requestArchiveSchema); err != nil {
		return result, errors.New("archive schema could not be initialized")
	}
	var runID int64
	if err = remote.QueryRow(ctx, `INSERT INTO rotakey_archive.runs(instance_id) VALUES($1) RETURNING id`, instance).Scan(&runID); err != nil {
		return result, errors.New("archive run could not be created")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status := "completed"
		if err != nil {
			status = "failed"
		}
		_, _ = remote.Exec(c, `UPDATE rotakey_archive.runs SET completed_at=now(),copied=$2,deleted=$3,status=$4 WHERE id=$1`, runID, result.Copied, result.Deleted, status)
	}()
	cutoff := time.Now().UTC()
	cursor := time.Time{}
	cursorID := ""
	for {
		rows, queryErr := owner.Query(ctx, `SELECT id,request_id,model_alias,provider_name,created_at,
 to_jsonb(l)-'request_body_cipher'-'response_body_cipher',request_body_cipher,response_body_cipher,
 md5(to_jsonb(l)::text) FROM request_logs l
 WHERE created_at<=$1 AND (created_at,id)>($2,$3) ORDER BY created_at,id LIMIT 1000`, cutoff, cursor, cursorID)
		if queryErr != nil {
			return result, errors.New("source request batch could not be read")
		}
		batch := []archiveRow{}
		bodyBytes := 0
		for rows.Next() {
			var row archiveRow
			var requestCipher, responseCipher []byte
			if err = rows.Scan(&row.id, &row.requestID, &row.model, &row.provider, &row.created, &row.metadata, &requestCipher, &responseCipher, &row.fingerprint); err != nil {
				rows.Close()
				return result, errors.New("source request could not be decoded")
			}
			for _, part := range []struct {
				cipher []byte
				dest   **string
			}{{requestCipher, &row.request}, {responseCipher, &row.response}} {
				if part.cipher != nil {
					plain, decryptErr := v.Decrypt(part.cipher)
					if decryptErr != nil {
						rows.Close()
						return result, fmt.Errorf("request %s could not be decrypted; source retained", row.requestID)
					}
					text := string(plain)
					*part.dest = &text
				}
			}
			batch = append(batch, row)
			bodyBytes += len(requestCipher) + len(responseCipher)
			if bodyBytes >= 8<<20 {
				break
			}
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil {
			return result, errors.New("source request batch interrupted")
		}
		if len(batch) == 0 {
			return result, nil
		}
		if err = copyArchiveBatch(ctx, remote, instance, batch); err != nil {
			return result, err
		}
		result.Copied += int64(len(batch))
		ids, fingerprints := make([]string, len(batch)), make([]string, len(batch))
		for i, row := range batch {
			ids[i], fingerprints[i] = row.id, row.fingerprint
		}
		tag, deleteErr := owner.Exec(ctx, `DELETE FROM request_logs l USING unnest($1::text[],$2::text[]) x(id,fingerprint) WHERE l.id=x.id AND md5(to_jsonb(l)::text)=x.fingerprint`, ids, fingerprints)
		if deleteErr != nil {
			return result, errors.New("archive verified but local deletion failed; rerun safely")
		}
		result.Deleted += tag.RowsAffected()
		cursor, cursorID = batch[len(batch)-1].created, batch[len(batch)-1].id
		if progress != nil {
			progress(result)
		}
	}
}

func copyArchiveBatch(ctx context.Context, remote *pgxpool.Pool, instance string, rows []archiveRow) error {
	tx, err := remote.Begin(ctx)
	if err != nil {
		return errors.New("remote archive transaction unavailable")
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err = tx.Exec(ctx, `CREATE TEMP TABLE archive_batch (LIKE rotakey_archive.requests INCLUDING DEFAULTS) ON COMMIT DROP`); err != nil {
		return errors.New("remote archive staging unavailable")
	}
	values := make([][]any, len(rows))
	for i, r := range rows {
		values[i] = []any{instance, r.requestID, r.model, r.provider, r.created, json.RawMessage(r.metadata), r.request, r.response, r.fingerprint}
	}
	columns := []string{"instance_id", "request_id", "model_alias", "provider_name", "created_at", "metadata", "request_body", "response_body", "source_fingerprint"}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"archive_batch"}, columns, pgx.CopyFromRows(values)); err != nil {
		return errors.New("remote archive copy failed; source retained")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO rotakey_archive.requests SELECT * FROM archive_batch
 ON CONFLICT(instance_id,request_id) DO UPDATE SET model_alias=excluded.model_alias,provider_name=excluded.provider_name,
 created_at=excluded.created_at,metadata=excluded.metadata,request_body=excluded.request_body,response_body=excluded.response_body,
 source_fingerprint=excluded.source_fingerprint,archived_at=now()`); err != nil {
		return errors.New("remote archive merge failed; source retained")
	}
	var verified int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM archive_batch b JOIN rotakey_archive.requests a USING(instance_id,request_id)
 WHERE a.metadata=b.metadata AND a.request_body IS NOT DISTINCT FROM b.request_body
 AND a.response_body IS NOT DISTINCT FROM b.response_body AND a.source_fingerprint=b.source_fingerprint`).Scan(&verified); err != nil || verified != len(rows) {
		return errors.New("archive verification failed; source retained")
	}
	if err = tx.Commit(ctx); err != nil {
		return errors.New("archive commit failed; source retained")
	}
	return nil
}
