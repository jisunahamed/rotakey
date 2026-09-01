package app

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"
)

// LearnedFact is one thing the gateway worked out from a provider's own answer
// and has been acting on ever since, without the operator asking for it or being
// able to see it.
//
// Each one is a repair. A route configured for /chat/completions whose provider
// answers "use /responses" is switched, and the switch is remembered so the next
// request does not pay the same 400. A parameter the provider rejects is dropped,
// or renamed to the spelling the provider named, on every later request. That is
// the right behaviour, and until this endpoint existed it was also invisible: the
// console showed a route's own configuration while the gateway sent something
// else, for a day, with nothing on any screen saying so. v3.0.0 shipped a defect
// where the remembered switch sent a body the endpoint could not accept, and the
// only way to find out was to read the attempt rows of a failed request.
//
// None of it is configuration. It lives in Redis, every fact expires within a
// day, and a Redis restart forgets all of it — so it is reported as facts with an
// expiry rather than as settings with a value.
type LearnedFact struct {
	// Kind is what was learned:
	//   prefer_responses  the provider refused Chat Completions and named /responses
	//   no_responses      the provider answered 404 at /responses
	//   strip_parameters  the provider rejected these parameters, so they are removed
	//   rename_parameters the provider named a different spelling for these parameters
	//   strip_item_fields the provider rejected these fields from inside the
	//                     caller's own turns, so they go from every turn
	Kind string `json:"kind"`
	// Endpoint the fact applies to, for the facts learned per endpoint. Empty on
	// the two that apply to the route as a whole.
	Endpoint string `json:"endpoint,omitempty"`
	// Parameters carries strip_parameters; Renames carries rename_parameters as
	// [sent, accepted] pairs, matching the shape the request evidence already uses.
	Parameters []string    `json:"parameters,omitempty"`
	Renames    [][2]string `json:"renames,omitempty"`
	// ExpiresAt is when Redis drops this fact and the gateway plans from the
	// route's own configuration again. Absolute rather than a countdown, so a
	// console that renders it needs no clock agreement with the server.
	ExpiresAt time.Time `json:"expires_at"`
}

// learnedRouteEndpoints are the two endpoint names compatibility learning is
// keyed by, as upstreamPlan.wireEndpoint spells them.
var learnedRouteEndpoints = []string{"chat", "responses"}

// learnedRouteFacts reports what the gateway currently believes about a route.
//
// It deliberately reads through learnedCompatibilityParameters and
// learnedCompatibilityReplacements rather than reading Redis directly, so the
// same allowlist that decides what the dispatcher will act on decides what the
// console is told. A parameter shown here is a parameter that will be stripped.
func (s *Server) learnedRouteFacts(ctx context.Context, modelID string) []LearnedFact {
	facts := make([]LearnedFact, 0, 4)
	expiry := func(key string) (time.Time, bool) {
		ttl, err := s.redis.TTL(ctx, key).Result()
		// Redis answers a missing key with -2 and a key without an expiry with -1.
		// Neither is a fact worth reporting: the first was never written, and the
		// second cannot happen, because every writer here sets a TTL in the same
		// transaction.
		if err != nil || ttl <= 0 {
			return time.Time{}, false
		}
		return time.Now().Add(ttl).UTC(), true
	}

	if expiresAt, ok := expiry(responsesPreferredKey(modelID)); ok {
		facts = append(facts, LearnedFact{Kind: "prefer_responses", ExpiresAt: expiresAt})
	}
	if expiresAt, ok := expiry(responsesMissingKey(modelID)); ok {
		facts = append(facts, LearnedFact{Kind: "no_responses", ExpiresAt: expiresAt})
	}
	if parameters := s.learnedCompatibilityParameters(ctx, modelID); len(parameters) > 0 {
		if expiresAt, ok := expiry(compatibilityStripKey(modelID)); ok {
			facts = append(facts, LearnedFact{Kind: "strip_parameters", Parameters: parameters, ExpiresAt: expiresAt})
		}
	}
	if strips := s.learnedItemFieldStrips(ctx, modelID); len(strips) > 0 {
		if expiresAt, ok := expiry(compatibilityItemStripKey(modelID)); ok {
			paths := make([]string, 0, len(strips))
			for _, strip := range strips {
				paths = append(paths, strip.String())
			}
			facts = append(facts, LearnedFact{Kind: "strip_item_fields", Parameters: paths, ExpiresAt: expiresAt})
		}
	}
	for _, endpoint := range learnedRouteEndpoints {
		replacements := s.learnedCompatibilityReplacements(ctx, modelID, endpoint)
		if len(replacements) == 0 {
			continue
		}
		expiresAt, ok := expiry(compatibilityReplaceKey(modelID, endpoint))
		if !ok {
			continue
		}
		renames := make([][2]string, 0, len(replacements))
		for from, to := range replacements {
			renames = append(renames, [2]string{from, to})
		}
		// A map iterates in a different order every time. Sorting keeps the list
		// from reshuffling under the operator on every poll.
		slices.SortFunc(renames, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
		facts = append(facts, LearnedFact{Kind: "rename_parameters", Endpoint: endpoint, Renames: renames, ExpiresAt: expiresAt})
	}
	return facts
}

// forgetLearnedRouteState drops everything the gateway learned about a route so
// the next request is planned from the route's own configuration.
//
// This is the operator's undo for a repair that was right once and is wrong now:
// a provider that has since gained /responses, or a parameter that was rejected
// during an outage and is accepted again. Editing or re-probing a route already
// clears the two endpoint facts, because both change what the route claims; this
// clears the parameter facts too, because nothing else ever does.
func (s *Server) forgetLearnedRouteState(ctx context.Context, modelID string) {
	keys := []string{
		responsesPreferredKey(modelID),
		responsesMissingKey(modelID),
		compatibilityStripKey(modelID),
		compatibilityItemStripKey(modelID),
	}
	for _, endpoint := range learnedRouteEndpoints {
		keys = append(keys, compatibilityReplaceKey(modelID, endpoint))
	}
	if err := s.redis.Del(ctx, keys...).Err(); err != nil {
		s.logger.Warn("learned route state reset failed", "model_id", modelID, "error", err)
	}
}

func (s *Server) handleModelLearned(w http.ResponseWriter, r *http.Request) {
	if !s.modelRouteExists(r.Context(), r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "model_not_found", "Model was not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"facts": s.learnedRouteFacts(r.Context(), r.PathValue("id"))})
}

func (s *Server) handleForgetModelLearned(w http.ResponseWriter, r *http.Request) {
	if !s.modelRouteExists(r.Context(), r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "model_not_found", "Model was not found.")
		return
	}
	s.forgetLearnedRouteState(r.Context(), r.PathValue("id"))
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.forget_learned", "model", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

// modelRouteExists keeps both handlers above from reporting "nothing learned" for
// a route that does not exist, which reads identically to a healthy route and
// would hide a stale link or a deleted alias.
func (s *Server) modelRouteExists(ctx context.Context, modelID string) bool {
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT TRUE FROM model_routes WHERE id=$1`, modelID).Scan(&exists); err != nil {
		return false
	}
	return exists
}
