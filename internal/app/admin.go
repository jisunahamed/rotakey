package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	slugPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	aliasPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{1,127}$`)
)

func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	admin := func(handler http.HandlerFunc) http.Handler { return s.requireAdmin(handler) }
	mux.Handle("GET /api/admin/overview", admin(s.handleAdminOverview))
	mux.Handle("GET /api/admin/providers", admin(s.handleListProviders))
	mux.Handle("POST /api/admin/providers", admin(s.handleCreateProvider))
	mux.Handle("PUT /api/admin/providers/{id}", admin(s.handleUpdateProvider))
	mux.Handle("DELETE /api/admin/providers/{id}", admin(s.handleDeleteProvider))
	mux.Handle("POST /api/admin/providers/{id}/test", admin(s.handleTestProvider))
	mux.Handle("POST /api/admin/providers/{id}/models", admin(s.handleCreateModel))
	mux.Handle("PUT /api/admin/models/{id}", admin(s.handleUpdateModel))
	mux.Handle("DELETE /api/admin/models/{id}", admin(s.handleDeleteModel))
	mux.Handle("POST /api/admin/providers/{id}/credentials", admin(s.handleCreateCredentials))
	mux.Handle("PUT /api/admin/credentials/{id}", admin(s.handleUpdateCredential))
	mux.Handle("DELETE /api/admin/credentials/{id}", admin(s.handleDeleteCredential))
	mux.Handle("PUT /api/admin/credentials/{id}/model-limits/{model_id}", admin(s.handleModelLimits))
	mux.Handle("DELETE /api/admin/credentials/{id}/model-limits/{model_id}", admin(s.handleDeleteModelLimits))
	mux.Handle("GET /api/admin/logs", admin(s.handleLogs))
	mux.Handle("GET /api/admin/logs/{id}/bodies", admin(s.handleLogBodies))
	mux.Handle("POST /api/admin/access/rotate", admin(s.handleRotateGatewayKey))
	mux.Handle("GET /api/admin/settings", admin(s.handleGetSettings))
	mux.Handle("PUT /api/admin/settings", admin(s.handleUpdateSettings))
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	providers, err := s.listProviders(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Dashboard data is unavailable.")
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Gateway settings are unavailable.")
		return
	}
	var requests24h, errors24h, tokens24h int64
	_ = s.db.QueryRow(r.Context(), `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE status_code >= 400),
		       COALESCE(SUM(input_tokens + output_tokens), 0)
		FROM request_logs WHERE created_at >= NOW() - INTERVAL '24 hours'
	`).Scan(&requests24h, &errors24h, &tokens24h)

	type routeCapacity struct {
		ID          string               `json:"id"`
		Alias       string               `json:"alias"`
		Provider    string               `json:"provider"`
		Enabled     bool                 `json:"enabled"`
		Healthy     int                  `json:"healthy_credentials"`
		Unavailable int                  `json:"unavailable_credentials"`
		Total       int                  `json:"total_credentials"`
		Requests24h int64                `json:"requests_24h"`
		Errors24h   int64                `json:"errors_24h"`
		Segments    []credentialCapacity `json:"segments"`
	}
	routes := make([]routeCapacity, 0)
	for _, provider := range providers {
		for _, model := range provider.Models {
			capacity := routeCapacity{
				ID: model.ID, Alias: model.PublicAlias, Provider: provider.Name,
				Enabled: provider.Enabled && model.Enabled, Total: len(provider.Credentials),
				Segments: make([]credentialCapacity, 0, len(provider.Credentials)),
			}
			eligible := make([]int, 0, len(provider.Credentials))
			for index, credential := range provider.Credentials {
				if credential.Enabled && credential.Status != "quarantined" {
					eligible = append(eligible, index)
				}
			}
			cursorIndex := -1
			if len(eligible) > 0 {
				if cursor, err := s.redis.Get(r.Context(), "rr:"+model.ID).Int64(); err == nil {
					cursorIndex = eligible[int(cursor)%len(eligible)]
				} else {
					cursorIndex = eligible[0]
				}
			}
			for index, credential := range provider.Credentials {
				segment := s.credentialCapacity(r.Context(), credential, model.ID)
				segment.Cursor = index == cursorIndex
				capacity.Segments = append(capacity.Segments, segment)
				if segment.Status == "healthy" {
					capacity.Healthy++
				} else {
					capacity.Unavailable++
				}
			}
			_ = s.db.QueryRow(r.Context(), `
				SELECT COUNT(*), COUNT(*) FILTER (WHERE status_code >= 400)
				FROM request_logs
				WHERE model_id = $1 AND created_at >= NOW() - INTERVAL '24 hours'
			`, model.ID).Scan(&capacity.Requests24h, &capacity.Errors24h)
			routes = append(routes, capacity)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"base_url":  s.cfg.PublicBaseURL + "/v1",
		"settings":  settings,
		"providers": len(providers),
		"routes":    routes,
		"usage": map[string]any{
			"requests_24h": requests24h, "errors_24h": errors24h, "tokens_24h": tokens24h,
		},
	})
}

func (s *Server) listProviders(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.Query(ctx, `SELECT `+providerColumns+` FROM providers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	providers := make([]Provider, 0)
	byID := map[string]int{}
	for rows.Next() {
		provider, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		provider.Models = []ModelRoute{}
		provider.Credentials = []CredentialView{}
		byID[provider.ID] = len(providers)
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	modelRows, err := s.db.Query(ctx, `
		SELECT id, provider_id, public_alias, upstream_model, supports_chat,
		       supports_responses, default_max_output_tokens, tokenizer,
		       capture_bodies, enabled, created_at, updated_at
		FROM model_routes ORDER BY created_at, id
	`)
	if err != nil {
		return nil, err
	}
	for modelRows.Next() {
		var model ModelRoute
		if err := modelRows.Scan(
			&model.ID, &model.ProviderID, &model.PublicAlias, &model.UpstreamModel,
			&model.SupportsChat, &model.SupportsResponses, &model.DefaultMaxOutputTokens,
			&model.Tokenizer, &model.CaptureBodies, &model.Enabled,
			&model.CreatedAt, &model.UpdatedAt,
		); err != nil {
			modelRows.Close()
			return nil, err
		}
		if index, ok := byID[model.ProviderID]; ok {
			providers[index].Models = append(providers[index].Models, model)
		}
	}
	modelRows.Close()

	credentialRows, err := s.db.Query(ctx, `
		SELECT c.id, c.provider_id, c.label, c.secret_suffix, c.enabled, c.status,
		       c.cooldown_until, c.created_at, c.updated_at,
		       r.scope_key, r.rps, r.rpm, r.rpd, r.tps, r.tpm, r.tpd, r.tpr
		FROM credentials c
		LEFT JOIN rate_policies r ON r.credential_id = c.id
		ORDER BY c.created_at, c.id, r.scope_key
	`)
	if err != nil {
		return nil, err
	}
	credentialIndex := map[string]*CredentialView{}
	credentialProvider := map[string]string{}
	credentialOrder := make([]string, 0)
	for credentialRows.Next() {
		var (
			id, providerID, label, suffix, status string
			enabled                               bool
			cooldown                              *time.Time
			createdAt, updatedAt                  time.Time
			scope                                 *string
			policy                                RatePolicy
		)
		if err := credentialRows.Scan(
			&id, &providerID, &label, &suffix, &enabled, &status,
			&cooldown, &createdAt, &updatedAt, &scope,
			&policy.RPS, &policy.RPM, &policy.RPD, &policy.TPS,
			&policy.TPM, &policy.TPD, &policy.TPR,
		); err != nil {
			credentialRows.Close()
			return nil, err
		}
		view := credentialIndex[id]
		if view == nil {
			if status == "cooldown" && (cooldown == nil || time.Now().After(*cooldown)) {
				status = "healthy"
				cooldown = nil
			}
			view = &CredentialView{
				ID: id, ProviderID: providerID, Label: label, SecretSuffix: suffix,
				Enabled: enabled, Status: status, CooldownUntil: cooldown,
				Limits: RatePolicy{}, ModelLimits: map[string]RatePolicy{},
				CreatedAt: createdAt, UpdatedAt: updatedAt,
			}
			credentialIndex[id] = view
			credentialProvider[id] = providerID
			credentialOrder = append(credentialOrder, id)
		}
		if scope != nil {
			if *scope == "*" {
				view.Limits = policy
			} else {
				view.ModelLimits[*scope] = policy
			}
		}
	}
	credentialRows.Close()
	for _, id := range credentialOrder {
		credential := credentialIndex[id]
		if index, ok := byID[credentialProvider[id]]; ok {
			providers[index].Credentials = append(providers[index].Credentials, *credential)
		}
	}
	return providers, nil
}

type limitHeadroom struct {
	Dimension string `json:"dimension"`
	Scope     string `json:"scope"`
	Remaining int64  `json:"remaining"`
	Limit     int64  `json:"limit"`
}

type credentialCapacity struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Status  string         `json:"status"`
	Cursor  bool           `json:"cursor"`
	Request *limitHeadroom `json:"request_headroom,omitempty"`
	Token   *limitHeadroom `json:"token_headroom,omitempty"`
	Unknown bool           `json:"unknown"`
}

func (s *Server) credentialCapacity(ctx context.Context, credential CredentialView, modelID string) credentialCapacity {
	result := credentialCapacity{ID: credential.ID, Label: credential.Label, Status: "healthy"}
	if !credential.Enabled {
		result.Status = "disabled"
		return result
	}
	if credential.Status == "quarantined" {
		result.Status = "quarantined"
		return result
	}
	if cooldown, err := s.redis.TTL(ctx, "cooldown:"+credential.ID).Result(); err != nil {
		result.Status = "unknown"
		result.Unknown = true
		return result
	} else if cooldown > 0 {
		result.Status = "cooldown"
	}

	runtime := credentialRuntime{CredentialView: credential}
	constraints, _ := buildConstraints(runtime, modelID, 0)
	now := time.Now().UnixMilli()
	for _, constraint := range constraints {
		values, err := s.redis.HMGet(ctx, constraint.Key, "count", "bucket").Result()
		if err != nil {
			result.Status = "unknown"
			result.Unknown = true
			return result
		}
		var count, bucket int64
		if len(values) == 2 {
			count, _ = strconv.ParseInt(fmt.Sprint(values[0]), 10, 64)
			bucket, _ = strconv.ParseInt(fmt.Sprint(values[1]), 10, 64)
		}
		if bucket != now/constraint.WindowMS {
			count = 0
		}
		remaining := constraint.Capacity - count
		if remaining < 0 {
			remaining = 0
		}
		parts := strings.Split(constraint.Key, ":")
		dimension := strings.ToUpper(parts[len(parts)-1])
		scope := "shared"
		if strings.Contains(constraint.Key, ":model:") {
			scope = "model"
		}
		candidate := &limitHeadroom{
			Dimension: dimension, Scope: scope, Remaining: remaining, Limit: constraint.Capacity,
		}
		if constraint.Token {
			result.Token = tighterHeadroom(result.Token, candidate)
		} else {
			result.Request = tighterHeadroom(result.Request, candidate)
		}
	}
	for _, scoped := range []struct {
		scope  string
		policy RatePolicy
	}{{scope: "shared", policy: credential.Limits}, {scope: "model", policy: credential.ModelLimits[modelID]}} {
		if scoped.policy.TPR != nil {
			result.Token = tighterHeadroom(result.Token, &limitHeadroom{
				Dimension: "TPR", Scope: scoped.scope, Remaining: *scoped.policy.TPR, Limit: *scoped.policy.TPR,
			})
		}
	}
	if (result.Request != nil && result.Request.Remaining == 0) ||
		(result.Token != nil && result.Token.Remaining == 0) {
		result.Status = "exhausted"
	}
	return result
}

func tighterHeadroom(current, candidate *limitHeadroom) *limitHeadroom {
	if current == nil {
		return candidate
	}
	currentRatio := float64(current.Remaining) / float64(current.Limit)
	candidateRatio := float64(candidate.Remaining) / float64(candidate.Limit)
	if candidateRatio < currentRatio {
		return candidate
	}
	return current
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.listProviders(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Providers could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

type providerInput struct {
	Name                string            `json:"name"`
	Slug                string            `json:"slug"`
	BaseURL             string            `json:"base_url"`
	AuthHeader          string            `json:"auth_header"`
	AuthScheme          string            `json:"auth_scheme"`
	ExtraHeaders        map[string]string `json:"extra_headers"`
	TimeoutSeconds      int               `json:"timeout_seconds"`
	Enabled             bool              `json:"enabled"`
	AllowPrivateNetwork bool              `json:"allow_private_network"`
}

func validateProviderInput(input *providerInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.AuthHeader = http.CanonicalHeaderKey(strings.TrimSpace(input.AuthHeader))
	input.AuthScheme = strings.TrimSpace(input.AuthScheme)
	if len(input.Name) < 2 || len(input.Name) > 100 || !slugPattern.MatchString(input.Slug) {
		return fmt.Errorf("name or slug is invalid")
	}
	if input.AuthHeader == "" {
		input.AuthHeader = "Authorization"
	}
	if input.TimeoutSeconds == 0 {
		input.TimeoutSeconds = 120
	}
	if input.TimeoutSeconds < 1 || input.TimeoutSeconds > 900 {
		return fmt.Errorf("timeout must be between 1 and 900 seconds")
	}
	if _, err := validateProviderURL(input.BaseURL, input.AllowPrivateNetwork); err != nil {
		return err
	}
	for key, value := range input.ExtraHeaders {
		canonical := http.CanonicalHeaderKey(key)
		if canonical == "" || strings.ContainsAny(key+value, "\r\n") ||
			canonical == "Host" || canonical == "Content-Length" || canonical == "Authorization" {
			return fmt.Errorf("extra header %q is not allowed", key)
		}
	}
	return nil
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var input providerInput
	if decodeJSON(w, r, 128<<10, &input) != nil {
		return
	}
	if err := validateProviderInput(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider", err.Error())
		return
	}
	id, _ := newID("prv")
	headers, _ := json.Marshal(input.ExtraHeaders)
	_, err := s.db.Exec(r.Context(), `
		INSERT INTO providers
		    (id, name, slug, base_url, auth_header, auth_scheme, extra_headers,
		     timeout_seconds, enabled, allow_private_network)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, id, input.Name, input.Slug, input.BaseURL, input.AuthHeader, input.AuthScheme,
		headers, input.TimeoutSeconds, input.Enabled, input.AllowPrivateNetwork)
	if err != nil {
		writeError(w, http.StatusConflict, "provider_conflict", "Provider slug already exists or the provider is invalid.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "provider.create", "provider", id, map[string]any{"name": input.Name})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	var input providerInput
	if decodeJSON(w, r, 128<<10, &input) != nil {
		return
	}
	if err := validateProviderInput(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider", err.Error())
		return
	}
	headers, _ := json.Marshal(input.ExtraHeaders)
	tag, err := s.db.Exec(r.Context(), `
		UPDATE providers SET name=$2, slug=$3, base_url=$4, auth_header=$5,
		    auth_scheme=$6, extra_headers=$7, timeout_seconds=$8, enabled=$9,
		    allow_private_network=$10, updated_at=NOW()
		WHERE id=$1
	`, r.PathValue("id"), input.Name, input.Slug, input.BaseURL, input.AuthHeader,
		input.AuthScheme, headers, input.TimeoutSeconds, input.Enabled, input.AllowPrivateNetwork)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "provider_update_failed", "Provider could not be updated.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "provider.update", "provider", r.PathValue("id"), map[string]any{"name": input.Name})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	tag, err := s.db.Exec(r.Context(), `DELETE FROM providers WHERE id=$1`, r.PathValue("id"))
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "provider.delete", "provider", r.PathValue("id"), map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}

type modelInput struct {
	PublicAlias            string `json:"public_alias"`
	UpstreamModel          string `json:"upstream_model"`
	SupportsChat           bool   `json:"supports_chat"`
	SupportsResponses      bool   `json:"supports_responses"`
	DefaultMaxOutputTokens int    `json:"default_max_output_tokens"`
	Tokenizer              string `json:"tokenizer"`
	CaptureBodies          bool   `json:"capture_bodies"`
	Enabled                bool   `json:"enabled"`
}

func validateModelInput(input *modelInput) error {
	input.PublicAlias = strings.TrimSpace(input.PublicAlias)
	input.UpstreamModel = strings.TrimSpace(input.UpstreamModel)
	input.Tokenizer = strings.TrimSpace(input.Tokenizer)
	if !aliasPattern.MatchString(input.PublicAlias) || input.UpstreamModel == "" || len(input.UpstreamModel) > 255 {
		return fmt.Errorf("model alias or upstream model is invalid")
	}
	if !input.SupportsChat && !input.SupportsResponses {
		return fmt.Errorf("at least one upstream endpoint must be supported")
	}
	if input.DefaultMaxOutputTokens == 0 {
		input.DefaultMaxOutputTokens = 1024
	}
	if input.DefaultMaxOutputTokens < 1 || input.DefaultMaxOutputTokens > 1_000_000 {
		return fmt.Errorf("default output tokens are invalid")
	}
	if input.Tokenizer == "" {
		input.Tokenizer = "heuristic"
	}
	switch input.Tokenizer {
	case "heuristic", "cl100k_base", "o200k_base":
	default:
		return fmt.Errorf("tokenizer is invalid")
	}
	return nil
}

func (s *Server) handleCreateModel(w http.ResponseWriter, r *http.Request) {
	var input modelInput
	if decodeJSON(w, r, 64<<10, &input) != nil {
		return
	}
	if err := validateModelInput(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_model", err.Error())
		return
	}
	id, _ := newID("mdl")
	_, err := s.db.Exec(r.Context(), `
		INSERT INTO model_routes
		    (id, provider_id, public_alias, upstream_model, supports_chat,
		     supports_responses, default_max_output_tokens, tokenizer,
		     capture_bodies, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, id, r.PathValue("id"), input.PublicAlias, input.UpstreamModel,
		input.SupportsChat, input.SupportsResponses, input.DefaultMaxOutputTokens,
		input.Tokenizer, input.CaptureBodies, input.Enabled)
	if err != nil {
		writeError(w, http.StatusConflict, "model_conflict", "Model alias already exists or provider was not found.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.create", "model", id, map[string]any{"alias": input.PublicAlias})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) handleUpdateModel(w http.ResponseWriter, r *http.Request) {
	var input modelInput
	if decodeJSON(w, r, 64<<10, &input) != nil {
		return
	}
	if err := validateModelInput(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_model", err.Error())
		return
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE model_routes SET public_alias=$2, upstream_model=$3, supports_chat=$4,
		    supports_responses=$5, default_max_output_tokens=$6, tokenizer=$7,
		    capture_bodies=$8, enabled=$9, updated_at=NOW()
		WHERE id=$1
	`, r.PathValue("id"), input.PublicAlias, input.UpstreamModel, input.SupportsChat,
		input.SupportsResponses, input.DefaultMaxOutputTokens, input.Tokenizer,
		input.CaptureBodies, input.Enabled)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "model_update_failed", "Model could not be updated.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.update", "model", r.PathValue("id"), map[string]any{"alias": input.PublicAlias})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Model route could not be deleted.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err := tx.Exec(r.Context(), `DELETE FROM rate_policies WHERE scope_key=$1`, r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, "model_delete_failed", "Model limits could not be removed.")
		return
	}
	tag, err := tx.Exec(r.Context(), `DELETE FROM model_routes WHERE id=$1`, r.PathValue("id"))
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "model_not_found", "Model was not found.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "model_delete_failed", "Model route could not be deleted.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.delete", "model", r.PathValue("id"), map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}

type credentialInput struct {
	Label   string     `json:"label"`
	Secret  string     `json:"secret"`
	Enabled *bool      `json:"enabled,omitempty"`
	Limits  RatePolicy `json:"limits"`
}

func (s *Server) handleCreateCredentials(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Credentials []credentialInput `json:"credentials"`
	}
	if decodeJSON(w, r, 1<<20, &input) != nil {
		return
	}
	if len(input.Credentials) == 0 || len(input.Credentials) > 100 {
		writeError(w, http.StatusBadRequest, "invalid_credentials", "Add between 1 and 100 credentials.")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Credentials could not be saved.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	created := make([]string, 0, len(input.Credentials))
	for _, credential := range input.Credentials {
		credential.Label = strings.TrimSpace(credential.Label)
		credential.Secret = strings.TrimSpace(credential.Secret)
		if len(credential.Label) < 1 || len(credential.Label) > 100 ||
			len(credential.Secret) < 8 || len(credential.Secret) > 8192 || !credential.Limits.Valid() {
			writeError(w, http.StatusBadRequest, "invalid_credential", "Credential label, secret, or rate limits are invalid.")
			return
		}
		encrypted, err := s.vault.Encrypt([]byte(credential.Secret))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption_failed", "Credential could not be encrypted.")
			return
		}
		id, _ := newID("key")
		enabled := true
		if credential.Enabled != nil {
			enabled = *credential.Enabled
		}
		status := "healthy"
		if !enabled {
			status = "disabled"
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO credentials
			    (id, provider_id, label, secret_cipher, secret_suffix, enabled, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
		`, id, r.PathValue("id"), credential.Label, encrypted, secretSuffix(credential.Secret), enabled, status); err != nil {
			writeError(w, http.StatusConflict, "credential_conflict", "Credential labels must be unique inside a provider.")
			return
		}
		if err := upsertPolicy(r.Context(), tx, id, "*", credential.Limits); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_limits", "Rate limits could not be saved.")
			return
		}
		created = append(created, id)
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "credential_create_failed", "Credentials could not be saved.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "credential.create", "provider", r.PathValue("id"), map[string]any{"count": len(created)})
	writeJSON(w, http.StatusCreated, map[string]any{"ids": created})
}

func (s *Server) handleUpdateCredential(w http.ResponseWriter, r *http.Request) {
	var input credentialInput
	if decodeJSON(w, r, 256<<10, &input) != nil {
		return
	}
	input.Label = strings.TrimSpace(input.Label)
	if input.Label == "" || len(input.Label) > 100 || !input.Limits.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_credential", "Credential label or limits are invalid.")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Credential could not be updated.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	status := "healthy"
	if !enabled {
		status = "disabled"
	}
	var affected int64
	if strings.TrimSpace(input.Secret) != "" {
		secret := strings.TrimSpace(input.Secret)
		if len(secret) < 8 || len(secret) > 8192 {
			writeError(w, http.StatusBadRequest, "invalid_credential", "Replacement secret is invalid.")
			return
		}
		encrypted, _ := s.vault.Encrypt([]byte(secret))
		commandTag, err := tx.Exec(r.Context(), `
			UPDATE credentials SET label=$2, secret_cipher=$3, secret_suffix=$4,
			    enabled=$5, status=$6, cooldown_until=NULL, consecutive_failures=0,
			    updated_at=NOW() WHERE id=$1
		`, r.PathValue("id"), input.Label, encrypted, secretSuffix(secret), enabled, status)
		if err != nil {
			writeError(w, http.StatusConflict, "credential_update_failed", "Credential could not be updated.")
			return
		}
		affected = commandTag.RowsAffected()
	} else {
		commandTag, err := tx.Exec(r.Context(), `
			UPDATE credentials SET label=$2, enabled=$3, status=$4,
			    cooldown_until=NULL, consecutive_failures=0, updated_at=NOW()
			WHERE id=$1
		`, r.PathValue("id"), input.Label, enabled, status)
		if err != nil {
			writeError(w, http.StatusConflict, "credential_update_failed", "Credential could not be updated.")
			return
		}
		affected = commandTag.RowsAffected()
	}
	if affected == 0 {
		writeError(w, http.StatusNotFound, "credential_not_found", "Credential was not found.")
		return
	}
	if err := upsertPolicy(r.Context(), tx, r.PathValue("id"), "*", input.Limits); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limits", "Rate limits could not be updated.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "credential_update_failed", "Credential could not be updated.")
		return
	}
	_ = s.redis.Del(r.Context(), "cooldown:"+r.PathValue("id")).Err()
	s.audit(r.Context(), adminIDFromContext(r.Context()), "credential.update", "credential", r.PathValue("id"), map[string]any{"label": input.Label})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	tag, err := s.db.Exec(r.Context(), `DELETE FROM credentials WHERE id=$1`, r.PathValue("id"))
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "credential_not_found", "Credential was not found.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "credential.delete", "credential", r.PathValue("id"), map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleModelLimits(w http.ResponseWriter, r *http.Request) {
	var policy RatePolicy
	if decodeJSON(w, r, 64<<10, &policy) != nil {
		return
	}
	if !policy.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_limits", "Every configured limit must be greater than zero.")
		return
	}
	if err := upsertPolicy(r.Context(), s.db, r.PathValue("id"), r.PathValue("model_id"), policy); err != nil {
		writeError(w, http.StatusConflict, "limit_update_failed", "Model limits could not be saved.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "limits.update", "credential", r.PathValue("id"), map[string]any{"model_id": r.PathValue("model_id")})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteModelLimits(w http.ResponseWriter, r *http.Request) {
	_, err := s.db.Exec(r.Context(), `
		DELETE FROM rate_policies WHERE credential_id=$1 AND scope_key=$2
	`, r.PathValue("id"), r.PathValue("model_id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "limit_delete_failed", "Model limits could not be removed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type policyExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func upsertPolicy(ctx context.Context, executor policyExecutor, credentialID, scope string, policy RatePolicy) error {
	if !policy.Valid() {
		return fmt.Errorf("invalid rate policy")
	}
	_, err := executor.Exec(ctx, `
		INSERT INTO rate_policies
		    (credential_id, scope_key, rps, rpm, rpd, tps, tpm, tpd, tpr)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (credential_id, scope_key) DO UPDATE SET
		    rps=EXCLUDED.rps, rpm=EXCLUDED.rpm, rpd=EXCLUDED.rpd,
		    tps=EXCLUDED.tps, tpm=EXCLUDED.tpm, tpd=EXCLUDED.tpd,
		    tpr=EXCLUDED.tpr, updated_at=NOW()
	`, credentialID, scope, policy.RPS, policy.RPM, policy.RPD, policy.TPS, policy.TPM, policy.TPD, policy.TPR)
	return err
}

func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := scanProvider(s.db.QueryRow(r.Context(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, r.PathValue("id")))
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	var ciphertext []byte
	if err := s.db.QueryRow(r.Context(), `
		SELECT secret_cipher FROM credentials
		WHERE provider_id=$1 AND enabled=TRUE ORDER BY created_at LIMIT 1
	`, provider.ID).Scan(&ciphertext); err != nil {
		writeError(w, http.StatusConflict, "credential_required", "Add an enabled credential before testing.")
		return
	}
	secret, err := s.vault.Decrypt(ciphertext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential_unavailable", "Credential could not be decrypted.")
		return
	}
	client, err := upstreamClient(provider)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsafe_provider_url", err.Error())
		return
	}
	target := strings.TrimRight(provider.BaseURL, "/") + "/models"
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	applyProviderHeaders(request, provider, secret)
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_unreachable", "Provider could not be reached: "+err.Error())
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          response.StatusCode >= 200 && response.StatusCode < 300,
		"status_code": response.StatusCode, "latency_ms": time.Since(started).Milliseconds(),
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	status, _ := strconv.Atoi(r.URL.Query().Get("status"))
	rows, err := s.db.Query(r.Context(), `
		SELECT id, request_id, model_alias, provider_name, credential_label, endpoint,
		       attempts, status_code, latency_ms, input_tokens, output_tokens,
		       COALESCE(error_code,''), request_body_cipher IS NOT NULL,
		       body_truncated, created_at
		FROM request_logs
		WHERE ($1 = '' OR model_alias ILIKE '%' || $1 || '%' OR request_id ILIKE '%' || $1 || '%')
		  AND ($2 = 0 OR status_code = $2)
		ORDER BY created_at DESC LIMIT $3
	`, query, status, limit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "logs_unavailable", "Request logs could not be loaded.")
		return
	}
	defer rows.Close()
	logs := []RequestLog{}
	for rows.Next() {
		var log RequestLog
		if err := rows.Scan(
			&log.ID, &log.RequestID, &log.ModelAlias, &log.ProviderName,
			&log.CredentialLabel, &log.Endpoint, &log.Attempts, &log.StatusCode,
			&log.LatencyMS, &log.InputTokens, &log.OutputTokens, &log.ErrorCode,
			&log.BodyCaptured, &log.BodyTruncated, &log.CreatedAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "logs_unavailable", "Request logs could not be decoded.")
			return
		}
		logs = append(logs, log)
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
}

func (s *Server) handleLogBodies(w http.ResponseWriter, r *http.Request) {
	var requestCipher, responseCipher []byte
	err := s.db.QueryRow(r.Context(), `
		SELECT request_body_cipher, response_body_cipher FROM request_logs WHERE id=$1
	`, r.PathValue("id")).Scan(&requestCipher, &responseCipher)
	if err != nil {
		writeError(w, http.StatusNotFound, "log_not_found", "Request log was not found.")
		return
	}
	result := map[string]any{"request": nil, "response": nil}
	if len(requestCipher) > 0 {
		if plain, err := s.vault.Decrypt(requestCipher); err == nil {
			result["request"] = string(plain)
		}
	}
	if len(responseCipher) > 0 {
		if plain, err := s.vault.Decrypt(responseCipher); err == nil {
			result["response"] = string(plain)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRotateGatewayKey(w http.ResponseWriter, r *http.Request) {
	key, err := randomToken("gw_", 32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "rotation_failed", "Gateway key could not be generated.")
		return
	}
	prefix := key[:11]
	if _, err := s.db.Exec(r.Context(), `
		UPDATE app_settings SET gateway_key_hash=$1, gateway_key_prefix=$2, updated_at=NOW()
		WHERE id=1
	`, hashAPIKey(key), prefix); err != nil {
		writeError(w, http.StatusInternalServerError, "rotation_failed", "Gateway key could not be rotated.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "access.rotate", "gateway_key", "", map[string]any{"prefix": prefix})
	writeJSON(w, http.StatusOK, map[string]any{
		"gateway_key": key,
		"message":     "The previous gateway key was revoked. Save this key now; it will not be shown again.",
	})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings could not be loaded.")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var input AppSettings
	if decodeJSON(w, r, 32<<10, &input) != nil {
		return
	}
	if input.MetadataRetentionDays < 1 || input.MetadataRetentionDays > 3650 ||
		input.BodyRetentionDays < 1 || input.BodyRetentionDays > 365 ||
		input.MaxWaitMS < 0 || input.MaxWaitMS > 30000 {
		writeError(w, http.StatusBadRequest, "invalid_settings", "Retention or wait settings are outside allowed ranges.")
		return
	}
	_, err := s.db.Exec(r.Context(), `
		UPDATE app_settings SET metadata_retention_days=$1, body_retention_days=$2,
		    max_wait_ms=$3, updated_at=NOW() WHERE id=1
	`, input.MetadataRetentionDays, input.BodyRetentionDays, input.MaxWaitMS)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_update_failed", "Settings could not be updated.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "settings.update", "system", "", map[string]any{
		"metadata_retention_days": input.MetadataRetentionDays,
		"body_retention_days":     input.BodyRetentionDays,
		"max_wait_ms":             input.MaxWaitMS,
	})
	w.WriteHeader(http.StatusNoContent)
}

func applyProviderHeaders(request *http.Request, provider Provider, secret []byte) {
	header := provider.AuthHeader
	if header == "" {
		header = "Authorization"
	}
	value := string(secret)
	if provider.AuthScheme != "" {
		value = provider.AuthScheme + " " + value
	}
	request.Header.Set(header, value)
	for key, value := range provider.ExtraHeaders {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
}

func boundedBody(body io.Reader, limit int64) ([]byte, bool, error) {
	buffer := bytes.NewBuffer(nil)
	written, err := io.CopyN(buffer, body, limit+1)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	if written > limit {
		return buffer.Bytes()[:limit], true, nil
	}
	return buffer.Bytes(), false, nil
}
