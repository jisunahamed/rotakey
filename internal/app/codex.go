package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleCodexManifest publishes only routes that can be used by Codex. The
// response is deliberately small and stable so local bridges can cache it.
func (s *Server) handleCodexManifest(w http.ResponseWriter, r *http.Request) {
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Gateway settings are unavailable.")
		return
	}
	const codexFilter = `
		m.enabled=TRUE AND p.enabled=TRUE
		  AND (m.supports_responses=TRUE OR m.supports_chat=TRUE)
		  AND m.capability_status IN ('catalog_verified', 'probe_verified')
		  AND EXISTS (SELECT 1 FROM credentials c WHERE c.provider_id=p.id AND c.enabled=TRUE AND c.status <> 'quarantined')
	`
	query := `
		SELECT m.public_alias, p.name, m.supports_responses, m.supports_chat,
		       m.capability_status, m.capability_profile
		FROM model_routes m JOIN providers p ON p.id=m.provider_id
		WHERE ` + codexFilter + `
		ORDER BY m.public_alias`
	if normalizeRoutingMode(settings.RoutingMode) == routingModeModel {
		// Model-wise routing publishes one entry per alias. The provider column
		// becomes the pool size because no single provider owns the alias, and
		// capabilities are the union of what the pool can do.
		query = `
			SELECT m.public_alias,
			       'pool of ' || COUNT(*)::text,
			       BOOL_OR(m.supports_responses), BOOL_OR(m.supports_chat),
			       MIN(m.capability_status),
			       (ARRAY_AGG(m.capability_profile ORDER BY m.created_at, m.id))[1]
			FROM model_routes m JOIN providers p ON p.id=m.provider_id
			WHERE ` + codexFilter + `
			GROUP BY m.public_alias
			ORDER BY m.public_alias`
	}
	rows, err := s.db.Query(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "manifest_unavailable", "Codex model manifest is unavailable.")
		return
	}
	defer rows.Close()
	models := make([]any, 0)
	for rows.Next() {
		var alias, provider string
		var responses, chat bool
		var capabilityStatus string
		var rawProfile []byte
		if rows.Scan(&alias, &provider, &responses, &chat, &capabilityStatus, &rawProfile) != nil {
			continue
		}
		profile := map[string]string{}
		_ = json.Unmarshal(rawProfile, &profile)
		contextWindow := 128000
		if parsed, err := strconv.Atoi(profile["context_window"]); err == nil && parsed > 0 {
			contextWindow = parsed
		}
		reasoningLevels := []string{"low", "medium", "high"}
		if configured := strings.TrimSpace(profile["reasoning_levels"]); configured != "" {
			reasoningLevels = strings.Split(configured, ",")
			for index := range reasoningLevels {
				reasoningLevels[index] = strings.TrimSpace(reasoningLevels[index])
			}
		}
		models = append(models, map[string]any{
			"id": alias, "alias": alias, "display_name": alias, "provider": provider,
			"context_window": contextWindow, "supports_responses": responses,
			"supports_tools":            profile["tools"] != "unsupported" && (responses || chat),
			"supports_images":           profile["images"] == "supported" || profile["images"] == "native",
			"verified_reasoning_levels": reasoningLevels,
			"catalog_ready":             capabilityStatus == "catalog_verified" || capabilityStatus == "probe_verified",
		})
	}
	body, _ := json.Marshal(map[string]any{"object": "codex_manifest", "models": models})
	hash := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(hash[:]) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (s *Server) handleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	raw, err := readRequestBody(w, r, s.cfg.MaxRequestBytes)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the configured limit.")
		return
	}
	var request map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.")
		return
	}
	model, _ := request["model"].(string)
	if model == "" {
		writeError(w, http.StatusBadRequest, "model_required", "A public model alias is required.")
		return
	}
	summary := compactInputSummary(request["input"])
	encrypted, err := s.vault.Encrypt([]byte(summary))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "compaction_failed", "Compaction state could not be protected.")
		return
	}
	encoded := base64.RawURLEncoding.EncodeToString(encrypted)
	id, _ := newID("cmp")
	response := map[string]any{
		"id": id, "object": "response.compaction", "created_at": started.Unix(), "model": model,
		"status": "completed", "output": []any{map[string]any{
			"type": "compaction", "id": id + "_item", "encrypted_content": encoded,
		}},
	}
	if stream, _ := request["stream"].(bool); stream {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(w, nil, "response.created", map[string]any{"type": "response.created", "response": response})
		writeSSE(w, nil, "response.completed", map[string]any{"type": "response.completed", "response": response})
		writeRaw(w, nil, []byte("data: [DONE]\n\n"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) expandRotakeyCompaction(request map[string]any) error {
	input, ok := request["input"].([]any)
	if !ok {
		return nil
	}
	expanded := make([]any, 0, len(input))
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "compaction" {
			expanded = append(expanded, raw)
			continue
		}
		encoded, _ := item["encrypted_content"].(string)
		ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil {
			return unsupportedFeatureError{Feature: "foreign or invalid compaction item"}
		}
		plain, err := s.vault.Decrypt(ciphertext)
		if err != nil || !strings.HasPrefix(string(plain), "rotakey-compaction-v1:") {
			return unsupportedFeatureError{Feature: "foreign or invalid compaction item"}
		}
		summary := strings.TrimPrefix(string(plain), "rotakey-compaction-v1:")
		expanded = append(expanded, map[string]any{
			"type": "message", "role": "developer",
			"content": []any{map[string]any{
				"type": "input_text", "text": "Rotakey continuity context from the earlier conversation:\n" + summary,
			}},
		})
	}
	request["input"] = expanded
	return nil
}

func compactInputSummary(input any) string {
	b, _ := json.Marshal(input)
	text := string(b)
	if len(text) > 12000 {
		text = text[len(text)-12000:]
	}
	return fmt.Sprintf("rotakey-compaction-v1:%s", text)
}
