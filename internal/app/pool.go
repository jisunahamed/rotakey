package app

import (
	"context"
	"math"
	"time"
)

// routeCandidate is one dispatchable (route, credential) pair. Model-wise
// routing pools candidates from every provider that publishes the same public
// alias, so a single request can fail over across providers as well as keys.
type routeCandidate struct {
	Route      routeRuntime
	Credential credentialRuntime
}

// key identifies a candidate uniquely inside one request's skip set.
func (c routeCandidate) key() string {
	return c.Route.Model.ID + "|" + c.Credential.ID
}

// loadPoolCandidates expands routes into candidates, keeping each route's own
// credentials so rate limits stay scoped to the model row they were configured
// on. Routes whose credentials cannot be loaded are skipped rather than failing
// the whole pool, because another provider may still be able to serve.
func (s *Server) loadPoolCandidates(ctx context.Context, routes []routeRuntime) ([]routeCandidate, error) {
	candidates := make([]routeCandidate, 0, len(routes))
	var lastErr error
	for _, route := range routes {
		credentials, err := s.loadCredentials(ctx, route.Provider.ID, route.Model.ID)
		if err != nil {
			lastErr = err
			continue
		}
		for _, credential := range credentials {
			candidates = append(candidates, routeCandidate{Route: route, Credential: credential})
		}
	}
	if len(candidates) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return candidates, nil
}

// candidateSelectionOrder rotates across providers first and across that
// provider's keys second, so consecutive requests spread over the pool instead
// of hammering one provider until it rate limits. Within a provider the primary
// credential keeps its priority, matching provider-wise behaviour.
func candidateSelectionOrder(candidates []routeCandidate, cursor int64) []int {
	if len(candidates) == 0 {
		return nil
	}
	groupIndex := map[string][]int{}
	groupOrder := []string{}
	for index, candidate := range candidates {
		id := candidate.Route.Model.ID
		if _, seen := groupIndex[id]; !seen {
			groupOrder = append(groupOrder, id)
		}
		groupIndex[id] = append(groupIndex[id], index)
	}
	rotated := make([][]int, 0, len(groupOrder))
	for _, id := range groupOrder {
		members := groupIndex[id]
		group := make([]routeCandidate, 0, len(members))
		for _, index := range members {
			group = append(group, candidates[index])
		}
		local := credentialOrderForCandidates(group, cursor)
		mapped := make([]int, 0, len(local))
		for _, position := range local {
			mapped = append(mapped, members[position])
		}
		rotated = append(rotated, mapped)
	}
	providerStart := 0
	if len(rotated) > 1 {
		providerStart = int((cursor - 1) % int64(len(rotated)))
		if providerStart < 0 {
			providerStart = 0
		}
	}
	order := make([]int, 0, len(candidates))
	for depth := 0; ; depth++ {
		added := false
		for offset := 0; offset < len(rotated); offset++ {
			group := rotated[(providerStart+offset)%len(rotated)]
			if depth < len(group) {
				order = append(order, group[depth])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return order
}

// credentialOrderForCandidates reuses the provider-wise rotation rules on a
// single provider's slice of the pool.
func credentialOrderForCandidates(group []routeCandidate, cursor int64) []int {
	credentials := make([]credentialRuntime, 0, len(group))
	for _, candidate := range group {
		credentials = append(credentials, candidate.Credential)
	}
	return credentialSelectionOrder(credentials, cursor)
}

// selectPoolCandidate picks the next candidate whose provider is reachable and
// whose limits allow the request. Cooldowns, quarantines and exhausted limits
// are skipped, which is what makes "whichever provider is live right now" work
// without any manual switching.
//
// tokenCosts is keyed by model route ID because each provider in the pool gets
// its own translated payload, so the reservation size differs per candidate.
func (s *Server) selectPoolCandidate(
	ctx context.Context,
	poolKey string,
	candidates []routeCandidate,
	tokenCosts map[string]int64,
	skipped map[string]bool,
	maxWait time.Duration,
) (*routeCandidate, reservation, time.Duration, []RoutingDecision, error) {
	deadline := time.Now().Add(maxWait)
	for {
		cursor, err := s.redis.Incr(ctx, "rr:pool:"+poolKey).Result()
		if err != nil {
			return nil, reservation{}, 0, nil, err
		}
		minRetry := time.Duration(math.MaxInt64)
		decisions := make([]RoutingDecision, 0)
		for _, index := range candidateSelectionOrder(candidates, cursor) {
			candidate := &candidates[index]
			if skipped[candidate.key()] {
				continue
			}
			credential := candidate.Credential
			if !credential.Enabled || credential.Status == "quarantined" {
				reason := credential.Status
				if !credential.Enabled {
					reason = "disabled"
				}
				decisions = append(decisions, RoutingDecision{
					CredentialID: credential.ID, CredentialLabel: credential.Label, Reason: reason,
				})
				continue
			}
			cooldown, err := s.redis.TTL(ctx, "cooldown:"+credential.ID).Result()
			if err != nil {
				return nil, reservation{}, 0, nil, err
			}
			if cooldown > 0 {
				resetAt := time.Now().Add(cooldown).UTC()
				decisions = append(decisions, RoutingDecision{
					CredentialID: credential.ID, CredentialLabel: credential.Label,
					Reason: "cooldown", RetryAfterMS: cooldown.Milliseconds(), ResetAt: &resetAt,
				})
				if cooldown < minRetry {
					minRetry = cooldown
				}
				continue
			}
			constraints, rejected := buildConstraintsWithCosts(credential, candidate.Route.Model.ID, 1, tokenCosts[candidate.Route.Model.ID], tokenCosts[candidate.Route.Model.ID])
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
					constraints: constraints, tokenCost: tokenCosts[candidate.Route.Model.ID], reservedAt: result.ReservedAt,
				}, 0, decisions, nil
			}
			for _, blocked := range result.Blocked {
				remaining := blocked.Constraint.Capacity - blocked.Used
				if remaining < 0 {
					remaining = 0
				}
				resetAt := time.Now().Add(blocked.Retry).UTC()
				decisions = append(decisions, RoutingDecision{
					CredentialID: credential.ID, CredentialLabel: credential.Label,
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
