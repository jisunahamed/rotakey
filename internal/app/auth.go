package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

const sessionCookieName = "relay_session"

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	var exists bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM admins)`).Scan(&exists); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Could not check setup status.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"setup_required": !exists})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Bootstrap-Token")), []byte(s.cfg.BootstrapToken)) != 1 {
		writeError(w, http.StatusForbidden, "invalid_bootstrap_token", "Bootstrap token is invalid.")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if decodeJSON(w, r, 32<<10, &input) != nil {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if len(input.Username) < 3 || len(input.Username) > 80 || len(input.Password) < 12 || len(input.Password) > 256 {
		writeError(w, http.StatusBadRequest, "invalid_setup", "Username must be 3–80 characters and password must be 12–256 characters.")
		return
	}
	passwordHash, err := hashPassword(input.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not secure the admin password.")
		return
	}
	adminID, err := newID("adm")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not create admin account.")
		return
	}
	gatewayKey, err := randomToken("gw_", 32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not create gateway key.")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Could not begin setup.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var exists bool
	if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM admins)`).Scan(&exists); err != nil || exists {
		writeError(w, http.StatusConflict, "already_configured", "Setup has already been completed.")
		return
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO admins (id, username, password_hash) VALUES ($1, $2, $3)
	`, adminID, input.Username, passwordHash); err != nil {
		writeError(w, http.StatusConflict, "setup_failed", "Admin account could not be created.")
		return
	}
	keyHash := hashAPIKey(gatewayKey)
	prefix := gatewayKey
	if len(prefix) > 11 {
		prefix = prefix[:11]
	}
	if _, err := tx.Exec(r.Context(), `
		UPDATE app_settings SET gateway_key_hash = $1, gateway_key_prefix = $2, updated_at = NOW()
		WHERE id = 1
	`, keyHash, prefix); err != nil {
		writeError(w, http.StatusInternalServerError, "setup_failed", "Gateway key could not be stored.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "setup_failed", "Setup could not be completed.")
		return
	}

	csrf, err := s.createSession(w, r.Context(), adminID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "session_unavailable", "Setup completed, but login session could not be created.")
		return
	}
	s.audit(r.Context(), adminID, "setup.complete", "system", "", map[string]any{"username": input.Username})
	writeJSON(w, http.StatusCreated, map[string]any{
		"gateway_key": gatewayKey,
		"csrf_token":  csrf,
		"message":     "Setup complete. Save the gateway key now; it will not be shown again.",
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	key := "login:" + ip
	count, err := s.redis.Incr(r.Context(), key).Result()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "session_unavailable", "Login service is unavailable.")
		return
	}
	if count == 1 {
		_ = s.redis.Expire(r.Context(), key, 15*time.Minute).Err()
	}
	if count > 8 {
		w.Header().Set("Retry-After", "900")
		writeError(w, http.StatusTooManyRequests, "login_rate_limited", "Too many login attempts. Try again later.")
		return
	}

	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if decodeJSON(w, r, 32<<10, &input) != nil {
		return
	}
	var adminID, passwordHash string
	err = s.db.QueryRow(r.Context(), `
		SELECT id, password_hash FROM admins WHERE username = $1
	`, strings.TrimSpace(input.Username)).Scan(&adminID, &passwordHash)
	if err != nil || !verifyPassword(passwordHash, input.Password) {
		time.Sleep(250 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Username or password is incorrect.")
		return
	}
	_ = s.redis.Del(r.Context(), key).Err()
	csrf, err := s.createSession(w, r.Context(), adminID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "session_unavailable", "Session could not be created.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"csrf_token": csrf})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sign in is required.")
		return
	}
	var username string
	if err := s.db.QueryRow(r.Context(), `SELECT username FROM admins WHERE id = $1`, session.AdminID).Scan(&username); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Admin account is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": username, "csrf_token": session.CSRF})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.redis.Del(r.Context(), "session:"+cookie.Value).Err()
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createSession(w http.ResponseWriter, ctx context.Context, adminID string) (string, error) {
	sessionID, err := randomToken("", 32)
	if err != nil {
		return "", err
	}
	csrf, err := randomToken("csrf_", 24)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(sessionData{AdminID: adminID, CSRF: csrf})
	if err != nil {
		return "", err
	}
	if err := s.redis.Set(ctx, "session:"+sessionID, payload, s.cfg.SessionTTL).Err(); err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: sessionID, Path: "/",
		HttpOnly: true, Secure: s.cfg.SessionCookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(s.cfg.SessionTTL.Seconds()),
	})
	return csrf, nil
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: s.cfg.SessionCookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || len(cookie.Value) < 32 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Sign in is required.")
			return
		}
		raw, err := s.redis.Get(r.Context(), "session:"+cookie.Value).Bytes()
		if err != nil {
			writeError(w, http.StatusUnauthorized, "session_expired", "Your session has expired.")
			return
		}
		var session sessionData
		if json.Unmarshal(raw, &session) != nil {
			writeError(w, http.StatusUnauthorized, "session_expired", "Your session has expired.")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			token := r.Header.Get("X-CSRF-Token")
			if len(token) == 0 || subtle.ConstantTimeCompare([]byte(token), []byte(session.CSRF)) != 1 {
				writeError(w, http.StatusForbidden, "csrf_failed", "Security token is missing or invalid.")
				return
			}
		}
		ctx := context.WithValue(r.Context(), adminContextKey{}, session.AdminID)
		ctx = context.WithValue(ctx, sessionContextKey{}, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireGatewayKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, anthropicKey, parseErr := gatewayKeysFromHeaders(r.Header)
		if parseErr != nil {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", parseErr.Error())
			return
		}
		if bearer != "" && anthropicKey != "" && bearer != anthropicKey {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "Authorization and x-api-key contain different gateway keys.")
			return
		}
		key := bearer
		if key == "" {
			key = anthropicKey
		}
		_, expected, err := s.settings(r.Context())
		if err != nil || len(expected) == 0 || key == "" || !secureEqual(hashAPIKey(key), expected) {
			writeProtocolError(w, r, http.StatusUnauthorized, "authentication_error", "A valid Rotakey gateway key is required.")
			return
		}
		if isAnthropicRequest(r) {
			version := strings.TrimSpace(r.Header.Get("Anthropic-Version"))
			if version == "" {
				writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "The anthropic-version header is required.")
				return
			}
			if version != "2023-06-01" {
				writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "Rotakey currently supports anthropic-version 2023-06-01.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func gatewayKeysFromHeaders(headers http.Header) (string, string, error) {
	auth := strings.TrimSpace(headers.Get("Authorization"))
	bearer := ""
	if auth != "" {
		parts := strings.Fields(auth)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			return "", "", errors.New("Authorization must use a single Bearer gateway key.")
		}
		bearer = parts[1]
	}
	return bearer, strings.TrimSpace(headers.Get("X-Api-Key")), nil
}

func isAnthropicRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/v1/messages") ||
		strings.HasPrefix(r.URL.Path, "/v1/files") ||
		r.Header.Get("Anthropic-Version") != "" || r.Header.Get("X-Api-Key") != ""
}

func writeProtocolError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if isAnthropicRequest(r) {
		writeAnthropicError(w, r, status, code, message)
		return
	}
	writeError(w, status, code, message)
}

func writeAnthropicError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	requestID := requestIDFromContext(r.Context())
	writeJSON(w, status, map[string]any{
		"type":       "error",
		"error":      map[string]any{"type": code, "message": message},
		"request_id": requestID,
	})
}

type sessionContextKey struct{}

func sessionFromContext(ctx context.Context) (sessionData, bool) {
	value, ok := ctx.Value(sessionContextKey{}).(sessionData)
	return value, ok
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
