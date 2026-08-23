package app

import (
	"net/http"
	"testing"
	"time"
)

func poolCandidate(providerID, modelID, credentialID string, primary bool) routeCandidate {
	return routeCandidate{
		Route: routeRuntime{
			Model:    ModelRoute{ID: modelID, ProviderID: providerID},
			Provider: Provider{ID: providerID},
		},
		Credential: credentialRuntime{CredentialView: CredentialView{
			ID: credentialID, Label: credentialID, IsPrimary: primary,
			Enabled: true, Status: "healthy",
		}},
	}
}

func labelsInOrder(candidates []routeCandidate, order []int) []string {
	labels := make([]string, 0, len(order))
	for _, index := range order {
		labels = append(labels, candidates[index].Credential.Label)
	}
	return labels
}

func TestCandidateSelectionOrderRotatesProvidersFirst(t *testing.T) {
	candidates := []routeCandidate{
		poolCandidate("p1", "m1", "a1", false),
		poolCandidate("p1", "m1", "a2", false),
		poolCandidate("p2", "m2", "b1", false),
		poolCandidate("p2", "m2", "b2", false),
	}
	// Providers alternate at every depth, so one provider is never drained before
	// the other is tried.
	first := labelsInOrder(candidates, candidateSelectionOrder(candidates, 1))
	if len(first) != 4 {
		t.Fatalf("order dropped candidates: %v", first)
	}
	if first[0][0] == first[1][0] {
		t.Fatalf("two consecutive picks came from one provider: %v", first)
	}
	// A later cursor starts on the other provider, which is what makes successive
	// requests spread across the pool.
	second := labelsInOrder(candidates, candidateSelectionOrder(candidates, 2))
	if first[0] == second[0] {
		t.Fatalf("cursor did not rotate the starting provider: %v then %v", first, second)
	}
}

func TestCandidateSelectionOrderKeepsPrimaryFirstWithinProvider(t *testing.T) {
	candidates := []routeCandidate{
		poolCandidate("p1", "m1", "fallback", false),
		poolCandidate("p1", "m1", "primary", true),
	}
	for cursor := int64(1); cursor <= 4; cursor++ {
		got := labelsInOrder(candidates, candidateSelectionOrder(candidates, cursor))
		if got[0] != "primary" {
			t.Fatalf("cursor %d selected %v, want primary first", cursor, got)
		}
	}
}

func TestCandidateSelectionOrderCoversEveryCandidate(t *testing.T) {
	candidates := []routeCandidate{
		poolCandidate("p1", "m1", "a1", false),
		poolCandidate("p2", "m2", "b1", false),
		poolCandidate("p2", "m2", "b2", false),
		poolCandidate("p3", "m3", "c1", true),
	}
	order := candidateSelectionOrder(candidates, 3)
	seen := map[int]bool{}
	for _, index := range order {
		if seen[index] {
			t.Fatalf("candidate %d selected twice: %v", index, order)
		}
		seen[index] = true
	}
	if len(seen) != len(candidates) {
		t.Fatalf("order covered %d of %d candidates", len(seen), len(candidates))
	}
}

func TestUpstreamRateLimitHoldClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header http.Header
		body   string
		want   time.Duration
		ok     bool
	}{
		{
			name:   "429 with retry-after",
			status: http.StatusTooManyRequests,
			header: http.Header{"Retry-After": []string{"30"}},
			want:   30 * time.Second,
			ok:     true,
		},
		{
			name:   "429 without any hint falls back to the caller default",
			status: http.StatusTooManyRequests,
			header: http.Header{},
			want:   0,
			ok:     true,
		},
		{
			name:   "429 uses an anthropic reset header",
			status: http.StatusTooManyRequests,
			header: http.Header{"Anthropic-Ratelimit-Tokens-Reset": []string{"45"}},
			want:   45 * time.Second,
			ok:     true,
		},
		{
			name:   "gemini resource exhausted at 400 is a rate limit",
			status: http.StatusBadRequest,
			header: http.Header{},
			body:   `{"error":{"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded"}}`,
			ok:     true,
		},
		{
			name:   "503 overloaded is a rate limit",
			status: http.StatusServiceUnavailable,
			header: http.Header{},
			body:   `{"error":{"type":"overloaded_error","message":"Overloaded"}}`,
			ok:     true,
		},
		{
			name:   "401 is never held, it must quarantine",
			status: http.StatusUnauthorized,
			header: http.Header{"Retry-After": []string{"60"}},
			body:   `{"error":{"message":"rate limit"}}`,
			ok:     false,
		},
		{
			name:   "plain bad request is not a rate limit",
			status: http.StatusBadRequest,
			header: http.Header{},
			body:   `{"error":{"message":"Unrecognized request argument supplied: thinking"}}`,
			ok:     false,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			hold, ok := upstreamRateLimitHold(test.status, test.header, []byte(test.body))
			if ok != test.ok {
				t.Fatalf("rate limited = %v, want %v", ok, test.ok)
			}
			if ok && hold != test.want {
				t.Fatalf("hold = %s, want %s", hold, test.want)
			}
		})
	}
}

func TestBodySignalsRateLimit(t *testing.T) {
	rateLimited := []string{
		`{"error":{"code":"rate_limit_exceeded"}}`,
		`{"error":{"type":"rate_limit_error"}}`,
		`{"error":{"status":"RESOURCE_EXHAUSTED"}}`,
		`{"error":{"message":"You are sending requests too quickly, too many requests"}}`,
		`{"detail":{"message":"Concurrent request limit reached"}}`,
		`{"message":"quota exceeded for this project"}`,
	}
	for _, body := range rateLimited {
		if !bodySignalsRateLimit([]byte(body)) {
			t.Fatalf("body was not recognised as rate limited: %s", body)
		}
	}
	other := []string{
		``,
		`not json`,
		`{"error":{"message":"model not found"}}`,
		`{"error":{"code":"invalid_api_key"}}`,
	}
	for _, body := range other {
		if bodySignalsRateLimit([]byte(body)) {
			t.Fatalf("body was wrongly treated as rate limited: %s", body)
		}
	}
}

func TestPoolRetryTimeoutUsesTheMostGenerousProvider(t *testing.T) {
	routes := []routeRuntime{
		{Provider: Provider{TimeoutSeconds: 30}},
		{Provider: Provider{TimeoutSeconds: 300}},
	}
	if got := poolRetryTimeout(routes, false); got != 300*time.Second {
		t.Fatalf("pool timeout = %s, want 300s", got)
	}
}

func TestFilterForcedCredentialNarrowsThePool(t *testing.T) {
	candidates := []routeCandidate{
		poolCandidate("p1", "m1", "a1", false),
		poolCandidate("p2", "m2", "b1", false),
	}
	if got := filterForcedCredential(candidates, ""); len(got) != 2 {
		t.Fatalf("empty forced credential filtered the pool: %d", len(got))
	}
	got := filterForcedCredential(candidates, "b1")
	if len(got) != 1 || got[0].Credential.ID != "b1" {
		t.Fatalf("forced credential filter = %#v", got)
	}
}

func TestRetainPlannedCandidatesDropsUntranslatableRoutes(t *testing.T) {
	candidates := []routeCandidate{
		poolCandidate("p1", "m1", "a1", false),
		poolCandidate("p2", "m2", "b1", false),
	}
	kept := retainPlannedCandidates(candidates, map[string]upstreamPlan{"m2": {}})
	if len(kept) != 1 || kept[0].Route.Model.ID != "m2" {
		t.Fatalf("retained = %#v", kept)
	}
}

func TestPlanTokenCostsAreKeyedPerRoute(t *testing.T) {
	costs := planTokenCosts(map[string]upstreamPlan{
		"m1": {TokenCost: 100},
		"m2": {TokenCost: 250},
	})
	if costs["m1"] != 100 || costs["m2"] != 250 {
		t.Fatalf("costs = %#v", costs)
	}
}

func TestRouteCandidateKeyIsUniquePerRouteAndCredential(t *testing.T) {
	first := poolCandidate("p1", "m1", "a1", false)
	sameKeyOtherRoute := poolCandidate("p2", "m2", "a1", false)
	if first.key() == sameKeyOtherRoute.key() {
		t.Fatal("one credential shared across routes collapsed to one key")
	}
}
