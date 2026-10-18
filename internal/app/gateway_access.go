package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

const (
	adminCreatedKeyRPM         int64 = 2000
	adminCreatedKeyConcurrency int64 = 100
)

type GatewayAccessKey struct {
	ID               string     `json:"id"`
	Label            string     `json:"label"`
	Prefix           string     `json:"prefix"`
	Enabled          bool       `json:"enabled"`
	RPM              int64      `json:"rpm"`
	ConcurrencyLimit int64      `json:"concurrency_limit"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type gatewayAccessRuntime struct {
	GatewayAccessKey
	Legacy bool
}

var reserveGatewayAccessScript = redis.NewScript(`
local active = tonumber(redis.call('GET', KEYS[2]) or '0')
if active >= tonumber(ARGV[2]) then
  return {2, active, tonumber(redis.call('GET', KEYS[1]) or '0')}
end
local used = tonumber(redis.call('GET', KEYS[1]) or '0')
if used >= tonumber(ARGV[1]) then
  return {1, active, used}
end
used = redis.call('INCR', KEYS[1])
if used == 1 then redis.call('PEXPIREAT', KEYS[1], ARGV[3]) end
active = redis.call('INCR', KEYS[2])
if active == 1 then redis.call('EXPIRE', KEYS[2], 3600) end
return {0, active, used}
`)

var releaseGatewayAccessScript = redis.NewScript(`
local active = tonumber(redis.call('GET', KEYS[1]) or '0')
if active <= 1 then
  redis.call('DEL', KEYS[1])
  return 0
end
return redis.call('DECR', KEYS[1])
`)

func (s *Server) gatewayAccessKey(ctx context.Context, key string) (gatewayAccessRuntime, error) {
	_, legacyHash, err := s.settings(ctx)
	if err != nil {
		return gatewayAccessRuntime{}, err
	}
	hash := hashAPIKey(key)
	if len(legacyHash) > 0 && secureEqual(hash, legacyHash) {
		return gatewayAccessRuntime{Legacy: true}, nil
	}
	var access gatewayAccessRuntime
	err = s.db.QueryRow(ctx, `
		SELECT id, label, key_prefix, enabled, rpm, concurrency_limit,
		       last_used_at, created_at, updated_at
		FROM gateway_access_keys WHERE key_hash=$1
	`, hash).Scan(
		&access.ID, &access.Label, &access.Prefix, &access.Enabled,
		&access.RPM, &access.ConcurrencyLimit, &access.LastUsedAt,
		&access.CreatedAt, &access.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !access.Enabled) {
		return gatewayAccessRuntime{}, pgx.ErrNoRows
	}
	return access, err
}

func (s *Server) reserveGatewayAccess(ctx context.Context, access gatewayAccessRuntime) (func(), string, error) {
	if access.Legacy {
		return func() {}, "", nil
	}
	now := time.Now().UTC()
	reset := now.Truncate(time.Minute).Add(time.Minute)
	base := "gateway-access:" + access.ID
	result, err := reserveGatewayAccessScript.Run(ctx, s.redis,
		[]string{base + ":rpm:" + strconv.FormatInt(now.Unix()/60, 10), base + ":active"},
		access.RPM, access.ConcurrencyLimit, reset.UnixMilli(),
	).Int64Slice()
	if err != nil || len(result) < 1 {
		if err == nil {
			err = errors.New("invalid limiter response")
		}
		return nil, "", err
	}
	switch result[0] {
	case 1:
		return nil, "rpm", nil
	case 2:
		return nil, "concurrency", nil
	}
	if s.redis.SetNX(ctx, base+":last-used-write", "1", time.Minute).Val() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = s.db.Exec(ctx, `UPDATE gateway_access_keys SET last_used_at=NOW() WHERE id=$1`, access.ID)
		}()
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = releaseGatewayAccessScript.Run(ctx, s.redis, []string{base + ":active"}).Err()
	}, "", nil
}

func gatewayAccessLimitError(w http.ResponseWriter, r *http.Request, kind string) {
	w.Header().Set("Retry-After", "60")
	if kind == "concurrency" {
		w.Header().Set("Retry-After", "1")
		writeProtocolError(w, r, http.StatusTooManyRequests, "concurrency_limit_exceeded", "This API key already has 100 requests running. Try again when one finishes.")
		return
	}
	writeProtocolError(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "This API key has reached its 2,000 requests per minute limit.")
}

func normalizeGatewayKeyLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "API key"
	}
	return value
}

func (s *Server) handleListGatewayAccessKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT id, label, key_prefix, enabled, rpm, concurrency_limit,
		       last_used_at, created_at, updated_at
		FROM gateway_access_keys ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "access_keys_unavailable", "API keys could not be loaded.")
		return
	}
	defer rows.Close()
	keys := make([]GatewayAccessKey, 0)
	for rows.Next() {
		var key GatewayAccessKey
		if err := rows.Scan(&key.ID, &key.Label, &key.Prefix, &key.Enabled, &key.RPM,
			&key.ConcurrencyLimit, &key.LastUsedAt, &key.CreatedAt, &key.UpdatedAt); err != nil {
			writeError(w, http.StatusServiceUnavailable, "access_keys_unavailable", "API keys could not be loaded.")
			return
		}
		keys = append(keys, key)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusServiceUnavailable, "access_keys_unavailable", "API keys could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleCreateGatewayAccessKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Label string `json:"label"`
	}
	if decodeJSON(w, r, 8<<10, &input) != nil {
		return
	}
	label := normalizeGatewayKeyLabel(input.Label)
	if len(label) > 80 {
		writeError(w, http.StatusBadRequest, "invalid_label", "API key name must be 80 characters or fewer.")
		return
	}
	plain, err := randomToken("gw_", 32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "access_key_create_failed", "API key could not be generated.")
		return
	}
	id, err := newID("gak")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "access_key_create_failed", "API key could not be generated.")
		return
	}
	prefix := plain
	if len(prefix) > 11 {
		prefix = prefix[:11]
	}
	var key GatewayAccessKey
	err = s.db.QueryRow(r.Context(), `
		INSERT INTO gateway_access_keys
		    (id, label, key_hash, key_prefix, rpm, concurrency_limit)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, label, key_prefix, enabled, rpm, concurrency_limit,
		          last_used_at, created_at, updated_at
	`, id, label, hashAPIKey(plain), prefix, adminCreatedKeyRPM, adminCreatedKeyConcurrency).Scan(
		&key.ID, &key.Label, &key.Prefix, &key.Enabled, &key.RPM,
		&key.ConcurrencyLimit, &key.LastUsedAt, &key.CreatedAt, &key.UpdatedAt,
	)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "access_key_create_failed", "API key could not be saved.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "access.create", "gateway_access_key", id,
		map[string]any{"label": label, "prefix": prefix, "rpm": adminCreatedKeyRPM, "concurrency_limit": adminCreatedKeyConcurrency})
	writeJSON(w, http.StatusCreated, map[string]any{
		"gateway_key": plain,
		"key":         key,
		"message":     "API key created. Save it now; it will not be shown again.",
	})
}

func (s *Server) handleDeleteGatewayAccessKey(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	result, err := s.db.Exec(r.Context(), `DELETE FROM gateway_access_keys WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "access_key_delete_failed", "API key could not be revoked.")
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "access_key_not_found", "API key was not found.")
		return
	}
	_ = s.redis.Del(r.Context(), "gateway-access:"+id+":active").Err()
	s.audit(r.Context(), adminIDFromContext(r.Context()), "access.delete", "gateway_access_key", id, map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}
