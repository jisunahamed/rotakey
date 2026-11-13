package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	driveScope      = "https://www.googleapis.com/auth/drive"
	driveFolderType = "application/vnd.google-apps.folder"
	drivePath       = "/api/admin/drive-backup"
	driveAPI        = "https://www.googleapis.com/drive/v3"
	driveUploadAPI  = "https://www.googleapis.com/upload/drive/v3/files"
)

var (
	driveID       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	driveClientID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}\.apps\.googleusercontent\.com$`)
	driveHTTP     = &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
)

type driveOAuthState struct {
	Session, Verifier, ClientID string
	Secret                      []byte
	Revision                    int64
}

func (s *Server) driveRedirectURI() string {
	u, err := url.Parse(s.cfg.PublicBaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return ""
	}
	return strings.TrimRight(u.String(), "/") + drivePath + "/callback"
}

func driveOAuth(clientID, secret, redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: clientID, ClientSecret: secret, RedirectURL: redirect, Scopes: []string{driveScope},
		Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", AuthStyle: oauth2.AuthStyleInParams}}
}

func (s *Server) handleDriveConnect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if decodeJSON(w, r, 4096, &in) != nil {
		return
	}
	in.ClientID, in.ClientSecret = strings.TrimSpace(in.ClientID), strings.TrimSpace(in.ClientSecret)
	redirect := s.driveRedirectURI()
	if redirect == "" || !driveClientID.MatchString(in.ClientID) || in.ClientSecret == "" || len(in.ClientSecret) > 512 {
		writeError(w, 400, "invalid_drive_connection", "Enter the client ID and secret of a Google Web application. Rotakey must have a valid HTTPS public URL.")
		return
	}
	cfg, err := readDriveSettings(r.Context(), s.db)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	secret, err := s.vault.Encrypt([]byte(in.ClientSecret))
	if err != nil {
		driveAdminError(w, err)
		return
	}
	state, err := randomToken("", 32)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	cookie, _ := r.Cookie(sessionCookieName) // validated by requireAdmin
	pending := driveOAuthState{Session: fmt.Sprintf("%x", hashAPIKey(cookie.Value)), Verifier: oauth2.GenerateVerifier(), ClientID: in.ClientID, Secret: secret, Revision: cfg.Revision}
	raw, err := json.Marshal(pending)
	if err != nil {
		driveAdminError(w, err)
		return
	}
	if err := s.redis.Set(r.Context(), "drive-oauth:"+state, raw, 10*time.Minute).Err(); err != nil {
		driveAdminError(w, err)
		return
	}
	link := driveOAuth(in.ClientID, in.ClientSecret, redirect).AuthCodeURL(state, oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent select_account"), oauth2.S256ChallengeOption(pending.Verifier))
	writeJSON(w, 200, map[string]string{"authorization_url": link})
}

func (s *Server) handleDriveCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	finish := func(result string) { http.Redirect(w, r, "/admin/settings?drive="+result, http.StatusSeeOther) }
	state := r.URL.Query().Get("state")
	if !driveID.MatchString(state) {
		finish("expired")
		return
	}
	var pending driveOAuthState
	raw, err := s.redis.GetDel(r.Context(), "drive-oauth:"+state).Bytes()
	cookie, _ := r.Cookie(sessionCookieName)
	if err != nil || json.Unmarshal(raw, &pending) != nil || !secureEqual([]byte(pending.Session), []byte(fmt.Sprintf("%x", hashAPIKey(cookie.Value)))) {
		finish("expired")
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		finish("denied")
		return
	}
	secret, err := s.vault.Decrypt(pending.Secret)
	if err != nil {
		finish("failed")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), oauth2.HTTPClient, driveHTTP), 35*time.Second)
	defer cancel()
	config := driveOAuth(pending.ClientID, string(secret), s.driveRedirectURI())
	token, err := config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(pending.Verifier))
	if err != nil || token.RefreshToken == "" {
		finish("failed")
		return
	}
	var about struct {
		User struct {
			DisplayName  string `json:"displayName"`
			EmailAddress string `json:"emailAddress"`
		}
	}
	client := config.Client(ctx, token)
	if err = driveJSON(ctx, client, http.MethodGet, driveAPI+"/about?fields=user(displayName,emailAddress)", nil, &about); err != nil {
		finish("permission")
		return
	}
	if granted, ok := token.Extra("scope").(string); ok && !strings.Contains(" "+granted+" ", " "+driveScope+" ") {
		finish("permission")
		return
	}
	sealed, err := s.vault.Encrypt([]byte(token.RefreshToken))
	if err != nil {
		finish("failed")
		return
	}
	label := about.User.EmailAddress
	if label == "" {
		label = about.User.DisplayName
	}
	if label == "" {
		label = "Google Drive"
	}
	tx, err := s.lockDriveSettings(ctx, pending.Revision)
	if err != nil {
		finish("changed")
		return
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE drive_backup_settings SET client_id=$1,client_secret_cipher=$2,refresh_token_cipher=$3,
 account_label=$4,folder_id='',folder_name='',destination_id='',scheduled=false,delete_after_upload=false,
 first_backup_pending=true,next_run_at=NULL,revision=revision+1 WHERE id=1`, pending.ClientID, pending.Secret, sealed, label)
	if err != nil || tx.Commit(ctx) != nil {
		finish("failed")
		return
	}
	finish("connected")
}

func (s *Server) driveClient(ctx context.Context, cfg driveSettings) (*http.Client, error) {
	if !cfg.Connected {
		return nil, errDriveNotConnected
	}
	secret, err := s.vault.Decrypt(cfg.Secret)
	if err != nil {
		return nil, errors.New("Google Drive credentials could not be read. Reconnect Google Drive.")
	}
	refresh, err := s.vault.Decrypt(cfg.Refresh)
	if err != nil {
		return nil, errors.New("Google Drive token could not be read. Reconnect Google Drive.")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, driveHTTP)
	return driveOAuth(cfg.ClientID, string(secret), s.driveRedirectURI()).Client(ctx, &oauth2.Token{RefreshToken: string(refresh)}), nil
}

// Only expose fixed diagnostic text, never Google's raw token or upload errors.
func driveCallError(err error) error {
	var token *oauth2.RetrieveError
	if errors.As(err, &token) {
		return errors.New("Google authorization failed. Reconnect your Google account.")
	}
	return errors.New("Google Drive could not be reached. Try again.")
}

func driveStatusError(resp *http.Response) error {
	var failure struct {
		Error struct{ Errors []struct{ Reason string } }
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&failure)
	for _, item := range failure.Error.Errors {
		switch item.Reason {
		case "storageQuotaExceeded":
			return errors.New("Google Drive storage is full. Free space or select another account.")
		case "rateLimitExceeded", "userRateLimitExceeded":
			return errors.New("Google Drive rate limit reached. Try again later.")
		case "accessNotConfigured", "serviceDisabled":
			return errors.New("Enable the Google Drive API in the OAuth client's Google Cloud project.")
		}
	}
	return fmt.Errorf("Google Drive returned HTTP %d. Check the connection and folder permissions, then try again.", resp.StatusCode)
}

func driveJSON(ctx context.Context, client *http.Client, method, endpoint string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return driveCallError(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return driveCallError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return driveStatusError(resp)
	}
	if out != nil && json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out) != nil {
		return errors.New("Google Drive returned an unreadable reply.")
	}
	return nil
}
