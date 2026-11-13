package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (s *Server) driveBackupLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := s.driveBackupTick(ctx); err != nil && ctx.Err() == nil {
			s.logger.Warn("drive backup worker unavailable", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) driveBackupTick(ctx context.Context) error {
	// A session lock makes the durable job safe across server processes and
	// lets startup take over a running job when the old process exits.
	owner, err := s.db.Acquire(ctx)
	if err != nil {
		return errors.New("backup database connection unavailable")
	}
	defer owner.Release()
	var locked bool
	if err := owner.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext('rotakey-drive-backup'))`).Scan(&locked); err != nil {
		return errors.New("backup worker lock unavailable")
	}
	if !locked {
		return nil
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := owner.Exec(c, `SELECT pg_advisory_unlock(hashtext('rotakey-drive-backup'))`); err != nil {
			_ = owner.Conn().Close(c)
		}
	}()
	run, cfg, err := claimDriveRun(ctx, owner, time.Now())
	if err != nil {
		return errors.New("backup job could not be loaded")
	}
	if run.ID == "" {
		return nil
	}
	state, message := "completed", ""
	if err := s.performDriveRun(ctx, owner, cfg, run, s.driveClient); err != nil {
		if ctx.Err() != nil {
			return nil
		} // resume the same job on the next process
		state, message = "failed", err.Error()
	}
	_, err = owner.Exec(ctx, `UPDATE drive_backup_runs SET state=$2,error_message=$3,finished_at=now() WHERE id=$1`, run.ID, state, message)
	if err != nil {
		return errors.New("backup result could not be saved")
	}
	return nil
}

func claimDriveRun(ctx context.Context, owner *pgxpool.Conn, now time.Time) (run driveRun, cfg driveSettings, err error) {
	tx, err := owner.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT revision FROM drive_backup_settings WHERE id=1 FOR UPDATE`).Scan(&revision); err != nil {
		return
	}
	cfg, err = readDriveSettings(ctx, tx)
	if err != nil {
		return
	}
	run, err = scanDriveRun(tx.QueryRow(ctx, `SELECT `+driveRunColumns+` FROM drive_backup_runs WHERE state IN ('queued','running')`))
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
		if !cfg.Connected || cfg.FolderID == "" || !cfg.Scheduled || cfg.FirstBackupPending || cfg.NextRun == nil || cfg.NextRun.After(now) {
			return
		}
		if err = enqueueDriveRun(ctx, tx, cfg, cfg.DeleteAfterUpload); err != nil {
			return
		}
		if _, err = tx.Exec(ctx, `UPDATE drive_backup_settings SET next_run_at=$1,revision=revision+1 WHERE id=1`, nextDriveRun(now, cfg.DailyTime)); err != nil {
			return
		}
		run, err = scanDriveRun(tx.QueryRow(ctx, `SELECT `+driveRunColumns+` FROM drive_backup_runs WHERE state='queued'`))
	}
	if err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE drive_backup_runs SET state='running' WHERE id=$1`, run.ID); err != nil {
		return
	}
	err = tx.Commit(ctx)
	return
}

type driveSnapshot struct {
	ID, Fingerprint string
	Created         time.Time
	Metadata        json.RawMessage
	Input, Output   []byte
}

func (r driveRun) selection() int16 {
	var bits int16
	if r.IncludeBodies {
		bits |= 1
	}
	if r.IncludeMetadata {
		bits |= 2
	}
	return bits
}

// Project metadata separately so PostgreSQL never hex-encodes large bodies
// just to remove them from the JSON. Hash the same values again at cleanup.
const driveMetadata = `jsonb_build_object(
 'id',l.id,'request_id',l.request_id,'model_id',l.model_id,'model_alias',l.model_alias,'provider_id',l.provider_id,
 'provider_name',l.provider_name,'credential_id',l.credential_id,'credential_label',l.credential_label,'endpoint',l.endpoint,
 'attempts',l.attempts,'routing_decisions',l.routing_decisions,'status_code',l.status_code,'latency_ms',l.latency_ms,
 'input_tokens',l.input_tokens,'output_tokens',l.output_tokens,'error_code',l.error_code,'error_message',l.error_message,
 'body_truncated',l.body_truncated,'created_at',l.created_at,'public_protocol',l.public_protocol,'upstream_protocol',l.upstream_protocol,
 'upstream_request_id',l.upstream_request_id,'queue_wait_ms',l.queue_wait_ms)`
const driveFingerprint = `md5((` + driveMetadata + `)::text || ':' || coalesce(md5(l.request_body_cipher),'') || ':' || coalesce(md5(l.response_body_cipher),''))`

func readDriveBatch(ctx context.Context, owner *pgxpool.Conn, run driveRun, after time.Time, afterID string) ([]driveSnapshot, error) {
	rows, err := owner.Query(ctx, `SELECT l.id,l.created_at,`+driveFingerprint+`,
 CASE WHEN $4 THEN `+driveMetadata+` END,
 CASE WHEN $5 THEN l.request_body_cipher END, CASE WHEN $5 THEN l.response_body_cipher END
 FROM request_logs l
 WHERE l.created_at<=$1 AND (l.created_at,l.id)>($2,$3)
 AND ($4 OR l.request_body_cipher IS NOT NULL OR l.response_body_cipher IS NOT NULL)
 AND NOT EXISTS (SELECT 1 FROM drive_backup_receipts b WHERE b.destination_id=$6 AND b.selection=$7 AND b.log_id=l.id
   AND (NOT $8 OR b.run_id=$9) AND b.fingerprint=`+driveFingerprint+`)
 ORDER BY l.created_at,l.id LIMIT 4000`, run.Cutoff, after, afterID, run.IncludeMetadata, run.IncludeBodies, run.Destination, run.selection(), run.DeleteSource, run.ID)
	if err != nil {
		return nil, errors.New("Saved requests could not be read. Source data was kept.")
	}
	defer rows.Close()
	batch := make([]driveSnapshot, 0, 256)
	size := 0
	for rows.Next() {
		var row driveSnapshot
		if err = rows.Scan(&row.ID, &row.Created, &row.Fingerprint, &row.Metadata, &row.Input, &row.Output); err != nil {
			return nil, errors.New("A saved request could not be read. Source data was kept.")
		}
		batch = append(batch, row)
		size += len(row.Metadata) + len(row.Input) + len(row.Output) + 128
		if size >= 12<<20 {
			break
		}
	}
	if rows.Err() != nil {
		return nil, errors.New("Reading saved requests was interrupted. Source data was kept.")
	}
	return batch, nil
}

func (s *Server) encodeDriveBatch(run driveRun, batch []driveSnapshot) ([]byte, error) {
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	enc := json.NewEncoder(z)
	enc.SetEscapeHTML(false)
	for _, row := range batch {
		entry := map[string]any{}
		if run.IncludeMetadata {
			entry["metadata"] = row.Metadata
		}
		if run.IncludeBodies {
			for key, sealed := range map[string][]byte{"input": row.Input, "output": row.Output} {
				entry[key] = nil
				if sealed == nil {
					continue
				}
				plain, err := s.vault.Decrypt(sealed)
				if err != nil {
					return nil, errors.New("A saved request body could not be decrypted. Source data was kept.")
				}
				if utf8.Valid(plain) {
					entry[key] = string(plain)
				} else {
					entry[key] = map[string]string{"encoding": "base64", "data": base64.StdEncoding.EncodeToString(plain)}
				}
			}
		}
		if err := enc.Encode(entry); err != nil {
			return nil, errors.New("A backup file could not be built. Source data was kept.")
		}
	}
	if err := z.Close(); err != nil {
		return nil, errors.New("A backup file could not be compressed. Source data was kept.")
	}
	return compressed.Bytes(), nil
}

func (s *Server) performDriveRun(ctx context.Context, owner *pgxpool.Conn, cfg driveSettings, run driveRun, connect func(context.Context, driveSettings) (*http.Client, error)) error {
	if cfg.Destination != run.Destination || cfg.FolderID == "" {
		return errDriveNotConnected
	}
	client, err := connect(ctx, cfg)
	if err != nil {
		return err
	}
	if !run.Verified {
		cursor := time.Time{}
		id := ""
		for ctx.Err() == nil {
			batch, err := readDriveBatch(ctx, owner, run, cursor, id)
			if err != nil {
				return err
			}
			if len(batch) == 0 {
				break
			}
			payload, err := s.encodeDriveBatch(run, batch)
			if err != nil {
				return err
			}
			name := fmt.Sprintf("rotakey-requests-%s-%s-%04d.jsonl.gz", run.Cutoff.UTC().Format("20060102T150405Z"), run.ID, run.Files+1)
			file, err := uploadDriveFile(ctx, client, cfg.FolderID, name, payload)
			if err != nil {
				return err
			}
			if err = saveDriveBatch(ctx, owner, run, batch, file); err != nil {
				return errors.New("Backup progress could not be saved. Source data was kept.")
			}
			run.Files++
			last := batch[len(batch)-1]
			cursor, id = last.Created, last.ID
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !run.DeleteSource {
		return nil
	}
	// Recheck all verified uploads together before deleting a single record,
	// including parts saved before a worker restart. Repeat on resumed cleanup.
	files, err := readDriveFiles(ctx, owner, run.ID)
	if err != nil {
		return errors.New("Backup receipts could not be read. Source data was kept.")
	}
	for _, file := range files {
		if err := verifyDriveFile(ctx, client, cfg.FolderID, file); err != nil {
			return err
		}
	}
	if _, err = owner.Exec(ctx, `UPDATE drive_backup_runs SET verified=true WHERE id=$1`, run.ID); err != nil {
		return errors.New("Backup verification could not be recorded. Source data was kept.")
	}
	return cleanDriveRun(ctx, owner, run)
}

func saveDriveBatch(ctx context.Context, owner *pgxpool.Conn, run driveRun, batch []driveSnapshot, file driveUploadReceipt) error {
	tx, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	ids, fingerprints := make([]string, len(batch)), make([]string, len(batch))
	for i, row := range batch {
		ids[i], fingerprints[i] = row.ID, row.Fingerprint
	}
	if _, err = tx.Exec(ctx, `INSERT INTO drive_backup_files(run_id,file_id,checksum,bytes) VALUES($1,$2,$3,$4)`, run.ID, file.ID, file.Checksum, file.Size); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO drive_backup_receipts(destination_id,log_id,selection,fingerprint,run_id)
 SELECT $1,l.id,$2,b.fingerprint,$3 FROM unnest($4::text[],$5::text[]) b(id,fingerprint) JOIN request_logs l USING(id)
 ON CONFLICT(destination_id,selection,log_id) DO UPDATE SET fingerprint=excluded.fingerprint,run_id=excluded.run_id`,
		run.Destination, run.selection(), run.ID, ids, fingerprints); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE drive_backup_runs SET uploaded=uploaded+$2,files=files+1,last_file_id=$3 WHERE id=$1`, run.ID, len(batch), file.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func readDriveFiles(ctx context.Context, owner *pgxpool.Conn, id string) ([]driveUploadReceipt, error) {
	rows, err := owner.Query(ctx, `SELECT file_id,checksum,bytes FROM drive_backup_files WHERE run_id=$1`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[driveUploadReceipt])
}

func cleanDriveRun(ctx context.Context, owner *pgxpool.Conn, run driveRun) error {
	// When only one kind is selected, do not discard data left out of the
	// backup. A metadata-only run may remove rows with no saved bodies.
	// Keep the lightweight ledger for daily and lifetime usage meters.
	if !run.IncludeBodies {
		return nil
	}
	modify := `UPDATE request_logs l SET request_body_cipher=NULL,response_body_cipher=NULL
 FROM batch WHERE l.id=batch.log_id AND ` + driveFingerprint + `=batch.fingerprint
 AND (l.request_body_cipher IS NOT NULL OR l.response_body_cipher IS NOT NULL)`
	last := ""
	for ctx.Err() == nil {
		var count int
		err := owner.QueryRow(ctx, `WITH batch AS MATERIALIZED (
 SELECT b.log_id,b.fingerprint FROM drive_backup_receipts b WHERE b.run_id=$1 AND b.log_id>$2 ORDER BY b.log_id LIMIT 1000
 ), cleaned AS (`+modify+` RETURNING l.id),
 progress AS (UPDATE drive_backup_runs SET deleted=deleted+(SELECT count(*) FROM cleaned) WHERE id=$1)
 SELECT count(*),coalesce(max(log_id),'') FROM batch`, run.ID, last).Scan(&count, &last)
		if err != nil {
			return errors.New("Drive backup is verified, but local cleanup could not finish. Try again.")
		}
		if count == 0 {
			return nil
		}
	}
	return ctx.Err()
}
