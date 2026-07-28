package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT m.public_alias, m.created_at
		FROM model_routes m JOIN providers p ON p.id=m.provider_id
		WHERE m.enabled=TRUE AND p.enabled=TRUE
		  AND EXISTS (
		    SELECT 1 FROM credentials c
		    WHERE c.provider_id=p.id AND c.enabled=TRUE AND c.status <> 'quarantined'
		  )
		ORDER BY m.public_alias
	`)
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
			data = append(data, map[string]any{
				"id": alias, "object": "model", "created": created.Unix(), "owned_by": "rotakey",
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
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
	raw, err := readRequestBody(w, r, s.cfg.MaxRequestBytes)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the configured limit.")
		return
	}
	var publicPayload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&publicPayload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON.")
		return
	}
	alias, ok := publicPayload["model"].(string)
	if !ok || alias == "" {
		writeError(w, http.StatusBadRequest, "model_required", "A public model alias is required.")
		return
	}
	route, err := s.loadRoute(r.Context(), alias)
	if err != nil {
		writeError(w, http.StatusNotFound, "model_not_found", "The requested model alias is not enabled.")
		return
	}
	if endpoint == "chat" && !route.Model.SupportsChat {
		writeError(w, http.StatusBadRequest, "unsupported_endpoint", "This model route does not support Chat Completions.")
		return
	}

	upstreamPayload := cloneMap(publicPayload)
	translated := false
	if endpoint == "responses" && !route.Model.SupportsResponses {
		if !route.Model.SupportsChat {
			writeError(w, http.StatusBadRequest, "unsupported_endpoint", "This model route does not support Responses.")
			return
		}
		upstreamPayload, err = translateResponsesRequest(publicPayload)
		if err != nil {
			var unsupported unsupportedFeatureError
			if errors.As(err, &unsupported) {
				writeError(w, http.StatusBadRequest, "unsupported_feature", unsupported.Error())
				return
			}
			writeError(w, http.StatusBadRequest, "invalid_request", "Responses request could not be translated.")
			return
		}
		translated = true
	}
	upstreamPayload["model"] = route.Model.UpstreamModel
	inputEstimate, outputReservation := prepareTokenReservation(
		upstreamPayload,
		endpoint,
		translated,
		route.Model.DefaultMaxOutputTokens,
		route.Model.Tokenizer,
		raw,
	)
	totalReservation := inputEstimate + outputReservation
	isStream, _ := publicPayload["stream"].(bool)
	encodedUpstream, err := json.Marshal(upstreamPayload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request could not be prepared.")
		return
	}

	credentials, err := s.loadCredentials(r.Context(), route.Provider.ID, route.Model.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "credentials_unavailable", "Provider credentials could not be loaded.")
		return
	}
	if len(credentials) == 0 {
		writeError(w, http.StatusServiceUnavailable, "no_credentials", "No healthy credential is configured for this model.")
		s.storeRequestLog(r.Context(), logInput{
			RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
			StatusCode: http.StatusServiceUnavailable, ErrorCode: "no_credentials",
			RequestBody: raw, Capture: route.Model.CaptureBodies,
		})
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "settings_unavailable", "Gateway settings are unavailable.")
		return
	}

	client, err := upstreamClient(route.Provider)
	if err != nil {
		writeError(w, http.StatusBadGateway, "unsafe_provider_url", err.Error())
		return
	}
	maxAttempts := 2
	skipped := map[string]bool{}
	attempts := make([]AttemptRecord, 0, maxAttempts)
	var (
		finalCredential credentialRuntime
		finalResponse   []byte
		finalStatus     int
		finalErrorCode  string
		inputTokens     = inputEstimate
		outputTokens    int64
		truncated       bool
	)

	for attemptNumber := 0; attemptNumber < maxAttempts; attemptNumber++ {
		selected, reservation, retryAfter, selectErr := s.selectCredential(
			r.Context(), route.Model.ID, credentials, totalReservation, skipped, time.Duration(settings.MaxWaitMS)*time.Millisecond,
		)
		if selectErr != nil {
			writeError(w, http.StatusServiceUnavailable, "limiter_unavailable", "Rate limiter is unavailable.")
			finalStatus = http.StatusServiceUnavailable
			finalErrorCode = "limiter_unavailable"
			break
		}
		if selected == nil {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "Every credential for this model is at capacity.")
			finalStatus = http.StatusTooManyRequests
			finalErrorCode = "rate_limit_exceeded"
			break
		}
		finalCredential = *selected
		skipped[selected.ID] = true

		path := "/chat/completions"
		if endpoint == "responses" && !translated {
			path = "/responses"
		}
		target := strings.TrimRight(route.Provider.BaseURL, "/") + path
		upstreamRequest, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(encodedUpstream))
		applyProviderHeaders(upstreamRequest, route.Provider, selected.Secret)
		attemptStarted := time.Now()
		response, requestErr := client.Do(upstreamRequest)
		if requestErr != nil {
			attempts = append(attempts, AttemptRecord{
				CredentialID: selected.ID, CredentialLabel: selected.Label,
				Error: "connection_error", Retryable: true, DurationMS: time.Since(attemptStarted).Milliseconds(),
			})
			s.markCredentialFailure(r.Context(), selected.ID, 0, 0)
			if attemptNumber+1 < maxAttempts {
				continue
			}
			writeError(w, http.StatusBadGateway, "upstream_unavailable", "The upstream provider could not be reached.")
			finalStatus = http.StatusBadGateway
			finalErrorCode = "upstream_unavailable"
			break
		}

		retryable := response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode == http.StatusInternalServerError ||
			response.StatusCode == http.StatusBadGateway ||
			response.StatusCode == http.StatusServiceUnavailable ||
			response.StatusCode == http.StatusGatewayTimeout ||
			response.StatusCode == http.StatusUnauthorized ||
			response.StatusCode == http.StatusForbidden
		if retryable && attemptNumber+1 < maxAttempts {
			errorBody, _, _ := boundedBody(response.Body, 1<<20)
			_ = response.Body.Close()
			attempts = append(attempts, AttemptRecord{
				CredentialID: selected.ID, CredentialLabel: selected.Label,
				StatusCode: response.StatusCode, Error: upstreamErrorCode(errorBody),
				Retryable: true, DurationMS: time.Since(attemptStarted).Milliseconds(),
			})
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			continue
		}

		finalStatus = response.StatusCode
		attempts = append(attempts, AttemptRecord{
			CredentialID: selected.ID, CredentialLabel: selected.Label,
			StatusCode: response.StatusCode, Retryable: retryable,
			DurationMS: time.Since(attemptStarted).Milliseconds(),
		})
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			body, wasTruncated, _ := boundedBody(response.Body, minInt64(s.cfg.MaxResponseBytes, 2<<20))
			_ = response.Body.Close()
			truncated = wasTruncated
			finalResponse = body
			finalErrorCode = upstreamErrorCode(body)
			copyUpstreamHeaders(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			_, _ = w.Write(body)
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			break
		}

		s.markCredentialSuccess(r.Context(), selected.ID)
		copyUpstreamHeaders(w.Header(), response.Header)
		if isStream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(response.StatusCode)
			var capture *limitedCapture
			if route.Model.CaptureBodies {
				capture = &limitedCapture{limit: s.cfg.CaptureBytes}
			}
			var streamErr error
			if translated {
				streamErr = translateChatStream(response.Body, w, alias, capture)
			} else {
				var captureWriter io.Writer
				if capture != nil {
					captureWriter = capture
				}
				streamErr = copyStreamingResponse(w, response.Body, captureWriter)
			}
			_ = response.Body.Close()
			if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
				finalErrorCode = "stream_interrupted"
			}
			if capture != nil {
				finalResponse = append([]byte(nil), capture.Bytes()...)
				truncated = capture.truncated
			}
			s.storeRequestLog(r.Context(), logInput{
				RequestID: requestID, Route: route, Credential: finalCredential,
				Endpoint: endpoint, Attempts: attempts, Started: started, StatusCode: finalStatus,
				InputTokens: inputTokens, OutputTokens: 0, ErrorCode: finalErrorCode, RequestBody: raw,
				ResponseBody: finalResponse, Capture: route.Model.CaptureBodies, Truncated: truncated,
			})
			return
		}

		body, wasTruncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
		_ = response.Body.Close()
		if readErr != nil || wasTruncated {
			writeError(w, http.StatusBadGateway, "upstream_response_too_large", "Upstream response exceeded the configured limit.")
			finalStatus = http.StatusBadGateway
			finalErrorCode = "upstream_response_too_large"
			truncated = wasTruncated
			break
		}
		if translated {
			finalResponse, inputTokens, outputTokens, err = translateChatResponse(body, alias)
			if err != nil {
				writeError(w, http.StatusBadGateway, "translation_failed", "Upstream response could not be translated.")
				finalStatus = http.StatusBadGateway
				finalErrorCode = "translation_failed"
				break
			}
		} else {
			finalResponse, inputTokens, outputTokens = replaceResponseModel(body, alias)
		}
		if inputTokens+outputTokens > 0 {
			_ = s.limiter.AdjustTokens(r.Context(), reservation, inputTokens+outputTokens)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(finalResponse)
		break
	}

	s.storeRequestLog(r.Context(), logInput{
		RequestID: requestID, Route: route, Credential: finalCredential,
		Endpoint: endpoint, Attempts: attempts, Started: started, StatusCode: finalStatus,
		InputTokens: inputTokens, OutputTokens: outputTokens, ErrorCode: finalErrorCode,
		RequestBody: raw, ResponseBody: finalResponse, Capture: route.Model.CaptureBodies,
		Truncated: truncated,
	})
}

func copyStreamingResponse(destination http.ResponseWriter, source io.Reader, capture io.Writer) error {
	buffer := make([]byte, 32<<10)
	flusher, _ := destination.(http.Flusher)
	for {
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return err
			}
			if capture != nil {
				_, _ = capture.Write(buffer[:read])
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func readRequestBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func cloneMap(source map[string]any) map[string]any {
	target := make(map[string]any, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func prepareTokenReservation(
	payload map[string]any,
	endpoint string,
	translated bool,
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
	if translated || endpoint == "chat" {
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
	deadline := time.Now().Add(maxWait)
	for {
		cursor, err := s.redis.Incr(ctx, "rr:"+modelID).Result()
		if err != nil {
			return nil, reservation{}, 0, err
		}
		minRetry := time.Duration(math.MaxInt64)
		for _, index := range credentialSelectionOrder(credentials, cursor) {
			candidate := &credentials[index]
			if skipped[candidate.ID] || !candidate.Enabled || candidate.Status == "quarantined" {
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
			constraints, valid := buildConstraints(*candidate, modelID, tokenCost)
			if !valid {
				continue
			}
			result, err := s.limiter.Reserve(ctx, constraints)
			if err != nil {
				return nil, reservation{}, 0, err
			}
			if result.Allowed {
				return candidate, reservation{
					constraints: constraints, tokenCost: tokenCost, reservedAt: result.ReservedAt,
				}, 0, nil
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

func (s *Server) markCredentialFailure(ctx context.Context, credentialID string, status int, retryAfter time.Duration) {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if _, err := s.db.Exec(ctx, `
			UPDATE credentials SET status='quarantined', cooldown_until=NULL,
			    consecutive_failures=consecutive_failures+1, updated_at=NOW()
			WHERE id=$1
		`, credentialID); err != nil {
			s.logger.Warn("credential quarantine state write failed", "credential_id", credentialID, "error", err)
		}
		return
	}
	if status == http.StatusTooManyRequests {
		if retryAfter <= 0 {
			retryAfter = time.Minute
		}
		_ = s.redis.Set(ctx, "cooldown:"+credentialID, "429", retryAfter).Err()
		if _, err := s.db.Exec(ctx, `
			UPDATE credentials SET status='cooldown', cooldown_until=$2,
			    updated_at=NOW() WHERE id=$1
		`, credentialID, time.Now().Add(retryAfter)); err != nil {
			s.logger.Warn("credential cooldown state write failed", "credential_id", credentialID, "error", err)
		}
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

func (s *Server) markCredentialSuccess(ctx context.Context, credentialID string) {
	_ = s.redis.Del(ctx, "failures:"+credentialID, "cooldown:"+credentialID).Err()
	_, _ = s.db.Exec(ctx, `
		UPDATE credentials SET status='healthy', cooldown_until=NULL,
		    consecutive_failures=0, updated_at=NOW()
		WHERE id=$1 AND status <> 'quarantined'
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
	}
	return "upstream_error"
}

func copyUpstreamHeaders(destination, source http.Header) {
	for _, header := range []string{"Content-Type", "Cache-Control", "Retry-After", "Openai-Processing-Ms"} {
		if value := source.Get(header); value != "" {
			destination.Set(header, value)
		}
	}
}

type logInput struct {
	RequestID    string
	Route        routeRuntime
	Credential   credentialRuntime
	Endpoint     string
	Attempts     []AttemptRecord
	Started      time.Time
	StatusCode   int
	InputTokens  int64
	OutputTokens int64
	ErrorCode    string
	RequestBody  []byte
	ResponseBody []byte
	Capture      bool
	Truncated    bool
}

func (s *Server) storeRequestLog(ctx context.Context, input logInput) {
	id, err := newID("log")
	if err != nil {
		return
	}
	attempts, _ := json.Marshal(input.Attempts)
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
	_, err = s.db.Exec(ctx, `
		INSERT INTO request_logs
		    (id, request_id, model_id, model_alias, provider_id, provider_name,
		     credential_id, credential_label, endpoint, attempts, status_code,
		     latency_ms, input_tokens, output_tokens, error_code,
		     request_body_cipher, response_body_cipher, body_truncated)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,
		        NULLIF($15,''),$16,$17,$18)
	`, id, input.RequestID, input.Route.Model.ID, input.Route.Model.PublicAlias,
		input.Route.Provider.ID, input.Route.Provider.Name, input.Credential.ID,
		input.Credential.Label, input.Endpoint, attempts, input.StatusCode,
		time.Since(input.Started).Milliseconds(), input.InputTokens, input.OutputTokens,
		input.ErrorCode, requestCipher, responseCipher, truncated)
	if err != nil {
		s.logger.Warn("request log write failed", "request_id", input.RequestID, "error", err)
	}
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
