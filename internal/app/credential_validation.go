package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type credentialInspection struct {
	Valid      bool              `json:"valid"`
	StatusCode int               `json:"status_code"`
	LatencyMS  int64             `json:"latency_ms"`
	Models     []DiscoveredModel `json:"models"`
	Warning    string            `json:"warning,omitempty"`
}

func inspectProviderSecret(ctx context.Context, provider Provider, secret []byte) credentialInspection {
	result := credentialInspection{Models: []DiscoveredModel{}}
	client, err := upstreamClient(provider)
	if err != nil {
		result.Warning = err.Error()
		return result
	}
	target := strings.TrimRight(provider.BaseURL, "/") + "/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		result.Warning = "Provider model-list request could not be created."
		return result
	}
	applyProviderHeaders(request, provider, secret)
	started := time.Now()
	response, err := client.Do(request)
	result.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Warning = "Provider could not be reached while checking the API key."
		return result
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		result.Warning = "Provider model list could not be read."
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		switch response.StatusCode {
		case http.StatusUnauthorized:
			result.Warning = "API key was rejected by the provider (HTTP 401)."
		case http.StatusForbidden:
			result.Warning = "API key does not have permission to list models (HTTP 403)."
		default:
			result.Warning = fmt.Sprintf("Provider returned HTTP %d while checking the API key.", response.StatusCode)
		}
		return result
	}

	var payload struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		result.Warning = "Provider returned an invalid OpenAI-compatible model list."
		return result
	}
	seen := make(map[string]bool, len(payload.Data))
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" || len(id) > 255 || seen[id] {
			continue
		}
		seen[id] = true
		result.Models = append(result.Models, DiscoveredModel{
			ID: id, OwnedBy: strings.TrimSpace(model.OwnedBy),
		})
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].ID < result.Models[j].ID })
	result.Valid = true
	return result
}

func providerFromInput(input providerInput) Provider {
	return Provider{
		Name: input.Name, Slug: input.Slug, BaseURL: input.BaseURL,
		AuthHeader: input.AuthHeader, AuthScheme: input.AuthScheme,
		ExtraHeaders: input.ExtraHeaders, TimeoutSeconds: input.TimeoutSeconds,
		Enabled: input.Enabled, AllowPrivateNetwork: input.AllowPrivateNetwork,
	}
}

func (s *Server) handleInspectUnsavedProvider(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Provider providerInput `json:"provider"`
		Secret   string        `json:"secret"`
	}
	if decodeJSON(w, r, 160<<10, &input) != nil {
		return
	}
	input.Secret = strings.TrimSpace(input.Secret)
	if err := validateProviderInput(&input.Provider); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider", err.Error())
		return
	}
	if len(input.Secret) < 8 || len(input.Secret) > 8192 {
		writeError(w, http.StatusBadRequest, "invalid_credential", "API key is invalid.")
		return
	}
	writeJSON(w, http.StatusOK, inspectProviderSecret(r.Context(), providerFromInput(input.Provider), []byte(input.Secret)))
}

func (s *Server) handleInspectProviderCredential(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Secret string `json:"secret"`
	}
	if decodeJSON(w, r, 32<<10, &input) != nil {
		return
	}
	input.Secret = strings.TrimSpace(input.Secret)
	if len(input.Secret) < 8 || len(input.Secret) > 8192 {
		writeError(w, http.StatusBadRequest, "invalid_credential", "API key is invalid.")
		return
	}
	provider, err := scanProvider(s.db.QueryRow(
		r.Context(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, r.PathValue("id"),
	))
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	writeJSON(w, http.StatusOK, inspectProviderSecret(r.Context(), provider, []byte(input.Secret)))
}

func (s *Server) handleDiscoverModels(w http.ResponseWriter, r *http.Request) {
	var input struct {
		CredentialID string `json:"credential_id"`
	}
	if decodeJSON(w, r, 16<<10, &input) != nil {
		return
	}
	provider, err := scanProvider(s.db.QueryRow(
		r.Context(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, r.PathValue("id"),
	))
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	var credentialID string
	var ciphertext []byte
	query := `
		SELECT id, secret_cipher FROM credentials
		WHERE provider_id=$1 AND enabled=TRUE`
	args := []any{provider.ID}
	if strings.TrimSpace(input.CredentialID) != "" {
		query += ` AND id=$2`
		args = append(args, strings.TrimSpace(input.CredentialID))
	}
	query += ` ORDER BY is_primary DESC, created_at, id LIMIT 1`
	if err := s.db.QueryRow(r.Context(), query, args...).Scan(&credentialID, &ciphertext); err != nil {
		writeError(w, http.StatusConflict, "credential_required", "Add an enabled API key before loading models.")
		return
	}
	secret, err := s.vault.Decrypt(ciphertext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential_unavailable", "API key could not be decrypted.")
		return
	}
	inspection := inspectProviderSecret(r.Context(), provider, secret)
	s.recordCredentialInspection(r.Context(), credentialID, inspection)
	writeJSON(w, http.StatusOK, inspection)
}

func (s *Server) handleCreateModelsBulk(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Models []modelInput `json:"models"`
	}
	if decodeJSON(w, r, 1<<20, &input) != nil {
		return
	}
	if len(input.Models) == 0 || len(input.Models) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_models", "Select between 1 and 500 models.")
		return
	}
	aliases := map[string]bool{}
	for index := range input.Models {
		if err := validateModelInput(&input.Models[index]); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_model", err.Error())
			return
		}
		if aliases[input.Models[index].PublicAlias] {
			writeError(w, http.StatusBadRequest, "duplicate_model_alias", "Every selected model needs a unique public alias.")
			return
		}
		aliases[input.Models[index].PublicAlias] = true
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Models could not be saved.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var providerExists bool
	if err := tx.QueryRow(
		r.Context(), `SELECT EXISTS(SELECT 1 FROM providers WHERE id=$1)`, r.PathValue("id"),
	).Scan(&providerExists); err != nil || !providerExists {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	rows, err := tx.Query(r.Context(), `SELECT upstream_model FROM model_routes WHERE provider_id=$1`, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "model_create_failed", "Existing models could not be checked.")
		return
	}
	existing := map[string]bool{}
	for rows.Next() {
		var upstream string
		if err := rows.Scan(&upstream); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "model_create_failed", "Existing models could not be checked.")
			return
		}
		existing[upstream] = true
	}
	rows.Close()

	created := 0
	skipped := 0
	for _, model := range input.Models {
		if existing[model.UpstreamModel] {
			skipped++
			continue
		}
		id, _ := newID("mdl")
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO model_routes
			    (id, provider_id, public_alias, upstream_model, supports_chat,
			     supports_responses, default_max_output_tokens, tokenizer,
			     capture_bodies, strip_parameters, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		`, id, r.PathValue("id"), model.PublicAlias, model.UpstreamModel,
			model.SupportsChat, model.SupportsResponses, model.DefaultMaxOutputTokens,
			model.Tokenizer, model.CaptureBodies, model.StripParameters, model.Enabled); err != nil {
			writeError(w, http.StatusConflict, "model_conflict", "A public model alias is already in use.")
			return
		}
		existing[model.UpstreamModel] = true
		created++
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "model_create_failed", "Models could not be saved.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.bulk_create", "provider", r.PathValue("id"), map[string]any{
		"created": created, "skipped": skipped,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"created": created, "skipped": skipped})
}

func (s *Server) recordCredentialInspection(ctx context.Context, credentialID string, inspection credentialInspection) {
	if inspection.Valid {
		_, _ = s.db.Exec(ctx, `
			UPDATE credentials SET
			    status=CASE WHEN enabled THEN 'healthy' ELSE 'disabled' END,
			    validation_error='', last_validated_at=NOW(), cooldown_until=NULL,
			    consecutive_failures=0, updated_at=NOW()
			WHERE id=$1
		`, credentialID)
		_ = s.redis.Del(ctx, "cooldown:"+credentialID, "failures:"+credentialID).Err()
		return
	}
	if inspection.StatusCode == http.StatusUnauthorized || inspection.StatusCode == http.StatusForbidden {
		_, _ = s.db.Exec(ctx, `
			UPDATE credentials SET
			    status=CASE WHEN enabled THEN 'quarantined' ELSE 'disabled' END,
			    validation_error=$2, last_validated_at=NOW(), cooldown_until=NULL,
			    consecutive_failures=consecutive_failures+1, updated_at=NOW()
			WHERE id=$1
		`, credentialID, inspection.Warning)
		return
	}
	_, _ = s.db.Exec(ctx, `
		UPDATE credentials SET validation_error=$2, last_validated_at=NOW(), updated_at=NOW()
		WHERE id=$1
	`, credentialID, inspection.Warning)
}

func mergeDiscoveredModels(results []credentialInspection) []DiscoveredModel {
	byID := map[string]DiscoveredModel{}
	for _, result := range results {
		for _, model := range result.Models {
			if _, exists := byID[model.ID]; !exists {
				byID[model.ID] = model
			}
		}
	}
	models := make([]DiscoveredModel, 0, len(byID))
	for _, model := range byID {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}
