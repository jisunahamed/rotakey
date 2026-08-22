package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	slugPattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
	slugSeparatorPattern = regexp.MustCompile(`[^a-z0-9]+`)
	aliasPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{1,127}$`)
	parameterPattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
	headerNamePattern    = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")
)

var forbiddenProviderHeaders = map[string]bool{
	"Connection": true, "Content-Length": true, "Host": true,
	"Proxy-Authenticate": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true,
}

func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	admin := func(handler http.HandlerFunc) http.Handler { return s.requireAdmin(handler) }
	mux.Handle("GET /api/admin/overview", admin(s.handleAdminOverview))
	mux.Handle("GET /api/admin/providers", admin(s.handleListProviders))
	mux.Handle("POST /api/admin/providers", admin(s.handleCreateProvider))
	mux.Handle("PUT /api/admin/providers/{id}", admin(s.handleUpdateProvider))
	mux.Handle("DELETE /api/admin/providers/{id}", admin(s.handleDeleteProvider))
	mux.Handle("POST /api/admin/providers/inspect", admin(s.handleInspectUnsavedProvider))
	mux.Handle("POST /api/admin/providers/{id}/test", admin(s.handleTestProvider))
	mux.Handle("POST /api/admin/providers/{id}/models", admin(s.handleCreateModel))
	mux.Handle("POST /api/admin/providers/{id}/models/bulk", admin(s.handleCreateModelsBulk))
	mux.Handle("POST /api/admin/providers/{id}/models/discover", admin(s.handleDiscoverModels))
	mux.Handle("PUT /api/admin/models/{id}", admin(s.handleUpdateModel))
	mux.Handle("POST /api/admin/models/{id}/probe", admin(s.handleProbeModel))
	mux.Handle("DELETE /api/admin/models/{id}", admin(s.handleDeleteModel))
	mux.Handle("POST /api/admin/providers/{id}/credentials", admin(s.handleCreateCredentials))
	mux.Handle("POST /api/admin/providers/{id}/credentials/inspect", admin(s.handleInspectProviderCredential))
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
	overview, err := s.buildAdminOverview(r.Context(), r.URL.Query().Get("range"))
	if err != nil {
		if errors.Is(err, errInvalidOverviewRange) {
			writeError(w, http.StatusBadRequest, "invalid_range", "Range must be 1h, 24h, 7d, or all.")
			return
		}
		s.logger.Error("admin overview build failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "overview_unavailable", "Overview data is unavailable.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, overview)
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
		       supports_responses, supports_messages, default_max_output_tokens, tokenizer,
		       input_cost_per_million_usd::float8, output_cost_per_million_usd::float8, request_cost_usd::float8,
		       capture_bodies, strip_parameters, capability_status, capability_profile,
		       capabilities_checked_at, capability_error, enabled, created_at, updated_at
		FROM model_routes ORDER BY created_at, id
	`)
	if err != nil {
		return nil, err
	}
	for modelRows.Next() {
		var model ModelRoute
		var capabilityProfile []byte
		if err := modelRows.Scan(
			&model.ID, &model.ProviderID, &model.PublicAlias, &model.UpstreamModel,
			&model.SupportsChat, &model.SupportsResponses, &model.SupportsMessages,
			&model.DefaultMaxOutputTokens,
			&model.Tokenizer, &model.InputCostPerMillionUSD, &model.OutputCostPerMillionUSD, &model.RequestCostUSD,
			&model.CaptureBodies, &model.StripParameters,
			&model.CapabilityStatus, &capabilityProfile, &model.CapabilitiesCheckedAt, &model.CapabilityError, &model.Enabled,
			&model.CreatedAt, &model.UpdatedAt,
		); err != nil {
			modelRows.Close()
			return nil, err
		}
		if json.Unmarshal(capabilityProfile, &model.CapabilityProfile) != nil {
			model.CapabilityProfile = map[string]string{}
		}
		if index, ok := byID[model.ProviderID]; ok {
			providers[index].Models = append(providers[index].Models, model)
		}
	}
	modelRows.Close()

	credentialRows, err := s.db.Query(ctx, `
		SELECT c.id, c.provider_id, c.label, c.secret_suffix, c.is_primary, c.enabled, c.status,
		       c.cooldown_until, c.last_validated_at, c.validation_error,
		       c.created_at, c.updated_at,
		       r.scope_key, r.rps, r.rpm, r.rpd, r.tps, r.tpm, r.tpd, r.tpr
		FROM credentials c
		LEFT JOIN rate_policies r ON r.credential_id = c.id
		ORDER BY c.is_primary DESC, c.created_at, c.id, r.scope_key
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
			isPrimary, enabled                    bool
			cooldown                              *time.Time
			lastValidated                         *time.Time
			validationError                       string
			createdAt, updatedAt                  time.Time
			scope                                 *string
			policy                                RatePolicy
		)
		if err := credentialRows.Scan(
			&id, &providerID, &label, &suffix, &isPrimary, &enabled, &status,
			&cooldown, &lastValidated, &validationError, &createdAt, &updatedAt, &scope,
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
				IsPrimary: isPrimary, Enabled: enabled, Status: status, CooldownUntil: cooldown,
				LastValidatedAt: lastValidated, ValidationError: validationError,
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

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.listProviders(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Providers could not be loaded.")
		return
	}
	for index := range providers {
		providers[index].Capacity = s.providerCapacity(r.Context(), providers[index].Credentials)
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
	APIFormat           string            `json:"api_format"`
	AnthropicVersion    string            `json:"anthropic_version"`
}

func validateProviderInput(input *providerInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	if input.Slug == "" {
		input.Slug = providerSlugFromName(input.Name)
	}
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.AuthHeader = http.CanonicalHeaderKey(strings.TrimSpace(input.AuthHeader))
	input.AuthScheme = strings.TrimSpace(input.AuthScheme)
	input.APIFormat = strings.ToLower(strings.TrimSpace(input.APIFormat))
	input.AnthropicVersion = strings.TrimSpace(input.AnthropicVersion)
	if len(input.Name) < 2 || len(input.Name) > 100 {
		return fmt.Errorf("provider name must be between 2 and 100 characters")
	}
	if !slugPattern.MatchString(input.Slug) {
		return fmt.Errorf("provider identifier is invalid")
	}
	if input.APIFormat == "" {
		input.APIFormat = "openai"
	}
	input.BaseURL = normalizeProviderCompatibilityURL(input.BaseURL, input.APIFormat)
	if input.APIFormat != "openai" && input.APIFormat != "anthropic" {
		return fmt.Errorf("provider protocol must be openai or anthropic")
	}
	if input.AnthropicVersion == "" {
		input.AnthropicVersion = "2023-06-01"
	}
	if input.APIFormat == "anthropic" && !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(input.AnthropicVersion) {
		return fmt.Errorf("Anthropic version must use YYYY-MM-DD")
	}
	if input.AuthHeader == "" {
		if input.APIFormat == "anthropic" {
			input.AuthHeader = "X-Api-Key"
		} else {
			input.AuthHeader = "Authorization"
		}
	}
	if !headerNamePattern.MatchString(input.AuthHeader) || forbiddenProviderHeaders[input.AuthHeader] {
		return fmt.Errorf("authentication header is not allowed")
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
		if canonical == "" || !headerNamePattern.MatchString(key) || strings.ContainsAny(key+value, "\r\n") ||
			forbiddenProviderHeaders[canonical] || canonical == "Authorization" ||
			canonical == "X-Api-Key" {
			return fmt.Errorf("extra header %q is not allowed", key)
		}
	}
	return nil
}

// normalizeProviderCompatibilityURL turns well-known provider roots and
// endpoint URLs into the documented compatibility root. This accepts the
// common values users paste from provider docs, including a direct /models URL.
func normalizeProviderCompatibilityURL(rawURL, apiFormat string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.TrimRight(parsed.Path, "/")
	if apiFormat == "openai" && host == "generativelanguage.googleapis.com" && path == "/v1beta" {
		parsed.Path = "/v1beta/openai/"
		parsed.RawPath = ""
		return parsed.String()
	}
	if apiFormat == "openai" && host == "api.openai.com" {
		if path == "" || path == "/v1" || path == "/v1/models" || path == "/v1/chat/completions" || path == "/v1/responses" {
			parsed.Path = "/v1"
			parsed.RawPath = ""
			return parsed.String()
		}
	}
	if apiFormat == "anthropic" && host == "api.anthropic.com" {
		if path == "" || path == "/v1" || path == "/v1/models" || path == "/v1/messages" {
			parsed.Path = "/v1"
			parsed.RawPath = ""
			return parsed.String()
		}
	}
	return rawURL
}

func providerSlugFromName(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = slugSeparatorPattern.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 63 {
		slug = strings.Trim(slug[:63], "-")
	}
	if len(slug) < 2 {
		return "provider"
	}
	return slug
}

func providerSlugCandidate(base string, duplicate int) string {
	if duplicate == 0 {
		return base
	}
	suffix := fmt.Sprintf("-%d", duplicate+1)
	maxBase := 63 - len(suffix)
	if maxBase < 1 {
		return "provider" + suffix
	}
	trimmed := base
	if len(trimmed) > maxBase {
		trimmed = trimmed[:maxBase]
	}
	trimmed = strings.Trim(trimmed, "-")
	if trimmed == "" {
		trimmed = "provider"
	}
	return trimmed + suffix
}

func (s *Server) availableProviderSlug(ctx context.Context, base string) (string, error) {
	for duplicate := 0; duplicate < 1000; duplicate++ {
		candidate := providerSlugCandidate(base, duplicate)
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM providers WHERE slug=$1)`, candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate a provider identifier")
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
	slug, err := s.availableProviderSlug(r.Context(), input.Slug)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "Provider identifier could not be generated.")
		return
	}
	input.Slug = slug
	id, _ := newID("prv")
	headers, _ := json.Marshal(input.ExtraHeaders)
	_, err = s.db.Exec(r.Context(), `
		INSERT INTO providers
		    (id, name, slug, base_url, auth_header, auth_scheme, extra_headers,
		     timeout_seconds, enabled, allow_private_network, api_format, anthropic_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, id, input.Name, input.Slug, input.BaseURL, input.AuthHeader, input.AuthScheme,
		headers, input.TimeoutSeconds, input.Enabled, input.AllowPrivateNetwork,
		input.APIFormat, input.AnthropicVersion)
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
	current, currentErr := scanProvider(s.db.QueryRow(r.Context(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, r.PathValue("id")))
	if currentErr != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	if strings.TrimSpace(input.Slug) == "" {
		input.Slug = current.Slug
	}
	if err := validateProviderInput(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_provider", err.Error())
		return
	}
	candidate := providerFromInput(input)
	connectionChanged := candidate.BaseURL != current.BaseURL || candidate.APIFormat != current.APIFormat ||
		candidate.AuthHeader != current.AuthHeader || candidate.AuthScheme != current.AuthScheme ||
		candidate.AnthropicVersion != current.AnthropicVersion || candidate.AllowPrivateNetwork != current.AllowPrivateNetwork
	if connectionChanged {
		var ciphertext []byte
		err := s.db.QueryRow(r.Context(), `SELECT secret_cipher FROM credentials WHERE provider_id=$1 AND enabled=TRUE ORDER BY is_primary DESC, created_at, id LIMIT 1`, current.ID).Scan(&ciphertext)
		if err == nil {
			secret, decryptErr := s.vault.Decrypt(ciphertext)
			if decryptErr != nil {
				writeError(w, http.StatusInternalServerError, "credential_unavailable", "API key could not be decrypted for the base URL check.")
				return
			}
			inspection := inspectProviderSecret(r.Context(), candidate, secret)
			if !inspection.Valid {
				writeError(w, http.StatusUnprocessableEntity, "provider_contract_mismatch", inspection.Warning+" Provider settings were not changed.")
				return
			}
		}
	}
	headers, _ := json.Marshal(input.ExtraHeaders)
	tag, err := s.db.Exec(r.Context(), `
		UPDATE providers SET name=$2, slug=$3, base_url=$4, auth_header=$5,
		    auth_scheme=$6, extra_headers=$7, timeout_seconds=$8, enabled=$9,
		    allow_private_network=$10, api_format=$11, anthropic_version=$12, updated_at=NOW()
		WHERE id=$1
	`, r.PathValue("id"), input.Name, input.Slug, input.BaseURL, input.AuthHeader,
		input.AuthScheme, headers, input.TimeoutSeconds, input.Enabled, input.AllowPrivateNetwork,
		input.APIFormat, input.AnthropicVersion)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "provider_update_failed", "Provider could not be updated.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "provider.update", "provider", r.PathValue("id"), map[string]any{"name": input.Name})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	var hasResources bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM anthropic_resources WHERE provider_id=$1)`, r.PathValue("id")).Scan(&hasResources); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Provider dependencies could not be checked.")
		return
	}
	if hasResources {
		writeError(w, http.StatusConflict, "resource_affinity_conflict", "Delete this provider's Files and Batches before removing it.")
		return
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM providers WHERE id=$1`, r.PathValue("id"))
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "provider.delete", "provider", r.PathValue("id"), map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}

type modelInput struct {
	PublicAlias             string   `json:"public_alias"`
	UpstreamModel           string   `json:"upstream_model"`
	Manual                  bool     `json:"manual,omitempty"`
	SupportsChat            bool     `json:"supports_chat"`
	SupportsResponses       bool     `json:"supports_responses"`
	SupportsMessages        bool     `json:"supports_messages"`
	DefaultMaxOutputTokens  int      `json:"default_max_output_tokens"`
	InputCostPerMillionUSD  float64  `json:"input_cost_per_million_usd"`
	OutputCostPerMillionUSD float64  `json:"output_cost_per_million_usd"`
	RequestCostUSD          *float64 `json:"request_cost_usd,omitempty"`
	Tokenizer               string   `json:"tokenizer"`
	CaptureBodies           bool     `json:"capture_bodies"`
	StripParameters         []string `json:"strip_parameters"`
	Enabled                 bool     `json:"enabled"`
}

func validateModelInput(input *modelInput) error {
	input.PublicAlias = strings.TrimSpace(input.PublicAlias)
	input.UpstreamModel = strings.TrimSpace(input.UpstreamModel)
	input.Tokenizer = strings.TrimSpace(input.Tokenizer)
	if !aliasPattern.MatchString(input.PublicAlias) || input.UpstreamModel == "" || len(input.UpstreamModel) > 255 {
		return fmt.Errorf("model alias or upstream model is invalid")
	}
	if !input.SupportsChat && !input.SupportsResponses && !input.SupportsMessages {
		return fmt.Errorf("at least one upstream endpoint must be supported")
	}
	if input.DefaultMaxOutputTokens == 0 {
		input.DefaultMaxOutputTokens = 1024
	}
	if input.DefaultMaxOutputTokens < 1 || input.DefaultMaxOutputTokens > 1_000_000 {
		return fmt.Errorf("default output tokens are invalid")
	}
	if input.InputCostPerMillionUSD < 0 || input.OutputCostPerMillionUSD < 0 || input.InputCostPerMillionUSD > 1_000_000 || input.OutputCostPerMillionUSD > 1_000_000 ||
		(input.RequestCostUSD != nil && (*input.RequestCostUSD < 0 || *input.RequestCostUSD > 1_000_000)) {
		return fmt.Errorf("model pricing is invalid")
	}
	if input.Tokenizer == "" {
		input.Tokenizer = "heuristic"
	}
	switch input.Tokenizer {
	case "heuristic", "cl100k_base", "o200k_base":
	default:
		return fmt.Errorf("tokenizer is invalid")
	}
	if len(input.StripParameters) > 32 {
		return fmt.Errorf("too many compatibility parameters")
	}
	seenParameters := map[string]bool{}
	protectedParameters := map[string]bool{
		"model": true, "messages": true, "input": true, "stream": true,
	}
	parameters := make([]string, 0, len(input.StripParameters))
	for _, parameter := range input.StripParameters {
		parameter = strings.TrimSpace(parameter)
		if !parameterPattern.MatchString(parameter) || protectedParameters[parameter] {
			return fmt.Errorf("compatibility parameter is invalid")
		}
		if !seenParameters[parameter] {
			seenParameters[parameter] = true
			parameters = append(parameters, parameter)
		}
	}
	input.StripParameters = parameters
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
	status, profile, checkedAt, err := s.probeProviderModel(r.Context(), r.PathValue("id"), &input)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "model_validation_failed", err.Error())
		return
	}
	profileJSON, _ := json.Marshal(profile)
	id, _ := newID("mdl")
	_, err = s.db.Exec(r.Context(), `
		INSERT INTO model_routes
		    (id, provider_id, public_alias, upstream_model, supports_chat,
		     supports_responses, supports_messages, default_max_output_tokens, tokenizer,
		     input_cost_per_million_usd, output_cost_per_million_usd, request_cost_usd,
		     capture_bodies, strip_parameters, capability_status, capability_profile,
		     capabilities_checked_at, capability_error, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'',$18)
	`, id, r.PathValue("id"), input.PublicAlias, input.UpstreamModel,
		input.SupportsChat, input.SupportsResponses, input.SupportsMessages, input.DefaultMaxOutputTokens,
		input.Tokenizer, input.InputCostPerMillionUSD, input.OutputCostPerMillionUSD, input.RequestCostUSD, input.CaptureBodies, input.StripParameters, status, profileJSON, checkedAt, input.Enabled)
	if err != nil {
		writeError(w, http.StatusConflict, "model_conflict", "Model alias already exists or provider was not found.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.create", "model", id, map[string]any{"alias": input.PublicAlias})
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "capability_status": status, "capability_profile": profile})
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
	var providerID, currentUpstream, capabilityStatus string
	var capabilityProfile []byte
	var checkedAt *time.Time
	if err := s.db.QueryRow(r.Context(), `SELECT provider_id, upstream_model, capability_status, capability_profile, capabilities_checked_at FROM model_routes WHERE id=$1`, r.PathValue("id")).Scan(&providerID, &currentUpstream, &capabilityStatus, &capabilityProfile, &checkedAt); err != nil {
		writeError(w, http.StatusNotFound, "model_not_found", "Model was not found.")
		return
	}
	if input.UpstreamModel != currentUpstream {
		var profile map[string]string
		var err error
		capabilityStatus, profile, checkedAt, err = s.probeProviderModel(r.Context(), providerID, &input)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "model_validation_failed", err.Error())
			return
		}
		capabilityProfile, _ = json.Marshal(profile)
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE model_routes SET public_alias=$2, upstream_model=$3, supports_chat=$4,
		    supports_responses=$5, supports_messages=$6, default_max_output_tokens=$7, tokenizer=$8,
		    input_cost_per_million_usd=$9, output_cost_per_million_usd=$10, request_cost_usd=$11,
		    capture_bodies=$12, strip_parameters=$13, capability_status=$14,
		    capability_profile=$15, capabilities_checked_at=$16, capability_error='', enabled=$17, updated_at=NOW()
		WHERE id=$1
	`, r.PathValue("id"), input.PublicAlias, input.UpstreamModel, input.SupportsChat,
		input.SupportsResponses, input.SupportsMessages, input.DefaultMaxOutputTokens, input.Tokenizer,
		input.InputCostPerMillionUSD, input.OutputCostPerMillionUSD, input.RequestCostUSD, input.CaptureBodies, input.StripParameters, capabilityStatus, capabilityProfile, checkedAt, input.Enabled)
	if err != nil || tag.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "model_update_failed", "Model could not be updated.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.update", "model", r.PathValue("id"), map[string]any{"alias": input.PublicAlias})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProbeModel(w http.ResponseWriter, r *http.Request) {
	var providerID string
	var input modelInput
	if err := s.db.QueryRow(r.Context(), `
		SELECT provider_id, public_alias, upstream_model, supports_chat, supports_responses,
		       supports_messages, default_max_output_tokens, tokenizer, capture_bodies,
		       strip_parameters, enabled
		FROM model_routes WHERE id=$1
	`, r.PathValue("id")).Scan(
		&providerID, &input.PublicAlias, &input.UpstreamModel, &input.SupportsChat,
		&input.SupportsResponses, &input.SupportsMessages, &input.DefaultMaxOutputTokens,
		&input.Tokenizer, &input.CaptureBodies, &input.StripParameters, &input.Enabled,
	); err != nil {
		writeError(w, http.StatusNotFound, "model_not_found", "Model was not found.")
		return
	}
	status, profile, checkedAt, err := s.probeProviderModel(r.Context(), providerID, &input)
	if err != nil {
		if errors.Is(err, errModelProbeCredentialUnavailable) {
			_, _ = s.db.Exec(r.Context(), `UPDATE model_routes SET capability_status='unverified', capability_error=$2, capabilities_checked_at=NOW(), updated_at=NOW() WHERE id=$1`, r.PathValue("id"), err.Error())
			writeError(w, http.StatusConflict, "model_probe_blocked", err.Error())
			return
		}
		_, _ = s.db.Exec(r.Context(), `UPDATE model_routes SET capability_status='failed', capability_error=$2, capabilities_checked_at=NOW(), updated_at=NOW() WHERE id=$1`, r.PathValue("id"), err.Error())
		writeError(w, http.StatusUnprocessableEntity, "model_validation_failed", err.Error())
		return
	}
	profileJSON, _ := json.Marshal(profile)
	_, err = s.db.Exec(r.Context(), `
		UPDATE model_routes SET supports_chat=$2, supports_responses=$3, supports_messages=$4,
		       capability_status=$5, capability_profile=$6, capabilities_checked_at=$7,
		       capability_error='', updated_at=NOW()
		WHERE id=$1
	`, r.PathValue("id"), input.SupportsChat, input.SupportsResponses, input.SupportsMessages,
		status, profileJSON, checkedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "model_probe_save_failed", "Capability result could not be saved.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.probe", "model", r.PathValue("id"), map[string]any{"status": status})
	writeJSON(w, http.StatusOK, map[string]any{"capability_status": status, "capability_profile": profile, "checked_at": checkedAt})
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
	Label           string     `json:"label"`
	Secret          string     `json:"secret"`
	IsPrimary       bool       `json:"is_primary"`
	Enabled         *bool      `json:"enabled,omitempty"`
	AllowUnverified bool       `json:"allow_unverified,omitempty"`
	Limits          RatePolicy `json:"limits"`
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
	primaryCount := 0
	for index := range input.Credentials {
		credential := &input.Credentials[index]
		credential.Label = strings.TrimSpace(credential.Label)
		credential.Secret = strings.TrimSpace(credential.Secret)
		if credential.IsPrimary {
			primaryCount++
		}
		if len(credential.Label) < 1 || len(credential.Label) > 100 ||
			len(credential.Secret) < 8 || len(credential.Secret) > 8192 || !credential.Limits.Valid() {
			writeError(w, http.StatusBadRequest, "invalid_credential", "API key label, value, or rate limits are invalid.")
			return
		}
	}
	if primaryCount > 1 {
		writeError(w, http.StatusBadRequest, "multiple_primary_credentials", "Choose at most one primary API key.")
		return
	}

	provider, err := scanProvider(s.db.QueryRow(
		r.Context(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, r.PathValue("id"),
	))
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	inspections := make([]credentialInspection, 0, len(input.Credentials))
	for _, credential := range input.Credentials {
		inspection := inspectProviderSecret(r.Context(), provider, []byte(credential.Secret))
		if !inspection.Valid && !credential.AllowUnverified {
			writeError(
				w, http.StatusUnprocessableEntity, "invalid_credential",
				fmt.Sprintf("%s: %s The API key was not saved.", credential.Label, inspection.Warning),
			)
			return
		}
		inspections = append(inspections, inspection)
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "API keys could not be saved.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if primaryCount == 1 {
		if _, err := tx.Exec(r.Context(), `
			UPDATE credentials SET is_primary=FALSE, updated_at=NOW() WHERE provider_id=$1
		`, r.PathValue("id")); err != nil {
			writeError(w, http.StatusInternalServerError, "credential_create_failed", "Primary API key could not be updated.")
			return
		}
	}
	created := make([]string, 0, len(input.Credentials))
	for index, credential := range input.Credentials {
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
		validationError := ""
		if !enabled {
			status = "disabled"
		}
		if !inspections[index].Valid {
			validationError = "Saved without validation: " + inspections[index].Warning
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO credentials
			    (id, provider_id, label, secret_cipher, secret_suffix, is_primary,
			     enabled, status, last_validated_at, validation_error)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NOW(),$9)
		`, id, r.PathValue("id"), credential.Label, encrypted, secretSuffix(credential.Secret),
			credential.IsPrimary, enabled, status, validationError); err != nil {
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
	writeJSON(w, http.StatusCreated, map[string]any{
		"ids": created, "models": mergeDiscoveredModels(inspections),
	})
}

func (s *Server) handleUpdateCredential(w http.ResponseWriter, r *http.Request) {
	var input credentialInput
	if decodeJSON(w, r, 256<<10, &input) != nil {
		return
	}
	input.Label = strings.TrimSpace(input.Label)
	if input.Label == "" || len(input.Label) > 100 || !input.Limits.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_credential", "API key label or limits are invalid.")
		return
	}
	var providerID string
	var currentCiphertext []byte
	if err := s.db.QueryRow(
		r.Context(), `SELECT provider_id, secret_cipher FROM credentials WHERE id=$1`, r.PathValue("id"),
	).Scan(&providerID, &currentCiphertext); err != nil {
		writeError(w, http.StatusNotFound, "credential_not_found", "Credential was not found.")
		return
	}
	provider, err := scanProvider(s.db.QueryRow(
		r.Context(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, providerID,
	))
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	replacement := strings.TrimSpace(input.Secret)
	if replacement != "" && (len(replacement) < 8 || len(replacement) > 8192) {
		writeError(w, http.StatusBadRequest, "invalid_credential", "Replacement API key is invalid.")
		return
	}
	shouldInspect := enabled || replacement != ""
	var inspection credentialInspection
	if shouldInspect {
		secret := []byte(replacement)
		if replacement == "" {
			secret, err = s.vault.Decrypt(currentCiphertext)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "credential_unavailable", "API key could not be decrypted.")
				return
			}
		}
		inspection = inspectProviderSecret(r.Context(), provider, secret)
		if !inspection.Valid {
			writeError(
				w, http.StatusUnprocessableEntity, "invalid_credential",
				inspection.Warning+" Changes were not saved.",
			)
			return
		}
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "API key could not be updated.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if input.IsPrimary {
		if _, err := tx.Exec(r.Context(), `
			UPDATE credentials SET is_primary=FALSE, updated_at=NOW()
			WHERE provider_id=$1 AND id<>$2 AND is_primary=TRUE
		`, providerID, r.PathValue("id")); err != nil {
			writeError(w, http.StatusInternalServerError, "credential_update_failed", "Primary API key could not be updated.")
			return
		}
	}
	status := "healthy"
	if !enabled {
		status = "disabled"
	}
	var affected int64
	if replacement != "" {
		encrypted, err := s.vault.Encrypt([]byte(replacement))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption_failed", "API key could not be encrypted.")
			return
		}
		commandTag, err := tx.Exec(r.Context(), `
			UPDATE credentials SET label=$2, secret_cipher=$3, secret_suffix=$4,
			    is_primary=$5, enabled=$6, status=$7, cooldown_until=NULL, consecutive_failures=0,
			    validation_error='', last_validated_at=NOW(), updated_at=NOW() WHERE id=$1
		`, r.PathValue("id"), input.Label, encrypted, secretSuffix(replacement), input.IsPrimary, enabled, status)
		if err != nil {
			writeError(w, http.StatusConflict, "credential_update_failed", "Credential could not be updated.")
			return
		}
		affected = commandTag.RowsAffected()
	} else {
		query := `
			UPDATE credentials SET label=$2, is_primary=$3, enabled=$4, status=$5,
			    cooldown_until=NULL, consecutive_failures=0, updated_at=NOW()`
		if shouldInspect {
			query += `, validation_error='', last_validated_at=NOW()`
		}
		query += ` WHERE id=$1`
		commandTag, err := tx.Exec(r.Context(), query, r.PathValue("id"), input.Label, input.IsPrimary, enabled, status)
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
	writeJSON(w, http.StatusOK, map[string]any{"models": inspection.Models})
}

func (s *Server) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	var resources int
	if err := s.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM anthropic_resources WHERE credential_id=$1`, r.PathValue("id")).Scan(&resources); err == nil && resources > 0 {
		writeError(w, http.StatusConflict, "credential_has_resources", "Delete the Anthropic files or batches pinned to this API key first.")
		return
	}
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
	rows, err := s.db.Query(r.Context(), `
		SELECT id, label, secret_cipher FROM credentials
		WHERE provider_id=$1 AND enabled=TRUE
		ORDER BY is_primary DESC, created_at, id
	`, provider.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "API keys could not be loaded.")
		return
	}
	defer rows.Close()
	type testResult struct {
		ID         string `json:"id"`
		Label      string `json:"label"`
		Valid      bool   `json:"valid"`
		StatusCode int    `json:"status_code"`
		LatencyMS  int64  `json:"latency_ms"`
		Warning    string `json:"warning,omitempty"`
		ModelCount int    `json:"model_count"`
	}
	results := []testResult{}
	valid := 0
	for rows.Next() {
		var id, label string
		var ciphertext []byte
		if err := rows.Scan(&id, &label, &ciphertext); err != nil {
			writeError(w, http.StatusInternalServerError, "credential_unavailable", "API key could not be loaded.")
			return
		}
		secret, err := s.vault.Decrypt(ciphertext)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "credential_unavailable", "API key could not be decrypted.")
			return
		}
		inspection := inspectProviderSecret(r.Context(), provider, secret)
		s.recordCredentialInspection(r.Context(), id, inspection)
		if inspection.Valid {
			valid++
		}
		results = append(results, testResult{
			ID: id, Label: label, Valid: inspection.Valid,
			StatusCode: inspection.StatusCode, LatencyMS: inspection.LatencyMS,
			Warning: inspection.Warning, ModelCount: len(inspection.Models),
		})
	}
	if len(results) == 0 {
		writeError(w, http.StatusConflict, "credential_required", "Add an enabled API key before testing.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": valid == len(results), "valid": valid, "total": len(results), "results": results,
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	status, _ := strconv.Atoi(statusFilter)
	errorsOnly := statusFilter == "errors"
	runningOnly := statusFilter == "running"
	activeLogs := s.activeRequestLogs(query)
	if runningOnly {
		if len(activeLogs) > limit {
			activeLogs = activeLogs[:limit]
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": activeLogs})
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, request_id, model_alias, provider_name, credential_label, endpoint,
		       attempts, routing_decisions, status_code, latency_ms, input_tokens, output_tokens,
		       COALESCE(error_code,''), COALESCE(error_message,''), request_body_cipher IS NOT NULL,
		       body_truncated, public_protocol, upstream_protocol, upstream_request_id, created_at
		FROM request_logs
		WHERE ($1 = '' OR model_alias ILIKE '%' || $1 || '%' OR request_id ILIKE '%' || $1 || '%')
		  AND ($2 = 0 OR status_code = $2)
		  AND (NOT $3 OR status_code >= 400)
		ORDER BY created_at DESC LIMIT $4
	`, query, status, errorsOnly, limit)
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
			&log.CredentialLabel, &log.Endpoint, &log.Attempts, &log.RoutingDecisions, &log.StatusCode,
			&log.LatencyMS, &log.InputTokens, &log.OutputTokens, &log.ErrorCode, &log.ErrorMessage,
			&log.BodyCaptured, &log.BodyTruncated,
			&log.PublicProtocol, &log.UpstreamProtocol, &log.UpstreamRequestID,
			&log.CreatedAt,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "logs_unavailable", "Request logs could not be decoded.")
			return
		}
		logs = append(logs, log)
	}
	if statusFilter == "" && len(activeLogs) > 0 {
		logs = append(activeLogs, logs...)
		if len(logs) > limit {
			logs = logs[:limit]
		}
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
	settings.BaseURL = strings.TrimRight(s.cfg.PublicBaseURL, "/")
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var input AppSettings
	if decodeJSON(w, r, 32<<10, &input) != nil {
		return
	}
	if input.MetadataRetentionDays < 1 || input.MetadataRetentionDays > 3650 ||
		input.BodyRetentionDays < 1 || input.BodyRetentionDays > 365 ||
		input.MaxWaitMS < 0 || input.MaxWaitMS > 30000 ||
		input.DefaultProviderTimeoutSecs < 1 || input.DefaultProviderTimeoutSecs > 900 {
		writeError(w, http.StatusBadRequest, "invalid_settings", "Retention, wait, or provider timeout settings are outside allowed ranges.")
		return
	}
	if input.DefaultAnthropicProviderID != "" {
		var valid bool
		if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM providers WHERE id=$1 AND api_format='anthropic' AND enabled=TRUE)`, input.DefaultAnthropicProviderID).Scan(&valid); err != nil || !valid {
			writeError(w, http.StatusBadRequest, "invalid_anthropic_provider", "Choose an enabled Anthropic-compatible provider.")
			return
		}
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Settings could not be updated.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err := tx.Exec(r.Context(), `
		UPDATE app_settings SET metadata_retention_days=$1, body_retention_days=$2,
		    max_wait_ms=$3, default_provider_timeout_seconds=$4,
		    default_anthropic_provider_id=NULLIF($5,''), updated_at=NOW() WHERE id=1
	`, input.MetadataRetentionDays, input.BodyRetentionDays, input.MaxWaitMS, input.DefaultProviderTimeoutSecs, input.DefaultAnthropicProviderID); err != nil {
		writeError(w, http.StatusInternalServerError, "settings_update_failed", "Settings could not be updated.")
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE providers SET timeout_seconds=$1, updated_at=NOW()`, input.DefaultProviderTimeoutSecs); err != nil {
		writeError(w, http.StatusInternalServerError, "settings_update_failed", "Provider timeouts could not be updated.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "settings_update_failed", "Settings could not be updated.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "settings.update", "system", "", map[string]any{
		"metadata_retention_days":          input.MetadataRetentionDays,
		"body_retention_days":              input.BodyRetentionDays,
		"max_wait_ms":                      input.MaxWaitMS,
		"default_provider_timeout_seconds": input.DefaultProviderTimeoutSecs,
		"default_anthropic_provider_id":    input.DefaultAnthropicProviderID,
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
	if provider.APIFormat == "anthropic" {
		version := provider.AnthropicVersion
		if version == "" {
			version = "2023-06-01"
		}
		request.Header.Set("Anthropic-Version", version)
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
