package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// resolveRoutes returns the candidate routes for an alias. Provider-wise mode
// keeps the historical one-route behaviour; model-wise mode pools every
// provider publishing the same public alias.
func (s *Server) resolveRoutes(ctx context.Context, alias string, mode string) ([]routeRuntime, error) {
	routes, err := s.loadRoutes(ctx, alias)
	if err != nil {
		return nil, err
	}
	if normalizeRoutingMode(mode) != routingModeModel && len(routes) > 1 {
		return routes[:1], nil
	}
	return routes, nil
}

// clientForProvider memoises one HTTP client per provider inside a request so
// pooled failover does not rebuild transports on every attempt.
type clientCache struct {
	clients map[string]*http.Client
	errors  map[string]error
}

func newClientCache() *clientCache {
	return &clientCache{clients: map[string]*http.Client{}, errors: map[string]error{}}
}

func (c *clientCache) get(provider Provider, isStream bool) (*http.Client, error) {
	if err, ok := c.errors[provider.ID]; ok {
		return nil, err
	}
	if client, ok := c.clients[provider.ID]; ok {
		return client, nil
	}
	client, err := upstreamClient(provider)
	if err != nil {
		c.errors[provider.ID] = err
		return nil, err
	}
	if isStream {
		// The request context stays the deadline authority for streams.
		client.Timeout = 0
	}
	c.clients[provider.ID] = client
	return client, nil
}

// writePoolError renders a failure in the protocol the caller spoke, so an
// Anthropic client always receives an Anthropic-shaped error even when the
// attempt that failed was against an OpenAI provider.
func (s *Server) writePoolError(w http.ResponseWriter, r *http.Request, mode string, status int, code, message string) {
	if mode == messageModeAnthropic {
		writeAnthropicError(w, r, status, anthropicErrorType(status), message)
		return
	}
	writeError(w, status, code, message)
}

// buildPoolPlans translates the public request once per pooled route. Routes
// whose translation fails are dropped instead of failing the request, because
// another provider in the pool may still accept the payload.
func (s *Server) buildPoolPlans(
	ctx context.Context,
	req dispatchRequest,
	candidates []routeCandidate,
	state dispatchState,
) (map[string]upstreamPlan, error) {
	plans := map[string]upstreamPlan{}
	var lastErr error
	for _, candidate := range candidates {
		id := candidate.Route.Model.ID
		if _, ok := plans[id]; ok {
			continue
		}
		plan, err := s.buildPlan(ctx, req, candidate.Route, state)
		if err != nil {
			lastErr = err
			continue
		}
		plans[id] = plan
	}
	if len(plans) == 0 {
		return nil, lastErr
	}
	return plans, nil
}

// planTokenCosts exposes each route's reservation size to the limiter, which
// differs across the pool because every provider gets its own translation.
func planTokenCosts(plans map[string]upstreamPlan) map[string]int64 {
	costs := make(map[string]int64, len(plans))
	for id, plan := range plans {
		costs[id] = plan.TokenCost
	}
	return costs
}

// poolRetryTimeout takes the most generous deadline in the pool so a slow but
// permitted provider is not cut short by a stricter sibling's timeout.
func poolRetryTimeout(routes []routeRuntime, isStream bool) time.Duration {
	longest := time.Duration(0)
	for _, route := range routes {
		if timeout := providerRetryTimeout(route.Provider, isStream); timeout > longest {
			longest = timeout
		}
	}
	return longest
}

// filterForcedCredential narrows the pool to one credential. File affinity ties
// a request to the exact key that uploaded the resource, so pooling must not
// move it to another provider.
func filterForcedCredential(candidates []routeCandidate, credentialID string) []routeCandidate {
	if credentialID == "" {
		return candidates
	}
	filtered := make([]routeCandidate, 0, 1)
	for _, candidate := range candidates {
		if candidate.Credential.ID == credentialID {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

// retainPlannedCandidates drops candidates whose provider could not translate
// the request, so the rotation never selects an undispatchable pair.
func retainPlannedCandidates(candidates []routeCandidate, plans map[string]upstreamPlan) []routeCandidate {
	kept := make([]routeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := plans[candidate.Route.Model.ID]; ok {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// rejectPool reports a pre-dispatch failure in the caller's protocol and logs it
// against the alias so the console still shows the attempt.
func (s *Server) rejectPool(
	w http.ResponseWriter,
	r *http.Request,
	req dispatchRequest,
	route routeRuntime,
	status int,
	code, message string,
) {
	s.writePoolError(w, r, req.PublicMode, status, code, message)
	s.storeRequestLog(r.Context(), logInput{
		RequestID: req.RequestID, Route: route, Endpoint: req.Endpoint, Started: req.Started,
		StatusCode: status, ErrorCode: code, ErrorMessage: message, RequestBody: req.Raw,
		Capture: route.Model.CaptureBodies, PublicProtocol: protocolName(req.PublicMode),
		UpstreamProtocol: valueOr(route.Provider.APIFormat, "openai"),
	})
}

// servePooled runs one public request against a pool of (route, credential)
// candidates, rotating until one provider accepts it. Every attempt re-plans the
// payload for its own provider, so a single alias can span Anthropic and OpenAI
// upstreams; compatibility repairs learned on one provider are reapplied to all
// of them.
func (s *Server) servePooled(
	w http.ResponseWriter,
	r *http.Request,
	req dispatchRequest,
	routes []routeRuntime,
	forcedCredential string,
) {
	primary := routes[0]
	candidates, err := s.loadPoolCandidates(r.Context(), routes)
	if err != nil {
		s.rejectPool(w, r, req, primary, http.StatusServiceUnavailable, "credentials_unavailable", "Provider credentials could not be loaded.")
		return
	}
	candidates = filterForcedCredential(candidates, forcedCredential)
	if len(candidates) == 0 {
		s.rejectPool(w, r, req, primary, http.StatusServiceUnavailable, "no_credentials", "No healthy credential is configured for this model.")
		return
	}

	state := dispatchState{Replaced: map[string]string{}}
	plans, err := s.buildPoolPlans(r.Context(), req, candidates, state)
	if err != nil {
		var unsupported unsupportedFeatureError
		if errors.As(err, &unsupported) {
			s.rejectPool(w, r, req, primary, http.StatusBadRequest, "unsupported_feature", unsupported.Error())
			return
		}
		s.rejectPool(w, r, req, primary, http.StatusBadRequest, "invalid_request", "Request could not be prepared for any provider.")
		return
	}
	candidates = retainPlannedCandidates(candidates, plans)

	retryContext, cancelRetries := context.WithTimeout(r.Context(), poolRetryTimeout(routes, req.Stream))
	defer cancelRetries()

	clients := newClientCache()
	skipped := map[string]bool{}
	compatibilityRetriesRemaining := 2
	maxAttempts := len(candidates) + compatibilityRetriesRemaining
	attempts := make([]AttemptRecord, 0, maxAttempts)
	decisions := make([]RoutingDecision, 0)

	result := poolResult{
		Route:       primary,
		InputTokens: plans[primary.Model.ID].InputEstimate,
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if len(skipped) >= len(candidates) {
			// Every candidate has been tried. Surface the last upstream failure
			// rather than the limiter's "at capacity" answer.
			break
		}
		selected, reserved, retryAfter, routing, selectErr := s.selectPoolCandidate(
			retryContext, req.Alias, candidates, planTokenCosts(plans), skipped, req.MaxWait,
		)
		decisions = append(decisions, routing...)
		if selectErr != nil {
			result.Status, result.ErrorCode, result.ErrorMessage = http.StatusServiceUnavailable, "limiter_unavailable", "Rate limiter is unavailable."
			s.writePoolError(w, r, req.PublicMode, result.Status, result.ErrorCode, result.ErrorMessage)
			return
		}
		if selected == nil {
			// An out-of-credit pool is not a rate limit: retrying cannot help, so it
			// is reported as a configuration problem rather than as backpressure.
			if balanceBlockedEveryCandidate(decisions) {
				result.Status, result.ErrorCode, result.ErrorMessage = http.StatusServiceUnavailable, "balance_exhausted", "Every API key for this model has spent its balance. Add balance to one of them to resume."
				s.writePoolError(w, r, req.PublicMode, result.Status, result.ErrorCode, result.ErrorMessage)
				s.storePoolLog(r.Context(), req, result, attempts, decisions)
				return
			}
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retryAfter.Seconds())))))
			result.Status, result.ErrorCode, result.ErrorMessage = http.StatusTooManyRequests, "rate_limit_exceeded", "Every provider and credential for this model is at capacity."
			s.writePoolError(w, r, req.PublicMode, result.Status, result.ErrorCode, result.ErrorMessage)
			s.storePoolLog(r.Context(), req, result, attempts, decisions)
			return
		}
		candidate := *selected
		plan := plans[candidate.Route.Model.ID]
		result.Route, result.Credential = candidate.Route, candidate.Credential
		result.InputTokens = plan.InputEstimate
		skipped[candidate.key()] = true
		s.updateActiveRequest(req.RequestID, func(log *RequestLog) {
			log.ProviderName = candidate.Route.Provider.Name
			log.CredentialLabel = candidate.Credential.Label
			log.UpstreamProtocol = plan.Format
		})

		client, clientErr := clients.get(candidate.Route.Provider, req.Stream)
		if clientErr != nil {
			_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
			attempts = append(attempts, AttemptRecord{
				CredentialID: candidate.Credential.ID, CredentialLabel: candidate.Credential.Label,
				Error: "unsafe_provider_url", ErrorMessage: clientErr.Error(), Retryable: true,
			})
			result.Status, result.ErrorCode, result.ErrorMessage = http.StatusBadGateway, "unsafe_provider_url", clientErr.Error()
			continue
		}

		outcome := s.runAttempt(w, r, req, candidate, plan, client, retryContext, reserved, compatibilityRetriesRemaining > 0)
		attempts = append(attempts, outcome.Record)
		if outcome.Status != 0 {
			result.Status = outcome.Status
		}
		if outcome.UpstreamRequestID != "" {
			result.UpstreamRequestID = outcome.UpstreamRequestID
		}
		result.ErrorCode, result.ErrorMessage = outcome.ErrorCode, outcome.ErrorMessage

		if len(outcome.LearnedStrip) > 0 || len(outcome.LearnedReplace) > 0 {
			state.Removed = appendUniqueStrings(state.Removed, outcome.LearnedStrip...)
			for from, to := range outcome.LearnedReplace {
				state.Replaced[from] = to
			}
			rebuilt, rebuildErr := s.buildPoolPlans(r.Context(), req, candidates, state)
			if rebuildErr != nil {
				result.Status, result.ErrorCode, result.ErrorMessage = http.StatusBadRequest, "invalid_request", "Request could not be prepared after compatibility repair."
				s.writePoolError(w, r, req.PublicMode, result.Status, result.ErrorCode, result.ErrorMessage)
				s.storePoolLog(r.Context(), req, result, attempts, decisions)
				return
			}
			plans = rebuilt
			candidates = retainPlannedCandidates(candidates, plans)
		}
		if outcome.Compatibility {
			compatibilityRetriesRemaining--
		}
		if outcome.ResetSkips {
			skipped = map[string]bool{}
		}

		if outcome.Done {
			result.Response, result.Truncated = outcome.ResponseBody, outcome.Truncated
			if outcome.InputTokens > 0 {
				result.InputTokens = outcome.InputTokens
			}
			result.OutputTokens = outcome.OutputTokens
			s.storePoolLog(r.Context(), req, result, attempts, decisions)
			return
		}
		result.Response, result.Truncated = outcome.ResponseBody, outcome.Truncated
		if retryContext.Err() != nil {
			break
		}
	}

	if result.Status == 0 {
		result.Status = http.StatusBadGateway
	}
	s.writePoolError(w, r, req.PublicMode, result.Status,
		valueOr(result.ErrorCode, "upstream_unavailable"),
		valueOr(result.ErrorMessage, "No provider in the pool could serve this request."))
	s.storePoolLog(r.Context(), req, result, attempts, decisions)
}

// poolResult accumulates what the request log needs across attempts.
type poolResult struct {
	Route             routeRuntime
	Credential        credentialRuntime
	Status            int
	ErrorCode         string
	ErrorMessage      string
	Response          []byte
	UpstreamRequestID string
	InputTokens       int64
	OutputTokens      int64
	Truncated         bool
}

func (s *Server) storePoolLog(
	ctx context.Context,
	req dispatchRequest,
	result poolResult,
	attempts []AttemptRecord,
	decisions []RoutingDecision,
) {
	s.storeRequestLog(ctx, logInput{
		RequestID: req.RequestID, Route: result.Route, Credential: result.Credential,
		Endpoint: req.Endpoint, Attempts: attempts, RoutingDecisions: decisions,
		Started: req.Started, StatusCode: result.Status,
		InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
		ErrorCode: result.ErrorCode, ErrorMessage: result.ErrorMessage,
		RequestBody: req.Raw, ResponseBody: result.Response,
		Capture: result.Route.Model.CaptureBodies, Truncated: result.Truncated,
		PublicProtocol:    protocolName(req.PublicMode),
		UpstreamProtocol:  valueOr(result.Route.Provider.APIFormat, "openai"),
		UpstreamRequestID: result.UpstreamRequestID,
	})
}

// copyResponseHeaders forwards the upstream headers the caller's protocol cares
// about. Anthropic callers additionally rely on the rate-limit headers.
func copyResponseHeaders(w http.ResponseWriter, publicMode string, source http.Header) {
	if publicMode == messageModeAnthropic {
		copyAnthropicHeaders(w.Header(), source)
		return
	}
	copyUpstreamHeaders(w.Header(), source)
}

// translateUpstreamResponse converts one provider's non-streaming answer into
// the protocol the caller spoke. Every combination is covered because a pooled
// alias can be served by an Anthropic or an OpenAI provider on any attempt.
func translateUpstreamResponse(req dispatchRequest, plan upstreamPlan, body []byte) ([]byte, int64, int64, error) {
	if plan.Format == "anthropic" {
		switch req.PublicMode {
		case messageModeAnthropic:
			payload, input, output := replaceAnthropicModel(body, req.Alias)
			return payload, input, output, nil
		case messageModeResponses:
			return translateAnthropicResponseToResponses(body, req.Alias)
		default:
			return translateAnthropicResponseToChat(body, req.Alias)
		}
	}
	switch {
	case req.PublicMode == messageModeAnthropic:
		return translateChatResponseToAnthropic(body, req.Alias)
	case plan.Translated:
		return translateChatResponse(body, req.Alias)
	default:
		payload, input, output := replaceResponseModel(body, req.Alias)
		return payload, input, output, nil
	}
}

// runAttempt sends one request to one candidate. It returns without writing
// anything when the failure is worth retrying elsewhere, and reports Done once
// bytes have reached the client.
func (s *Server) runAttempt(
	w http.ResponseWriter,
	r *http.Request,
	req dispatchRequest,
	candidate routeCandidate,
	plan upstreamPlan,
	client *http.Client,
	retryContext context.Context,
	reserved reservation,
	allowCompatibility bool,
) attemptOutcome {
	credential := candidate.Credential
	record := AttemptRecord{CredentialID: credential.ID, CredentialLabel: credential.Label}
	target := strings.TrimRight(candidate.Route.Provider.BaseURL, "/") + plan.Path
	upstreamRequest, buildErr := http.NewRequestWithContext(retryContext, http.MethodPost, target, bytes.NewReader(plan.Encoded))
	if buildErr != nil {
		_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
		record.Error, record.ErrorMessage, record.Retryable = "invalid_upstream_url", buildErr.Error(), true
		return attemptOutcome{Record: record, ErrorCode: record.Error, ErrorMessage: record.ErrorMessage}
	}
	applyProviderHeaders(upstreamRequest, candidate.Route.Provider, credential.Secret)
	if plan.Format == "anthropic" && req.PublicMode == messageModeAnthropic {
		forwardAnthropicHeaders(upstreamRequest.Header, r.Header)
	}
	attemptStarted := time.Now()
	response, requestErr := client.Do(upstreamRequest)
	if requestErr != nil {
		_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
		record.Error = "connection_error"
		record.ErrorMessage = "The upstream connection failed before a response started."
		record.Retryable = true
		record.DurationMS = time.Since(attemptStarted).Milliseconds()
		s.markCredentialFailure(r.Context(), credential.ID, 0, 0)
		return attemptOutcome{
			Record:    record,
			ErrorCode: "upstream_unavailable", ErrorMessage: "The upstream provider could not be reached.",
		}
	}
	record.DurationMS = time.Since(attemptStarted).Milliseconds()
	record.StatusCode = response.StatusCode
	upstreamRequestID := valueOr(response.Header.Get("Request-Id"), response.Header.Get("X-Request-Id"))

	if response.StatusCode == http.StatusBadRequest && allowCompatibility {
		errorBody, wasTruncated, readErr := boundedBody(response.Body, minInt64(s.cfg.MaxResponseBytes, 2<<20))
		_ = response.Body.Close()
		_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
		record.Error = upstreamErrorCode(errorBody)
		record.ErrorMessage = upstreamErrorMessage(errorBody, credential.Secret)
		if readErr == nil && !wasTruncated {
			if repair, ok := unsupportedCompatibilityReplacement(errorBody, plan.Payload); ok {
				record.Retryable = true
				record.ReplacedParameters = map[string]string{repair.From: repair.To}
				s.rememberCompatibilityReplacement(r.Context(), candidate.Route.Model.ID, plan.wireEndpoint(), repair)
				s.logger.Info("learned upstream parameter replacement",
					"request_id", req.RequestID, "model", candidate.Route.Model.PublicAlias,
					"provider", candidate.Route.Provider.Name, "from", repair.From, "to", repair.To)
				return attemptOutcome{
					Record: record, Status: response.StatusCode,
					UpstreamRequestID: upstreamRequestID, Compatibility: true, ResetSkips: true,
					LearnedReplace: map[string]string{repair.From: repair.To},
				}
			}
			if parameters := unsupportedCompatibilityParameters(errorBody, plan.Payload); len(parameters) > 0 {
				record.Retryable = true
				record.RemovedParameters = parameters
				s.rememberCompatibilityParameters(r.Context(), candidate.Route.Model.ID, parameters)
				s.logger.Info("learned unsupported upstream parameters",
					"request_id", req.RequestID, "model", candidate.Route.Model.PublicAlias,
					"provider", candidate.Route.Provider.Name, "parameters", strings.Join(parameters, ","))
				return attemptOutcome{
					Record: record, Status: response.StatusCode,
					UpstreamRequestID: upstreamRequestID, Compatibility: true, ResetSkips: true,
					LearnedStrip: parameters,
				}
			}
		}
		s.markUpstreamFailure(r.Context(), credential.ID, response.StatusCode, response.Header, errorBody)
		return s.writeAttemptFailure(w, r, req, response, errorBody, wasTruncated, record, credential, upstreamRequestID)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, wasTruncated, _ := boundedBody(response.Body, minInt64(s.cfg.MaxResponseBytes, 2<<20))
		_ = response.Body.Close()
		_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
		s.markUpstreamFailure(r.Context(), credential.ID, response.StatusCode, response.Header, body)
		if anthropicRetryableStatus(response.StatusCode) {
			// Nothing has been written yet, so another provider may still serve
			// this request. The caller decides whether budget remains.
			record.Error = upstreamErrorCode(body)
			record.ErrorMessage = upstreamErrorMessage(body, credential.Secret)
			record.Retryable = true
			return attemptOutcome{
				Record: record, Status: response.StatusCode,
				ErrorCode: record.Error, ErrorMessage: record.ErrorMessage,
				ResponseBody: body, Truncated: wasTruncated, UpstreamRequestID: upstreamRequestID,
			}
		}
		return s.writeAttemptFailure(w, r, req, response, body, wasTruncated, record, credential, upstreamRequestID)
	}

	s.markCredentialSuccess(r.Context(), credential.ID)
	if req.Stream {
		return s.streamAttempt(w, r, req, candidate, plan, response, reserved, record, upstreamRequestID)
	}
	body, wasTruncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
	_ = response.Body.Close()
	if readErr != nil || wasTruncated {
		record.Error, record.ErrorMessage = "upstream_response_too_large", "Upstream response exceeded the configured limit."
		s.writePoolError(w, r, req.PublicMode, http.StatusBadGateway, record.Error, record.ErrorMessage)
		return attemptOutcome{
			Done: true, Record: record, Status: http.StatusBadGateway,
			ErrorCode: record.Error, ErrorMessage: record.ErrorMessage, Truncated: wasTruncated,
			UpstreamRequestID: upstreamRequestID,
		}
	}
	translatedBody, inputTokens, outputTokens, translateErr := translateUpstreamResponse(req, plan, body)
	if translateErr != nil {
		record.Error, record.ErrorMessage = "translation_failed", "Upstream response could not be translated."
		s.writePoolError(w, r, req.PublicMode, http.StatusBadGateway, record.Error, record.ErrorMessage)
		return attemptOutcome{
			Done: true, Record: record, Status: http.StatusBadGateway,
			ErrorCode: record.Error, ErrorMessage: record.ErrorMessage, ResponseBody: body,
			UpstreamRequestID: upstreamRequestID,
		}
	}
	if inputTokens+outputTokens > 0 {
		_ = s.limiter.AdjustTokens(r.Context(), reserved, inputTokens+outputTokens)
	}
	copyResponseHeaders(w, req.PublicMode, response.Header)
	s.setCompatibilityHeaders(w, plan)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(translatedBody)
	return attemptOutcome{
		Done: true, Record: record, Status: response.StatusCode,
		ResponseBody: translatedBody, InputTokens: inputTokens, OutputTokens: outputTokens,
		UpstreamRequestID: upstreamRequestID,
	}
}

// setCompatibilityHeaders tells the caller which parameters the gateway had to
// drop or rename to make the request acceptable upstream.
func (s *Server) setCompatibilityHeaders(w http.ResponseWriter, plan upstreamPlan) {
	if len(plan.Removed) > 0 {
		w.Header().Set("X-Rotakey-Removed-Parameters", strings.Join(plan.Removed, ","))
	}
	if len(plan.Replaced) > 0 {
		w.Header().Set("X-Rotakey-Replaced-Parameters", formatCompatibilityReplacements(plan.Replaced))
	}
}

// writeAttemptFailure surfaces an upstream rejection to the caller. The upstream
// body is passed straight through for same-protocol requests and reshaped when
// the caller spoke a different protocol.
func (s *Server) writeAttemptFailure(
	w http.ResponseWriter,
	r *http.Request,
	req dispatchRequest,
	response *http.Response,
	body []byte,
	truncated bool,
	record AttemptRecord,
	credential credentialRuntime,
	upstreamRequestID string,
) attemptOutcome {
	code := upstreamErrorCode(body)
	message := upstreamErrorMessage(body, credential.Secret)
	record.Error, record.ErrorMessage, record.Retryable = code, message, false
	copyResponseHeaders(w, req.PublicMode, response.Header)
	sameProtocol := (req.PublicMode == messageModeAnthropic) == (upstreamProtocolIsAnthropic(response))
	if sameProtocol {
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
	} else {
		s.writePoolError(w, r, req.PublicMode, response.StatusCode, code,
			valueOr(message, "The upstream provider rejected the request."))
	}
	return attemptOutcome{
		Done: true, Record: record, Status: response.StatusCode,
		ErrorCode: code, ErrorMessage: message, ResponseBody: body, Truncated: truncated,
		UpstreamRequestID: upstreamRequestID,
	}
}

// upstreamProtocolIsAnthropic reports whether an error body can be forwarded to
// an Anthropic client untouched, judged from the URL the attempt used.
func upstreamProtocolIsAnthropic(response *http.Response) bool {
	if response.Request == nil || response.Request.URL == nil {
		return false
	}
	return strings.HasSuffix(response.Request.URL.Path, "/messages")
}

// streamAttempt relays a streaming upstream response, translating the event
// stream when the caller's protocol differs from the provider's. It is only
// reached after a 2xx, so headers are committed here and the attempt is final.
func (s *Server) streamAttempt(
	w http.ResponseWriter,
	r *http.Request,
	req dispatchRequest,
	candidate routeCandidate,
	plan upstreamPlan,
	response *http.Response,
	reserved reservation,
	record AttemptRecord,
	upstreamRequestID string,
) attemptOutcome {
	source := io.Reader(response.Body)
	if plan.Format == "anthropic" {
		prepared, retryable, prepareErr := s.prepareAnthropicStreamSource(response)
		if prepareErr != nil {
			_ = response.Body.Close()
			record.Error, record.ErrorMessage, record.Retryable = "upstream_stream_invalid", prepareErr.Error(), retryable
			s.markCredentialFailure(r.Context(), candidate.Credential.ID, http.StatusBadGateway, 0)
			if retryable {
				return attemptOutcome{
					Record: record, Status: http.StatusBadGateway,
					ErrorCode: record.Error, ErrorMessage: record.ErrorMessage, UpstreamRequestID: upstreamRequestID,
				}
			}
			s.writePoolError(w, r, req.PublicMode, http.StatusBadGateway, record.Error, record.ErrorMessage)
			return attemptOutcome{
				Done: true, Record: record, Status: http.StatusBadGateway,
				ErrorCode: record.Error, ErrorMessage: record.ErrorMessage, UpstreamRequestID: upstreamRequestID,
			}
		}
		source = prepared
	}
	defer response.Body.Close()

	copyResponseHeaders(w, req.PublicMode, response.Header)
	s.setCompatibilityHeaders(w, plan)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(response.StatusCode)

	var capture *limitedCapture
	if candidate.Route.Model.CaptureBodies {
		capture = &limitedCapture{limit: s.cfg.CaptureBytes}
	}
	var (
		streamCode          string
		streamErr           error
		inputTokens         int64
		outputTokens        int64
		writeStreamFallback bool
	)
	switch {
	case plan.Format == "anthropic" && req.PublicMode == messageModeAnthropic:
		streamCode, streamErr = rewriteAnthropicStream(source, w, req.Alias, capture)
	case plan.Format == "anthropic" && req.PublicMode == messageModeResponses:
		streamCode, streamErr = translateAnthropicStreamToResponses(source, w, req.Alias, capture)
	case plan.Format == "anthropic":
		stats := &anthropicStreamStats{}
		streamCode, streamErr = translateAnthropicStreamToOpenAI(source, w, req.Alias, capture, req.IncludeOpenAIUsage, stats)
		inputTokens, outputTokens = stats.InputTokens, stats.OutputTokens
	case req.PublicMode == messageModeAnthropic:
		streamCode, streamErr = translateOpenAIStreamToAnthropic(source, w, req.Alias, capture)
	case plan.Translated:
		streamErr = translateChatStream(source, w, req.Alias, capture)
	default:
		var captureWriter io.Writer
		if capture != nil {
			captureWriter = capture
		}
		streamErr = copyStreamingResponse(w, source, captureWriter)
		writeStreamFallback = true
	}

	if streamCode != "" {
		record.Error = streamCode
		record.ErrorMessage = "The upstream provider sent an error after streaming started."
	}
	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		record.Error = "stream_interrupted"
		record.ErrorMessage = "The response stream ended unexpectedly after it started."
		if writeStreamFallback {
			writeStreamFailure(w, capture, req.Endpoint, record.Error, record.ErrorMessage, req.Alias)
		}
	}
	actualInput := inputTokens
	if actualInput == 0 {
		actualInput = plan.InputEstimate
	}
	if actualInput+outputTokens > 0 {
		_ = s.limiter.AdjustTokens(r.Context(), reserved, actualInput+outputTokens)
	}
	outcome := attemptOutcome{
		Done: true, Record: record, Status: response.StatusCode,
		ErrorCode: record.Error, ErrorMessage: record.ErrorMessage,
		InputTokens: plan.InputEstimate, OutputTokens: outputTokens,
		UpstreamRequestID: upstreamRequestID,
	}
	if capture != nil {
		outcome.ResponseBody = append([]byte(nil), capture.Bytes()...)
		outcome.Truncated = capture.truncated
	}
	return outcome
}

// prepareAnthropicStreamSource validates that an Anthropic upstream really sent
// a stream. Some providers answer a streaming request with a single JSON
// Message, which is converted to synthetic SSE rather than failing.
func (s *Server) prepareAnthropicStreamSource(response *http.Response) (io.Reader, bool, error) {
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		prepared, err := prepareAnthropicSSE(response.Body)
		if err != nil {
			return nil, true, errors.New("The upstream returned HTTP 200 without a valid Anthropic SSE event.")
		}
		return prepared, false, nil
	}
	body, truncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
	if readErr != nil || truncated {
		return nil, true, errors.New("The upstream returned an unreadable non-SSE response for a streaming request.")
	}
	synthetic, err := anthropicJSONToSSE(body)
	if err != nil {
		return nil, true, errors.New("The upstream returned HTTP 200 without a valid Anthropic stream or Message response.")
	}
	return bytes.NewReader(synthetic), false, nil
}
