package app

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

//go:embed all:webdist
var webFiles embed.FS

type Server struct {
	cfg            Config
	db             *pgxpool.Pool
	redis          *redis.Client
	vault          *vault
	limiter        *limiter
	logger         *slog.Logger
	handler        http.Handler
	activeRequests sync.Map
	release        releaseCache
}

func NewServer(ctx context.Context, cfg Config, logger *slog.Logger) (*Server, error) {
	if err := cfg.ValidateProduction(); err != nil {
		return nil, err
	}
	db, err := openDatabase(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		db.Close()
		return nil, err
	}
	redisClient := redis.NewClient(redisOptions)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		db.Close()
		_ = redisClient.Close()
		return nil, errors.New("redis is unavailable: " + err.Error())
	}
	vault, err := newVault(cfg.MasterKey)
	if err != nil {
		db.Close()
		_ = redisClient.Close()
		return nil, err
	}

	server := &Server{
		cfg: cfg, db: db, redis: redisClient, vault: vault,
		limiter: newLimiter(redisClient), logger: logger,
	}
	server.handler = server.routes()
	go server.retentionLoop(ctx)
	return server, nil
}

func (s *Server) Close() {
	s.db.Close()
	_ = s.redis.Close()
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.Handle("GET /api/auth/session", s.requireAdmin(http.HandlerFunc(s.handleSession)))
	mux.Handle("POST /api/auth/logout", s.requireAdmin(http.HandlerFunc(s.handleLogout)))

	mux.Handle("GET /v1/models", s.requireGatewayKey(http.HandlerFunc(s.handleModels)))
	mux.Handle("GET /v1/models/{id...}", s.requireGatewayKey(http.HandlerFunc(s.handleModel)))
	mux.Handle("GET /v1/codex/manifest", s.requireGatewayKey(http.HandlerFunc(s.handleCodexManifest)))
	mux.Handle("POST /v1/chat/completions", s.requireGatewayKey(http.HandlerFunc(s.handleChatCompletions)))
	mux.Handle("POST /v1/responses", s.requireGatewayKey(http.HandlerFunc(s.handleResponses)))
	mux.Handle("POST /v1/responses/compact", s.requireGatewayKey(http.HandlerFunc(s.handleResponsesCompact)))
	mux.Handle("POST /v1/messages", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicMessages)))
	mux.Handle("POST /v1/messages/count_tokens", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicCountTokens)))
	mux.Handle("POST /v1/messages/batches", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicBatchCreate)))
	mux.Handle("GET /v1/messages/batches", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicBatchList)))
	mux.Handle("GET /v1/messages/batches/{id}", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicBatchGet)))
	mux.Handle("DELETE /v1/messages/batches/{id}", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicBatchDelete)))
	mux.Handle("POST /v1/messages/batches/{id}/cancel", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicBatchCancel)))
	mux.Handle("GET /v1/messages/batches/{id}/results", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicBatchResults)))
	mux.Handle("POST /v1/files", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicFileCreate)))
	mux.Handle("GET /v1/files", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicFileList)))
	mux.Handle("GET /v1/files/{id}", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicFileGet)))
	mux.Handle("DELETE /v1/files/{id}", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicFileDelete)))
	mux.Handle("GET /v1/files/{id}/content", s.requireGatewayKey(http.HandlerFunc(s.handleAnthropicFileContent)))

	s.registerAdminRoutes(mux)
	s.registerPortableRoutes(mux)
	mux.HandleFunc("GET /", s.serveWeb)
	return s.requestIDMiddleware(s.securityHeaders(s.recoverMiddleware(mux)))
}

func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "version": Version, "commit": BuildCommit})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Database is unavailable.")
		return
	}
	if err := s.redis.Ping(ctx).Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "limiter_unavailable", "Rate limiter is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := newID("req")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Could not create request identifier.")
			return
		}
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Request-Id", requestID)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, requestID)))
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &responseStateWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error(
					"request panic",
					"request_id", requestIDFromContext(r.Context()),
					"error", recovered,
					"stack", string(debug.Stack()),
				)
				if !state.wroteHeader {
					writeError(state, http.StatusInternalServerError, "internal_error", "The gateway could not complete the request.")
				}
			}
		}()
		next.ServeHTTP(state, r)
	})
}

type responseStateWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *responseStateWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStateWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *responseStateWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseStateWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.Redirect(w, r, "/admin/", http.StatusTemporaryRedirect)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/admin") {
		http.NotFound(w, r)
		return
	}
	sub, err := fs.Sub(webFiles, "webdist")
	if err != nil {
		http.Error(w, "UI unavailable", http.StatusInternalServerError)
		return
	}
	assetPath := strings.TrimPrefix(r.URL.Path, "/admin/")
	if assetPath == "" || assetPath == "admin" {
		assetPath = "index.html"
	}
	body, err := fs.ReadFile(sub, assetPath)
	if err != nil {
		body, err = fs.ReadFile(sub, "index.html")
		assetPath = "index.html"
	}
	if err != nil {
		http.Error(w, "UI unavailable", http.StatusInternalServerError)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(assetPath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if assetPath == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(body)
}

func (s *Server) retentionLoop(ctx context.Context) {
	s.runRetention(ctx)
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runRetention(ctx)
		}
	}
}

func (s *Server) runRetention(ctx context.Context) {
	settings, _, err := s.settings(ctx)
	if err != nil {
		s.logger.Warn("retention settings unavailable", "error", err)
		return
	}
	_, _ = s.db.Exec(ctx, `
		UPDATE request_logs
		SET request_body_cipher = NULL, response_body_cipher = NULL
		WHERE created_at < NOW() - ($1::text || ' days')::interval
		  AND (request_body_cipher IS NOT NULL OR response_body_cipher IS NOT NULL)
	`, settings.BodyRetentionDays)
	_, _ = s.db.Exec(ctx, `
		DELETE FROM request_logs
		WHERE created_at < NOW() - ($1::text || ' days')::interval
	`, settings.MetadataRetentionDays)
}

type requestIDContextKey struct{}
type adminContextKey struct{}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return value
}

func adminIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(adminContextKey{}).(string)
	return value
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.")
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": message, "type": code, "code": code},
	})
}
