package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

type compatibilityScope struct {
	Removed  []string
	Replaced map[string]string
	Items    []itemFieldStrip
}

type repairSession struct {
	server              *Server
	ctx                 context.Context
	request             dispatchRequest
	policy              RepairPolicy
	rounds, tests       int
	diagnosisTime       time.Duration
	incidents           map[string]*RepairIncident
	proposals           map[string][]RepairProposal
	fingerprints        map[string]bool
	timeouts            map[string]int
	selectedCredentials map[string]string
	endpoints           map[string]string
	originalRoutes      map[string]routeRuntime
	pendingRoutes       map[string]bool
	compatibility       map[string][]attemptOutcome
	finalStatus         int
	backgroundEvidence  map[string][]byte
	extraCalls          int
	targetCalls         int
}

func (s *Server) newRepairSession(ctx context.Context, req dispatchRequest, routes []routeRuntime) *repairSession {
	originals := map[string]routeRuntime{}
	for _, route := range routes {
		originals[route.Model.ID] = route
	}
	return &repairSession{server: s, ctx: ctx, request: req, policy: s.repairPolicy(ctx), incidents: map[string]*RepairIncident{}, proposals: map[string][]RepairProposal{}, fingerprints: map[string]bool{}, timeouts: map[string]int{}, selectedCredentials: map[string]string{}, endpoints: map[string]string{}, originalRoutes: originals, pendingRoutes: map[string]bool{}, compatibility: map[string][]attemptOutcome{}, backgroundEvidence: map[string][]byte{}}
}

func (rs *repairSession) enabled(route routeRuntime) bool {
	return rs.policy.Enabled && (len(rs.policy.RouteIDs) == 0 || slices.Contains(rs.policy.RouteIDs, route.Model.ID))
}

func (rs *repairSession) refreshPolicy(ctx context.Context) {
	latest := rs.server.repairPolicy(ctx)
	if latest.Version != rs.policy.Version || !latest.Enabled {
		rs.proposals = map[string][]RepairProposal{}
		rs.timeouts = map[string]int{}
		rs.endpoints = map[string]string{}
		rs.selectedCredentials = map[string]string{}
	}
	rs.policy = latest
}

func (rs *repairSession) prepare(route routeRuntime, plan upstreamPlan) upstreamPlan {
	if !rs.enabled(route) || rs.policy.Mode == "observe" {
		return plan
	}
	if endpoint := rs.endpoints[route.Model.ID]; endpoint != "" {
		state := dispatchState{PreferNativeResponses: map[string]bool{route.Model.ID: endpoint == "responses"}, NativeResponsesUnavailable: map[string]bool{route.Model.ID: endpoint == "chat"}}
		if rebuilt, err := rs.server.buildPlan(rs.ctx, rs.request, route, state); err == nil {
			plan = rebuilt
		}
	}
	payload := cloneMap(plan.Payload)
	proposals := append([]RepairProposal{}, rs.server.learnedRepairs(rs.ctx, route, plan.wireEndpoint())...)
	proposals = append(proposals, rs.proposals[route.Model.ID+":"+plan.wireEndpoint()]...)
	for _, p := range proposals {
		current, _ := json.Marshal(payload[p.Parameter])
		if len(p.MatchValue) > 0 && string(current) != string(p.MatchValue) {
			continue
		}
		if rs.policy.permits(p.Action) && validateRepairProposal(p, payload) == nil {
			plan.Recovered = applyRepairProposal(payload, p) || plan.Recovered
		}
	}
	plan.Payload = payload
	plan.Encoded, _ = json.Marshal(payload)
	plan.TokenCost = plan.InputEstimate + currentOutputCap(payload, plan.Format, plan.wireEndpoint())
	return plan
}

func (rs *repairSession) handle(ctx context.Context, candidate *routeCandidate, plan upstreamPlan, outcome *attemptOutcome, clients *clientCache) bool {
	if !outcome.Done && outcome.InputTokens+outcome.OutputTokens > 0 && rs.server.db != nil {
		rs.server.recordCredentialSpend(ctx, candidate.Credential.ID, requestSpendUSD(candidate.Route.Model, outcome.InputTokens, outcome.OutputTokens))
	}
	if outcome.Done {
		rs.finalStatus = outcome.Status
		if outcome.ErrorCode != "" {
			rs.finalStatus = 502
		}
	}
	route := candidate.Route
	key := route.Model.ID + ":" + plan.wireEndpoint()
	if outcome.Compatibility {
		rs.tests++
		rs.compatibility[key] = append(rs.compatibility[key], *outcome)
	}
	incident := rs.incidents[key]
	if incident == nil && (rs.endpoints[route.Model.ID] != "" || outcome.Done) {
		for incidentKey, existing := range rs.incidents {
			if existing.RouteID == route.Model.ID {
				incident = existing
				key = incidentKey
				break
			}
		}
	}
	if incident != nil {
		for i := range incident.Attempts {
			if incident.Attempts[i].Status == "testing" {
				incident.Attempts[i].TestTokens = outcome.InputTokens + outcome.OutputTokens
				incident.Attempts[i].TestDurationMS = outcome.Record.DurationMS
			}
		}
	}
	if outcome.Done && outcome.Status >= 200 && outcome.Status < 300 && outcome.ErrorCode == "" {
		if !rs.request.Stream && validRepairResponse(outcome.ResponseBody, rs.request.PublicMode) {
			for _, learned := range rs.compatibility[key] {
				if len(learned.LearnedStrip) > 0 {
					rs.server.rememberCompatibilityParameters(ctx, route.Model.ID, learned.LearnedStrip)
				}
				for from, to := range learned.LearnedReplace {
					rs.server.rememberCompatibilityReplacement(ctx, route.Model.ID, plan.wireEndpoint(), compatibilityReplacement{From: from, To: to})
				}
				if learned.LearnedItemStrip.Field != "" {
					rs.server.rememberItemFieldStrip(ctx, route.Model.ID, learned.LearnedItemStrip)
				}
				if learned.LearnedDetachIDs {
					rs.server.rememberDetachReplayedIDs(ctx, route.Model.ID)
				}
			}
			if plan.SwitchedToResponses {
				rs.server.rememberResponsesEndpointPreferred(ctx, route.Model.ID)
			}
			if plan.ReplyFloor > 0 {
				rs.server.rememberReplyFloor(ctx, route.Model.ID, plan.ReplyFloor)
			}
		}
		if incident != nil {
			if !rs.request.Stream && !validRepairResponse(outcome.ResponseBody, rs.request.PublicMode) {
				incident.Status = "invalid_response"
				return false
			}
			incident.Status = "verified"
			for i := range incident.Attempts {
				if incident.Attempts[i].Status == "testing" {
					incident.Attempts[i].Status = "verified"
				}
			}
			// Streaming completion is not used to learn a payload rule yet: its public
			// status may already have been committed before a later upstream failure.
			latest := rs.server.repairPolicy(ctx)
			if latest.Enabled && latest.Version == rs.policy.Version && !rs.request.Stream && len(rs.proposals[key]) > 0 && rs.server.redis != nil {
				raw, _ := json.Marshal(rs.proposals[key])
				_ = rs.server.redis.Set(ctx, repairRuleKey(route, plan.wireEndpoint()), raw, 24*time.Hour).Err()
			}
			rs.commitVerified(ctx, route, incident)
		}
		return false
	}
	if outcome.Record.Error == "" && outcome.Status < 400 {
		return false
	}
	if incident == nil {
		id, _ := newID("repair")
		incident = &RepairIncident{ID: id, RequestID: rs.request.RequestID, RouteID: route.Model.ID, Category: repairCategory(outcome.Status), Status: "observed", Attempts: []RepairAttempt{}}
		rs.incidents[key] = incident
	}
	outcome.Record.RepairIncidentID = incident.ID
	incident.Error = outcome.Record.ErrorMessage
	if len(incident.Error) > 4000 {
		incident.Error = incident.Error[:4000]
	}
	if rs.enabled(route) {
		rs.backgroundEvidence[key] = repairEvidence(plan, route, incident.Error, outcome.Status, nil, repairTools)
	}
	for i := range incident.Attempts {
		if incident.Attempts[i].Status == "testing" {
			incident.Attempts[i].Status = "rolled_back"
		}
	}
	delete(rs.proposals, key)
	if rs.server.redis != nil && plan.Recovered {
		_ = rs.server.redis.Del(ctx, repairRuleKey(route, plan.wireEndpoint())).Err()
	}
	if _, changed := rs.timeouts[route.Provider.ID]; changed {
		if client := clients.clients[route.Provider.ID]; client != nil {
			client.CloseIdleConnections()
		}
		delete(clients.clients, route.Provider.ID)
	}
	delete(rs.timeouts, route.Provider.ID)
	delete(rs.endpoints, route.Model.ID)
	if !rs.enabled(route) || outcome.Done || outcome.Compatibility || rs.tests >= 3 || ctx.Err() != nil {
		return false
	}
	proposal, known := tokenConstraintRepair(incident.Error, plan.Payload)
	entry := RepairAttempt{Status: "proposed"}
	if !known {
		remaining := time.Duration(rs.policy.DiagnosisSeconds)*time.Second - rs.diagnosisTime
		if rs.rounds >= 2 || remaining <= 0 {
			return false
		}
		rs.rounds++
		if rs.targetCalls+rs.extraCalls >= 12 {
			return false
		}
		rs.extraCalls++
		tools := []string{}
		for _, tool := range repairTools {
			if rs.policy.Mode == "observe" || rs.policy.permits(tool) {
				tools = append(tools, tool)
			}
		}
		started := time.Now()
		diagnosisCtx, cancel := context.WithTimeout(ctx, remaining)
		var err error
		proposal, entry.AgentTokens, err = rs.server.sharedRepairDiagnosis(diagnosisCtx, rs.policy, repairEvidence(plan, route, incident.Error, outcome.Status, incident.Attempts, tools))
		cancel()
		entry.DurationMS = time.Since(started).Milliseconds()
		rs.diagnosisTime += time.Since(started)
		if err != nil {
			entry.Status = "diagnosis_failed"
			entry.Reason = err.Error()
			incident.Attempts = append(incident.Attempts, entry)
			return false
		}
	}
	entry.Proposal = proposal
	if err := validateRepairProposal(proposal, plan.Payload); err != nil {
		entry.Status = "rejected"
		entry.Reason = err.Error()
	} else if !rs.policy.permits(proposal.Action) {
		entry.Status = "observed"
		if rs.policy.Mode != "observe" {
			entry.Status = "permission_denied"
		}
	} else {
		// Re-read policy immediately before executing: disabling the feature stops
		// new actions even in a request whose diagnosis was already in flight.
		latest := rs.server.repairPolicy(ctx)
		if latest.Version != rs.policy.Version || !latest.permits(proposal.Action) {
			entry.Status = "policy_changed"
		} else {
			entry.Before, _ = json.Marshal(plan.Payload[proposal.Parameter])
			proposal.MatchValue = entry.Before
			raw, _ := json.Marshal(proposal)
			fingerprint := fmt.Sprintf("%s:%x", key, sha256.Sum256(raw))
			if rs.fingerprints[fingerprint] {
				entry.Status = "duplicate"
			} else {
				rs.fingerprints[fingerprint] = true
				entry.Status, entry.Reason = rs.execute(ctx, candidate, plan, proposal, clients)
				if entry.Status == "testing" {
					rs.tests++
					incident.Status = "testing"
					outcome.Record.RecoveryRetry = true
				}
			}
		}
	}
	incident.Attempts = append(incident.Attempts, entry)
	return entry.Status == "testing"
}

func (rs *repairSession) execute(ctx context.Context, candidate *routeCandidate, plan upstreamPlan, p RepairProposal, clients *clientCache) (string, string) {
	key := candidate.Route.Model.ID + ":" + plan.wireEndpoint()
	switch p.Action {
	case "set_parameter", "remove_parameter":
		payload := cloneMap(plan.Payload)
		if !applyRepairProposal(payload, p) {
			return "rejected", "Proposal does not change the request"
		}
		rs.proposals[key] = []RepairProposal{p}
		return "testing", ""
	case "refresh_connection":
		if client := clients.clients[candidate.Route.Provider.ID]; client != nil {
			client.CloseIdleConnections()
		}
		delete(clients.clients, candidate.Route.Provider.ID)
		delete(clients.errors, candidate.Route.Provider.ID)
		return "testing", "Idle connections refreshed"
	case "reset_cooldown":
		if rs.incidents[key].Category == "rate_limit" {
			return "rejected", "Provider rate-limit cooldown is still authoritative"
		}
		fallthrough
	case "validate_credential":
		if rs.targetCalls+rs.extraCalls >= 12 {
			return "rejected", "Upstream call budget exhausted"
		}
		rs.extraCalls++
		inspection := inspectRepairCredential(ctx, candidate.Route.Provider, candidate.Credential.Secret)
		if !inspection.Valid {
			return "rejected", "Credential verification failed; state unchanged"
		}
		rs.server.recordCredentialInspection(ctx, candidate.Credential.ID, inspection)
		return "testing", "Credential verified and cooldown cleared"
	case "select_credential":
		var id string
		_ = json.Unmarshal(p.Value, &id)
		credentials, err := rs.server.loadCredentials(ctx, candidate.Route.Provider.ID, candidate.Route.Model.ID)
		if err != nil {
			return "rejected", "Credential list unavailable"
		}
		for _, credential := range credentials {
			if credential.ID == id && credential.Enabled && credential.Status != "quarantined" && !credential.BalanceExhausted() {
				rs.selectedCredentials[candidate.Route.Model.ID] = id
				return "testing", "Selected existing credential; capacity is checked before dispatch"
			}
		}
		return "rejected", "Credential is not available on this route"
	case "switch_endpoint":
		var endpoint string
		_ = json.Unmarshal(p.Value, &endpoint)
		if plan.Format == "anthropic" || plan.wireEndpoint() == endpoint {
			return "rejected", "Endpoint switch is not applicable"
		}
		state := dispatchState{PreferNativeResponses: map[string]bool{candidate.Route.Model.ID: endpoint == "responses"}, NativeResponsesUnavailable: map[string]bool{candidate.Route.Model.ID: endpoint == "chat"}}
		rebuilt, err := rs.server.buildPlan(ctx, rs.request, candidate.Route, state)
		if err != nil || len(rebuilt.Removed) > len(plan.Removed) {
			return "rejected", "Endpoint cannot preserve the request"
		}
		rs.endpoints[candidate.Route.Model.ID] = endpoint
		return "testing", "Endpoint translated for verification"
	case "set_timeout":
		var seconds int
		_ = json.Unmarshal(p.Value, &seconds)
		if seconds == candidate.Route.Provider.TimeoutSeconds {
			return "rejected", "Timeout is unchanged"
		}
		rs.timeouts[candidate.Route.Provider.ID] = seconds
		if client := clients.clients[candidate.Route.Provider.ID]; client != nil {
			client.CloseIdleConnections()
		}
		delete(clients.clients, candidate.Route.Provider.ID)
		return "testing", "Timeout staged; persistent update requires a successful response"
	case "set_route_enabled":
		var enabled bool
		_ = json.Unmarshal(p.Value, &enabled)
		if enabled == candidate.Route.Model.Enabled {
			return "rejected", "Route state is unchanged"
		}
		// Disable only an outage route, after the caller finishes. A malformed
		// request is not evidence that a route should be taken offline.
		if !enabled && rs.incidents[key].Category != "provider_outage" {
			return "rejected", "Only a provider outage permits automatic route disable"
		}
		rs.pendingRoutes[candidate.Route.Model.ID] = enabled
		return "scheduled", "Route state will be verified after the request completes"
	default:
		return "rejected", "Unknown administration tool"
	}
}

// A single bounded catalog call verifies authorization without recursive probes.
func inspectRepairCredential(ctx context.Context, provider Provider, secret []byte) credentialInspection {
	client, err := upstreamClient(provider)
	if err != nil {
		return credentialInspection{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(provider.BaseURL, "/")+"/models", nil)
	if err != nil {
		return credentialInspection{}
	}
	applyProviderHeaders(request, provider, secret)
	response, err := client.Do(request)
	if err != nil {
		return credentialInspection{}
	}
	defer response.Body.Close()
	body, truncated, err := boundedBody(response.Body, 1<<20)
	var catalog struct {
		Data []json.RawMessage `json:"data"`
	}
	valid := err == nil && !truncated && response.StatusCode == 200 && json.Unmarshal(body, &catalog) == nil && catalog.Data != nil
	return credentialInspection{Valid: valid, StatusCode: response.StatusCode, CatalogAvailable: valid}
}

func (rs *repairSession) finish() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(rs.ctx), 2*time.Second)
	defer cancel()
	for id, enabled := range rs.pendingRoutes {
		original := rs.originalRoutes[id]
		for _, incident := range rs.incidents {
			if incident.RouteID != id {
				continue
			}
			status, reason := "cancelled", "Route recovered; scheduled change cancelled"
			if incident.Status != "verified" {
				latest := rs.server.repairPolicy(ctx)
				if latest.Version == rs.policy.Version && latest.permits("set_route_enabled") {
					tag, err := rs.server.db.Exec(ctx, `UPDATE model_routes SET enabled=$1,updated_at=now() WHERE id=$2 AND updated_at=$3`, enabled, id, original.Model.UpdatedAt)
					if err == nil && tag.RowsAffected() == 1 {
						status, reason = "applied", "Route state changed after the caller finished"
					} else {
						status, reason = "configuration_conflict", "Route changed concurrently; no update applied"
					}
				} else {
					status, reason = "policy_changed", "Permission changed; no update applied"
				}
			}
			for i := range incident.Attempts {
				if incident.Attempts[i].Status == "scheduled" {
					incident.Attempts[i].Status = status
					incident.Attempts[i].Reason = reason
				}
			}
		}
	}
	for _, incident := range rs.incidents {
		if rs.finalStatus >= 200 && rs.finalStatus < 300 && incident.Status != "verified" && incident.Status != "verified_configuration_conflict" {
			incident.Status = "recovered_elsewhere"
		}
		if incident.Status == "testing" {
			incident.Status = "failed"
		}
		for i := range incident.Attempts {
			if incident.Attempts[i].Status == "testing" {
				incident.Attempts[i].Status = "rolled_back"
			}
		}
		rs.server.storeRepair(ctx, incident)
	}
	// Completed streams and fast deterministic repairs can be diagnosed without
	// delaying the caller. A bounded worker only proposes; future requests must
	// still execute and verify the proposal under the then-current permission.
	if rs.rounds == 0 && rs.policy.Enabled && rs.server.repairWorkers != nil {
		for key, evidence := range rs.backgroundEvidence {
			incident := rs.incidents[key]
			if incident == nil {
				continue
			}
			select {
			case rs.server.repairWorkers <- struct{}{}:
				copyIncident := *incident
				copyIncident.Attempts = append([]RepairAttempt{}, incident.Attempts...)
				go rs.server.reviewRepairBackground(rs.policy, copyIncident, evidence)
			default:
			}
			break
		}
	}
}

func (s *Server) reviewRepairBackground(policy RepairPolicy, incident RepairIncident, evidence []byte) {
	defer func() { <-s.repairWorkers }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(policy.DiagnosisSeconds)*time.Second)
	defer cancel()
	latest := s.repairPolicy(ctx)
	if !latest.Enabled || latest.Version != policy.Version {
		return
	}
	started := time.Now()
	proposal, tokens, err := s.sharedRepairDiagnosis(ctx, policy, evidence)
	entry := RepairAttempt{Proposal: proposal, AgentTokens: tokens, DurationMS: time.Since(started).Milliseconds(), Status: "background_observed"}
	if err != nil {
		entry.Status = "diagnosis_failed"
		entry.Reason = err.Error()
	}
	incident.Attempts = append(incident.Attempts, entry)
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer writeCancel()
	s.storeRepair(writeCtx, &incident)
}

func validRepairResponse(raw []byte, mode string) bool {
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil || body["error"] != nil {
		return false
	}
	if mode == messageModeAnthropic {
		content, _ := body["content"].([]any)
		for _, value := range content {
			item, _ := value.(map[string]any)
			text, _ := item["text"].(string)
			name, _ := item["name"].(string)
			if (item["type"] == "text" && text != "") || (item["type"] == "tool_use" && name != "" && item["input"] != nil) {
				return true
			}
		}
		return false
	}
	if mode == messageModeResponses {
		output, _ := body["output"].([]any)
		for _, value := range output {
			item, _ := value.(map[string]any)
			name, _ := item["name"].(string)
			if item["type"] == "function_call" && name != "" && item["arguments"] != nil {
				return true
			}
			content, _ := item["content"].([]any)
			for _, part := range content {
				p, _ := part.(map[string]any)
				text, _ := p["text"].(string)
				refusal, _ := p["refusal"].(string)
				if (p["type"] == "output_text" && text != "") || refusal != "" {
					return true
				}
			}
		}
		return false
	}
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 {
		return false
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	content, _ := message["content"].(string)
	calls, _ := message["tool_calls"].([]any)
	if content != "" || message["refusal"] != nil {
		return true
	}
	for _, value := range calls {
		call, _ := value.(map[string]any)
		function, _ := call["function"].(map[string]any)
		name, _ := function["name"].(string)
		if name != "" && function["arguments"] != nil {
			return true
		}
	}
	return false
}

func (rs *repairSession) commitVerified(ctx context.Context, route routeRuntime, incident *RepairIncident) {
	if rs.request.Stream {
		return
	}
	latest := rs.server.repairPolicy(ctx)
	if latest.Version != rs.policy.Version || !latest.Enabled {
		return
	}
	original := rs.originalRoutes[route.Model.ID]
	if seconds := rs.timeouts[route.Provider.ID]; seconds > 0 && rs.server.db != nil {
		tag, err := rs.server.db.Exec(ctx, `UPDATE providers SET timeout_seconds=$1,updated_at=now() WHERE id=$2 AND updated_at=$3`, seconds, route.Provider.ID, original.Provider.UpdatedAt)
		if err != nil || tag.RowsAffected() != 1 {
			incident.Status = "verified_configuration_conflict"
		}
	}
	if endpoint := rs.endpoints[route.Model.ID]; endpoint == "responses" {
		rs.server.rememberResponsesEndpointPreferred(ctx, route.Model.ID)
	} else if endpoint == "chat" {
		rs.server.rememberResponsesEndpointMissing(ctx, route.Model.ID)
	}
}
