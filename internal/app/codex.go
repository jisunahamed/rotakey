package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// handleCodexManifest publishes only routes that can be used by Codex. The
// response is deliberately small and stable so local bridges can cache it.
func (s *Server) handleCodexManifest(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT m.public_alias, p.name, m.default_max_output_tokens,
		       m.supports_responses, m.supports_chat
		FROM model_routes m JOIN providers p ON p.id=m.provider_id
		WHERE m.enabled=TRUE AND p.enabled=TRUE
		  AND (m.supports_responses=TRUE OR m.supports_chat=TRUE)
		  AND EXISTS (SELECT 1 FROM credentials c WHERE c.provider_id=p.id AND c.enabled=TRUE AND c.status <> 'quarantined')
		ORDER BY m.public_alias`)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "manifest_unavailable", "Codex model manifest is unavailable.")
		return
	}
	defer rows.Close()
	models := make([]any, 0)
	for rows.Next() {
		var alias, provider string
		var contextWindow int
		var responses, chat bool
		if rows.Scan(&alias, &provider, &contextWindow, &responses, &chat) != nil {
			continue
		}
		if contextWindow <= 0 {
			contextWindow = 128000
		}
		models = append(models, map[string]any{
			"id": alias, "alias": alias, "display_name": alias, "provider": provider,
			"context_window": contextWindow, "supports_responses": responses,
			"supports_tools": responses || chat, "supports_images": false,
			"verified_reasoning_levels": []string{"low", "medium", "high"},
			"catalog_ready": true,
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
	encoded := base64.RawURLEncoding.EncodeToString([]byte(summary))
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

func compactInputSummary(input any) string {
	b, _ := json.Marshal(input)
	text := string(b)
	if len(text) > 12000 {
		text = text[len(text)-12000:]
	}
	return fmt.Sprintf("rotakey-compaction-v1:%s", text)
}
