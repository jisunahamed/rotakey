package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const adaptiveCompatibilityTTL = 24 * time.Hour

var (
	adaptiveCompatibilityParameters = map[string]bool{
		"thinking":            true,
		"reasoning":           true,
		"reasoning_effort":    true,
		"verbosity":           true,
		"temperature":         true,
		"top_p":               true,
		"frequency_penalty":   true,
		"presence_penalty":    true,
		"seed":                true,
		"logprobs":            true,
		"top_logprobs":        true,
		"parallel_tool_calls": true,
		"service_tier":        true,
		"stream_options":      true,
		"store":               true,
		"metadata":            true,
		"user":                true,
	}
	// unsupportedParameterPattern reads the parameter's name out of an upstream
	// rejection. Providers disagree on the adjective for the same complaint:
	// OpenAI writes "Unsupported parameter", Azure writes "Unknown parameter",
	// and Gemini's OpenAI surface writes "Unrecognized request argument supplied".
	unsupportedParameterPattern = regexp.MustCompile(
		`(?i)(?:unrecognized request argument supplied|(?:unsupported|unknown) (?:request )?(?:argument|parameter)(?:\(s\))?)\s*:\s*['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})`,
	)
	suggestedReplacementPattern = regexp.MustCompile(
		`(?i)\buse\s+['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})['"` + "`" + `]?\s+instead\b`,
	)
	deprecatedParameterPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})['"` + "`" + `]?\s+is\s+deprecated(?:\s+for\s+(?:this|the)\s+model)?`),
		regexp.MustCompile(`(?i)(?:parameter|argument)\s+['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})['"` + "`" + `]?\s+(?:is|has\s+been)\s+deprecated`),
		regexp.MustCompile(`(?i)['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})['"` + "`" + `]?\s+(?:is|are)\s+not\s+supported\s+(?:for|with|in)\b`),
	}
	// responsesEndpointSuggestionPattern recognizes an upstream 400 that tells
	// the caller to move the request to the Responses endpoint, such as Azure's
	// "To use function tools, use /v1/responses". The verb must sit directly
	// before the endpoint so "use /v1/chat/completions instead of /v1/responses"
	// does not match.
	responsesEndpointSuggestionPattern = regexp.MustCompile(
		`(?i)\buse\s+(?:the\s+)?(?:['"` + "`" + `]?/?v1/responses\b|responses\s+api\b)`,
	)
)

type compatibilityReplacement struct {
	From string
	To   string
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Gateway settings are unavailable.")
		return
	}
	// Model-wise routing publishes one entry per public alias even when several
	// providers carry it, so callers never see the same model twice.
	query := `
		SELECT m.public_alias, m.created_at
		FROM model_routes m JOIN providers p ON p.id=m.provider_id
		WHERE ` + routeFilter + `
		ORDER BY m.public_alias
	`
	if normalizeRoutingMode(settings.RoutingMode) == routingModeModel {
		query = `
			SELECT m.public_alias, MIN(m.created_at)
			FROM model_routes m JOIN providers p ON p.id=m.provider_id
			WHERE ` + routeFilter + `
			GROUP BY m.public_alias
			ORDER BY m.public_alias
		`
	}
	rows, err := s.db.Query(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "models_unavailable", "Model routes could not be loaded.")
		return
	}
	defer rows.Close()
	data := []any{}
	for rows.Next() {
		var alias string
		var created time.Time
		if rows.Scan(&alias, &created) == nil {
			// The dispatcher translates every eligible OpenAI and Anthropic route
			// into the caller's protocol. Hiding chat-only routes from Anthropic
			// discovery made Claude Code conclude that this gateway had no models.
			if isAnthropicRequest(r) {
				discoveryID := anthropicDiscoveryModelID(alias)
				data = append(data, map[string]any{
					"id": discoveryID, "type": "model", "display_name": alias,
					"created_at": created.UTC().Format(time.RFC3339),
				})
				continue
			}
			data = append(data, map[string]any{
				"id": alias, "object": "model", "created": created.Unix(), "owned_by": "rotakey",
			})
		}
	}
	if isAnthropicRequest(r) {
		var firstID, lastID any
		if len(data) > 0 {
			firstID = data[0].(map[string]any)["id"]
			lastID = data[len(data)-1].(map[string]any)["id"]
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data, "has_more": false, "first_id": firstID, "last_id": lastID})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	requestedID := r.PathValue("id")
	alias := requestedID
	if isAnthropicRequest(r) {
		alias = resolveAnthropicDiscoveryModelID(alias)
	}
	route, err := s.loadRoute(r.Context(), alias)
	if err != nil {
		writeProtocolError(w, r, http.StatusNotFound, "not_found_error", "The requested model alias is not enabled.")
		return
	}
	if isAnthropicRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": anthropicDiscoveryModelID(route.Model.PublicAlias), "type": "model", "display_name": route.Model.PublicAlias,
			"created_at": route.Model.CreatedAt.UTC().Format(time.RFC3339),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": route.Model.PublicAlias, "object": "model", "created": route.Model.CreatedAt.Unix(), "owned_by": "rotakey",
	})
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.handleGatewayRequest(w, r, "chat")
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.handleGatewayRequest(w, r, "responses")
}

func (s *Server) handleGatewayRequest(w http.ResponseWriter, r *http.Request, endpoint string) {
	started := time.Now()
	requestID := requestIDFromContext(r.Context())
	s.beginActiveRequest(requestID, endpoint, started)
	defer s.activeRequests.Delete(requestID)
	raw, err := readRequestBody(w, r, s.cfg.MaxRequestBytes)
	if err != nil {
		s.rejectGatewayRequest(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the configured limit.", logInput{
			RequestID: requestID, Endpoint: endpoint, Started: started,
		})
		return
	}
	var publicPayload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&publicPayload); err != nil {
		s.rejectGatewayRequest(w, r, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.", logInput{
			RequestID: requestID, Endpoint: endpoint, Started: started, RequestBody: raw,
		})
		return
	}
	alias, ok := publicPayload["model"].(string)
	if !ok || alias == "" {
		s.rejectGatewayRequest(w, r, http.StatusBadRequest, "model_required", "A public model alias is required.", logInput{
			RequestID: requestID, Endpoint: endpoint, Started: started, RequestBody: raw,
		})
		return
	}
	if endpoint == "responses" {
		if err := s.expandRotakeyCompaction(publicPayload); err != nil {
			s.rejectGatewayRequest(w, r, http.StatusBadRequest, "unsupported_feature", err.Error(), logInput{
				RequestID: requestID, Route: routeRuntime{Model: ModelRoute{PublicAlias: alias}},
				Endpoint: endpoint, Started: started, RequestBody: raw,
			})
			return
		}
	}
	s.updateActiveRequest(requestID, func(log *RequestLog) {
		log.ModelAlias = alias
	})
	settings, _, err := s.settings(r.Context())
	if err != nil {
		s.rejectGatewayRequest(w, r, http.StatusServiceUnavailable, "settings_unavailable", "Gateway settings are unavailable.", logInput{
			RequestID: requestID, Route: routeRuntime{Model: ModelRoute{PublicAlias: alias}},
			Endpoint: endpoint, Started: started, RequestBody: raw,
		})
		return
	}
	req := dispatchRequest{
		RequestID: requestID, Started: started, Endpoint: endpoint,
		PublicMode: messageModeChat, Alias: alias, Raw: raw, Public: publicPayload,
		MaxWait: time.Duration(settings.MaxWaitMS) * time.Millisecond,
	}
	if endpoint == "responses" {
		req.PublicMode = messageModeResponses
	}
	req.Stream, _ = publicPayload["stream"].(bool)
	if options, ok := publicPayload["stream_options"].(map[string]any); ok {
		req.IncludeOpenAIUsage, _ = options["include_usage"].(bool)
	}

	routes, err := s.resolveRoutes(r.Context(), alias, settings.RoutingMode)
	if err != nil {
		s.rejectGatewayRequest(w, r, http.StatusNotFound, "model_not_found", "The requested model alias is not enabled.", logInput{
			RequestID: requestID, Route: routeRuntime{Model: ModelRoute{PublicAlias: alias}},
			Endpoint: endpoint, Started: started, RequestBody: raw,
		})
		return
	}
	// Providers that cannot serve the caller's protocol leave the pool before any
	// request is sent, so failover never wastes an attempt on them.
	usable := make([]routeRuntime, 0, len(routes))
	for _, route := range routes {
		if routeSupportsRequest(route, req) {
			usable = append(usable, route)
		}
	}
	if len(usable) == 0 {
		message := "This model route does not support Chat Completions."
		if endpoint == "responses" {
			message = "This model route does not support Responses."
		}
		s.rejectGatewayRequest(w, r, http.StatusBadRequest, "unsupported_endpoint", message, logInput{
			RequestID: requestID, Route: routes[0], Endpoint: endpoint, Started: started,
			RequestBody: raw, Capture: routes[0].Model.CaptureBodies,
		})
		return
	}
	s.updateActiveRequest(requestID, func(log *RequestLog) {
		log.ModelAlias = usable[0].Model.PublicAlias
		log.ProviderName = usable[0].Provider.Name
	})
	s.servePooled(w, r, req, usable, "")
}

func copyStreamingResponse(destination http.ResponseWriter, source io.Reader, capture io.Writer) error {
	reader := bufio.NewReaderSize(source, 64<<10)
	flusher, _ := destination.(http.Flusher)
	completed := false
	for {
		line, readErr := reader.ReadString('\n')
		if line != "" {
			if strings.Contains(line, "event: response.completed") || strings.Contains(line, "data: [DONE]") {
				completed = true
			}
			data := []byte(line)
			if _, err := destination.Write(data); err != nil {
				return err
			}
			if capture != nil {
				_, _ = capture.Write(data)
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				if completed {
					return nil
				}
				return io.ErrUnexpectedEOF
			}
			return readErr
		}
	}
}

func writeStreamFailure(destination io.Writer, capture *limitedCapture, endpoint, code, message, model string) {
	if endpoint == "responses" {
		id, _ := newID("resp")
		writeSSE(destination, capture, "response.failed", map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id":         id,
				"object":     "response",
				"created_at": time.Now().Unix(),
				"status":     "failed",
				"model":      model,
				"error":      map[string]any{"code": code, "message": message},
			},
		})
		writeRaw(destination, capture, []byte("data: [DONE]\n\n"))
		return
	}
	payload := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "api_error",
			"code":    code,
		},
	}
	encoded, _ := json.Marshal(payload)
	writeRaw(destination, capture, append(append([]byte("data: "), encoded...), []byte("\n\n")...))
	writeRaw(destination, capture, []byte("data: [DONE]\n\n"))
}

func readRequestBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if !isCompressed(r) {
		return raw, nil
	}
	decompressed, decErr := decompressBody(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))), raw, limit)
	if decErr != nil {
		return nil, decErr
	}
	return decompressed, nil
}

func cloneMap(source map[string]any) map[string]any {
	target := make(map[string]any, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func stripTopLevelParameters(payload map[string]any, parameters []string) []string {
	stripped := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if _, exists := payload[parameter]; exists {
			delete(payload, parameter)
			stripped = append(stripped, parameter)
		}
	}
	return stripped
}

func unsupportedCompatibilityParameters(body []byte, payload map[string]any) []string {
	candidates := make([]string, 0, 2)
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Param   any    `json:"param"`
			Code    any    `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		matches := compatibilityParameterMatches(envelope.Error.Message)
		if unsupportedParameterCode(envelope.Error.Code, envelope.Error.Type) || len(matches) > 0 {
			if parameter, ok := envelope.Error.Param.(string); ok {
				candidates = append(candidates, parameter)
			}
		}
		for _, match := range matches {
			if len(match) > 1 {
				candidates = append(candidates, match[1])
			}
		}
	}

	parameters := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if !adaptiveCompatibilityParameters[candidate] {
			continue
		}
		if _, exists := payload[candidate]; !exists || slices.Contains(parameters, candidate) {
			continue
		}
		parameters = append(parameters, candidate)
	}
	if len(parameters) == 0 {
		return nil
	}
	return parameters
}

// unsupportedParameterCode reports whether the provider's machine-readable code
// or type says the request carried a parameter it does not accept. Both readers
// of this signal — the strip pass and the replacement pass — need the same three
// spellings, so the list is written once rather than drifting apart in two
// boolean expressions.
func unsupportedParameterCode(code any, errorType string) bool {
	text, _ := code.(string)
	text = strings.ToLower(text + " " + errorType)
	return strings.Contains(text, "unsupported_parameter") ||
		strings.Contains(text, "unknown_parameter") ||
		strings.Contains(text, "unrecognized_request_argument")
}

func compatibilityParameterMatches(message string) [][]string {
	matches := unsupportedParameterPattern.FindAllStringSubmatch(message, -1)
	for _, pattern := range deprecatedParameterPatterns {
		matches = append(matches, pattern.FindAllStringSubmatch(message, -1)...)
	}
	return matches
}

// errorDemandsResponsesEndpoint reports whether an upstream 400 explicitly
// directs the request to the Responses endpoint. The provider's own error text
// is stronger evidence than the route's configured booleans, so the caller may
// retry at /responses even when the route never claimed to support it.
func errorDemandsResponsesEndpoint(body []byte) bool {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	return responsesEndpointSuggestionPattern.MatchString(envelope.Error.Message)
}

func unsupportedCompatibilityReplacement(body []byte, payload map[string]any) (compatibilityReplacement, bool) {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Param   any    `json:"param"`
			Code    any    `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return compatibilityReplacement{}, false
	}

	unsupported := unsupportedParameterCode(envelope.Error.Code, envelope.Error.Type) ||
		len(unsupportedParameterPattern.FindStringSubmatch(envelope.Error.Message)) > 1
	if !unsupported {
		return compatibilityReplacement{}, false
	}

	var source string
	if parameter, ok := envelope.Error.Param.(string); ok {
		source = strings.TrimSpace(parameter)
	}
	if source == "" {
		match := unsupportedParameterPattern.FindStringSubmatch(envelope.Error.Message)
		if len(match) > 1 {
			source = match[1]
		}
	}
	replacementMatch := suggestedReplacementPattern.FindStringSubmatch(envelope.Error.Message)
	if len(replacementMatch) < 2 {
		return compatibilityReplacement{}, false
	}
	target := replacementMatch[1]
	if !safeCompatibilityReplacement(source, target) {
		return compatibilityReplacement{}, false
	}
	if _, exists := payload[source]; !exists {
		return compatibilityReplacement{}, false
	}
	return compatibilityReplacement{From: source, To: target}, true
}

func safeCompatibilityReplacement(source, target string) bool {
	if source == target {
		return false
	}
	tokenLimits := map[string]bool{
		"max_tokens":            true,
		"max_completion_tokens": true,
		"max_output_tokens":     true,
	}
	return tokenLimits[source] && tokenLimits[target]
}

func applyCompatibilityReplacement(payload map[string]any, replacement compatibilityReplacement) {
	value, exists := payload[replacement.From]
	if !exists {
		return
	}
	if _, targetExists := payload[replacement.To]; !targetExists {
		payload[replacement.To] = value
	}
	delete(payload, replacement.From)
}

func applyCompatibilityReplacements(payload map[string]any, replacements map[string]string) map[string]string {
	sources := make([]string, 0, len(replacements))
	for source := range replacements {
		sources = append(sources, source)
	}
	slices.Sort(sources)

	applied := make(map[string]string)
	for _, source := range sources {
		if _, exists := payload[source]; !exists {
			continue
		}
		target, ok := resolveCompatibilityReplacement(source, replacements)
		if !ok {
			continue
		}
		applyCompatibilityReplacement(payload, compatibilityReplacement{From: source, To: target})
		applied[source] = target
	}
	return applied
}

func resolveCompatibilityReplacement(source string, replacements map[string]string) (string, bool) {
	current := source
	visited := map[string]bool{source: true}
	for {
		target, exists := replacements[current]
		if !exists {
			return current, current != source
		}
		if !safeCompatibilityReplacement(current, target) || visited[target] {
			return "", false
		}
		visited[target] = true
		current = target
	}
}

func formatCompatibilityReplacements(replacements map[string]string) string {
	sources := make([]string, 0, len(replacements))
	for source := range replacements {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	values := make([]string, 0, len(sources))
	for _, source := range sources {
		values = append(values, source+"="+replacements[source])
	}
	return strings.Join(values, ",")
}

func appendUniqueStrings(values []string, additions ...string) []string {
	for _, addition := range additions {
		if !slices.Contains(values, addition) {
			values = append(values, addition)
		}
	}
	return values
}

func compatibilityStripKey(modelID string) string {
	return "compatibility:strip:" + modelID
}

func compatibilityReplaceKey(modelID, endpoint string) string {
	return "compatibility:replace:" + modelID + ":" + endpoint
}

func responsesMissingKey(modelID string) string {
	return "compatibility:no-responses:" + modelID
}

// responsesEndpointMissing reports whether this route's provider has already
// answered 404 at /responses. The flag is remembered so the very first request
// pays the wasted round trip and later ones go straight to the translation.
func (s *Server) responsesEndpointMissing(ctx context.Context, modelIDs []string) map[string]bool {
	missing := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		if found, err := s.redis.Exists(ctx, responsesMissingKey(modelID)).Result(); err == nil && found > 0 {
			missing[modelID] = true
		}
	}
	return missing
}

func (s *Server) rememberResponsesEndpointMissing(ctx context.Context, modelID string) {
	if err := s.redis.Set(ctx, responsesMissingKey(modelID), "404", adaptiveCompatibilityTTL).Err(); err != nil {
		s.logger.Warn("responses endpoint cache write failed", "model_id", modelID, "error", err)
	}
}

// forgetResponsesEndpointMissing drops the learned 404 so a route that has just
// been edited or re-probed is trusted again. Without this a corrected base URL
// would keep translating to Chat Completions until the cache expired.
func (s *Server) forgetResponsesEndpointMissing(ctx context.Context, modelID string) {
	if err := s.redis.Del(ctx, responsesMissingKey(modelID)).Err(); err != nil {
		s.logger.Warn("responses endpoint cache reset failed", "model_id", modelID, "error", err)
	}
}

func responsesPreferredKey(modelID string) string {
	return "compatibility:prefer-responses:" + modelID
}

// responsesEndpointPreferred reports which routes' providers have rejected a
// Chat Completions request by directing it to /responses. Later requests start
// on the Responses translation instead of paying the same 400 again.
func (s *Server) responsesEndpointPreferred(ctx context.Context, modelIDs []string) map[string]bool {
	preferred := make(map[string]bool, len(modelIDs))
	for _, modelID := range modelIDs {
		if found, err := s.redis.Exists(ctx, responsesPreferredKey(modelID)).Result(); err == nil && found > 0 {
			preferred[modelID] = true
		}
	}
	return preferred
}

func (s *Server) rememberResponsesEndpointPreferred(ctx context.Context, modelID string) {
	if err := s.redis.Set(ctx, responsesPreferredKey(modelID), "400", adaptiveCompatibilityTTL).Err(); err != nil {
		s.logger.Warn("preferred responses cache write failed", "model_id", modelID, "error", err)
	}
}

// forgetResponsesEndpointPreferred drops the learned preference so an edited or
// re-probed route is planned from its own configuration again, mirroring
// forgetResponsesEndpointMissing.
func (s *Server) forgetResponsesEndpointPreferred(ctx context.Context, modelID string) {
	if err := s.redis.Del(ctx, responsesPreferredKey(modelID)).Err(); err != nil {
		s.logger.Warn("preferred responses cache reset failed", "model_id", modelID, "error", err)
	}
}

func (s *Server) learnedCompatibilityParameters(ctx context.Context, modelID string) []string {
	parameters, err := s.redis.SMembers(ctx, compatibilityStripKey(modelID)).Result()
	if err != nil {
		return nil
	}
	filtered := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if adaptiveCompatibilityParameters[parameter] {
			filtered = append(filtered, parameter)
		}
	}
	slices.Sort(filtered)
	return filtered
}

func (s *Server) rememberCompatibilityParameters(ctx context.Context, modelID string, parameters []string) {
	if len(parameters) == 0 {
		return
	}
	key := compatibilityStripKey(modelID)
	values := make([]any, 0, len(parameters))
	for _, parameter := range parameters {
		if adaptiveCompatibilityParameters[parameter] {
			values = append(values, parameter)
		}
	}
	if len(values) == 0 {
		return
	}
	pipe := s.redis.TxPipeline()
	pipe.SAdd(ctx, key, values...)
	pipe.Expire(ctx, key, adaptiveCompatibilityTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		s.logger.Warn("compatibility cache write failed", "model_id", modelID, "error", err)
	}
}

func (s *Server) learnedCompatibilityReplacements(ctx context.Context, modelID, endpoint string) map[string]string {
	replacements, err := s.redis.HGetAll(ctx, compatibilityReplaceKey(modelID, endpoint)).Result()
	if err != nil {
		return nil
	}
	filtered := make(map[string]string)
	for source, target := range replacements {
		if safeCompatibilityReplacement(source, target) {
			filtered[source] = target
		}
	}
	return filtered
}

func (s *Server) rememberCompatibilityReplacement(
	ctx context.Context,
	modelID string,
	endpoint string,
	replacement compatibilityReplacement,
) {
	if !safeCompatibilityReplacement(replacement.From, replacement.To) {
		return
	}
	key := compatibilityReplaceKey(modelID, endpoint)
	pipe := s.redis.TxPipeline()
	pipe.HDel(ctx, key, replacement.To)
	pipe.HSet(ctx, key, replacement.From, replacement.To)
	pipe.Expire(ctx, key, adaptiveCompatibilityTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		s.logger.Warn("compatibility replacement cache write failed", "model_id", modelID, "error", err)
	}
}

// prepareTokenReservation estimates the request's token cost and, when the caller
// named no output cap, writes the default one in the spelling the endpoint on the
// wire accepts: max_tokens at /chat/completions, max_output_tokens at /responses.
//
// The endpoint is the only thing that decides this. An earlier version also took
// a `translated` flag and treated it as "translated down into Chat", which was
// true when Chat was the only shape anything was translated into. Once chat and
// anthropic callers began translating *up* into /responses, that flag put
// max_tokens on a Responses payload and the provider rejected the whole request
// with "Unknown parameter: 'max_tokens'".
func prepareTokenReservation(
	payload map[string]any,
	endpoint string,
	defaultOutput int,
	tokenizerProfile string,
	requestBody []byte,
) (int64, int64) {
	inputEstimate := estimateInputTokens(requestBody, tokenizerProfile)
	output := int64(defaultOutput)
	fields := []string{"max_tokens", "max_completion_tokens", "max_output_tokens"}
	for _, field := range fields {
		if value := numberAsInt64(payload[field]); value > 0 {
			output = value
			break
		}
	}
	if endpoint == "chat" {
		if numberAsInt64(payload["max_tokens"]) == 0 && numberAsInt64(payload["max_completion_tokens"]) == 0 {
			payload["max_tokens"] = output
		}
	} else if numberAsInt64(payload["max_output_tokens"]) == 0 {
		payload["max_output_tokens"] = output
	}
	return inputEstimate, output
}

func (s *Server) selectCredential(
	ctx context.Context,
	modelID string,
	credentials []credentialRuntime,
	tokenCost int64,
	skipped map[string]bool,
	maxWait time.Duration,
) (*credentialRuntime, reservation, time.Duration, error) {
	selected, reserved, retry, _, err := s.selectCredentialWithDiagnostics(ctx, modelID, credentials, tokenCost, skipped, maxWait)
	return selected, reserved, retry, err
}

func (s *Server) selectCredentialWithDiagnostics(
	ctx context.Context,
	modelID string,
	credentials []credentialRuntime,
	tokenCost int64,
	skipped map[string]bool,
	maxWait time.Duration,
) (*credentialRuntime, reservation, time.Duration, []RoutingDecision, error) {
	return s.selectCredentialWithCosts(ctx, modelID, credentials, 1, tokenCost, tokenCost, skipped, maxWait)
}

func (s *Server) selectCredentialWithCosts(
	ctx context.Context,
	modelID string,
	credentials []credentialRuntime,
	requestCost int64,
	tokenCost int64,
	tprCost int64,
	skipped map[string]bool,
	maxWait time.Duration,
) (*credentialRuntime, reservation, time.Duration, []RoutingDecision, error) {
	deadline := time.Now().Add(maxWait)
	for {
		cursor, err := s.redis.Incr(ctx, "rr:"+modelID).Result()
		if err != nil {
			return nil, reservation{}, 0, nil, err
		}
		minRetry := time.Duration(math.MaxInt64)
		decisions := make([]RoutingDecision, 0)
		for _, index := range credentialSelectionOrder(credentials, cursor) {
			candidate := &credentials[index]
			if skipped[candidate.ID] {
				continue
			}
			if !candidate.Enabled || candidate.Status == "quarantined" {
				reason := candidate.Status
				if !candidate.Enabled {
					reason = "disabled"
				}
				decisions = append(decisions, RoutingDecision{
					CredentialID: candidate.ID, CredentialLabel: candidate.Label,
					Reason: reason,
				})
				continue
			}
			if decision := balanceRoutingDecision(*candidate); decision != nil {
				decisions = append(decisions, *decision)
				continue
			}
			cooldown, err := s.redis.TTL(ctx, "cooldown:"+candidate.ID).Result()
			if err != nil {
				return nil, reservation{}, 0, nil, err
			}
			if cooldown > 0 {
				resetAt := time.Now().Add(cooldown).UTC()
				decisions = append(decisions, RoutingDecision{
					CredentialID: candidate.ID, CredentialLabel: candidate.Label,
					Reason: "cooldown", RetryAfterMS: cooldown.Milliseconds(), ResetAt: &resetAt,
				})
				if cooldown < minRetry {
					minRetry = cooldown
				}
				continue
			}
			constraints, rejected := buildConstraintsWithCosts(*candidate, modelID, requestCost, tokenCost, tprCost)
			if len(rejected) > 0 {
				decisions = append(decisions, rejected...)
				continue
			}
			result, err := s.limiter.Reserve(ctx, constraints)
			if err != nil {
				return nil, reservation{}, 0, nil, err
			}
			if result.Allowed {
				return candidate, reservation{
					constraints: constraints, tokenCost: tokenCost, reservedAt: result.ReservedAt,
				}, 0, decisions, nil
			}
			for _, blocked := range result.Blocked {
				remaining := blocked.Constraint.Capacity - blocked.Used
				if remaining < 0 {
					remaining = 0
				}
				resetAt := time.Now().Add(blocked.Retry).UTC()
				decisions = append(decisions, RoutingDecision{
					CredentialID: candidate.ID, CredentialLabel: candidate.Label,
					Reason: "limit_exhausted", Scope: blocked.Constraint.Scope,
					Dimension: blocked.Constraint.Dimension, Limit: blocked.Constraint.Capacity,
					Used: blocked.Used, Remaining: remaining, Required: blocked.Constraint.Cost,
					RetryAfterMS: blocked.Retry.Milliseconds(), ResetAt: &resetAt,
				})
			}
			if result.Retry < minRetry {
				minRetry = result.Retry
			}
		}
		if minRetry == time.Duration(math.MaxInt64) {
			return nil, reservation{}, time.Minute, decisions, nil
		}
		remaining := time.Until(deadline)
		if maxWait <= 0 || minRetry > remaining {
			return nil, reservation{}, minRetry, decisions, nil
		}
		timer := time.NewTimer(minRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, reservation{}, 0, decisions, ctx.Err()
		case <-timer.C:
		}
	}
}

type modelReservationCost struct {
	Requests int64
	Tokens   int64
	TPR      int64
}

func buildBatchConstraints(credential credentialRuntime, costs map[string]modelReservationCost) ([]limitConstraint, []RoutingDecision, int64) {
	var totalRequests, totalTokens, maxTPR int64
	modelIDs := make([]string, 0, len(costs))
	for modelID, cost := range costs {
		modelIDs = append(modelIDs, modelID)
		totalRequests += cost.Requests
		totalTokens += cost.Tokens
		if cost.TPR > maxTPR {
			maxTPR = cost.TPR
		}
	}
	slices.Sort(modelIDs)
	shared := credential
	shared.ModelLimits = nil
	constraints, rejected := buildConstraintsWithCosts(shared, "", totalRequests, totalTokens, maxTPR)
	for _, modelID := range modelIDs {
		policy, ok := credential.ModelLimits[modelID]
		if !ok {
			continue
		}
		modelOnly := credential
		modelOnly.Limits = RatePolicy{}
		modelOnly.ModelLimits = map[string]RatePolicy{modelID: policy}
		cost := costs[modelID]
		modelConstraints, modelRejected := buildConstraintsWithCosts(modelOnly, modelID, cost.Requests, cost.Tokens, cost.TPR)
		constraints = append(constraints, modelConstraints...)
		rejected = append(rejected, modelRejected...)
	}
	return constraints, rejected, totalTokens
}

func (s *Server) selectBatchCredential(
	ctx context.Context,
	providerID string,
	credentials []credentialRuntime,
	costs map[string]modelReservationCost,
	maxWait time.Duration,
) (*credentialRuntime, reservation, time.Duration, error) {
	deadline := time.Now().Add(maxWait)
	for {
		cursor, err := s.redis.Incr(ctx, "rr:batch:"+providerID).Result()
		if err != nil {
			return nil, reservation{}, 0, err
		}
		minRetry := time.Duration(math.MaxInt64)
		for _, index := range credentialSelectionOrder(credentials, cursor) {
			candidate := &credentials[index]
			if !candidate.Enabled || candidate.Status == "quarantined" || candidate.BalanceExhausted() {
				continue
			}
			cooldown, err := s.redis.TTL(ctx, "cooldown:"+candidate.ID).Result()
			if err != nil {
				return nil, reservation{}, 0, err
			}
			if cooldown > 0 {
				if cooldown < minRetry {
					minRetry = cooldown
				}
				continue
			}
			constraints, rejected, totalTokens := buildBatchConstraints(*candidate, costs)
			if len(rejected) > 0 {
				continue
			}
			result, err := s.limiter.Reserve(ctx, constraints)
			if err != nil {
				return nil, reservation{}, 0, err
			}
			if result.Allowed {
				return candidate, reservation{constraints: constraints, tokenCost: totalTokens, reservedAt: result.ReservedAt}, 0, nil
			}
			if result.Retry < minRetry {
				minRetry = result.Retry
			}
		}
		if minRetry == time.Duration(math.MaxInt64) {
			return nil, reservation{}, time.Minute, nil
		}
		remaining := time.Until(deadline)
		if maxWait <= 0 || minRetry > remaining {
			return nil, reservation{}, minRetry, nil
		}
		timer := time.NewTimer(minRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, reservation{}, 0, ctx.Err()
		case <-timer.C:
		}
	}
}

func credentialSelectionOrder(credentials []credentialRuntime, cursor int64) []int {
	if len(credentials) == 0 {
		return nil
	}
	primary := -1
	fallbacks := make([]int, 0, len(credentials))
	for index, credential := range credentials {
		if credential.IsPrimary && primary == -1 {
			primary = index
			continue
		}
		fallbacks = append(fallbacks, index)
	}
	order := make([]int, 0, len(credentials))
	if primary >= 0 {
		order = append(order, primary)
	}
	if len(fallbacks) == 0 {
		return order
	}
	start := int((cursor - 1) % int64(len(fallbacks)))
	if start < 0 {
		start = 0
	}
	for offset := 0; offset < len(fallbacks); offset++ {
		order = append(order, fallbacks[(start+offset)%len(fallbacks)])
	}
	return order
}

// markCredentialFailure records what one upstream rejection means for a key.
//
// Only 401 quarantines on sight: the provider said the key itself is not valid.
// A 403 is treated as a hold that escalates, because in practice it is far more
// often "this key may not use that one model", a region block, or an edge/WAF
// page than a dead key — and quarantining on the first one took every alias on
// the provider out of service for a per-model entitlement problem.
func (s *Server) markCredentialFailure(ctx context.Context, credentialID string, status int, retryAfter time.Duration) {
	if status == http.StatusUnauthorized {
		s.quarantineCredential(ctx, credentialID, status)
		return
	}
	if status == http.StatusForbidden {
		// A key that really has been revoked must still stop being retried, so the
		// third consecutive 403 escalates. consecutive_failures is reset by any
		// success, so a key that works between rejections never reaches the count.
		var failures int
		if err := s.db.QueryRow(ctx, `
			UPDATE credentials SET status='cooldown', cooldown_until=$2,
			    validation_error=$3, last_validated_at=NOW(),
			    consecutive_failures=consecutive_failures+1, updated_at=NOW()
			WHERE id=$1
			RETURNING consecutive_failures
		`, credentialID, time.Now().Add(forbiddenHold),
			fmt.Sprintf("Provider refused this API key for a request (HTTP %d). It may not be allowed to use that model.", status),
		).Scan(&failures); err != nil {
			s.logger.Warn("credential refusal state write failed", "credential_id", credentialID, "error", err)
			return
		}
		_ = s.redis.Set(ctx, "cooldown:"+credentialID, "403", forbiddenHold).Err()
		if failures >= 3 {
			s.quarantineCredential(ctx, credentialID, status)
		}
		return
	}
	if status == http.StatusTooManyRequests {
		s.holdCredential(ctx, credentialID, retryAfter)
		return
	}
	failures, _ := s.redis.Incr(ctx, "failures:"+credentialID).Result()
	_ = s.redis.Expire(ctx, "failures:"+credentialID, 5*time.Minute).Err()
	if failures >= 3 {
		_ = s.redis.Set(ctx, "cooldown:"+credentialID, "circuit", 30*time.Second).Err()
		if _, err := s.db.Exec(ctx, `
			UPDATE credentials SET status='cooldown', cooldown_until=$2,
			    consecutive_failures=consecutive_failures+1, updated_at=NOW() WHERE id=$1
		`, credentialID, time.Now().Add(30*time.Second)); err != nil {
			s.logger.Warn("credential circuit state write failed", "credential_id", credentialID, "error", err)
		}
	}
}

// forbiddenHold is how long a refused key is parked before it is tried again. It
// is short because the common cause — one model the key may not use — clears as
// soon as the request goes to a different model.
const forbiddenHold = 2 * time.Minute

// quarantineCredential takes a key out of rotation until an operator or a later
// success brings it back. The routes it served stay published: the request is
// answered 503 naming this, rather than 404 claiming the model does not exist.
func (s *Server) quarantineCredential(ctx context.Context, credentialID string, status int) {
	if _, err := s.db.Exec(ctx, `
		UPDATE credentials SET status='quarantined', cooldown_until=NULL,
		    validation_error=$2, last_validated_at=NOW(),
		    consecutive_failures=consecutive_failures+1, updated_at=NOW()
		WHERE id=$1
	`, credentialID, fmt.Sprintf("Provider rejected this API key during a request (HTTP %d).", status)); err != nil {
		s.logger.Warn("credential quarantine state write failed", "credential_id", credentialID, "error", err)
	}
}

// holdCredential parks a credential for the provider's stated cooldown. It is
// the shared path for HTTP 429 and for providers that signal exhaustion some
// other way, so a rate-limited key is never retried until the window passes
// even when the gateway has no local limit configured for it.
func (s *Server) holdCredential(ctx context.Context, credentialID string, retryAfter time.Duration) {
	if retryAfter <= 0 {
		retryAfter = time.Minute
	}
	if retryAfter > 24*time.Hour {
		retryAfter = 24 * time.Hour
	}
	_ = s.redis.Set(ctx, "cooldown:"+credentialID, "429", retryAfter).Err()
	if _, err := s.db.Exec(ctx, `
		UPDATE credentials SET status='cooldown', cooldown_until=$2,
		    updated_at=NOW() WHERE id=$1
	`, credentialID, time.Now().Add(retryAfter)); err != nil {
		s.logger.Warn("credential cooldown state write failed", "credential_id", credentialID, "error", err)
	}
}

// markUpstreamFailure classifies an upstream rejection using both the status
// line and the error body. Providers disagree about how they report quota
// exhaustion — some use 429, others answer 400/403/503 with a rate-limit code —
// and all of those must put the credential on hold rather than fall through to
// the generic three-strike circuit breaker.
func (s *Server) markUpstreamFailure(ctx context.Context, credentialID string, status int, header http.Header, body []byte) {
	if hold, ok := upstreamRateLimitHold(status, header, body); ok {
		s.holdCredential(ctx, credentialID, hold)
		return
	}
	s.markCredentialFailure(ctx, credentialID, status, parseRetryAfter(header.Get("Retry-After")))
}

// upstreamRateLimitHold reports how long a credential should be held when the
// provider signalled rate limiting. HTTP 401 is excluded because an invalid key
// must be quarantined instead of retried later.
func upstreamRateLimitHold(status int, header http.Header, body []byte) (time.Duration, bool) {
	if status == http.StatusUnauthorized {
		return 0, false
	}
	rateLimited := status == http.StatusTooManyRequests || bodySignalsRateLimit(body)
	if !rateLimited {
		return 0, false
	}
	hold := parseRetryAfter(header.Get("Retry-After"))
	if hold <= 0 {
		for _, name := range []string{"X-Ratelimit-Reset", "X-Ratelimit-Reset-Requests", "X-Ratelimit-Reset-Tokens", "Anthropic-Ratelimit-Requests-Reset", "Anthropic-Ratelimit-Tokens-Reset"} {
			if reset := parseRetryAfter(header.Get(name)); reset > 0 {
				hold = reset
				break
			}
		}
	}
	return hold, true
}

// rateLimitSignals are the substrings providers use to say "you are throttled".
// They are matched against the error code, type and message rather than the
// status code so a non-429 rejection is still recognised.
var rateLimitSignals = []string{
	"rate_limit", "ratelimit", "rate limit", "too many requests",
	"quota", "resource_exhausted", "resource exhausted", "throttl",
	"over capacity", "overloaded", "concurrent request",
}

func bodySignalsRateLimit(body []byte) bool {
	if len(body) == 0 || len(body) > 64<<10 {
		return false
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	fields := []string{}
	collect := func(value any) {
		container, ok := value.(map[string]any)
		if !ok {
			return
		}
		for _, key := range []string{"code", "type", "status", "message", "reason"} {
			if text, ok := container[key].(string); ok {
				fields = append(fields, text)
			}
		}
	}
	collect(payload)
	collect(payload["error"])
	collect(payload["detail"])
	for _, field := range fields {
		lowered := strings.ToLower(field)
		for _, signal := range rateLimitSignals {
			if strings.Contains(lowered, signal) {
				return true
			}
		}
	}
	return false
}

// markCredentialSuccess clears every fault the key was carrying, quarantine
// included. A 200 from the upstream is the strongest evidence available that the
// key works, and it must outweigh an older rejection: the fence that used to
// exclude quarantined rows here meant a key that started working again never
// healed, so one stale 401 kept a provider dark until an operator noticed.
func (s *Server) markCredentialSuccess(ctx context.Context, credentialID string) {
	_ = s.redis.Del(ctx, "failures:"+credentialID, "cooldown:"+credentialID).Err()
	_, _ = s.db.Exec(ctx, `
		UPDATE credentials SET status='healthy', cooldown_until=NULL,
		    validation_error='', last_validated_at=NOW(),
		    consecutive_failures=0, updated_at=NOW()
		WHERE id=$1
	`, credentialID)
}

func parseRetryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		return time.Until(when)
	}
	return 0
}

func upstreamErrorCode(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		if rawError, ok := payload["error"].(map[string]any); ok {
			for _, key := range []string{"code", "type"} {
				if value, ok := rawError[key].(string); ok && value != "" {
					return value
				}
			}
		}
		if detail, ok := payload["detail"].(map[string]any); ok {
			for _, key := range []string{"code", "type"} {
				if value, ok := detail[key].(string); ok && value != "" {
					return value
				}
			}
		}
	}
	return "upstream_error"
}

func upstreamErrorMessage(body, secret []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	message := ""
	if rawError, ok := payload["error"].(map[string]any); ok {
		message, _ = rawError["message"].(string)
	}
	if message == "" {
		switch detail := payload["detail"].(type) {
		case string:
			message = detail
		case map[string]any:
			message, _ = detail["message"].(string)
			if message == "" {
				message, _ = detail["detail"].(string)
			}
		}
	}
	if message == "" {
		message, _ = payload["message"].(string)
	}
	message = strings.TrimSpace(strings.ToValidUTF8(message, ""))
	if len(secret) > 0 {
		message = strings.ReplaceAll(message, string(secret), "[redacted]")
	}
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) > 500 {
		message = string(runes[:500]) + "…"
	}
	return message
}

func copyUpstreamHeaders(destination, source http.Header) {
	for _, header := range []string{"Content-Type", "Cache-Control", "Retry-After", "Openai-Processing-Ms", "Request-Id", "X-Request-Id"} {
		if value := source.Get(header); value != "" {
			destination.Set(header, value)
		}
	}
}

func (s *Server) beginActiveRequest(requestID, endpoint string, started time.Time) {
	s.activeRequests.Store(requestID, RequestLog{
		ID:           requestID,
		RequestID:    requestID,
		ModelAlias:   "unresolved",
		ProviderName: "gateway",
		Endpoint:     endpoint,
		Running:      true,
		CreatedAt:    started.UTC(),
	})
}

func (s *Server) updateActiveRequest(requestID string, update func(*RequestLog)) {
	value, ok := s.activeRequests.Load(requestID)
	if !ok {
		return
	}
	log, ok := value.(RequestLog)
	if !ok {
		return
	}
	update(&log)
	s.activeRequests.Store(requestID, log)
}

func (s *Server) activeRequestLogs(query string) []RequestLog {
	query = strings.ToLower(strings.TrimSpace(query))
	now := time.Now()
	logs := make([]RequestLog, 0)
	s.activeRequests.Range(func(_, value any) bool {
		log, ok := value.(RequestLog)
		if !ok {
			return true
		}
		if query != "" && !strings.Contains(strings.ToLower(log.ModelAlias), query) &&
			!strings.Contains(strings.ToLower(log.RequestID), query) {
			return true
		}
		log.LatencyMS = now.Sub(log.CreatedAt).Milliseconds()
		logs = append(logs, log)
		return true
	})
	slices.SortFunc(logs, func(left, right RequestLog) int {
		return right.CreatedAt.Compare(left.CreatedAt)
	})
	return logs
}

type logInput struct {
	RequestID         string
	Route             routeRuntime
	Credential        credentialRuntime
	Endpoint          string
	PublicProtocol    string
	UpstreamProtocol  string
	UpstreamRequestID string
	Attempts          []AttemptRecord
	RoutingDecisions  []RoutingDecision
	Started           time.Time
	StatusCode        int
	InputTokens       int64
	OutputTokens      int64
	ErrorCode         string
	ErrorMessage      string
	RequestBody       []byte
	ResponseBody      []byte
	Capture           bool
	Truncated         bool
}

func (s *Server) rejectGatewayRequest(
	w http.ResponseWriter,
	r *http.Request,
	status int,
	code string,
	message string,
	input logInput,
) {
	writeError(w, status, code, message)
	input.StatusCode = status
	input.ErrorCode = code
	input.ErrorMessage = message
	s.storeRequestLog(r.Context(), input)
}

func (s *Server) storeRequestLog(ctx context.Context, input logInput) {
	id, err := newID("log")
	if err != nil {
		return
	}
	if input.Attempts == nil {
		input.Attempts = []AttemptRecord{}
	}
	if input.RoutingDecisions == nil {
		input.RoutingDecisions = []RoutingDecision{}
	}
	attempts, _ := json.Marshal(input.Attempts)
	routingDecisions, _ := json.Marshal(input.RoutingDecisions)
	var requestCipher, responseCipher []byte
	truncated := input.Truncated
	if input.Capture {
		requestBody := input.RequestBody
		if int64(len(requestBody)) > s.cfg.CaptureBytes {
			requestBody = requestBody[:s.cfg.CaptureBytes]
			truncated = true
		}
		responseBody := input.ResponseBody
		if int64(len(responseBody)) > s.cfg.CaptureBytes {
			responseBody = responseBody[:s.cfg.CaptureBytes]
			truncated = true
		}
		requestCipher, _ = s.vault.Encrypt(requestBody)
		if len(responseBody) > 0 {
			responseCipher, _ = s.vault.Encrypt(responseBody)
		}
	}
	if input.StatusCode == 0 {
		input.StatusCode = http.StatusBadGateway
	}
	modelAlias := input.Route.Model.PublicAlias
	if modelAlias == "" {
		modelAlias = "unresolved"
	}
	providerName := input.Route.Provider.Name
	if providerName == "" {
		providerName = "gateway"
	}
	logContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, err = s.db.Exec(logContext, `
		INSERT INTO request_logs
		    (id, request_id, model_id, model_alias, provider_id, provider_name,
		     credential_id, credential_label, endpoint, attempts, routing_decisions, status_code,
		     latency_ms, input_tokens, output_tokens, error_code, error_message,
		     request_body_cipher, response_body_cipher, body_truncated,
		     public_protocol, upstream_protocol, upstream_request_id)
		VALUES ($1,$2,NULLIF($3,''),$4,NULLIF($5,''),$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,
		        NULLIF($16,''),$17,$18,$19,$20,$21,$22,$23)
	`, id, input.RequestID, input.Route.Model.ID, modelAlias,
		input.Route.Provider.ID, providerName, input.Credential.ID,
		input.Credential.Label, input.Endpoint, attempts, routingDecisions, input.StatusCode,
		time.Since(input.Started).Milliseconds(), input.InputTokens, input.OutputTokens,
		input.ErrorCode, input.ErrorMessage, requestCipher, responseCipher, truncated,
		protocolOrDefault(input.PublicProtocol), protocolOrDefault(input.UpstreamProtocol), input.UpstreamRequestID)
	if err != nil {
		s.logger.Warn("request log write failed", "request_id", input.RequestID, "error", err)
	}
	// Balance is charged from the same place the log is written so every counted
	// request is also a charged request. Only requests that actually consumed
	// tokens are billed: a rejection that never reached the model costs nothing.
	// When the attempt ended before a key was picked, the provider is still known,
	// so the cost lands on its pooled credit rather than being lost.
	if input.InputTokens+input.OutputTokens > 0 {
		spend := requestSpendUSD(input.Route.Model, input.InputTokens, input.OutputTokens)
		if input.Credential.ID != "" {
			s.recordCredentialSpend(ctx, input.Credential.ID, spend)
		} else {
			s.recordProviderSpend(ctx, input.Route.Provider.ID, spend)
		}
	}
}

func protocolOrDefault(value string) string {
	if value == "" {
		return "openai"
	}
	return value
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
