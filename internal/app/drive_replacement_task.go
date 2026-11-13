package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// ReplaceDriveTask is an operator-only recovery utility, never an HTTP route.
func ReplaceDriveTask(ctx context.Context, cfg Config, runID, manifestPath string) error {
	if runID == "" || manifestPath == "" {
		return errors.New("a stopped backup run ID and a private progress file are required")
	}
	db, err := openDatabase(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	v, err := newVault(cfg.MasterKey)
	if err != nil {
		return err
	}
	s := &Server{db: db, vault: v, cfg: cfg}
	owner, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer owner.Release()
	var locked bool
	if err = owner.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext('rotakey-drive-backup'))`).Scan(&locked); err != nil || !locked {
		return errors.New("backup worker is busy")
	}
	defer owner.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('rotakey-drive-backup'))`)
	run, err := scanDriveRun(owner.QueryRow(ctx, `SELECT `+driveRunColumns+` FROM drive_backup_runs WHERE id=$1`, runID))
	if err != nil {
		return err
	}
	if run.State == "running" || run.State == "queued" {
		return errors.New("stop the original backup before replacing its files")
	}
	settings, err := readDriveSettings(ctx, db)
	if err != nil {
		return err
	}
	if settings.Destination != run.Destination {
		return errors.New("destination changed")
	}
	client, err := s.driveClient(ctx, settings)
	if err != nil {
		return err
	}
	files, err := readDriveFiles(ctx, owner, run.ID)
	if err != nil {
		return err
	}
	type part struct {
		Old     driveUploadReceipt
		New     driveUploadReceipt
		Rows    int
		Removed bool
	}
	type progress struct {
		RunID string
		Parts []part
		Done  bool
	}
	state := progress{}
	if raw, e := os.ReadFile(manifestPath); e == nil {
		if err = json.Unmarshal(raw, &state); err != nil {
			return err
		}
	}
	if state.RunID != "" && state.RunID != runID {
		return errors.New("progress file belongs to a different backup")
	}
	state.RunID = runID
	if state.Done {
		return nil
	}
	save := func() error {
		raw, e := json.Marshal(state)
		if e != nil {
			return e
		}
		if e = os.WriteFile(manifestPath+".tmp", raw, 0600); e != nil {
			return e
		}
		return os.Rename(manifestPath+".tmp", manifestPath)
	}
	var mu sync.Mutex
	process := func(old driveUploadReceipt) error {
		var err error
		if err = verifyDriveFile(ctx, client, settings.FolderID, old); err != nil {
			return err
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", driveAPI+"/files/"+old.ID+"?alt=media&supportsAllDrives=true", nil)
		resp, e := client.Do(req)
		if e != nil {
			return errors.New("backup download failed")
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return errors.New("backup download rejected")
		}
		z, e := gzip.NewReader(resp.Body)
		if e != nil {
			resp.Body.Close()
			return e
		}
		var payload bytes.Buffer
		writer := gzip.NewWriter(&payload)
		enc := json.NewEncoder(writer)
		enc.SetEscapeHTML(false)
		dec := json.NewDecoder(z)
		count := 0
		for {
			var row map[string]json.RawMessage
			e = dec.Decode(&row)
			if e == io.EOF {
				break
			}
			if e != nil {
				z.Close()
				resp.Body.Close()
				return e
			}
			input, output := row["input"], row["output"]
			if (len(input) == 0 || string(input) == "null") && (len(output) == 0 || string(output) == "null") {
				continue
			}
			if e = enc.Encode(map[string]json.RawMessage{"input": input, "output": output}); e != nil {
				return e
			}
			count++
		}
		z.Close()
		resp.Body.Close()
		if e = writer.Close(); e != nil {
			return e
		}
		p := part{Old: old, Rows: count}
		if count > 0 {
			p.New, err = uploadDriveFile(ctx, client, settings.FolderID, "rotakey-input-output-"+old.ID+".jsonl.gz", payload.Bytes())
			if err != nil {
				return err
			}
		}
		mu.Lock()
		defer mu.Unlock()
		state.Parts = append(state.Parts, p)
		if err = save(); err != nil {
			return err
		}
		fmt.Printf("Body-only replacement: %d/%d files, %d bodies in this part\n", len(state.Parts), len(files), count)
		return nil
	}
	existing := map[string]bool{}
	for _, p := range state.Parts {
		existing[p.Old.ID] = true
	}
	jobs := make(chan driveUploadReceipt)
	var wg sync.WaitGroup
	var firstErr error
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for old := range jobs {
				if e := process(old); e != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = e
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, old := range files {
		if !existing[old.ID] {
			mu.Lock()
			failed := firstErr != nil
			mu.Unlock()
			if failed {
				break
			}
			jobs <- old
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	// No old file is removed until every replacement is independently verified.
	for _, p := range state.Parts {
		if p.New.ID != "" {
			if err = verifyDriveFile(ctx, client, settings.FolderID, p.New); err != nil {
				return err
			}
		}
	}
	for i, p := range state.Parts {
		if p.Removed {
			continue
		}
		if err = driveJSON(ctx, client, "DELETE", driveAPI+"/files/"+p.Old.ID+"?supportsAllDrives=true", nil, nil); err != nil {
			return err
		}
		state.Parts[i].Removed = true
		if err = save(); err != nil {
			return err
		}
		fmt.Printf("Removed previous archive %d/%d\n", i+1, len(state.Parts))
	}
	// Preserve the usage ledger, clearing verified source bodies in small batches.
	run.IncludeBodies = true
	if err = cleanDriveRun(ctx, owner, run); err != nil {
		return err
	}
	_, err = owner.Exec(ctx, `UPDATE drive_backup_runs SET state='completed',error_message='Replaced with verified input/output-only files; original files removed; metadata retained',finished_at=$2 WHERE id=$1`, run.ID, time.Now())
	if err != nil {
		return err
	}
	state.Done = true
	if err = save(); err != nil {
		return err
	}
	fmt.Println("Replacement and verified body cleanup completed")
	return nil
}
