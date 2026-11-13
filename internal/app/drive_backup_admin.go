package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	errDriveConflict     = errors.New("Backup settings changed or a backup is running. Refresh and try again.")
	errDriveNotConnected = errors.New("Connect Google Drive and choose a writable folder first.")
	driveZone            = time.FixedZone("Asia/Dhaka", 6*60*60)
)

type driveSettings struct {
	Connected          bool       `json:"connected"`
	ClientID           string     `json:"client_id,omitempty"`
	Account            string     `json:"account,omitempty"`
	FolderID           string     `json:"folder_id,omitempty"`
	FolderName         string     `json:"folder_name,omitempty"`
	DailyTime          string     `json:"daily_time"`
	Scheduled          bool       `json:"scheduled"`
	DeleteAfterUpload  bool       `json:"delete_after_upload"`
	IncludeBodies      bool       `json:"include_bodies"`
	IncludeMetadata    bool       `json:"include_metadata"`
	FirstBackupPending bool       `json:"first_backup_pending"`
	NextRun            *time.Time `json:"next_run_at"`
	Revision           int64      `json:"revision"`
	Destination        string     `json:"-"`
	Secret, Refresh    []byte     `json:"-"`
}

type driveRun struct {
	ID              string     `json:"id"`
	State           string     `json:"state"`
	Cutoff          time.Time  `json:"started_at"`
	Finished        *time.Time `json:"finished_at"`
	Uploaded        int64      `json:"uploaded"`
	Deleted         int64      `json:"deleted"`
	Files           int        `json:"files"`
	LastFile        string     `json:"last_file_id,omitempty"`
	Error           string     `json:"error,omitempty"`
	DeleteSource    bool       `json:"delete_source"`
	IncludeBodies   bool       `json:"include_bodies"`
	IncludeMetadata bool       `json:"include_metadata"`
	Destination     string     `json:"-"`
	Verified        bool       `json:"-"`
}

type driveQueryRow interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readDriveSettings(ctx context.Context, db driveQueryRow) (v driveSettings, err error) {
	err = db.QueryRow(ctx, `SELECT client_id,client_secret_cipher,refresh_token_cipher,account_label,folder_id,folder_name,
 daily_time,scheduled,delete_after_upload,include_bodies,include_metadata,first_backup_pending,next_run_at,revision,destination_id
 FROM drive_backup_settings WHERE id=1`).Scan(&v.ClientID, &v.Secret, &v.Refresh, &v.Account, &v.FolderID, &v.FolderName,
		&v.DailyTime, &v.Scheduled, &v.DeleteAfterUpload, &v.IncludeBodies, &v.IncludeMetadata, &v.FirstBackupPending, &v.NextRun, &v.Revision, &v.Destination)
	v.Connected = len(v.Refresh) != 0
	return v, err
}

const driveRunColumns = `id,state,cutoff,finished_at,uploaded,deleted,files,last_file_id,error_message,delete_source,include_bodies,include_metadata,destination_id,verified`

func scanDriveRun(row pgx.Row) (v driveRun, err error) {
	err = row.Scan(&v.ID, &v.State, &v.Cutoff, &v.Finished, &v.Uploaded, &v.Deleted, &v.Files, &v.LastFile, &v.Error, &v.DeleteSource, &v.IncludeBodies, &v.IncludeMetadata, &v.Destination, &v.Verified)
	return v, err
}

func nextDriveRun(now time.Time, daily string) time.Time {
	local := now.In(driveZone)
	clock, _ := time.Parse("15:04", daily)
	next := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, driveZone)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}

func (s *Server) registerDriveBackupRoutes(mux *http.ServeMux) {
	for p, f := range map[string]http.HandlerFunc{
		"GET " + drivePath:                  s.handleDriveStatus,
		"POST " + drivePath + "/connect":    s.handleDriveConnect,
		"GET " + drivePath + "/callback":    s.handleDriveCallback,
		"GET " + drivePath + "/folders":     s.handleDriveFolders,
		"PUT " + drivePath:                  s.handleDriveSave,
		"POST " + drivePath + "/run":        s.handleDriveRun,
		"POST " + drivePath + "/disconnect": s.handleDriveDisconnect,
	} {
		mux.Handle(p, s.requireAdmin(f))
	}
}

func driveAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errDriveConflict):
		writeError(w, 409, "drive_conflict", errDriveConflict.Error())
	case errors.Is(err, errDriveNotConnected):
		writeError(w, 400, "drive_not_connected", errDriveNotConnected.Error())
	default:
		writeError(w, 500, "drive_backup_error", "Backup settings could not be read or saved. Try again.")
	}
}

func (s *Server) handleDriveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	cfg, err := readDriveSettings(r.Context(), s.db)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT `+driveRunColumns+` FROM drive_backup_runs ORDER BY cutoff DESC LIMIT 10`)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	defer rows.Close()
	runs := []driveRun{}
	for rows.Next() {
		run, err := scanDriveRun(rows)
		if err != nil {
			driveAdminError(w, err)
			return
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		driveAdminError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"settings": cfg, "runs": runs, "redirect_uri": s.driveRedirectURI(), "preserve_local_logs": envBool("PRESERVE_REQUEST_LOGS", false)})
}

// Serialize settings changes and job creation on the same singleton row.
func (s *Server) lockDriveSettings(ctx context.Context, expected int64) (pgx.Tx, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	var version int64
	var active bool
	err = tx.QueryRow(ctx, `SELECT revision FROM drive_backup_settings WHERE id=1 FOR UPDATE`).Scan(&version)
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM drive_backup_runs WHERE state IN ('queued','running'))`).Scan(&active)
	}
	if err != nil || active || version != expected {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		if err == nil {
			err = errDriveConflict
		}
		return nil, err
	}
	return tx, nil
}

type driveFolder struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Mime         string `json:"mimeType,omitempty"`
	Trashed      bool   `json:"trashed,omitempty"`
	Capabilities struct {
		Add bool `json:"canAddChildren"`
	} `json:"capabilities"`
}

const driveFolderFields = "id,name,mimeType,trashed,capabilities(canAddChildren)"

func (s *Server) handleDriveFolders(w http.ResponseWriter, r *http.Request) {
	cfg, err := readDriveSettings(r.Context(), s.db)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	client, err := s.driveClient(ctx, cfg)
	if err != nil {
		writeError(w, 400, "drive_connection_error", err.Error())
		return
	}
	q := `trashed = false and mimeType = '` + driveFolderType + `'`
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > 200 {
		writeError(w, 400, "invalid_search", "Search is too long.")
		return
	}
	if search != "" {
		q += ` and name contains '` + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(search) + `'`
	}
	query := url.Values{"q": {q}, "fields": {"nextPageToken,files(" + driveFolderFields + ")"}, "orderBy": {"name_natural"}, "pageSize": {"100"}, "supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"}}
	if token := r.URL.Query().Get("page_token"); token != "" {
		query.Set("pageToken", token)
	}
	var result struct {
		Files []driveFolder `json:"files"`
		Next  string        `json:"nextPageToken"`
	}
	if err = driveJSON(ctx, client, "GET", driveAPI+"/files?"+query.Encode(), nil, &result); err != nil {
		writeError(w, 502, "drive_folders_error", err.Error())
		return
	}
	folders := []driveFolder{}
	if search == "" && query.Get("pageToken") == "" {
		folders = append(folders, driveFolder{ID: "root", Name: "My Drive"})
	}
	for _, f := range result.Files {
		if f.Capabilities.Add {
			folders = append(folders, f)
		}
	}
	writeJSON(w, 200, map[string]any{"folders": folders, "next_page_token": result.Next})
}

func (s *Server) handleDriveSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FolderID          string `json:"folder_id"`
		DailyTime         string `json:"daily_time"`
		Scheduled         bool   `json:"scheduled"`
		DeleteAfterUpload bool   `json:"delete_after_upload"`
		IncludeBodies     bool   `json:"include_bodies"`
		IncludeMetadata   bool   `json:"include_metadata"`
		Revision          int64  `json:"revision"`
	}
	if decodeJSON(w, r, 4096, &in) != nil {
		return
	}
	clock, err := time.Parse("15:04", in.DailyTime)
	if err != nil || clock.Format("15:04") != in.DailyTime || !driveID.MatchString(in.FolderID) || (!in.IncludeBodies && !in.IncludeMetadata) {
		writeError(w, 400, "invalid_drive_settings", "Choose a folder, a time and at least one kind of data.")
		return
	}
	cfg, err := readDriveSettings(r.Context(), s.db)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	if in.Revision != cfg.Revision {
		driveAdminError(w, errDriveConflict)
		return
	}
	if !cfg.Connected {
		driveAdminError(w, errDriveNotConnected)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	folder := driveFolder{ID: cfg.FolderID, Name: cfg.FolderName}
	// Turning a schedule off must work even if Google has revoked access.
	if cfg.FolderID != in.FolderID || in.Scheduled {
		client, err := s.driveClient(ctx, cfg)
		if err != nil {
			writeError(w, 400, "drive_connection_error", err.Error())
			return
		}
		query := url.Values{"fields": {driveFolderFields}, "supportsAllDrives": {"true"}}
		if err = driveJSON(ctx, client, "GET", driveAPI+"/files/"+in.FolderID+"?"+query.Encode(), nil, &folder); err != nil {
			writeError(w, 502, "drive_folder_error", err.Error())
			return
		}
		if !driveID.MatchString(folder.ID) || folder.Mime != driveFolderType || folder.Trashed || !folder.Capabilities.Add {
			writeError(w, 400, "invalid_drive_folder", "The selected Google Drive folder does not allow uploads.")
			return
		}
	}
	destination := cfg.Destination
	if cfg.FolderID != folder.ID || destination == "" {
		destination, err = newID("drive")
		if err != nil {
			driveAdminError(w, err)
			return
		}
	}
	var next *time.Time
	if in.Scheduled {
		value := nextDriveRun(time.Now(), in.DailyTime)
		if cfg.Scheduled && cfg.DailyTime == in.DailyTime && cfg.NextRun != nil {
			value = *cfg.NextRun
		}
		next = &value
	}
	tx, err := s.lockDriveSettings(ctx, in.Revision)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	_, err = tx.Exec(ctx, `UPDATE drive_backup_settings SET folder_id=$1,folder_name=$2,destination_id=$3,daily_time=$4,
 scheduled=$5,delete_after_upload=$6,include_bodies=$7,include_metadata=$8,next_run_at=$9,revision=revision+1 WHERE id=1`,
		folder.ID, folder.Name, destination, in.DailyTime, in.Scheduled, in.DeleteAfterUpload, in.IncludeBodies, in.IncludeMetadata, next)
	if err != nil || tx.Commit(ctx) != nil {
		driveAdminError(w, errors.New("save failed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDriveRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action   string `json:"action"`
		Revision int64  `json:"revision"`
	}
	if decodeJSON(w, r, 4096, &in) != nil {
		return
	}
	if in.Action != "upload" && in.Action != "upload_delete" && in.Action != "cancel" {
		writeError(w, 400, "invalid_backup_action", "Choose Upload only, Upload and delete, or Cancel.")
		return
	}
	ctx := r.Context()
	tx, err := s.lockDriveSettings(ctx, in.Revision)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	cfg, err := readDriveSettings(ctx, tx)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	if in.Action != "cancel" {
		if !cfg.Connected || cfg.FolderID == "" {
			driveAdminError(w, errDriveNotConnected)
			return
		}
		if err = enqueueDriveRun(ctx, tx, cfg, in.Action == "upload_delete"); err != nil {
			driveAdminError(w, err)
			return
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE drive_backup_settings SET first_backup_pending=false,revision=revision+1 WHERE id=1`); err != nil || tx.Commit(ctx) != nil {
		driveAdminError(w, errors.New("queue failed"))
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func enqueueDriveRun(ctx context.Context, tx pgx.Tx, cfg driveSettings, deleteSource bool) error {
	id, err := newID("backup")
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO drive_backup_runs(id,destination_id,delete_source,include_bodies,include_metadata) VALUES($1,$2,$3,$4,$5)`,
		id, cfg.Destination, deleteSource, cfg.IncludeBodies, cfg.IncludeMetadata)
	return err
}

func (s *Server) handleDriveDisconnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
	}
	if decodeJSON(w, r, 1024, &in) != nil {
		return
	}
	ctx := r.Context()
	tx, err := s.lockDriveSettings(ctx, in.Revision)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	_, err = tx.Exec(ctx, `UPDATE drive_backup_settings SET client_id='',client_secret_cipher=NULL,refresh_token_cipher=NULL,
 account_label='',folder_id='',folder_name='',destination_id='',scheduled=false,delete_after_upload=false,
 first_backup_pending=false,next_run_at=NULL,revision=revision+1 WHERE id=1`)
	if err != nil || tx.Commit(ctx) != nil {
		driveAdminError(w, errors.New("disconnect failed"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func driveJSONBody(value any) *bytes.Reader {
	data, _ := json.Marshal(value)
	return bytes.NewReader(data)
}
