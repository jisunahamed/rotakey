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
	unsupportedParameterPattern = regexp.MustCompile(
		`(?i)(?:unrecognized request argument supplied|unsupported (?:request )?(?:argument|parameter)(?:\(s\))?)\s*:\s*['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})`,
	)
	suggestedReplacementPattern = regexp.MustCompile(
		`(?i)\buse\s+['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})['"` + "`" + `]?\s+instead\b`,
	)
	deprecatedParameterPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})['"` + "`" + `]?\s+is\s+deprecated(?:\s+for\s+(?:this|the)\s+model)?`),
		regexp.MustCompile(`(?i)(?:parameter|argument)\s+['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})['"` + "`" + `]?\s+(?:is|has\s+been)\s+deprecated`),
		regexp.MustCompile(`(?i)['"` + "`" + `]?([A-Za-z][A-Za-z0-9_.-]{0,63})['"` + "`" + `]?\s+is\s+not\s+supported\s+(?:for|with)\s+this\s+model`),
	}
)

type compatibilityReplacement struct {
	From string
	To   string
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT m.public_alias, m.created_at, m.supports_messages
		FROM model_routes m JOIN providers p ON p.id=m.provider_id
		WHERE m.enabled=TRUE AND p.enabled=TRUE
		  AND m.capability_status IN ('catalog_verified', 'probe_verified')
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
		var supportsMessages bool
		if rows.Scan(&alias, &created, &supportsMessages) == nil {
			if isAnthropicRequest(r) && !supportsMessages {
				continue
			}
			if isAnthropicRequest(r) {
				data = append(data, map[string]any{
					"id": alias, "type": "model", "display_name": alias,
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
	route, err := s.loadRoute(r.Context(), r.PathValue("id"))
	if err != nil || (isAnthropicRequest(r) && !route.Model.SupportsMessages) {
		writeProtocolError(w, r, http.StatusNotFound, "not_found_error", "The requested model alias is not enabled.")
		return
	}
	if isAnthropicRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": route.Model.PublicAlias, "type": "model", "display_name": route.Model.PublicAlias,
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
	route, err := s.loadRoute(r.Context(), alias)
	if err != nil {
		s.rejectGatewayRequest(w, r, http.StatusNotFound, "model_not_found", "The requested model alias is not enabled.", logInput{
			RequestID: requestID, Route: routeRuntime{Model: ModelRoute{PublicAlias: alias}},
			Endpoint: endpoint, Started: started, RequestBody: raw,
		})
		return
	}
	s.updateActiveRequest(requestID, func(log *RequestLog) {
		log.ModelAlias = route.Model.PublicAlias
		log.ProviderName = route.Provider.Name
	})
	if endpoint == "chat" && !route.Model.SupportsChat {
		s.rejectGatewayRequest(w, r, http.StatusBadRequest, "unsupported_endpoint", "This model route does not support Chat Completions.", logInput{
			RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
			RequestBody: raw, Capture: route.Model.CaptureBodies,
		})
		return
	}
	if route.Provider.APIFormat == "anthropic" {
		if endpoint == "responses" && !route.Model.SupportsResponses && !route.Model.SupportsChat {
			s.rejectGatewayRequest(w, r, http.StatusBadRequest, "unsupported_endpoint", "This model route does not support Responses.", logInput{
				RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
				RequestBody: raw, Capture: route.Model.CaptureBodies, PublicProtocol: "openai", UpstreamProtocol: "anthropic",
			})
			return
		}
		s.handleOpenAIThroughAnthropic(w, r, raw, publicPayload, route, endpoint, started, requestID)
		return
	}

	upstreamPayload := cloneMap(publicPayload)
	translated := false
	if endpoint == "responses" && !route.Model.SupportsResponses {
		if !route.Model.SupportsChat {
			s.rejectGatewayRequest(w, r, http.StatusBadRequest, "unsupported_endpoint", "This model route does not support Responses.", logInput{
				RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
				RequestBody: raw, Capture: route.Model.CaptureBodies,
			})
			return
		}
		upstreamPayload, err = translateResponsesRequest(publicPayload)
		if err != nil {
			var unsupported unsupportedFeatureError
			if errors.As(err, &unsupported) {
				s.rejectGatewayRequest(w, r, http.StatusBadRequest, "unsupported_feature", unsupported.Error(), logInput{
					RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
					RequestBody: raw, Capture: route.Model.CaptureBodies,
				})
				return
			}
			s.rejectGatewayRequest(w, r, http.StatusBadRequest, "invalid_request", "Responses request could not be translated.", logInput{
				RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
				RequestBody: raw, Capture: route.Model.CaptureBodies,
			})
			return
		}
		translated = true
	}
	strippedParameters := stripTopLevelParameters(upstreamPayload, route.Model.StripParameters)
	learnedParameters := s.learnedCompatibilityParameters(r.Context(), route.Model.ID)
	strippedParameters = append(strippedParameters, stripTopLevelParameters(upstreamPayload, learnedParameters)...)
	replacedParameters := applyCompatibilityReplacements(
		upstreamPayload,
		s.learnedCompatibilityReplacements(r.Context(), route.Model.ID, endpoint),
	)
	if len(strippedParameters) > 0 {
		s.logger.Info(
			"removed unsupported upstream parameters",
			"request_id", requestID,
			"model", route.Model.PublicAlias,
			"parameters", strings.Join(strippedParameters, ","),
		)
	}
	if len(replacedParameters) > 0 {
		s.logger.Info(
			"replaced unsupported upstream parameters",
			"request_id", requestID,
			"model", route.Model.PublicAlias,
			"parameters", formatCompatibilityReplacements(replacedParameters),
		)
	}
	upstreamPayload["model"] = upstreamModelForProvider(route.Provider, route.Model.UpstreamModel)
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
		s.rejectGatewayRequest(w, r, http.StatusBadRequest, "invalid_request", "Request could not be prepared.", logInput{
			RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
			InputTokens: inputEstimate, RequestBody: raw, Capture: route.Model.CaptureBodies,
		})
		return
	}

	credentials, err := s.loadCredentials(r.Context(), route.Provider.ID, route.Model.ID)
	if err != nil {
		s.rejectGatewayRequest(w, r, http.StatusServiceUnavailable, "credentials_unavailable", "Provider credentials could not be loaded.", logInput{
			RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
			InputTokens: inputEstimate, RequestBody: raw, Capture: route.Model.CaptureBodies,
		})
		return
	}
	if len(credentials) == 0 {
		s.rejectGatewayRequest(w, r, http.StatusServiceUnavailable, "no_credentials", "No healthy credential is configured for this model.", logInput{
			RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
			InputTokens: inputEstimate, RequestBody: raw, Capture: route.Model.CaptureBodies,
		})
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		s.rejectGatewayRequest(w, r, http.StatusServiceUnavailable, "settings_unavailable", "Gateway settings are unavailable.", logInput{
			RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
			InputTokens: inputEstimate, RequestBody: raw, Capture: route.Model.CaptureBodies,
		})
		return
	}

	client, err := upstreamClient(route.Provider)
	if err != nil {
		s.rejectGatewayRequest(w, r, http.StatusBadGateway, "unsafe_provider_url", err.Error(), logInput{
			RequestID: requestID, Route: route, Endpoint: endpoint, Started: started,
			InputTokens: inputEstimate, RequestBody: raw, Capture: route.Model.CaptureBodies,
		})
		return
	}
	compatibilityRetriesRemaining := 2
	maxAttempts := len(credentials) + compatibilityRetriesRemaining
	transientRetriesRemaining := max(0, len(credentials)-1)
	// Use the configured provider deadline for the entire retry window so raising
	// a provider's timeout actually extends how long a request may run.
	retryTimeout := providerRetryTimeout(route.Provider, isStream)
	if isStream {
		// The request context remains the deadline authority for streams; disable
		// http.Client's competing total timeout.
		client.Timeout = 0
	}
	retryContext, cancelRetries := context.WithTimeout(r.Context(), retryTimeout)
	defer cancelRetries()
	compatibilityRemoved := append([]string(nil), strippedParameters...)
	compatibilityReplaced := cloneStringMap(replacedParameters)
	skipped := map[string]bool{}
	attempts := make([]AttemptRecord, 0, maxAttempts)
	routingDecisions := make([]RoutingDecision, 0)
	var (
		finalCredential   credentialRuntime
		finalResponse     []byte
		finalStatus       int
		finalErrorCode    string
		finalErrorMessage string
		inputTokens       = inputEstimate
		outputTokens      int64
		truncated         bool
	)

	for attemptNumber := 0; attemptNumber < maxAttempts; attemptNumber++ {
		selected, reservation, retryAfter, decisions, selectErr := s.selectCredentialWithDiagnostics(
			retryContext, route.Model.ID, credentials, totalReservation, skipped, time.Duration(settings.MaxWaitMS)*time.Millisecond,
		)
		routingDecisions = append(routingDecisions, decisions...)
		if selectErr != nil {
			finalErrorMessage = "Rate limiter is unavailable."
			writeError(w, http.StatusServiceUnavailable, "limiter_unavailable", finalErrorMessage)
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
			finalErrorMessage = "Every credential for this model is at capacity."
			writeError(w, http.StatusTooManyRequests, "rate_limit_exceeded", finalErrorMessage)
			finalStatus = http.StatusTooManyRequests
			finalErrorCode = "rate_limit_exceeded"
			break
		}
		finalCredential = *selected
		s.updateActiveRequest(requestID, func(log *RequestLog) {
			log.CredentialLabel = selected.Label
		})
		skipped[selected.ID] = true

		path := "/chat/completions"
		if endpoint == "responses" && !translated {
			path = "/responses"
		}
		target := strings.TrimRight(route.Provider.BaseURL, "/") + path
		upstreamRequest, _ := http.NewRequestWithContext(retryContext, http.MethodPost, target, bytes.NewReader(encodedUpstream))
		applyProviderHeaders(upstreamRequest, route.Provider, selected.Secret)
		attemptStarted := time.Now()
		response, requestErr := client.Do(upstreamRequest)
		if requestErr != nil {
			// The request attempt still consumes request quota, but no upstream
			// token usage was produced. Return the unused token reservation.
			_ = s.limiter.AdjustTokens(r.Context(), reservation, 0)
			attempts = append(attempts, AttemptRecord{
				CredentialID: selected.ID, CredentialLabel: selected.Label,
				Error: "connection_error", ErrorMessage: "The upstream connection failed before a response started.",
				Retryable: true, DurationMS: time.Since(attemptStarted).Milliseconds(),
			})
			s.markCredentialFailure(r.Context(), selected.ID, 0, 0)
			if retryContext.Err() == nil && transientRetriesRemaining > 0 && attemptNumber+1 < maxAttempts {
				transientRetriesRemaining--
				continue
			}
			finalErrorMessage = "The upstream provider could not be reached."
			writeError(w, http.StatusBadGateway, "upstream_unavailable", finalErrorMessage)
			finalStatus = http.StatusBadGateway
			finalErrorCode = "upstream_unavailable"
			break
		}

		if response.StatusCode == http.StatusBadRequest && compatibilityRetriesRemaining > 0 {
			errorBody, wasTruncated, readErr := boundedBody(response.Body, minInt64(s.cfg.MaxResponseBytes, 2<<20))
			_ = response.Body.Close()
			_ = s.limiter.AdjustTokens(r.Context(), reservation, 0)
			if readErr == nil && !wasTruncated {
				if replacement, ok := unsupportedCompatibilityReplacement(errorBody, upstreamPayload); ok {
					applyCompatibilityReplacement(upstreamPayload, replacement)
					encodedUpstream, err = json.Marshal(upstreamPayload)
					if err != nil {
						finalErrorMessage = "Request could not be prepared."
						writeError(w, http.StatusBadRequest, "invalid_request", finalErrorMessage)
						finalStatus = http.StatusBadRequest
						finalErrorCode = "invalid_request"
						break
					}
					replaced := map[string]string{replacement.From: replacement.To}
					attempts = append(attempts, AttemptRecord{
						CredentialID: selected.ID, CredentialLabel: selected.Label,
						StatusCode: response.StatusCode, Error: upstreamErrorCode(errorBody),
						ErrorMessage: upstreamErrorMessage(errorBody, selected.Secret),
						Retryable:    true, DurationMS: time.Since(attemptStarted).Milliseconds(),
						ReplacedParameters: replaced,
					})
					compatibilityRetriesRemaining--
					compatibilityReplaced[replacement.From] = replacement.To
					s.rememberCompatibilityReplacement(r.Context(), route.Model.ID, endpoint, replacement)
					s.logger.Info(
						"learned upstream parameter replacement",
						"request_id", requestID,
						"model", route.Model.PublicAlias,
						"from", replacement.From,
						"to", replacement.To,
					)
					skipped = map[string]bool{}
					continue
				}

				parameters := unsupportedCompatibilityParameters(errorBody, upstreamPayload)
				if len(parameters) > 0 {
					for _, parameter := range parameters {
						delete(upstreamPayload, parameter)
					}
					encodedUpstream, err = json.Marshal(upstreamPayload)
					if err != nil {
						finalErrorMessage = "Request could not be prepared."
						writeError(w, http.StatusBadRequest, "invalid_request", finalErrorMessage)
						finalStatus = http.StatusBadRequest
						finalErrorCode = "invalid_request"
						break
					}
					attempts = append(attempts, AttemptRecord{
						CredentialID: selected.ID, CredentialLabel: selected.Label,
						StatusCode: response.StatusCode, Error: upstreamErrorCode(errorBody),
						ErrorMessage: upstreamErrorMessage(errorBody, selected.Secret),
						Retryable:    true, DurationMS: time.Since(attemptStarted).Milliseconds(),
						RemovedParameters: parameters,
					})
					compatibilityRetriesRemaining--
					compatibilityRemoved = appendUniqueStrings(compatibilityRemoved, parameters...)
					s.rememberCompatibilityParameters(r.Context(), route.Model.ID, parameters)
					s.logger.Info(
						"learned unsupported upstream parameters",
						"request_id", requestID,
						"model", route.Model.PublicAlias,
						"parameters", strings.Join(parameters, ","),
					)
					// This was a request-shape failure, not a credential failure.
					// Let the normal round-robin cursor select any eligible key again.
					skipped = map[string]bool{}
					continue
				}
			}

			finalStatus = response.StatusCode
			attempts = append(attempts, AttemptRecord{
				CredentialID: selected.ID, CredentialLabel: selected.Label,
				StatusCode: response.StatusCode, Error: upstreamErrorCode(errorBody),
				ErrorMessage: upstreamErrorMessage(errorBody, selected.Secret), Retryable: false,
				DurationMS: time.Since(attemptStarted).Milliseconds(),
			})
			truncated = wasTruncated
			finalResponse = errorBody
			finalErrorCode = upstreamErrorCode(errorBody)
			finalErrorMessage = upstreamErrorMessage(errorBody, selected.Secret)
			copyUpstreamHeaders(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			_, _ = w.Write(errorBody)
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			break
		}

		retryable := response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode == 529 ||
			response.StatusCode == http.StatusInternalServerError ||
			response.StatusCode == http.StatusBadGateway ||
			response.StatusCode == http.StatusServiceUnavailable ||
			response.StatusCode == http.StatusGatewayTimeout ||
			response.StatusCode == http.StatusUnauthorized ||
			response.StatusCode == http.StatusForbidden
		if retryable && transientRetriesRemaining > 0 && attemptNumber+1 < maxAttempts {
			errorBody, _, _ := boundedBody(response.Body, 1<<20)
			_ = response.Body.Close()
			_ = s.limiter.AdjustTokens(r.Context(), reservation, 0)
			attempts = append(attempts, AttemptRecord{
				CredentialID: selected.ID, CredentialLabel: selected.Label,
				StatusCode: response.StatusCode, Error: upstreamErrorCode(errorBody),
				ErrorMessage: upstreamErrorMessage(errorBody, selected.Secret),
				Retryable:    true, DurationMS: time.Since(attemptStarted).Milliseconds(),
			})
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			transientRetriesRemaining--
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
			_ = s.limiter.AdjustTokens(r.Context(), reservation, 0)
			attempts[len(attempts)-1].Error = upstreamErrorCode(body)
			attempts[len(attempts)-1].ErrorMessage = upstreamErrorMessage(body, selected.Secret)
			truncated = wasTruncated
			finalResponse = body
			finalErrorCode = upstreamErrorCode(body)
			finalErrorMessage = upstreamErrorMessage(body, selected.Secret)
			copyUpstreamHeaders(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			_, _ = w.Write(body)
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			break
		}

		s.markCredentialSuccess(r.Context(), selected.ID)
		copyUpstreamHeaders(w.Header(), response.Header)
		if len(compatibilityRemoved) > 0 {
			w.Header().Set("X-Rotakey-Removed-Parameters", strings.Join(compatibilityRemoved, ","))
		}
		if len(compatibilityReplaced) > 0 {
			w.Header().Set("X-Rotakey-Replaced-Parameters", formatCompatibilityReplacements(compatibilityReplaced))
		}
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
				finalErrorMessage = "The response stream ended unexpectedly after it started."
				if !translated {
					writeStreamFailure(w, capture, endpoint, finalErrorCode, finalErrorMessage, alias)
				}
			}
			if capture != nil {
				finalResponse = append([]byte(nil), capture.Bytes()...)
				truncated = capture.truncated
			}
			s.storeRequestLog(r.Context(), logInput{
				RequestID: requestID, Route: route, Credential: finalCredential,
				Endpoint: endpoint, Attempts: attempts, RoutingDecisions: routingDecisions,
				Started: started, StatusCode: finalStatus,
				InputTokens: inputTokens, OutputTokens: 0, ErrorCode: finalErrorCode, ErrorMessage: finalErrorMessage, RequestBody: raw,
				ResponseBody: finalResponse, Capture: route.Model.CaptureBodies, Truncated: truncated,
			})
			return
		}

		body, wasTruncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
		_ = response.Body.Close()
		if readErr != nil || wasTruncated {
			finalErrorMessage = "Upstream response exceeded the configured limit."
			writeError(w, http.StatusBadGateway, "upstream_response_too_large", finalErrorMessage)
			finalStatus = http.StatusBadGateway
			finalErrorCode = "upstream_response_too_large"
			truncated = wasTruncated
			break
		}
		if translated {
			finalResponse, inputTokens, outputTokens, err = translateChatResponse(body, alias)
			if err != nil {
				finalErrorMessage = "Upstream response could not be translated."
				writeError(w, http.StatusBadGateway, "translation_failed", finalErrorMessage)
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
		Endpoint: endpoint, Attempts: attempts, RoutingDecisions: routingDecisions,
		Started: started, StatusCode: finalStatus,
		InputTokens: inputTokens, OutputTokens: outputTokens, ErrorCode: finalErrorCode, ErrorMessage: finalErrorMessage,
		RequestBody: raw, ResponseBody: finalResponse, Capture: route.Model.CaptureBodies,
		Truncated: truncated,
	})
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
		code, _ := envelope.Error.Code.(string)
		signal := strings.Contains(strings.ToLower(code), "unrecognized_request_argument") ||
			strings.Contains(strings.ToLower(code), "unsupported_parameter") ||
			strings.Contains(strings.ToLower(envelope.Error.Type), "unsupported_parameter") ||
			len(matches) > 0
		if signal {
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

func compatibilityParameterMatches(message string) [][]string {
	matches := unsupportedParameterPattern.FindAllStringSubmatch(message, -1)
	for _, pattern := range deprecatedParameterPatterns {
		matches = append(matches, pattern.FindAllStringSubmatch(message, -1)...)
	}
	return matches
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

	code, _ := envelope.Error.Code.(string)
	unsupported := strings.Contains(strings.ToLower(code), "unsupported_parameter") ||
		strings.Contains(strings.ToLower(envelope.Error.Type), "unsupported_parameter") ||
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

func cloneStringMap(source map[string]string) map[string]string {
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
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
			if !candidate.Enabled || candidate.Status == "quarantined" {
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

func (s *Server) markCredentialFailure(ctx context.Context, credentialID string, status int, retryAfter time.Duration) {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if _, err := s.db.Exec(ctx, `
			UPDATE credentials SET status='quarantined', cooldown_until=NULL,
			    validation_error=$2, last_validated_at=NOW(),
			    consecutive_failures=consecutive_failures+1, updated_at=NOW()
			WHERE id=$1
		`, credentialID, fmt.Sprintf("Provider rejected this API key during a request (HTTP %d).", status)); err != nil {
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
		    validation_error='', last_validated_at=NOW(),
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
