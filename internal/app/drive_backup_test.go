package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type driveTestTransport func(*http.Request) (*http.Response, error)

func (f driveTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDriveScheduleUsesDhakaMidnight(t *testing.T) {
	before := time.Date(2026, 11, 14, 17, 59, 59, 0, time.UTC)
	if got := nextDriveRun(before, "00:00"); !got.Equal(before.Add(time.Second)) {
		t.Fatalf("next midnight: %s", got)
	}
	if got := nextDriveRun(before.Add(time.Second), "00:00"); !got.Equal(before.Add(24*time.Hour + time.Second)) {
		t.Fatalf("following midnight: %s", got)
	}
}

func TestDriveUploadResumesAndChecksStoredBytes(t *testing.T) {
	payload := bytes.Repeat([]byte{7}, (4<<20)+13)
	var saved []byte
	var puts int
	client := &http.Client{Transport: driveTestTransport(func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case "POST":
			return &http.Response{StatusCode: 200, Header: http.Header{"Location": {driveUploadAPI + "?upload_id=abc"}}, Body: http.NoBody}, nil
		case "PUT":
			puts++
			switch puts {
			case 1:
				saved, _ = io.ReadAll(r.Body)
				return nil, io.ErrUnexpectedEOF // first chunk received, response lost
			case 2:
				if got := r.Header.Get("Content-Range"); got != fmt.Sprintf("bytes */%d", len(payload)) {
					t.Fatalf("missing status probe: %s", got)
				}
				return &http.Response{StatusCode: 308, Header: http.Header{"Range": {fmt.Sprintf("bytes=0-%d", len(saved)-1)}}, Body: http.NoBody}, nil
			default:
				if got := r.Header.Get("Content-Range"); !strings.HasPrefix(got, fmt.Sprintf("bytes %d-", len(saved))) {
					t.Fatalf("bad resume offset: %s", got)
				}
				last, _ := io.ReadAll(r.Body)
				saved = append(saved, last...)
				return driveReply(200, `{"id":"file-a"}`), nil
			}
		case "GET":
			return driveReply(200, fmt.Sprintf(`{"id":"file-a","parents":["folder"],"size":"%d","sha256Checksum":"%x"}`, len(saved), sha256.Sum256(saved))), nil
		}
		return nil, fmt.Errorf("unexpected request: %s", r.Method)
	})}
	receipt, err := uploadDriveFile(context.Background(), client, "folder", "archive.jsonl.gz", payload)
	if err != nil || receipt.ID != "file-a" || !bytes.Equal(saved, payload) || puts != 3 {
		t.Fatalf("resume failed: %+v, %v, puts=%d", receipt, err, puts)
	}
}

func driveReply(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestIntegrationDriveBackupSelectionAndVerifiedDeletion(t *testing.T) {
	for _, tc := range []struct {
		name                                            string
		bodies, metadata, del, badChecksum, changed     bool
		wantRows, wantBodies, wantUploaded, wantDeleted int
	}{
		{"upload-only", true, true, false, false, false, 2, 1, 2, 0},
		{"all-with-cleanup", true, true, true, false, false, 2, 0, 2, 1},
		{"body-only", true, false, true, false, false, 2, 0, 1, 1},
		{"metadata-only", false, true, true, false, false, 2, 1, 2, 0},
		{"bad-remote-checksum", true, true, true, true, false, 2, 1, 2, 0},
		{"source-changed", true, true, true, false, true, 2, 1, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := repairIntegrationServer(t)
			ctx := context.Background()
			body, _ := s.vault.Encrypt([]byte(`{"model":"example","messages":["input"]}`))
			output, _ := s.vault.Encrypt([]byte("data: first\n\ndata: second\n\n"))
			if _, err := s.db.Exec(ctx, `INSERT INTO request_logs(id,request_id,model_alias,provider_name,credential_label,endpoint,status_code,latency_ms,request_body_cipher,response_body_cipher,created_at)
 VALUES('log1','req1','example','provider','key','/v1/responses',200,25,$1,$2,now()-interval '1 second'),
 ('log2','req2','example','provider','key','/v1/responses',200,30,NULL,NULL,now()-interval '1 second')`, body, output); err != nil {
				t.Fatal(err)
			}
			cfg := driveSettings{Destination: "dest", FolderID: "folder", IncludeBodies: tc.bodies, IncludeMetadata: tc.metadata}
			run := driveRun{ID: "run", Destination: "dest", Cutoff: time.Now(), IncludeBodies: tc.bodies, IncludeMetadata: tc.metadata, DeleteSource: tc.del}
			if _, err := s.db.Exec(ctx, `INSERT INTO drive_backup_runs(id,destination_id,delete_source,include_bodies,include_metadata,state) VALUES($1,$2,$3,$4,$5,'running')`, run.ID, run.Destination, run.DeleteSource, run.IncludeBodies, run.IncludeMetadata); err != nil {
				t.Fatal(err)
			}
			var content []byte
			var gets int
			client := &http.Client{Transport: driveTestTransport(func(r *http.Request) (*http.Response, error) {
				switch r.Method {
				case "POST":
					return &http.Response{StatusCode: 200, Header: http.Header{"Location": {driveUploadAPI + "?upload_id=test"}}, Body: http.NoBody}, nil
				case "PUT":
					content, _ = io.ReadAll(r.Body)
					return driveReply(200, `{"id":"file-a"}`), nil
				case "GET":
					gets++
					if gets == 2 {
						if tc.changed {
							if _, err := s.db.Exec(ctx, `UPDATE request_logs SET latency_ms=latency_ms+1 WHERE id='log1'`); err != nil {
								t.Fatal(err)
							}
						}
						if tc.badChecksum {
							return driveReply(200, fmt.Sprintf(`{"id":"file-a","parents":["folder"],"size":"%d","sha256Checksum":"wrong"}`, len(content))), nil
						}
					}
					return driveReply(200, fmt.Sprintf(`{"id":"file-a","parents":["folder"],"size":"%d","sha256Checksum":"%x"}`, len(content), sha256.Sum256(content))), nil
				}
				return nil, fmt.Errorf("unexpected request: %s", r.Method)
			})}
			owner, err := s.db.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Release()
			err = s.performDriveRun(ctx, owner, cfg, run, func(context.Context, driveSettings) (*http.Client, error) { return client, nil })
			if (err != nil) != tc.badChecksum {
				t.Fatalf("backup error=%v", err)
			}
			var rows, bodies, uploaded, deleted int
			if err := s.db.QueryRow(ctx, `SELECT count(*),count(request_body_cipher) FROM request_logs`).Scan(&rows, &bodies); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(ctx, `SELECT uploaded,deleted FROM drive_backup_runs WHERE id='run'`).Scan(&uploaded, &deleted); err != nil {
				t.Fatal(err)
			}
			if rows != tc.wantRows || bodies != tc.wantBodies || uploaded != tc.wantUploaded || deleted != tc.wantDeleted {
				t.Fatalf("rows=%d bodies=%d uploaded=%d deleted=%d", rows, bodies, uploaded, deleted)
			}
			z, err := gzip.NewReader(bytes.NewReader(content))
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			dec := json.NewDecoder(z)
			for i := 0; i < tc.wantUploaded; i++ {
				var row map[string]any
				if err := dec.Decode(&row); err != nil {
					t.Fatal(err)
				}
				if _, ok := row["metadata"]; ok != tc.metadata {
					t.Fatalf("metadata selection: %+v", row)
				}
				if _, ok := row["input"]; ok != tc.bodies {
					t.Fatalf("body selection: %+v", row)
				}
				if tc.bodies && i == 0 && row["output"] != "data: first\n\ndata: second\n\n" {
					t.Fatalf("stream bytes changed: %+v", row)
				}
			}
		})
	}
}

func TestIntegrationDriveScheduleWaitsForFirstDecision(t *testing.T) {
	s := repairIntegrationServer(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := s.db.Exec(ctx, `UPDATE drive_backup_settings SET scheduled=true,first_backup_pending=true,next_run_at=$1,
 folder_id='folder',destination_id='dest',refresh_token_cipher=decode('01','hex'),delete_after_upload=true WHERE id=1`, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	owner, err := s.db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Release()
	checkDriveClaim(t, ctx, owner, now, false)
	if _, err := s.db.Exec(ctx, `UPDATE drive_backup_settings SET first_backup_pending=false WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	checkDriveClaim(t, ctx, owner, now, true)
}

func checkDriveClaim(t *testing.T, ctx context.Context, owner *pgxpool.Conn, now time.Time, expected bool) {
	t.Helper()
	run, _, err := claimDriveRun(ctx, owner, now)
	if err != nil || (run.ID != "") != expected || (expected && !run.DeleteSource) {
		t.Fatalf("claim: %+v %v", run, err)
	}
}
