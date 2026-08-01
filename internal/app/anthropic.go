package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	route, err := s.loadRoute(r.Context(), alias)
	if err != nil {
		s.rejectAnthropic(w, r, http.StatusNotFound, "not_found_error", "The requested model alias is not enabled.", logInput{RequestID: requestID, Route: routeRuntime{Model: ModelRoute{PublicAlias: alias}}, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic"})
		return
	}
	if !route.Model.SupportsMessages {
		s.rejectAnthropic(w, r, http.StatusBadRequest, "invalid_request_error", "This model route does not support Messages.", logInput{RequestID: requestID, Route: route, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic", UpstreamProtocol: route.Provider.APIFormat})
		return
	}
	s.updateActiveRequest(requestID, func(log *RequestLog) {
		log.ModelAlias = alias
		log.ProviderName = route.Provider.Name
		log.PublicProtocol = "anthropic"
		log.UpstreamProtocol = route.Provider.APIFormat
	})
	forcedCredential, affinityErr := s.resolveMessageResourceAffinity(r.Context(), payload, route.Provider.ID)
	if affinityErr != nil {
		s.rejectAnthropic(w, r, http.StatusBadRequest, "resource_affinity_conflict", affinityErr.Error(), logInput{RequestID: requestID, Route: route, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic", UpstreamProtocol: route.Provider.APIFormat})
		return
	}
	if route.Provider.APIFormat == "anthropic" {
		upstream := cloneMap(payload)
		upstream["model"] = route.Model.UpstreamModel
		s.proxyAnthropicUpstream(w, r, raw, upstream, route, started, requestID, messageModeAnthropic, forcedCredential, false)
		return
	}
	if forcedCredential != "" {
		s.rejectAnthropic(w, r, http.StatusBadRequest, "unsupported_feature", "File references require a native Anthropic provider route.", logInput{RequestID: requestID, Route: route, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic", UpstreamProtocol: "openai"})
		return
	}
	chat, err := translateAnthropicRequestToChat(payload)
	if err != nil {
		s.rejectAnthropic(w, r, http.StatusBadRequest, "unsupported_feature", err.Error(), logInput{RequestID: requestID, Route: route, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic", UpstreamProtocol: "openai"})
		return
	}
	chat["model"] = route.Model.UpstreamModel
	s.proxyOpenAIUpstreamForAnthropic(w, r, raw, chat, route, started, requestID)
}

func (s *Server) handleOpenAIThroughAnthropic(w http.ResponseWriter, r *http.Request, raw []byte, public map[string]any, route routeRuntime, endpoint string, started time.Time, requestID string) {
	chat := public
	var err error
	if endpoint == "responses" {
		chat, err = translateResponsesRequest(public)
		if err != nil {
			s.rejectGatewayRequest(w, r, http.StatusBadRequest, "unsupported_feature", err.Error(), logInput{RequestID: requestID, Route: route, Endpoint: endpoint, Started: started, RequestBody: raw, Capture: route.Model.CaptureBodies, PublicProtocol: "openai", UpstreamProtocol: "anthropic"})
			return
		}
	}
	upstream, err := translateChatRequestToAnthropic(chat)
	if err != nil {
		s.rejectGatewayRequest(w, r, http.StatusBadRequest, "unsupported_feature", err.Error(), logInput{RequestID: requestID, Route: route, Endpoint: endpoint, Started: started, RequestBody: raw, Capture: route.Model.CaptureBodies, PublicProtocol: "openai", UpstreamProtocol: "anthropic"})
		return
	}
	upstream["model"] = route.Model.UpstreamModel
	includeUsage := false
	if options, ok := chat["stream_options"].(map[string]any); ok {
		includeUsage, _ = options["include_usage"].(bool)
	}
	mode := messageModeChat
	if endpoint == "responses" {
		mode = messageModeResponses
	}
	s.proxyAnthropicUpstream(w, r, raw, upstream, route, started, requestID, mode, "", includeUsage)
}

func (s *Server) proxyAnthropicUpstream(w http.ResponseWriter, r *http.Request, raw []byte, payload map[string]any, route routeRuntime, started time.Time, requestID, publicMode, forcedCredential string, includeOpenAIUsage bool) {
	if numberAsInt64(payload["max_tokens"]) <= 0 {
		payload["max_tokens"] = route.Model.DefaultMaxOutputTokens
	}
	inputEstimate := estimateInputTokens(raw, route.Model.Tokenizer)
	outputReservation := numberAsInt64(payload["max_tokens"])
	tokenCost := inputEstimate + outputReservation
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.writeMessageProxyError(w, r, publicMode, http.StatusBadRequest, "invalid_request_error", "Request could not be prepared.")
		return
	}
	credentials, err := s.loadCredentials(r.Context(), route.Provider.ID, route.Model.ID)
	if err == nil && forcedCredential != "" {
		filtered := credentials[:0]
		for _, credential := range credentials {
			if credential.ID == forcedCredential {
				filtered = append(filtered, credential)
			}
		}
		credentials = filtered
	}
	if err != nil || len(credentials) == 0 {
		s.writeMessageProxyError(w, r, publicMode, http.StatusServiceUnavailable, "api_error", "No healthy credential is available for this route.")
		s.storeRequestLog(r.Context(), logInput{RequestID: requestID, Route: route, Endpoint: protocolEndpoint(publicMode), Started: started, StatusCode: http.StatusServiceUnavailable, ErrorCode: "no_credentials", ErrorMessage: "No healthy credential is available for this route.", RequestBody: raw, Capture: route.Model.CaptureBodies, PublicProtocol: protocolName(publicMode), UpstreamProtocol: "anthropic"})
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		s.writeMessageProxyError(w, r, publicMode, http.StatusServiceUnavailable, "api_error", "Gateway settings are unavailable.")
		return
	}
	client, err := upstreamClient(route.Provider)
	if err != nil {
		s.writeMessageProxyError(w, r, publicMode, http.StatusBadGateway, "api_error", err.Error())
		return
	}
	stream, _ := payload["stream"].(bool)
	skipped := map[string]bool{}
	attempts := make([]AttemptRecord, 0, 2)
	decisions := make([]RoutingDecision, 0)
	var finalCredential credentialRuntime
	var finalResponse []byte
	var finalStatus int
	var finalCode, finalMessage, upstreamRequestID string
	var inputTokens, outputTokens int64
	var truncated bool
	retryContext, cancelRetries := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancelRetries()
	maxAttempts := len(credentials)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selected, reservation, retryAfter, routing, selectErr := s.selectCredentialWithDiagnostics(retryContext, route.Model.ID, credentials, tokenCost, skipped, time.Duration(settings.MaxWaitMS)*time.Millisecond)
		decisions = append(decisions, routing...)
		if selectErr != nil {
			finalStatus, finalCode, finalMessage = http.StatusServiceUnavailable, "limiter_unavailable", "Rate limiter is unavailable."
			s.writeMessageProxyError(w, r, publicMode, finalStatus, "api_error", finalMessage)
			break
		}
		if selected == nil {
			seconds := max(1, int(math.Ceil(retryAfter.Seconds())))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			finalStatus, finalCode, finalMessage = http.StatusTooManyRequests, "rate_limit_exceeded", "Every credential for this model is at capacity."
			s.writeMessageProxyError(w, r, publicMode, finalStatus, "rate_limit_error", finalMessage)
			break
		}
		finalCredential = *selected
		skipped[selected.ID] = true
		s.updateActiveRequest(requestID, func(log *RequestLog) { log.CredentialLabel = selected.Label })
		target := strings.TrimRight(route.Provider.BaseURL, "/") + "/messages"
		upstreamRequest, _ := http.NewRequestWithContext(retryContext, http.MethodPost, target, bytes.NewReader(encoded))
		applyProviderHeaders(upstreamRequest, route.Provider, selected.Secret)
		forwardAnthropicHeaders(upstreamRequest.Header, r.Header)
		attemptStarted := time.Now()
		response, requestErr := client.Do(upstreamRequest)
		if requestErr != nil {
			attempts = append(attempts, AttemptRecord{CredentialID: selected.ID, CredentialLabel: selected.Label, Error: "connection_error", ErrorMessage: "The upstream connection failed before a response started.", Retryable: true, DurationMS: time.Since(attemptStarted).Milliseconds()})
			s.markCredentialFailure(r.Context(), selected.ID, 0, 0)
			if retryContext.Err() == nil && attempt+1 < maxAttempts {
				continue
			}
			finalStatus, finalCode, finalMessage = http.StatusBadGateway, "upstream_unavailable", "The upstream provider could not be reached."
			s.writeMessageProxyError(w, r, publicMode, finalStatus, "api_error", finalMessage)
			break
		}
		upstreamRequestID = response.Header.Get("Request-Id")
		retryable := anthropicRetryableStatus(response.StatusCode)
		if retryable && retryContext.Err() == nil && attempt+1 < maxAttempts {
			body, _, _ := boundedBody(response.Body, 1<<20)
			_ = response.Body.Close()
			attempts = append(attempts, AttemptRecord{CredentialID: selected.ID, CredentialLabel: selected.Label, StatusCode: response.StatusCode, Error: upstreamErrorCode(body), ErrorMessage: upstreamErrorMessage(body, selected.Secret), Retryable: true, DurationMS: time.Since(attemptStarted).Milliseconds()})
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			continue
		}
		finalStatus = response.StatusCode
		attempts = append(attempts, AttemptRecord{CredentialID: selected.ID, CredentialLabel: selected.Label, StatusCode: response.StatusCode, Retryable: retryable, DurationMS: time.Since(attemptStarted).Milliseconds()})
		copyAnthropicHeaders(w.Header(), response.Header)
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			body, wasTruncated, _ := boundedBody(response.Body, minInt64(s.cfg.MaxResponseBytes, 2<<20))
			_ = response.Body.Close()
			truncated = wasTruncated
			finalResponse = body
			finalCode = upstreamErrorCode(body)
			finalMessage = upstreamErrorMessage(body, selected.Secret)
			attempts[len(attempts)-1].Error = finalCode
			attempts[len(attempts)-1].ErrorMessage = finalMessage
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			if publicMode == messageModeAnthropic {
				w.WriteHeader(response.StatusCode)
				_, _ = w.Write(body)
			} else {
				writeError(w, response.StatusCode, finalCode, valueOr(finalMessage, "The upstream provider rejected the request."))
			}
			break
		}
		if stream {
			streamSource := io.Reader(response.Body)
			if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
				body, wasTruncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
				_ = response.Body.Close()
				if readErr != nil || wasTruncated {
					finalStatus, finalCode, finalMessage, truncated = http.StatusBadGateway, "upstream_stream_invalid", "The upstream returned an unreadable non-SSE response for a streaming request.", wasTruncated
					s.markCredentialFailure(r.Context(), selected.ID, http.StatusBadGateway, 0)
					attempts[len(attempts)-1].Error, attempts[len(attempts)-1].ErrorMessage = finalCode, finalMessage
					if retryContext.Err() == nil && attempt+1 < maxAttempts {
						attempts[len(attempts)-1].Retryable = true
						continue
					}
					s.writeMessageProxyError(w, r, publicMode, finalStatus, "api_error", finalMessage)
					break
				}
				synthetic, syntheticErr := anthropicJSONToSSE(body)
				if syntheticErr != nil {
					finalStatus, finalCode, finalMessage = http.StatusBadGateway, "upstream_stream_invalid", "The upstream returned HTTP 200 without a valid Anthropic stream or Message response."
					finalResponse = body
					s.markCredentialFailure(r.Context(), selected.ID, http.StatusBadGateway, 0)
					attempts[len(attempts)-1].Error, attempts[len(attempts)-1].ErrorMessage = finalCode, finalMessage
					if retryContext.Err() == nil && attempt+1 < maxAttempts {
						attempts[len(attempts)-1].Retryable = true
						continue
					}
					s.writeMessageProxyError(w, r, publicMode, finalStatus, "api_error", finalMessage)
					break
				}
				streamSource = bytes.NewReader(synthetic)
			} else {
				prepared, prepareErr := prepareAnthropicSSE(streamSource)
				if prepareErr != nil {
					_ = response.Body.Close()
					finalStatus, finalCode, finalMessage = http.StatusBadGateway, "upstream_stream_invalid", "The upstream returned HTTP 200 without a valid Anthropic SSE event."
					s.markCredentialFailure(r.Context(), selected.ID, http.StatusBadGateway, 0)
					attempts[len(attempts)-1].Error, attempts[len(attempts)-1].ErrorMessage = finalCode, finalMessage
					if retryContext.Err() == nil && attempt+1 < maxAttempts {
						attempts[len(attempts)-1].Retryable = true
						continue
					}
					s.writeMessageProxyError(w, r, publicMode, finalStatus, "api_error", finalMessage)
					break
				}
				streamSource = prepared
			}
			s.markCredentialSuccess(r.Context(), selected.ID)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(response.StatusCode)
			var capture *limitedCapture
			if route.Model.CaptureBodies {
				capture = &limitedCapture{limit: s.cfg.CaptureBytes}
			}
			var streamCode string
			var streamErr error
			switch publicMode {
			case messageModeAnthropic:
				streamCode, streamErr = rewriteAnthropicStream(streamSource, w, route.Model.PublicAlias, capture)
			case messageModeResponses:
				streamCode, streamErr = translateAnthropicStreamToResponses(streamSource, w, route.Model.PublicAlias, capture)
			default:
				streamCode, streamErr = translateAnthropicStreamToOpenAI(streamSource, w, route.Model.PublicAlias, capture, includeOpenAIUsage)
			}
			_ = response.Body.Close()
			if streamCode != "" {
				finalCode, finalMessage = streamCode, "The upstream provider sent an error after streaming started."
			}
			if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
				finalCode, finalMessage = "stream_interrupted", "The response stream ended unexpectedly after it started."
			}
			if capture != nil {
				finalResponse = append([]byte(nil), capture.Bytes()...)
				truncated = capture.truncated
			}
			s.storeRequestLog(r.Context(), logInput{RequestID: requestID, Route: route, Credential: finalCredential, Endpoint: protocolEndpoint(publicMode), Attempts: attempts, RoutingDecisions: decisions, Started: started, StatusCode: finalStatus, InputTokens: inputEstimate, ErrorCode: finalCode, ErrorMessage: finalMessage, RequestBody: raw, ResponseBody: finalResponse, Capture: route.Model.CaptureBodies, Truncated: truncated, PublicProtocol: protocolName(publicMode), UpstreamProtocol: "anthropic", UpstreamRequestID: upstreamRequestID})
			return
		}
		s.markCredentialSuccess(r.Context(), selected.ID)
		body, wasTruncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
		_ = response.Body.Close()
		if readErr != nil || wasTruncated {
			finalStatus, finalCode, finalMessage, truncated = http.StatusBadGateway, "upstream_response_too_large", "Upstream response exceeded the configured limit.", wasTruncated
			s.writeMessageProxyError(w, r, publicMode, finalStatus, "api_error", finalMessage)
			break
		}
		switch publicMode {
		case messageModeAnthropic:
			finalResponse, inputTokens, outputTokens = replaceAnthropicModel(body, route.Model.PublicAlias)
		case messageModeResponses:
			finalResponse, inputTokens, outputTokens, err = translateAnthropicResponseToResponses(body, route.Model.PublicAlias)
		default:
			finalResponse, inputTokens, outputTokens, err = translateAnthropicResponseToChat(body, route.Model.PublicAlias)
		}
		if err != nil {
			finalStatus, finalCode, finalMessage = http.StatusBadGateway, "translation_failed", "Upstream response could not be translated."
			s.writeMessageProxyError(w, r, publicMode, finalStatus, "api_error", finalMessage)
			break
		}
		if inputTokens+outputTokens > 0 {
			_ = s.limiter.AdjustTokens(r.Context(), reservation, inputTokens+outputTokens)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(finalResponse)
		break
	}
	s.storeRequestLog(r.Context(), logInput{RequestID: requestID, Route: route, Credential: finalCredential, Endpoint: protocolEndpoint(publicMode), Attempts: attempts, RoutingDecisions: decisions, Started: started, StatusCode: finalStatus, InputTokens: inputTokens, OutputTokens: outputTokens, ErrorCode: finalCode, ErrorMessage: finalMessage, RequestBody: raw, ResponseBody: finalResponse, Capture: route.Model.CaptureBodies, Truncated: truncated, PublicProtocol: protocolName(publicMode), UpstreamProtocol: "anthropic", UpstreamRequestID: upstreamRequestID})
}

func (s *Server) proxyOpenAIUpstreamForAnthropic(w http.ResponseWriter, r *http.Request, raw []byte, payload map[string]any, route routeRuntime, started time.Time, requestID string) {
	if numberAsInt64(payload["max_tokens"]) <= 0 {
		payload["max_tokens"] = route.Model.DefaultMaxOutputTokens
	}
	inputEstimate := estimateInputTokens(raw, route.Model.Tokenizer)
	tokenCost := inputEstimate + numberAsInt64(payload["max_tokens"])
	encoded, _ := json.Marshal(payload)
	credentials, err := s.loadCredentials(r.Context(), route.Provider.ID, route.Model.ID)
	if err != nil || len(credentials) == 0 {
		s.rejectAnthropic(w, r, http.StatusServiceUnavailable, "api_error", "No healthy credential is available for this route.", logInput{RequestID: requestID, Route: route, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic", UpstreamProtocol: "openai"})
		return
	}
	settings, _, _ := s.settings(r.Context())
	client, err := upstreamClient(route.Provider)
	if err != nil {
		s.rejectAnthropic(w, r, http.StatusBadGateway, "api_error", err.Error(), logInput{RequestID: requestID, Route: route, Endpoint: "messages", Started: started, RequestBody: raw, PublicProtocol: "anthropic", UpstreamProtocol: "openai"})
		return
	}
	skipped := map[string]bool{}
	attempts := []AttemptRecord{}
	decisions := []RoutingDecision{}
	var finalCredential credentialRuntime
	var finalResponse []byte
	var finalStatus int
	var finalCode, finalMessage, upstreamRequestID string
	var inputTokens, outputTokens int64
	stream, _ := payload["stream"].(bool)
	retryContext, cancelRetries := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancelRetries()
	maxAttempts := len(credentials)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		selected, reservation, retryAfter, routing, selectErr := s.selectCredentialWithDiagnostics(retryContext, route.Model.ID, credentials, tokenCost, skipped, time.Duration(settings.MaxWaitMS)*time.Millisecond)
		decisions = append(decisions, routing...)
		if selectErr != nil {
			finalStatus, finalCode, finalMessage = http.StatusServiceUnavailable, "limiter_unavailable", "Rate limiter is unavailable."
			writeAnthropicError(w, r, finalStatus, "api_error", finalMessage)
			break
		}
		if selected == nil {
			seconds := max(1, int(math.Ceil(retryAfter.Seconds())))
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			finalStatus, finalCode, finalMessage = http.StatusTooManyRequests, "rate_limit_exceeded", "Every credential for this model is at capacity."
			writeAnthropicError(w, r, finalStatus, "rate_limit_error", finalMessage)
			break
		}
		finalCredential = *selected
		skipped[selected.ID] = true
		target := strings.TrimRight(route.Provider.BaseURL, "/") + "/chat/completions"
		upstreamRequest, _ := http.NewRequestWithContext(retryContext, http.MethodPost, target, bytes.NewReader(encoded))
		applyProviderHeaders(upstreamRequest, route.Provider, selected.Secret)
		response, requestErr := client.Do(upstreamRequest)
		if requestErr != nil {
			attempts = append(attempts, AttemptRecord{CredentialID: selected.ID, CredentialLabel: selected.Label, Error: "connection_error", Retryable: true})
			s.markCredentialFailure(r.Context(), selected.ID, 0, 0)
			if retryContext.Err() == nil && attempt+1 < maxAttempts {
				continue
			}
			finalStatus, finalCode, finalMessage = http.StatusBadGateway, "upstream_unavailable", "The upstream provider could not be reached."
			writeAnthropicError(w, r, finalStatus, "api_error", finalMessage)
			break
		}
		upstreamRequestID = valueOr(response.Header.Get("Request-Id"), response.Header.Get("X-Request-Id"))
		retryable := anthropicRetryableStatus(response.StatusCode)
		if retryable && retryContext.Err() == nil && attempt+1 < maxAttempts {
			body, _, _ := boundedBody(response.Body, 1<<20)
			_ = response.Body.Close()
			attempts = append(attempts, AttemptRecord{CredentialID: selected.ID, CredentialLabel: selected.Label, StatusCode: response.StatusCode, Error: upstreamErrorCode(body), Retryable: true})
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			continue
		}
		finalStatus = response.StatusCode
		attempts = append(attempts, AttemptRecord{CredentialID: selected.ID, CredentialLabel: selected.Label, StatusCode: response.StatusCode, Retryable: retryable})
		copyAnthropicHeaders(w.Header(), response.Header)
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			body, _, _ := boundedBody(response.Body, minInt64(s.cfg.MaxResponseBytes, 2<<20))
			_ = response.Body.Close()
			finalResponse, finalCode, finalMessage = body, upstreamErrorCode(body), upstreamErrorMessage(body, selected.Secret)
			s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
			writeAnthropicError(w, r, response.StatusCode, anthropicErrorType(response.StatusCode), valueOr(finalMessage, "The upstream provider rejected the request."))
			break
		}
		s.markCredentialSuccess(r.Context(), selected.ID)
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(response.StatusCode)
			streamCode, streamErr := translateOpenAIStreamToAnthropic(response.Body, w, route.Model.PublicAlias, nil)
			_ = response.Body.Close()
			if streamCode != "" {
				finalCode, finalMessage = streamCode, "The upstream provider sent an error after streaming started."
			}
			if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
				finalCode, finalMessage = "stream_interrupted", "The response stream ended unexpectedly."
			}
			s.storeRequestLog(r.Context(), logInput{RequestID: requestID, Route: route, Credential: finalCredential, Endpoint: "messages", Attempts: attempts, RoutingDecisions: decisions, Started: started, StatusCode: finalStatus, InputTokens: inputEstimate, ErrorCode: finalCode, ErrorMessage: finalMessage, RequestBody: raw, Capture: route.Model.CaptureBodies, PublicProtocol: "anthropic", UpstreamProtocol: "openai", UpstreamRequestID: upstreamRequestID})
			return
		}
		body, _, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
		_ = response.Body.Close()
		if readErr != nil {
			finalStatus, finalCode, finalMessage = http.StatusBadGateway, "upstream_response_invalid", "Upstream response could not be read."
			writeAnthropicError(w, r, finalStatus, "api_error", finalMessage)
			break
		}
		finalResponse, inputTokens, outputTokens, err = translateChatResponseToAnthropic(body, route.Model.PublicAlias)
		if err != nil {
			finalStatus, finalCode, finalMessage = http.StatusBadGateway, "translation_failed", "Upstream response could not be translated."
			writeAnthropicError(w, r, finalStatus, "api_error", finalMessage)
			break
		}
		_ = s.limiter.AdjustTokens(r.Context(), reservation, inputTokens+outputTokens)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(finalResponse)
		break
	}
	s.storeRequestLog(r.Context(), logInput{RequestID: requestID, Route: route, Credential: finalCredential, Endpoint: "messages", Attempts: attempts, RoutingDecisions: decisions, Started: started, StatusCode: finalStatus, InputTokens: inputTokens, OutputTokens: outputTokens, ErrorCode: finalCode, ErrorMessage: finalMessage, RequestBody: raw, ResponseBody: finalResponse, Capture: route.Model.CaptureBodies, PublicProtocol: "anthropic", UpstreamProtocol: "openai", UpstreamRequestID: upstreamRequestID})
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

func (s *Server) writeMessageProxyError(w http.ResponseWriter, r *http.Request, mode string, status int, code, message string) {
	if mode == messageModeAnthropic {
		writeAnthropicError(w, r, status, code, message)
		return
	}
	writeError(w, status, code, message)
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
