package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// resolveRoutes returns the candidate routes for an alias. Provider-wise mode
// keeps the historical one-route behaviour; model-wise mode pools every
// provider publishing the same public alias.
func (s *Server) resolveUnconfiguredRoutes(ctx context.Context, alias string, mode string) ([]routeRuntime, error) {
	routes, err := s.loadRoutes(ctx, alias)
	if err != nil {
		return nil, err
	}
	if normalizeRoutingMode(mode) != routingModeModel {
		primary := routes[0]
		// An explicit provider alias remains first. Only identical upstream model
		// identifiers qualify as fallback; different models are never substituted.
		rows, queryErr := s.db.Query(ctx, `SELECT `+routeColumns+` FROM model_routes m JOIN providers p ON p.id=m.provider_id WHERE m.upstream_model=$1 AND m.id<>$2 AND `+routeFilter+` ORDER BY m.created_at,m.id`, primary.Model.UpstreamModel, primary.Model.ID)
		if queryErr != nil {
			return []routeRuntime{primary}, nil
		}
		defer rows.Close()
		fallbacks := []routeRuntime{primary}
		for rows.Next() {
			route, scanErr := scanRoute(rows)
			if scanErr == nil {
				route.FallbackOnly = true
				fallbacks = append(fallbacks, route)
			}
		}
		return fallbacks, nil
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

// routeModelIDs lists the model routes in a pool, which is the key the
// compatibility caches are stored under.
func routeModelIDs(routes []routeRuntime) []string {
	ids := make([]string, 0, len(routes))
	for _, route := range routes {
		ids = append(ids, route.Model.ID)
	}
	return ids
}

// sharedProviderName is the provider to name in an error when the whole pool
// belongs to one, and "" when several providers publish the alias. Naming a
// single provider tells the operator where to look; naming one of many would be
// misleading, so the message falls back to "for this model".
func sharedProviderName(routes []routeRuntime) string {
	if len(routes) == 0 {
		return ""
	}
	name := routes[0].Provider.Name
	for _, route := range routes[1:] {
		if route.Provider.Name != name {
			return ""
		}
	}
	return name
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
	// The authenticated inference body has been parsed. Keep the body-transfer
	// deadline from turning the connection's disconnect watcher into a queue cap.
	_ = http.NewResponseController(w).SetReadDeadline(time.Time{})
	primary := routes[0]
	req.DeferFailures = true
	candidates, err := s.loadPoolCandidates(r.Context(), routes)
	if err != nil {
		s.rejectPool(w, r, req, primary, http.StatusServiceUnavailable, "credentials_unavailable", "Provider credentials could not be loaded.")
		return
	}
	candidates = filterForcedCredential(candidates, forcedCredential)
	if len(candidates) == 0 {
		// Quarantined keys now reach the selection ladder, so this fires only when
		// the pool genuinely holds no enabled key at all. Saying so is more useful
		// than "no healthy credential", which read like a transient fault.
		s.rejectPool(w, r, req, primary, http.StatusServiceUnavailable, "no_credentials", "This provider has no enabled API key.")
		return
	}

	// Both learned endpoint facts are loaded for every public protocol. A provider
	// that answered 404 at /responses within the last day is not asked again, and
	// one that rejected Chat Completions by naming /responses starts there instead
	// of paying the same 400 over again. Loading the 404 unconditionally is what
	// keeps the two from trading a request back and forth across requests.
	modelIDs := routeModelIDs(routes)
	state := dispatchState{
		Scoped:                     map[string]compatibilityScope{},
		Replaced:                   map[string]string{},
		MinOutputTokens:            map[string]int64{},
		DetachReplayedIDs:          map[string]bool{},
		NativeResponsesUnavailable: s.responsesEndpointMissing(r.Context(), modelIDs),
		PreferNativeResponses:      s.responsesEndpointPreferred(r.Context(), modelIDs),
	}
	plans, err := s.buildPoolPlans(r.Context(), req, candidates, state)
	if err != nil {
		// Only a request with nothing left to send reaches this point now that
		// unsupported fields are dropped instead of refused, so the reason is
		// reported verbatim whichever protocol raised it.
		var unsupported unsupportedFeatureError
		if errors.As(err, &unsupported) {
			s.rejectPool(w, r, req, primary, http.StatusBadRequest, "unsupported_feature", unsupported.Error())
			return
		}
		var crossProtocol anthropicUnsupportedError
		if errors.As(err, &crossProtocol) {
			s.rejectPool(w, r, req, primary, http.StatusBadRequest, "unsupported_feature", crossProtocol.Error())
			return
		}
		s.rejectPool(w, r, req, primary, http.StatusBadRequest, "invalid_request", "Request could not be prepared for any provider.")
		return
	}
	candidates = retainPlannedCandidates(candidates, plans)

	retryContext, cancelRetries := context.WithCancel(r.Context())
	defer cancelRetries()

	clients := newClientCache()
	recovery := s.newRepairSession(r.Context(), req, routes)
	defer recovery.finish()
	skipped := map[string]bool{}
	repairCandidateKey := ""
	waitingProviderID := ""
	compatibilityRetriesRemaining := 2
	maxAttempts := min(12, len(candidates)+compatibilityRetriesRemaining+3)
	attempts := make([]AttemptRecord, 0, maxAttempts)
	decisions := make([]RoutingDecision, 0)

	result := poolResult{
		Route:       primary,
		InputTokens: plans[primary.Model.ID].InputEstimate,
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt+recovery.extraCalls >= 12 {
			break
		}
		recovery.targetCalls = attempt + 1
		recovery.refreshPolicy(retryContext)
		if len(skipped) >= len(candidates) {
			// Every candidate has been tried. Surface the last upstream failure
			// rather than the limiter's "at capacity" answer.
			break
		}
		preparedPlans := map[string]upstreamPlan{}
		for i := range candidates {
			candidates[i].repairPreferred = candidates[i].key() == repairCandidateKey
		}
		repairCandidateKey = ""
		for _, c := range candidates {
			preparedPlans[c.Route.Model.ID] = recovery.prepare(c.Route, plans[c.Route.Model.ID])
		}
		selected, reserved, routing, selectErr := s.selectQueuedCandidate(retryContext, req, candidates, planTokenCosts(preparedPlans), skipped, forcedCredential, waitingProviderID)
		waitingProviderID = ""
		decisions = append(decisions, routing...)
		if selectErr != nil {
			if errors.Is(selectErr, context.Canceled) || errors.Is(selectErr, context.DeadlineExceeded) {
				if len(attempts) == 0 {
					result.InputTokens = 0
					result.OutputTokens = 0
				}
				result.Status = 499
				result.ErrorCode = "cancelled"
				result.ErrorMessage = "Client cancelled while waiting for capacity."
				s.storePoolLog(r.Context(), req, result, attempts, decisions)
				return
			}
			result.Status, result.ErrorCode, result.ErrorMessage = http.StatusServiceUnavailable, "limiter_unavailable", "Rate limiter is unavailable."
			s.writePoolError(w, r, req.PublicMode, result.Status, result.ErrorCode, result.ErrorMessage)
			s.storePoolLog(r.Context(), req, result, attempts, decisions)
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
			// A pool that was rejected by the provider or switched off is the same
			// kind of answer: the alias exists and is configured, but no key behind
			// it can serve, and only the operator can change that. This is what the
			// alias disappearing from /v1/models used to hide behind a 404.
			if message, terminal := unavailablePoolMessage(soleBlockingReason(decisions), sharedProviderName(routes)); terminal {
				result.Status, result.ErrorCode, result.ErrorMessage = http.StatusServiceUnavailable, "no_healthy_credential", message
				s.writePoolError(w, r, req.PublicMode, result.Status, result.ErrorCode, result.ErrorMessage)
				s.storePoolLog(r.Context(), req, result, attempts, decisions)
				return
			}
			result.Status, result.ErrorCode, result.ErrorMessage = http.StatusServiceUnavailable, "no_usable_provider", "No configured provider can serve this request."
			for _, d := range routing {
				if d.Reason == "request_exceeds_limit" {
					result.Status = 400
					result.ErrorCode = "request_exceeds_limit"
					result.ErrorMessage = "Request exceeds every eligible key's per-request token capacity."
				}
			}
			s.writePoolError(w, r, req.PublicMode, result.Status, result.ErrorCode, result.ErrorMessage)
			s.storePoolLog(r.Context(), req, result, attempts, decisions)
			return
		}
		candidate := *selected
		plan := preparedPlans[candidate.Route.Model.ID]
		if timeout := recovery.timeouts[candidate.Route.Provider.ID]; timeout > 0 {
			candidate.Route.Provider.TimeoutSeconds = timeout
		}
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
			reserved.lease.release()
			_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
			attempts = append(attempts, AttemptRecord{
				CredentialID: candidate.Credential.ID, CredentialLabel: candidate.Credential.Label,
				Error: "unsafe_provider_url", ErrorMessage: clientErr.Error(), Retryable: true,
			})
			result.Status, result.ErrorCode, result.ErrorMessage = http.StatusBadGateway, "unsafe_provider_url", clientErr.Error()
			continue
		}

		attemptCtx, cancelAttempt := context.WithTimeout(retryContext, providerRetryTimeout(candidate.Route.Provider, req.Stream))
		attemptCtx = context.WithValue(attemptCtx, admissionHeldKey{}, true)
		stopLeaseWatch := make(chan struct{})
		go func() {
			select {
			case <-reserved.lease.lost:
				cancelAttempt()
			case <-stopLeaseWatch:
			}
		}()
		var timedOut bool
		outcome := func() attemptOutcome {
			defer func() {
				timedOut = errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
				close(stopLeaseWatch)
				cancelAttempt()
				reserved.lease.release()
			}()
			return s.runAttempt(w, r, req, candidate, plan, client, attemptCtx, reserved, compatibilityRetriesRemaining > 0 && recovery.tests < 3)
		}()
		if timedOut && !outcome.ResponseComplete {
			if !outcome.Done {
				outcome.Status = 504
			}
			outcome.ErrorCode = "timeout"
			outcome.ErrorMessage = "The upstream call timed out after dispatch."
			outcome.Record.Error = "timeout"
			outcome.Record.ErrorMessage = outcome.ErrorMessage
		}
		if r.Context().Err() != nil && !outcome.ResponseComplete {
			if !outcome.Done {
				outcome.Status = 499
			}
			outcome.ErrorCode = "cancelled"
			outcome.ErrorMessage = "Client disconnected or cancelled before the full response was delivered. Partial output may have been sent."
			outcome.Done = true
		}
		if !outcome.Done && (outcome.Status == 429 || outcome.Record.Error == "rate_limit_exceeded") {
			waitingProviderID = candidate.Route.Provider.ID
			delete(skipped, candidate.key())
			attempt--
			continue
		}
		configuredFailover := false
		if !outcome.Done && !outcome.Compatibility && outcome.Status != http.StatusOK {
			configuredFailover = skipFailedPrimary(candidates, candidate, skipped)
		}
		if !configuredFailover && recovery.handle(retryContext, &candidate, plan, &outcome, clients) {
			delete(skipped, candidate.key())
			repairCandidateKey = candidate.key()
			if credentialID := recovery.selectedCredentials[candidate.Route.Model.ID]; credentialID != "" {
				for _, c := range candidates {
					if c.Route.Model.ID == candidate.Route.Model.ID {
						if c.Credential.ID == credentialID {
							delete(skipped, c.key())
							repairCandidateKey = c.key()
						} else {
							skipped[c.key()] = true
						}
					}
				}
			}
		}
		if !outcome.Done && !outcome.Compatibility && (outcome.Status == 400 || outcome.Status == 422) && !outcome.Record.RecoveryRetry {
			for _, other := range candidates {
				if other.Route.Model.ID == candidate.Route.Model.ID {
					skipped[other.key()] = true
				}
			}
		}
		attempts = append(attempts, outcome.Record)
		if outcome.Status != 0 {
			result.Status = outcome.Status
		}
		if outcome.UpstreamRequestID != "" {
			result.UpstreamRequestID = outcome.UpstreamRequestID
		}
		result.ErrorCode, result.ErrorMessage = outcome.ErrorCode, outcome.ErrorMessage

		if len(outcome.LearnedStrip) > 0 || len(outcome.LearnedReplace) > 0 ||
			outcome.LearnedItemStrip.Field != "" ||
			outcome.LearnedReplyFloor > 0 || outcome.LearnedDetachIDs ||
			outcome.NativeResponsesMissing || outcome.NativeResponsesPreferred {
			state.Removed = appendUniqueStrings(state.Removed, outcome.LearnedStrip...)
			for from, to := range outcome.LearnedReplace {
				state.Replaced[from] = to
			}
			if outcome.LearnedItemStrip.Field != "" && !slices.Contains(state.RemovedItemFields, outcome.LearnedItemStrip) {
				state.RemovedItemFields = append(state.RemovedItemFields, outcome.LearnedItemStrip)
			}
			scope := state.Scoped[candidate.Route.Model.ID]
			scope.Removed = appendUniqueStrings(scope.Removed, outcome.LearnedStrip...)
			if scope.Replaced == nil {
				scope.Replaced = map[string]string{}
			}
			for from, to := range outcome.LearnedReplace {
				scope.Replaced[from] = to
			}
			if outcome.LearnedItemStrip.Field != "" {
				scope.Items = append(scope.Items, outcome.LearnedItemStrip)
			}
			state.Scoped[candidate.Route.Model.ID] = scope
			if outcome.LearnedReplyFloor > state.MinOutputTokens[candidate.Route.Model.ID] {
				state.MinOutputTokens[candidate.Route.Model.ID] = outcome.LearnedReplyFloor
			}
			if outcome.LearnedDetachIDs {
				state.DetachReplayedIDs[candidate.Route.Model.ID] = true
			}
			if outcome.NativeResponsesMissing {
				state.NativeResponsesUnavailable[candidate.Route.Model.ID] = true
				// A 404 seen at /responses is worth more than a preference inferred
				// from prose, so it also retires the preference. Without this the two
				// signals would send the next request back to the endpoint that has
				// already proved absent.
				delete(state.PreferNativeResponses, candidate.Route.Model.ID)
				s.forgetResponsesEndpointPreferred(r.Context(), candidate.Route.Model.ID)
				s.rememberResponsesEndpointMissing(r.Context(), candidate.Route.Model.ID)
				s.logger.Info("provider has no Responses endpoint; translating to Chat Completions",
					"request_id", req.RequestID, "model", candidate.Route.Model.PublicAlias,
					"provider", candidate.Route.Provider.Name)
			}
			// Believed for the rest of this request, but not written down yet. The
			// provider naming /responses proves only that Chat was refused; whether
			// the gateway can actually build a Responses request this provider
			// accepts is proved by the next attempt, and that is where the
			// preference is stored. Caching it here once cost a day of requests
			// that failed on their first attempt with nothing in the log to say
			// why the endpoint had changed.
			if outcome.NativeResponsesPreferred && !state.NativeResponsesUnavailable[candidate.Route.Model.ID] {
				state.PreferNativeResponses[candidate.Route.Model.ID] = true
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
			delete(skipped, candidate.key())
			repairCandidateKey = candidate.key()
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
// the protocol the caller spoke. The upstream's own shape is read from the wire
// endpoint rather than from a translated flag, because a route may now be sent
// to /responses from any of the three public protocols.
func translateUpstreamResponse(req dispatchRequest, plan upstreamPlan, body []byte) ([]byte, int64, int64, error) {
	if req.PublicMode == messageModeEmbeddings {
		payload, input, output := replaceResponseModel(body, req.Alias)
		return payload, input, output, nil
	}
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
	if plan.wireEndpoint() == "responses" {
		switch req.PublicMode {
		case messageModeAnthropic:
			return translateResponsesResponseToAnthropic(body, req.Alias)
		case messageModeResponses:
			payload, input, output := replaceResponseModel(body, req.Alias)
			return payload, input, output, nil
		default:
			return translateResponsesResponseToChat(body, req.Alias)
		}
	}
	switch req.PublicMode {
	case messageModeAnthropic:
		return translateChatResponseToAnthropic(body, req.Alias)
	case messageModeResponses:
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

	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnprocessableEntity {
		errorBody, wasTruncated, readErr := boundedBody(response.Body, minInt64(s.cfg.MaxResponseBytes, 2<<20))
		_ = response.Body.Close()
		_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
		record.Error = upstreamErrorCode(errorBody)
		record.ErrorMessage = upstreamErrorMessage(errorBody, credential.Secret)
		// The signals are read even when the compatibility budget is spent. A 400
		// or 422 validation response the gateway can name is a fault in the
		// request's shape rather than in the key, and the strike further down
		// would otherwise punish a healthy
		// credential for a rejection no rotation can avoid.
		var (
			switchEndpoint bool
			replacement    compatibilityReplacement
			hasReplacement bool
			parameters     []string
			itemStrip      itemFieldStrip
			hasItemStrip   bool
			hasDetach      bool
		)
		if readErr == nil && !wasTruncated {
			// A provider that names /responses in its rejection is answered by moving
			// the request there, before any parameter is considered for removal:
			// switching endpoint keeps the tools the caller asked for, while stripping
			// the field the provider blamed would silently drop them.
			switchEndpoint = plan.Path == "/chat/completions" && !plan.ResponsesUnavailable &&
				errorDemandsResponsesEndpoint(errorBody)
			if !switchEndpoint {
				if replacement, hasReplacement = unsupportedCompatibilityReplacement(errorBody, plan.Payload); !hasReplacement {
					parameters = unsupportedCompatibilityParameters(errorBody, plan.Payload)
				}
			}
			// Last, because the two passes above answer a parameter the gateway or
			// the operator put on the request, and this one answers a field the
			// caller's client hung off a turn. The names cannot collide: a path with
			// an index in it is never an allowlisted top-level parameter.
			if !switchEndpoint && !hasReplacement && len(parameters) == 0 {
				itemStrip, hasItemStrip = unsupportedItemField(errorBody, plan.Payload)
			}
			if !switchEndpoint && !hasReplacement && len(parameters) == 0 && !hasItemStrip {
				hasDetach = orphanedReasoningPairing(errorBody, plan.Payload)
			}
		}
		if allowCompatibility {
			switch {
			case switchEndpoint:
				record.Error = "responses_endpoint_preferred"
				record.ErrorMessage = "Provider requires the Responses endpoint for this request; retried at /responses."
				record.Retryable = true
				record.SwitchedEndpoint = "responses"
				s.logger.Info("provider directed this request to the Responses endpoint",
					"request_id", req.RequestID, "model", candidate.Route.Model.PublicAlias,
					"provider", candidate.Route.Provider.Name)
				return attemptOutcome{
					Record: record, Status: response.StatusCode,
					UpstreamRequestID: upstreamRequestID, Compatibility: true, ResetSkips: true,
					NativeResponsesPreferred: true,
				}
			case hasReplacement:
				record.Retryable = true
				record.ReplacedParameters = map[string]string{replacement.From: replacement.To}
				if !req.DeferFailures {
					s.rememberCompatibilityReplacement(r.Context(), candidate.Route.Model.ID, plan.wireEndpoint(), replacement)
				}
				s.logger.Info("learned upstream parameter replacement",
					"request_id", req.RequestID, "model", candidate.Route.Model.PublicAlias,
					"provider", candidate.Route.Provider.Name, "from", replacement.From, "to", replacement.To)
				return attemptOutcome{
					Record: record, Status: response.StatusCode,
					UpstreamRequestID: upstreamRequestID, Compatibility: true, ResetSkips: true,
					LearnedReplace: map[string]string{replacement.From: replacement.To},
				}
			case len(parameters) > 0:
				record.Retryable = true
				record.RemovedParameters = parameters
				if !req.DeferFailures {
					s.rememberCompatibilityParameters(r.Context(), candidate.Route.Model.ID, parameters)
				}
				s.logger.Info("learned unsupported upstream parameters",
					"request_id", req.RequestID, "model", candidate.Route.Model.PublicAlias,
					"provider", candidate.Route.Provider.Name, "parameters", strings.Join(parameters, ","))
				return attemptOutcome{
					Record: record, Status: response.StatusCode,
					UpstreamRequestID: upstreamRequestID, Compatibility: true, ResetSkips: true,
					LearnedStrip: parameters,
				}
			case hasItemStrip:
				record.Retryable = true
				record.RemovedParameters = []string{itemStrip.String()}
				if !req.DeferFailures {
					s.rememberItemFieldStrip(r.Context(), candidate.Route.Model.ID, itemStrip)
				}
				s.logger.Info("learned unsupported field inside the caller's turns",
					"request_id", req.RequestID, "model", candidate.Route.Model.PublicAlias,
					"provider", candidate.Route.Provider.Name, "field", itemStrip.String())
				return attemptOutcome{
					Record: record, Status: response.StatusCode,
					UpstreamRequestID: upstreamRequestID, Compatibility: true, ResetSkips: true,
					LearnedItemStrip: itemStrip,
				}
			case hasDetach:
				record.Retryable = true
				record.RemovedParameters = []string{"input[].id"}
				if !req.DeferFailures {
					s.rememberDetachReplayedIDs(r.Context(), candidate.Route.Model.ID)
				}
				s.logger.Info("provider refused a replayed message id's pairing; retrying without the ids",
					"request_id", req.RequestID, "model", candidate.Route.Model.PublicAlias,
					"provider", candidate.Route.Provider.Name)
				return attemptOutcome{
					Record: record, Status: response.StatusCode,
					UpstreamRequestID: upstreamRequestID, Compatibility: true, ResetSkips: true,
					LearnedDetachIDs: true,
				}
			}
		}
		// A 400 the gateway can name is already excluded above. So is a 400 on an
		// endpoint the gateway chose for itself: the key had no part in that
		// decision and every other key in the pool would be rejected identically,
		// so striking this one only shrinks the rotation over the gateway's own
		// mistake. A route that publishes /responses natively is not covered —
		// there the configuration is the operator's and the 400 is real evidence.
		if response.StatusCode != http.StatusUnprocessableEntity && !req.DeferFailures && !switchEndpoint && !hasReplacement && len(parameters) == 0 && !hasItemStrip && !hasDetach && !plan.SwitchedToResponses {
			s.markUpstreamFailure(r.Context(), credential.ID, response.StatusCode, response.Header, errorBody)
		}
		return s.writeAttemptFailure(w, r, req, plan, response, errorBody, wasTruncated, record, credential, upstreamRequestID)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, wasTruncated, _ := boundedBody(response.Body, minInt64(s.cfg.MaxResponseBytes, 2<<20))
		_ = response.Body.Close()
		_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
		// A 404 on /responses means the provider does not serve that endpoint at
		// all: an OpenAI-compatible host that only implements Chat Completions
		// answers this way for every model, so the route's own configuration is
		// what is wrong, not the key or the model. Retry the same candidate with
		// the Chat translation instead of burning the pool on a path that cannot
		// exist. The credential is left untouched because it never got to fail.
		if response.StatusCode == http.StatusNotFound && plan.Path == "/responses" && allowCompatibility {
			record.Error = "responses_endpoint_missing"
			record.ErrorMessage = "Provider has no Responses endpoint; retried as Chat Completions."
			record.Retryable = true
			return attemptOutcome{
				Record: record, Status: response.StatusCode,
				UpstreamRequestID: upstreamRequestID, Compatibility: true, ResetSkips: true,
				NativeResponsesMissing: true,
			}
		}
		if response.StatusCode != http.StatusUnprocessableEntity {
			s.markUpstreamFailure(r.Context(), credential.ID, response.StatusCode, response.Header, body)
		}
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
		return s.writeAttemptFailure(w, r, req, plan, response, body, wasTruncated, record, credential, upstreamRequestID)
	}

	s.markCredentialSuccess(r.Context(), credential.ID)
	// The endpoint switch is now proved rather than inferred, so later requests
	// for this model may start at /responses. Recorded here and nowhere else: a
	// switch that ends in a 400 teaches nothing beyond this request.
	if plan.SwitchedToResponses && !req.DeferFailures {
		s.rememberResponsesEndpointPreferred(r.Context(), candidate.Route.Model.ID)
	}
	// Same proved-not-inferred rule for the reply budget: the raised cap is
	// remembered only on the attempt that actually answered under it.
	if plan.ReplyFloor > 0 && !req.DeferFailures {
		s.rememberReplyFloor(r.Context(), candidate.Route.Model.ID, plan.ReplyFloor)
	}
	if req.Stream {
		return s.streamAttempt(w, r, req, candidate, plan, response, reserved, record, upstreamRequestID)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, s.cfg.MaxResponseBytes))
	if readErr != nil || int64(len(body)) >= s.cfg.MaxResponseBytes {
		return s.relaySuccessfulBody(w, r, req, plan, response, io.MultiReader(bytes.NewReader(body), response.Body), reserved, record, upstreamRequestID)
	}
	// Provider success is authoritative. Usage or visible-output validation must
	// never manufacture a failure or a duplicate paid request from a 2xx reply.
	translatedBody, inputTokens, outputTokens := successfulUpstreamBody(req, plan, body)
	if inputTokens+outputTokens > 0 {
		_ = s.limiter.AdjustTokens(r.Context(), reserved, inputTokens+outputTokens)
	}
	copyResponseHeaders(w, req.PublicMode, response.Header)
	s.setCompatibilityHeaders(w, plan)
	w.Header().Set("Content-Type", "application/json")
	if bytes.Equal(body, translatedBody) && response.Header.Get("Content-Type") != "" {
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	}
	w.WriteHeader(response.StatusCode)
	written, writeErr := w.Write(translatedBody)
	return attemptOutcome{
		Done: true, ResponseComplete: writeErr == nil && written == len(translatedBody), Record: record, Status: response.StatusCode,
		ResponseBody: translatedBody, InputTokens: inputTokens, OutputTokens: outputTokens,
		UpstreamRequestID: upstreamRequestID,
	}
}

// setCompatibilityHeaders tells the caller which parameters the gateway had to
// drop or rename to make the request acceptable upstream, and whether it had to
// send the request to a different endpoint than the route publishes.
func (s *Server) setCompatibilityHeaders(w http.ResponseWriter, plan upstreamPlan) {
	if plan.Recovered {
		w.Header().Set("X-Rotakey-Recovery", "applied")
	}
	if len(plan.Removed) > 0 {
		w.Header().Set("X-Rotakey-Removed-Parameters", strings.Join(plan.Removed, ","))
	}
	if len(plan.Replaced) > 0 {
		w.Header().Set("X-Rotakey-Replaced-Parameters", formatCompatibilityReplacements(plan.Replaced))
	}
	if plan.SwitchedToResponses {
		w.Header().Set("X-Rotakey-Switched-Endpoint", "responses")
	}
}

// writeAttemptFailure surfaces an upstream rejection to the caller. The upstream
// body is passed straight through for same-protocol requests and reshaped when
// the caller spoke a different protocol.
func (s *Server) writeAttemptFailure(
	w http.ResponseWriter,
	r *http.Request,
	req dispatchRequest,
	plan upstreamPlan,
	response *http.Response,
	body []byte,
	truncated bool,
	record AttemptRecord,
	credential credentialRuntime,
	upstreamRequestID string,
) attemptOutcome {
	code := upstreamErrorCode(body)
	message := upstreamFailureMessage(response.StatusCode, plan.Path, upstreamErrorMessage(body, credential.Secret))
	record.Error, record.ErrorMessage, record.Retryable = code, message, false
	if req.DeferFailures {
		record.Retryable = true
		return attemptOutcome{Record: record, Status: response.StatusCode, ErrorCode: code, ErrorMessage: message, ResponseBody: body, Truncated: truncated, UpstreamRequestID: upstreamRequestID}
	}
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

// upstreamFailureMessage keeps the provider's own words when it sent any, and
// otherwise says what the status means for this endpoint. A bare "404 page not
// found" is the common answer from an OpenAI-compatible host that does not serve
// the path the route asked for, and it carries no JSON message to forward, so
// without this the console can only report "upstream_error".
func upstreamFailureMessage(status int, path, message string) string {
	if message != "" {
		return message
	}
	if status != http.StatusNotFound {
		return ""
	}
	switch path {
	case "/embeddings":
		return "The provider has no Embeddings endpoint at this base URL, or this is not an embedding model. Check the route type, upstream model ID and provider base URL."
	case "/responses":
		return "The provider has no Responses endpoint at this base URL. Turn off \"Upstream supports Responses natively\" for this route, or correct the provider base URL."
	case "/messages":
		return "The provider has no Messages endpoint at this base URL. Check the provider base URL and API format."
	default:
		return "The provider does not serve this model at " + path + ". Check the upstream model ID and the provider base URL."
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
	defer response.Body.Close()
	// A streaming request may receive a JSON reply. Preserve that reply and
	// status instead of insisting on SSE and manufacturing a 502.
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, s.cfg.MaxResponseBytes))
		if readErr == nil && int64(len(body)) < s.cfg.MaxResponseBytes {
			body, input, output := successfulUpstreamBody(req, plan, body)
			if req.PublicMode == messageModeAnthropic {
				if synthetic, err := anthropicJSONToSSE(body); err == nil {
					cloned := new(http.Response)
					*cloned = *response
					cloned.Header = response.Header.Clone()
					cloned.Header.Set("Content-Type", "text/event-stream")
					cloned.Body = io.NopCloser(bytes.NewReader(synthetic))
					return s.streamAttempt(w, r, req, candidate, plan, cloned, reserved, record, upstreamRequestID)
				}
			}
			copyResponseHeaders(w, req.PublicMode, response.Header)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(response.StatusCode)
			written, writeErr := w.Write(body)
			_ = s.limiter.AdjustTokens(r.Context(), reserved, input+output)
			return attemptOutcome{Done: true, ResponseComplete: writeErr == nil && written == len(body), Record: record, Status: response.StatusCode, ResponseBody: body, InputTokens: input, OutputTokens: output, UpstreamRequestID: upstreamRequestID}
		}
		return s.relaySuccessfulBody(w, r, req, plan, response, io.MultiReader(bytes.NewReader(body), response.Body), reserved, record, upstreamRequestID)
	}
	source, actual := successfulStreamSource(source)
	if actual == messageModeAnthropic {
		plan.Format = "anthropic"
	} else if actual != "" {
		plan.Format = "openai"
		plan.Path = "/chat/completions"
		if actual == messageModeResponses {
			plan.Path = "/responses"
		}
	}

	evidence := &streamEvidenceReader{source: source}
	source = evidence
	delivery := &streamCompletionWriter{ResponseWriter: w}
	w = delivery
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
	case actual == "" || actual == req.PublicMode:
		var captureWriter io.Writer
		if capture != nil {
			captureWriter = capture
		}
		streamErr = relaySuccessfulStream(w, source, captureWriter)
	case plan.Format == "anthropic" && req.PublicMode == messageModeResponses:
		streamCode, streamErr = translateAnthropicStreamToResponses(source, w, req.Alias, capture)
	case plan.Format == "anthropic":
		stats := &anthropicStreamStats{}
		streamCode, streamErr = translateAnthropicStreamToOpenAI(source, w, req.Alias, capture, req.IncludeOpenAIUsage, stats)
		inputTokens, outputTokens = stats.InputTokens, stats.OutputTokens
	case plan.wireEndpoint() == "responses" && req.PublicMode == messageModeAnthropic:
		streamCode, streamErr = translateResponsesStreamToAnthropic(source, w, req.Alias, capture)
	case plan.wireEndpoint() == "responses" && req.PublicMode == messageModeChat:
		streamCode, streamErr = translateResponsesStreamToChat(source, w, req.Alias, capture, req.IncludeOpenAIUsage)
	case req.PublicMode == messageModeAnthropic:
		streamCode, streamErr = translateOpenAIStreamToAnthropic(source, w, req.Alias, capture)
	case req.PublicMode == messageModeResponses && plan.wireEndpoint() != "responses":
		streamErr = translateChatStream(source, w, req.Alias, capture)
	default:
		var captureWriter io.Writer
		if capture != nil {
			captureWriter = capture
		}
		streamErr = copyStreamingResponse(w, source, captureWriter)
		writeStreamFallback = true
	}

	if evidence != nil {
		if evidence.hasUsage {
			inputTokens, outputTokens = evidence.input, evidence.output
		}
		if streamCode == "" {
			streamCode = evidence.errorCode
		}
	}
	if streamCode != "" {
		record.Error = streamCode
		record.ErrorMessage = "The upstream provider sent an error after streaming started."
	}
	responseComplete := !delivery.writeFailed && (streamErr == nil || delivery.terminalSent)
	if streamErr != nil && !responseComplete && !errors.Is(streamErr, context.Canceled) {
		record.Error = "stream_interrupted"
		record.ErrorMessage = "The response stream ended unexpectedly after it started."
		if writeStreamFallback {
			writeStreamFailure(w, capture, req.Endpoint, record.Error, record.ErrorMessage, req.Alias)
		}
	}
	// A stream that reported its own input count is the only measurement anyone
	// has; the estimate is what the tokenizer guessed before the request left.
	// The limiter was already being corrected with the real number while the log
	// kept the guess, so the same value now reaches both.
	actualInput := inputTokens
	if actualInput == 0 {
		actualInput = plan.InputEstimate
	}
	if actualInput+outputTokens > 0 {
		_ = s.limiter.AdjustTokens(r.Context(), reserved, actualInput+outputTokens)
	}
	outcome := attemptOutcome{
		Done: true, ResponseComplete: responseComplete, Record: record, Status: response.StatusCode,
		ErrorCode: record.Error, ErrorMessage: record.ErrorMessage,
		InputTokens: actualInput, OutputTokens: outputTokens,
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
