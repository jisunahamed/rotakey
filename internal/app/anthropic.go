package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	messageModeAnthropic = "anthropic"
	messageModeChat      = "chat"
	messageModeResponses = "responses"
)

func (s *Server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := requestIDFromContext(r.Context())
	s.beginActiveRequest(requestID, "messages", started)
	defer s.activeRequests.Delete(requestID)
	raw, err := readRequestBody(w, r, s.cfg.MaxMessageBytes)
	if err != nil {
		s.rejectAnthropic(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Message request exceeds 32 MB.", logInput{RequestID: requestID, Endpoint: "messages", Started: started, PublicProtocol: "anthropic"})
		return
	}
	payload, err := decodeJSONMap(raw)
	if err != nil {
		s.rejectAnthropic(w, r, http.StatusBadRequest, "invalid_request_error", "Request body is not valid JSON.", logInput{RequestID: requestID, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic"})
		return
	}
	alias, _ := payload["model"].(string)
	if strings.TrimSpace(alias) == "" {
		s.rejectAnthropic(w, r, http.StatusBadRequest, "invalid_request_error", "A public model alias is required.", logInput{RequestID: requestID, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic"})
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		s.rejectAnthropic(w, r, http.StatusServiceUnavailable, "api_error", "Gateway settings are unavailable.", logInput{RequestID: requestID, Route: routeRuntime{Model: ModelRoute{PublicAlias: alias}}, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic"})
		return
	}
	routes, err := s.resolveRoutes(r.Context(), alias, settings.RoutingMode)
	if err != nil {
		s.rejectAnthropic(w, r, http.StatusNotFound, "not_found_error", "The requested model alias is not enabled.", logInput{RequestID: requestID, Route: routeRuntime{Model: ModelRoute{PublicAlias: alias}}, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic"})
		return
	}
	req := dispatchRequest{
		RequestID: requestID, Started: started, Endpoint: "messages",
		PublicMode: messageModeAnthropic, Alias: alias, Raw: raw, Public: payload,
		MaxWait: time.Duration(settings.MaxWaitMS) * time.Millisecond,
	}
	req.Stream, _ = payload["stream"].(bool)
	usable := make([]routeRuntime, 0, len(routes))
	for _, route := range routes {
		if routeSupportsRequest(route, req) {
			usable = append(usable, route)
		}
	}
	if len(usable) == 0 {
		s.rejectAnthropic(w, r, http.StatusBadRequest, "invalid_request_error", "This model route does not support Messages.", logInput{RequestID: requestID, Route: routes[0], Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic", UpstreamProtocol: routes[0].Provider.APIFormat})
		return
	}
	s.updateActiveRequest(requestID, func(log *RequestLog) {
		log.ModelAlias = alias
		log.ProviderName = usable[0].Provider.Name
		log.PublicProtocol = "anthropic"
		log.UpstreamProtocol = usable[0].Provider.APIFormat
	})
	// File references bind a request to the exact provider and key that hold the
	// upload, so affinity resolution restricts the pool to that one route.
	forcedCredential, affinityRoutes, affinityErr := s.resolvePooledResourceAffinity(r.Context(), payload, usable)
	if affinityErr != nil {
		s.rejectAnthropic(w, r, http.StatusBadRequest, "resource_affinity_conflict", affinityErr.Error(), logInput{RequestID: requestID, Route: usable[0], Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic", UpstreamProtocol: usable[0].Provider.APIFormat})
		return
	}
	s.servePooled(w, r, req, affinityRoutes, forcedCredential)
}

// resolvePooledResourceAffinity finds the credential a request's file
// references belong to and narrows the pool to that credential's provider. A
// pooled alias may span providers, so the affinity check runs against each
// native Anthropic route until one owns the files.
func (s *Server) resolvePooledResourceAffinity(
	ctx context.Context,
	payload map[string]any,
	routes []routeRuntime,
) (string, []routeRuntime, error) {
	if len(collectStringFields(payload, "file_id")) == 0 {
		return "", routes, nil
	}
	var lastErr error
	for _, route := range routes {
		if route.Provider.APIFormat != "anthropic" {
			continue
		}
		// resolveMessageResourceAffinity rewrites file_id in place once it
		// matches, so a probe against the wrong provider must not mutate the
		// payload the next probe inspects.
		probe := cloneMap(payload)
		credentialID, err := s.resolveMessageResourceAffinity(ctx, probe, route.Provider.ID)
		if err != nil {
			lastErr = err
			continue
		}
		if _, err := s.resolveMessageResourceAffinity(ctx, payload, route.Provider.ID); err != nil {
			lastErr = err
			continue
		}
		return credentialID, []routeRuntime{route}, nil
	}
	if lastErr != nil {
		return "", nil, lastErr
	}
	return "", nil, errors.New("file references require a native Anthropic provider route")
}

func (s *Server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	raw, err := readRequestBody(w, r, s.cfg.MaxMessageBytes)
	if err != nil {
		writeAnthropicError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Token-count request exceeds 32 MB.")
		return
	}
	payload, err := decodeJSONMap(raw)
	if err != nil {
		writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "Request body is not valid JSON.")
		return
	}
	alias, _ := payload["model"].(string)
	route, err := s.loadRoute(r.Context(), alias)
	if err != nil {
		writeAnthropicError(w, r, http.StatusNotFound, "not_found_error", "The requested model alias is not enabled.")
		return
	}
	if route.Provider.APIFormat != "anthropic" {
		writeAnthropicError(w, r, http.StatusBadRequest, "unsupported_feature", "Exact token counting requires a native Anthropic provider route.")
		return
	}
	payload["model"] = route.Model.UpstreamModel
	credentials, err := s.loadCredentials(r.Context(), route.Provider.ID, route.Model.ID)
	if err != nil || len(credentials) == 0 {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "No healthy credential is available.")
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Gateway settings are unavailable.")
		return
	}
	selected, _, retry, _, err := s.selectCredentialWithDiagnostics(r.Context(), route.Model.ID, credentials, 0, map[string]bool{}, time.Duration(settings.MaxWaitMS)*time.Millisecond)
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Rate limiter is unavailable.")
		return
	}
	if selected == nil {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
		writeAnthropicError(w, r, http.StatusTooManyRequests, "rate_limit_error", "Every credential is at capacity.")
		return
	}
	encoded, _ := json.Marshal(payload)
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(route.Provider.BaseURL, "/")+"/messages/count_tokens", bytes.NewReader(encoded))
	applyProviderHeaders(request, route.Provider, selected.Secret)
	forwardAnthropicHeaders(request.Header, r.Header)
	client, err := upstreamClient(route.Provider)
	if err != nil {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", err.Error())
		return
	}
	response, err := client.Do(request)
	if err != nil {
		s.markCredentialFailure(r.Context(), selected.ID, 0, 0)
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The upstream provider could not be reached.")
		return
	}
	defer response.Body.Close()
	copyAnthropicHeaders(w.Header(), response.Header)
	body, truncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
	if readErr != nil || truncated {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The token-count response was too large or invalid.")
		return
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		s.markCredentialSuccess(r.Context(), selected.ID)
	} else {
		s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func (s *Server) rejectAnthropic(w http.ResponseWriter, r *http.Request, status int, code, message string, input logInput) {
	writeAnthropicError(w, r, status, code, message)
	input.StatusCode, input.ErrorCode, input.ErrorMessage = status, code, message
	s.storeRequestLog(r.Context(), input)
}

func decodeJSONMap(raw []byte) (map[string]any, error) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&payload)
	return payload, err
}

func forwardAnthropicHeaders(destination, source http.Header) {
	for key, values := range source {
		canonical := http.CanonicalHeaderKey(key)
		lower := strings.ToLower(canonical)
		if !strings.HasPrefix(lower, "anthropic-") || canonical == "Anthropic-Version" || canonical == "Anthropic-Organization-Id" {
			continue
		}
		for _, value := range values {
			if len(value) <= 8192 && !strings.ContainsAny(value, "\r\n") {
				destination.Add(canonical, value)
			}
		}
	}
}

func copyAnthropicHeaders(destination, source http.Header) {
	for _, key := range []string{"Content-Type", "Content-Disposition", "Content-Md5", "Accept-Ranges", "Etag", "Last-Modified", "Cache-Control", "Retry-After", "Anthropic-Ratelimit-Requests-Limit", "Anthropic-Ratelimit-Requests-Remaining", "Anthropic-Ratelimit-Requests-Reset", "Anthropic-Ratelimit-Tokens-Limit", "Anthropic-Ratelimit-Tokens-Remaining", "Anthropic-Ratelimit-Tokens-Reset"} {
		if value := source.Get(key); value != "" {
			destination.Set(key, value)
		}
	}
}

func anthropicRetryableStatus(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests || status == 529 || status == http.StatusInternalServerError || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

func protocolName(mode string) string {
	if mode == messageModeAnthropic {
		return "anthropic"
	}
	return "openai"
}
func protocolEndpoint(mode string) string {
	if mode == messageModeAnthropic {
		return "messages"
	}
	return mode
}
func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (s *Server) resolveMessageResourceAffinity(ctx context.Context, payload map[string]any, providerID string) (string, error) {
	ids := collectStringFields(payload, "file_id")
	credentialID := ""
	for _, id := range ids {
		var mappedProvider, mappedCredential string
		if err := s.db.QueryRow(ctx, `SELECT provider_id, credential_id FROM anthropic_resources WHERE id=$1 AND resource_type='file'`, id).Scan(&mappedProvider, &mappedCredential); err != nil {
			return "", fmt.Errorf("file %s is not managed by Rotakey", id)
		}
		if mappedProvider != providerID || (credentialID != "" && credentialID != mappedCredential) {
			return "", fmt.Errorf("all file references must use the model route's provider and one API key")
		}
		credentialID = mappedCredential
		rewriteStringField(payload, "file_id", id, s.resourceUpstreamID(ctx, id))
	}
	return credentialID, nil
}

func collectStringFields(value any, key string) []string {
	result := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for field, child := range typed {
				if field == key {
					if text, ok := child.(string); ok {
						result = append(result, text)
					}
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	return result
}

func rewriteStringField(value any, key, from, to string) {
	switch typed := value.(type) {
	case map[string]any:
		for field, child := range typed {
			if field == key && child == from {
				typed[field] = to
			}
			rewriteStringField(child, key, from, to)
		}
	case []any:
		for _, child := range typed {
			rewriteStringField(child, key, from, to)
		}
	}
}

func (s *Server) resourceUpstreamID(ctx context.Context, id string) string {
	var upstream string
	_ = s.db.QueryRow(ctx, `SELECT upstream_id FROM anthropic_resources WHERE id=$1`, id).Scan(&upstream)
	return upstream
}
