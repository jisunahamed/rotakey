package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

var errModelProbeCredentialUnavailable = errors.New("add a healthy API key before validating this model")

type credentialInspection struct {
	Valid            bool              `json:"valid"`
	CatalogAvailable bool              `json:"catalog_available"`
	ProtocolVerified bool              `json:"protocol_verified"`
	Protocol         string            `json:"protocol"`
	DetectedProtocol string            `json:"detected_protocol,omitempty"`
	StatusCode       int               `json:"status_code"`
	LatencyMS        int64             `json:"latency_ms"`
	Models           []DiscoveredModel `json:"models"`
	Warning          string            `json:"warning,omitempty"`
}

type providerModelCatalog struct {
	Data []struct {
		ID          string `json:"id"`
		OwnedBy     string `json:"owned_by"`
		DisplayName string `json:"display_name"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

func inspectProviderSecret(ctx context.Context, provider Provider, secret []byte) credentialInspection {
	protocol := provider.APIFormat
	if protocol == "" {
		protocol = "openai"
	}
	result := credentialInspection{Models: []DiscoveredModel{}, Protocol: protocol}
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
			if provider.APIFormat == "anthropic" && (response.StatusCode == http.StatusUseProxy || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode/100 == 3) {
				result.Valid = true
				result.Warning = fmt.Sprintf("Model catalog is unavailable (HTTP %d). Add a model ID manually; Rotakey will validate it with Messages before use.", response.StatusCode)
				return result
			}
			result.Warning = fmt.Sprintf("Provider returned HTTP %d while checking the API key.", response.StatusCode)
		}
		return result
	}

	var payload providerModelCatalog
	if err := json.Unmarshal(body, &payload); err != nil {
		if provider.APIFormat == "anthropic" {
			result.Valid = true
			result.Warning = "The Anthropic model catalog has a non-standard response. Add a model ID manually; Rotakey will validate it with Messages before use."
			return result
		}
		result.Warning = "Provider returned an invalid OpenAI-compatible model list."
		return result
	}
	if provider.APIFormat == "anthropic" {
		for page := 1; payload.HasMore && page < 10 && len(payload.Data) < 500; page++ {
			afterID := payload.LastID
			if afterID == "" && len(payload.Data) > 0 {
				afterID = payload.Data[len(payload.Data)-1].ID
			}
			if afterID == "" {
				break
			}
			pageTarget := strings.TrimRight(provider.BaseURL, "/") + "/models?limit=100&after_id=" + url.QueryEscape(afterID)
			pageRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, pageTarget, nil)
			applyProviderHeaders(pageRequest, provider, secret)
			pageResponse, pageErr := client.Do(pageRequest)
			if pageErr != nil {
				result.Warning = "The provider model catalog ended before all pages were loaded."
				break
			}
			pageBody, _, pageReadErr := boundedBody(pageResponse.Body, 4<<20)
			_ = pageResponse.Body.Close()
			if pageReadErr != nil || pageResponse.StatusCode < 200 || pageResponse.StatusCode >= 300 {
				result.Warning = fmt.Sprintf("The provider model catalog ended at HTTP %d; loaded models remain selectable.", pageResponse.StatusCode)
				break
			}
			var next providerModelCatalog
			if json.Unmarshal(pageBody, &next) != nil {
				result.Warning = "The provider model catalog returned an invalid later page; loaded models remain selectable."
				break
			}
			payload.Data = append(payload.Data, next.Data...)
			payload.HasMore, payload.LastID = next.HasMore, next.LastID
		}
	}
	seen := make(map[string]bool, len(payload.Data))
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" || len(id) > 255 || seen[id] {
			continue
		}
		seen[id] = true
		result.Models = append(result.Models, DiscoveredModel{
			ID: id, OwnedBy: valueOr(strings.TrimSpace(model.OwnedBy), strings.TrimSpace(model.DisplayName)),
		})
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].ID < result.Models[j].ID })
	result.CatalogAvailable = true
	if len(result.Models) == 0 {
		result.Warning = "The provider returned an empty model catalog. Check that the base URL includes the provider's API prefix (usually /v1)."
		return result
	}
	// A provider-wide inference probe uses an arbitrary catalog entry. Some
	// providers expose catalog-only, entitlement-gated, or non-chat entries, so
	// that probe can reject a valid credential. Their authenticated OpenAI
	// catalog is enough to validate the key and base URL; each selected route is
	// checked separately by the model capability probe.
	if catalogVerifiesProvider(provider) {
		result.ProtocolVerified = true
		result.DetectedProtocol = protocol
		result.Valid = true
		if isNVIDIAOpenAIProvider(provider) {
			result.Warning = "NVIDIA model catalog and API key verified. Use Check all models to verify each selected inference route."
		}
		return result
	}
	verified, detected, status, warning := verifyProviderProtocol(ctx, client, provider, secret, result.Models[0].ID)
	result.ProtocolVerified = verified
	result.DetectedProtocol = detected
	if status != 0 {
		result.StatusCode = status
	}
	if !verified {
		// A model catalog proves that this credential can authenticate to the
		// provider, but its first entry is not necessarily an inference model the
		// credential can use. Keep the catalog usable when that arbitrary probe
		// returns model-specific 404 rather than rejecting the whole provider.
		if result.CatalogAvailable && (detected == "" || detected == protocol) && status == http.StatusNotFound {
			result.Valid = true
			result.Warning = fmt.Sprintf("Model catalog loaded, but %q could not be used for the automatic protocol check. Select only models this API key can access. %s", result.Models[0].ID, warning)
			return result
		}
		result.Warning = warning
		return result
	}
	result.Valid = true
	return result
}

func isGeminiOpenAIProvider(provider Provider) bool {
	if provider.APIFormat != "" && provider.APIFormat != "openai" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(provider.BaseURL))
	if err != nil || !strings.EqualFold(parsed.Hostname(), "generativelanguage.googleapis.com") {
		return false
	}
	return strings.TrimRight(parsed.Path, "/") == "/v1beta/openai"
}

func isNVIDIAOpenAIProvider(provider Provider) bool {
	if provider.APIFormat != "" && provider.APIFormat != "openai" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(provider.BaseURL))
	if err != nil || !strings.EqualFold(parsed.Hostname(), "integrate.api.nvidia.com") {
		return false
	}
	return strings.TrimRight(parsed.Path, "/") == "/v1"
}

func catalogVerifiesProvider(provider Provider) bool {
	if isGeminiOpenAIProvider(provider) || isNVIDIAOpenAIProvider(provider) {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(provider.BaseURL))
	if err != nil {
		return false
	}
	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case provider.APIFormat == "openai" && strings.EqualFold(parsed.Hostname(), "api.openai.com"):
		return path == "/v1"
	case provider.APIFormat == "anthropic" && strings.EqualFold(parsed.Hostname(), "api.anthropic.com"):
		return path == "/v1"
	default:
		return false
	}
}

func upstreamModelForProvider(provider Provider, model string) string {
	model = strings.TrimSpace(model)
	if isGeminiOpenAIProvider(provider) {
		return strings.TrimPrefix(model, "models/")
	}
	return model
}

func verifyProviderProtocol(ctx context.Context, client *http.Client, provider Provider, secret []byte, model string) (bool, string, int, string) {
	protocol := provider.APIFormat
	if protocol == "" {
		protocol = "openai"
	}
	path := "/chat/completions"
	payload := map[string]any{
		"model":      upstreamModelForProvider(provider, model),
		"messages":   []any{map[string]any{"role": "user", "content": "Reply with one character."}},
		"max_tokens": 1,
	}
	if protocol == "anthropic" {
		path = "/messages"
	}
	body, _ := json.Marshal(payload)
	probeCtx, cancel := context.WithTimeout(ctx, modelProbeTimeout(provider))
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodPost, strings.TrimRight(provider.BaseURL, "/")+path, strings.NewReader(string(body)))
	if err != nil {
		return false, "", 0, "The provider base URL could not be checked."
	}
	applyProviderHeaders(request, provider, secret)
	response, err := client.Do(request)
	if err != nil {
		return false, "", 0, "The model catalog worked, but the protocol endpoint could not be reached. Check the provider base URL."
	}
	defer response.Body.Close()
	raw, _, readErr := boundedBody(response.Body, 1<<20)
	if readErr != nil {
		return false, "", response.StatusCode, "The provider protocol-check response could not be read."
	}
	var decoded map[string]any
	if json.Unmarshal(raw, &decoded) != nil {
		return false, "", response.StatusCode, fmt.Sprintf("Base URL/API format mismatch: POST %s returned HTTP %d with a non-JSON response. Check the API prefix (usually /v1).", path, response.StatusCode)
	}
	detected := detectProviderProtocol(decoded)
	if detected != "" && detected != protocol {
		return false, detected, response.StatusCode, fmt.Sprintf("Base URL/API format mismatch: configured as %s-compatible, but POST %s returned a %s response.", protocol, path, detected)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return false, detected, response.StatusCode, fmt.Sprintf("API key was rejected by the provider (HTTP %d).", response.StatusCode)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && detected == protocol {
		return true, detected, response.StatusCode, ""
	}
	if detected == protocol {
		message := upstreamErrorMessage(raw, secret)
		if message == "" {
			message = fmt.Sprintf("Provider returned HTTP %d.", response.StatusCode)
		}
		switch {
		case response.StatusCode == http.StatusNotFound:
			return false, detected, response.StatusCode, fmt.Sprintf("Model or endpoint was not found during protocol check. Check the base URL path and the upstream model ID. Provider said: %s", message)
		case response.StatusCode == http.StatusBadRequest:
			return false, detected, response.StatusCode, fmt.Sprintf("The provider understood %s-compatible JSON but rejected the protocol probe. Check model availability and required request parameters. Provider said: %s", protocol, message)
		case response.StatusCode >= 500:
			return false, detected, response.StatusCode, fmt.Sprintf("Provider returned HTTP %d during protocol check. This is usually an upstream outage or proxy issue. Provider said: %s", response.StatusCode, message)
		default:
			return false, detected, response.StatusCode, fmt.Sprintf("Provider returned HTTP %d during protocol check. Provider said: %s", response.StatusCode, message)
		}
	}
	if response.StatusCode == http.StatusNotFound {
		message := upstreamErrorMessage(raw, secret)
		if message == "" {
			message = "The provider did not return a standard error message."
		}
		return false, detected, response.StatusCode, fmt.Sprintf("The protocol probe returned HTTP 404. The API key and model catalog may still be valid; check the base URL path and run a model capability check on a selected route. Provider said: %s", message)
	}
	return false, detected, response.StatusCode, fmt.Sprintf("Base URL/API format mismatch: POST %s returned HTTP %d without a valid %s response envelope. Check the API prefix and selected protocol.", path, response.StatusCode, protocol)
}

func detectProviderProtocol(payload map[string]any) string {
	if payload["type"] == "message" {
		return "anthropic"
	}
	if choices, ok := payload["choices"].([]any); ok && len(choices) > 0 {
		return "openai"
	}
	if rawError, ok := payload["error"].(map[string]any); ok {
		if payload["type"] == "error" {
			return "anthropic"
		}
		if rawError["message"] != nil || rawError["code"] != nil {
			return "openai"
		}
	}
	return ""
}

func providerFromInput(input providerInput) Provider {
	return Provider{
		Name: input.Name, Slug: input.Slug, BaseURL: input.BaseURL,
		AuthHeader: input.AuthHeader, AuthScheme: input.AuthScheme,
		ExtraHeaders: input.ExtraHeaders, TimeoutSeconds: input.TimeoutSeconds,
		Enabled: input.Enabled, AllowPrivateNetwork: input.AllowPrivateNetwork,
		APIFormat: input.APIFormat, AnthropicVersion: input.AnthropicVersion,
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
	provider, err := scanProvider(s.db.QueryRow(r.Context(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, r.PathValue("id")))
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	type capabilityResult struct {
		Status    string
		Profile   map[string]string
		CheckedAt *time.Time
	}
	capabilities := map[string]capabilityResult{}
	for index := range input.Models {
		model := &input.Models[index]
		if existing, ok := capabilities[model.UpstreamModel]; ok {
			model.SupportsChat = existing.Profile["chat"] != "off"
			model.SupportsMessages = existing.Profile["messages"] != "off"
			continue
		}
		if model.Manual {
			status, profile, checkedAt, probeErr := s.probeProviderModel(r.Context(), provider.ID, model)
			if probeErr != nil {
				writeError(w, http.StatusUnprocessableEntity, "model_validation_failed", probeErr.Error())
				return
			}
			capabilities[model.UpstreamModel] = capabilityResult{Status: status, Profile: profile, CheckedAt: checkedAt}
			continue
		}
		profile := modelCapabilityProfile(provider, model, "catalog")
		now := time.Now().UTC()
		capabilities[model.UpstreamModel] = capabilityResult{Status: "catalog_verified", Profile: profile, CheckedAt: &now}
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
		capability := capabilities[model.UpstreamModel]
		profileJSON, _ := json.Marshal(capability.Profile)
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO model_routes
			    (id, provider_id, public_alias, upstream_model, supports_chat,
			     supports_responses, supports_messages, default_max_output_tokens, tokenizer,
			     capture_bodies, strip_parameters, capability_status, capability_profile,
			     capabilities_checked_at, capability_error, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'',$15)
		`, id, r.PathValue("id"), model.PublicAlias, model.UpstreamModel,
			model.SupportsChat, model.SupportsResponses, model.SupportsMessages, model.DefaultMaxOutputTokens,
			model.Tokenizer, model.CaptureBodies, model.StripParameters, capability.Status, profileJSON, capability.CheckedAt, model.Enabled); err != nil {
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
	// Only 401 quarantines from a check. The check is a GET on the provider's model
	// list, and a 403 there routinely means "this key may not list models" on
	// upstreams that still serve inference with it perfectly well — quarantining on
	// that took a whole provider off the air whenever "Check every key" was
	// pressed. A 403 is recorded as a note instead, so the operator sees it and the
	// key keeps routing until a real request proves otherwise.
	if inspection.StatusCode == http.StatusUnauthorized {
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

func modelCapabilityProfile(provider Provider, input *modelInput, source string) map[string]string {
	profile := map[string]string{
		"availability":      map[bool]string{true: "verified", false: "unknown"}[source == "probe"],
		"verification":      source,
		"upstream_protocol": provider.APIFormat,
		"streaming":         "gateway_normalized",
		"json_output":       "unknown",
	}
	if provider.APIFormat == "anthropic" {
		input.SupportsChat = true
		input.SupportsMessages = true
		input.SupportsResponses = false
		profile["chat"] = "translated"
		profile["responses"] = "translated"
		profile["messages"] = "native"
		profile["tools"] = "native"
		profile["thinking"] = "native_unverified"
	} else {
		if input.SupportsChat {
			input.SupportsMessages = true
			profile["chat"] = "native"
			profile["messages"] = "translated"
			profile["tools"] = "native_unverified"
		} else if input.SupportsResponses {
			// A route that serves only Responses can still answer a Chat or an
			// Anthropic caller, because the gateway translates the request up into
			// Responses and the answer back down.
			input.SupportsMessages = true
			profile["chat"] = "translated"
			profile["messages"] = "translated"
			profile["tools"] = "native_unverified"
		} else {
			input.SupportsMessages = false
			profile["chat"] = "off"
			profile["messages"] = "off"
			profile["tools"] = "unknown"
		}
		if input.SupportsResponses {
			profile["responses"] = "native"
		} else if input.SupportsChat {
			profile["responses"] = "translated"
		} else {
			profile["responses"] = "off"
		}
		profile["thinking"] = "unsupported_cross_protocol"
	}
	if source == "catalog" {
		profile["availability"] = "catalog_visible"
	}
	return profile
}

func (s *Server) probeProviderModel(ctx context.Context, providerID string, input *modelInput) (string, map[string]string, *time.Time, error) {
	provider, err := scanProvider(s.db.QueryRow(ctx, `SELECT `+providerColumns+` FROM providers WHERE id=$1`, providerID))
	if err != nil {
		return "failed", nil, nil, fmt.Errorf("provider was not found")
	}
	rows, err := s.db.Query(ctx, `
		SELECT secret_cipher FROM credentials
		WHERE provider_id=$1 AND enabled=TRUE
		  AND (status='healthy' OR (status='cooldown' AND cooldown_until <= NOW()))
		ORDER BY is_primary DESC, created_at, id
		LIMIT 3
	`, providerID)
	if err != nil {
		return "failed", nil, nil, fmt.Errorf("healthy API keys could not be loaded")
	}
	defer rows.Close()
	ciphertexts := make([][]byte, 0, 3)
	for rows.Next() {
		var ciphertext []byte
		if err := rows.Scan(&ciphertext); err != nil {
			return "failed", nil, nil, fmt.Errorf("healthy API keys could not be loaded")
		}
		ciphertexts = append(ciphertexts, ciphertext)
	}
	if err := rows.Err(); err != nil {
		return "failed", nil, nil, fmt.Errorf("healthy API keys could not be loaded")
	}
	if len(ciphertexts) == 0 {
		return "unverified", nil, nil, errModelProbeCredentialUnavailable
	}
	var lastErr error
	for _, ciphertext := range ciphertexts {
		secret, decryptErr := s.vault.Decrypt(ciphertext)
		if decryptErr != nil {
			lastErr = fmt.Errorf("API key could not be decrypted")
			continue
		}
		status, profile, checkedAt, statusCode, probeErr := probeProviderModelWithSecret(ctx, provider, input, secret)
		if probeErr == nil {
			return status, profile, checkedAt, nil
		}
		lastErr = probeErr
		if !retryModelProbeWithAnotherCredential(statusCode) {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("model capability probe failed")
	}
	return "failed", nil, nil, lastErr
}

func probeProviderModelWithSecret(ctx context.Context, provider Provider, input *modelInput, secret []byte) (string, map[string]string, *time.Time, int, error) {
	probeContext, cancel := context.WithTimeout(ctx, modelProbeTimeout(provider))
	defer cancel()
	path := "/chat/completions"
	upstreamModel := upstreamModelForProvider(provider, input.UpstreamModel)
	payload := map[string]any{
		"model":      upstreamModel,
		"messages":   []any{map[string]any{"role": "user", "content": "Reply with one character."}},
		"max_tokens": 1,
	}
	if provider.APIFormat == "anthropic" {
		path = "/messages"
	} else if !input.SupportsChat && input.SupportsResponses {
		path = "/responses"
		payload = map[string]any{"model": upstreamModel, "input": "Reply with one character.", "max_output_tokens": 1}
	}
	body, _ := json.Marshal(payload)
	request, _ := http.NewRequestWithContext(probeContext, http.MethodPost, strings.TrimRight(provider.BaseURL, "/")+path, strings.NewReader(string(body)))
	applyProviderHeaders(request, provider, secret)
	client, err := upstreamClient(provider)
	if err != nil {
		return "failed", nil, nil, 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return "failed", nil, nil, probeTransportStatus(err), fmt.Errorf("provider could not be reached for the model capability probe: %s", safeProbeTransportError(err))
	}
	defer response.Body.Close()
	raw, _, readErr := boundedBody(response.Body, 1<<20)
	if readErr != nil {
		return "failed", nil, nil, response.StatusCode, fmt.Errorf("model probe response could not be read")
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var decoded map[string]any
		if json.Unmarshal(raw, &decoded) != nil {
			return "failed", nil, nil, response.StatusCode, fmt.Errorf("model probe returned an invalid response")
		}
		if provider.APIFormat == "anthropic" && decoded["type"] != "message" {
			return "failed", nil, nil, response.StatusCode, fmt.Errorf("Messages probe did not return an Anthropic Message")
		}
		if provider.APIFormat == "openai" && path == "/chat/completions" {
			if choices, ok := decoded["choices"].([]any); !ok || len(choices) == 0 {
				return "failed", nil, nil, response.StatusCode, fmt.Errorf("Chat probe did not return a completion choice")
			}
		}
		now := time.Now().UTC()
		if native, checked := probeNativeResponses(probeContext, provider, input, secret, path); checked {
			// The chat probe passed, so a 404 here is the endpoint's absence rather
			// than a bad model or key: most OpenAI-compatible hosts implement only
			// Chat Completions. Recording that now means /v1/responses traffic is
			// translated from the first request instead of failing once per route.
			input.SupportsResponses = native
		}
		profile := modelCapabilityProfile(provider, input, "probe")
		return "probe_verified", profile, &now, response.StatusCode, nil
	}
	message := upstreamErrorMessage(raw, secret)
	if message == "" {
		message = fmt.Sprintf("provider returned HTTP %d", response.StatusCode)
	}
	if response.StatusCode == http.StatusNotFound && path == "/responses" {
		return "failed", nil, nil, response.StatusCode, fmt.Errorf(
			"provider has no %s endpoint at this base URL; turn on Chat Completions for this route or correct the base URL. Provider said: %s",
			strings.TrimPrefix(path, "/"), message)
	}
	return "failed", nil, nil, response.StatusCode, fmt.Errorf("model capability probe failed: %s", message)
}

// probeNativeResponses checks whether an OpenAI-compatible provider really
// serves /responses for a route that claims it does. It reports the verified
// value and whether the check ran at all; the caller keeps the configured flag
// when it did not.
func probeNativeResponses(ctx context.Context, provider Provider, input *modelInput, secret []byte, probedPath string) (bool, bool) {
	if provider.APIFormat == "anthropic" || !input.SupportsResponses || probedPath == "/responses" {
		return false, false
	}
	payload, _ := json.Marshal(map[string]any{
		"model":             upstreamModelForProvider(provider, input.UpstreamModel),
		"input":             "Reply with one character.",
		"max_output_tokens": 1,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(provider.BaseURL, "/")+"/responses", strings.NewReader(string(payload)))
	if err != nil {
		return false, false
	}
	applyProviderHeaders(request, provider, secret)
	client, err := upstreamClient(provider)
	if err != nil {
		return false, false
	}
	response, err := client.Do(request)
	if err != nil {
		return false, false
	}
	defer response.Body.Close()
	_, _, _ = boundedBody(response.Body, 1<<20)
	// Only an outright 404 disproves the endpoint. Any other rejection — a bad
	// parameter, a quota, a per-model restriction — says the endpoint exists.
	return response.StatusCode != http.StatusNotFound, true
}

func modelProbeTimeout(provider Provider) time.Duration {
	seconds := provider.TimeoutSeconds
	if seconds <= 0 {
		seconds = 120
	}
	return time.Duration(seconds) * time.Second
}

func retryModelProbeWithAnotherCredential(statusCode int) bool {
	return statusCode == 0 || statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden ||
		statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func probeTransportStatus(err error) int {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return -1
	}
	return 0
}

func safeProbeTransportError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "request was canceled"
	default:
		return "connection failed"
	}
}
