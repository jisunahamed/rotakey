package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"
)

// learnedServer is a Server with nothing but the two things the learned-state
// helpers touch: Redis and a logger. No Postgres, no limiter, no config — if
// either helper ever needs more than this, the test should be the thing that
// notices.
func learnedServer(t *testing.T) *Server {
	t.Helper()
	return &Server{
		redis:  integrationRedis(t),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestLearnedRouteFactsReportsEveryRepairTheGatewayIsMaking is the guard for the
// invisibility that made the v3.0.0 endpoint-switch defect so expensive to find:
// the gateway was rewriting requests for a day and no screen said so. Every fact
// the dispatcher can act on must be reportable, so a route behaving unlike its
// own configuration can be explained without reading Redis by hand.
func TestLearnedRouteFactsReportsEveryRepairTheGatewayIsMaking(t *testing.T) {
	server := learnedServer(t)
	ctx := context.Background()
	modelID := fmt.Sprintf("mdl_learned_%d", time.Now().UnixNano())
	t.Cleanup(func() { server.forgetLearnedRouteState(ctx, modelID) })

	if facts := server.learnedRouteFacts(ctx, modelID); len(facts) != 0 {
		t.Fatalf("a route nothing has been learned about reported %d facts", len(facts))
	}

	server.rememberResponsesEndpointPreferred(ctx, modelID)
	server.rememberResponsesEndpointMissing(ctx, modelID)
	server.rememberCompatibilityParameters(ctx, modelID, []string{"stream_options", "service_tier"})
	server.rememberCompatibilityReplacement(ctx, modelID, "responses",
		compatibilityReplacement{From: "max_tokens", To: "max_output_tokens"})
	server.rememberItemFieldStrip(ctx, modelID,
		itemFieldStrip{Root: "input", Field: "internal_chat_message_metadata_passthrough"})
	server.rememberReplyFloor(ctx, modelID, 4096)
	server.rememberDetachReplayedIDs(ctx, modelID)

	byKind := make(map[string]LearnedFact)
	for _, fact := range server.learnedRouteFacts(ctx, modelID) {
		byKind[fact.Kind] = fact
	}
	for _, kind := range []string{"prefer_responses", "no_responses", "strip_parameters", "rename_parameters", "strip_item_fields", "raise_reply_budget", "detach_replayed_ids"} {
		fact, ok := byKind[kind]
		if !ok {
			t.Fatalf("%s was learned and is not reported", kind)
		}
		// Every fact expires; one reported without an expiry would read on screen
		// as a setting the operator chose.
		if fact.ExpiresAt.IsZero() || fact.ExpiresAt.After(time.Now().Add(adaptiveCompatibilityTTL+time.Minute)) {
			t.Fatalf("%s reported an expiry of %v", kind, fact.ExpiresAt)
		}
	}

	// Sorted, because a map iterates differently every time and the console polls.
	if got := byKind["strip_parameters"].Parameters; len(got) != 2 || got[0] != "service_tier" || got[1] != "stream_options" {
		t.Fatalf("stripped parameters were %v", got)
	}
	renames := byKind["rename_parameters"]
	if renames.Endpoint != "responses" {
		t.Fatalf("renames were reported for endpoint %q", renames.Endpoint)
	}
	if len(renames.Renames) != 1 || renames.Renames[0] != [2]string{"max_tokens", "max_output_tokens"} {
		t.Fatalf("renames were %v", renames.Renames)
	}
	// The path, not the bare field name: "internal_chat_message_metadata_passthrough"
	// on its own does not say where the gateway is deleting it from, and the whole
	// point of the panel is that the operator can tell what left their request.
	if got := byKind["strip_item_fields"].Parameters; len(got) != 1 ||
		got[0] != "input[].internal_chat_message_metadata_passthrough" {
		t.Fatalf("stripped turn fields were %v", got)
	}
	// The number, so the panel can say what uncapped requests now ask for.
	if got := byKind["raise_reply_budget"].Parameters; len(got) != 1 || got[0] != "4096" {
		t.Fatalf("the reply floor was reported as %v", got)
	}
}

// TestLearnedRouteFactsHidesWhatTheDispatcherWillNotActOn is the honesty guard.
// The report reads through the same allowlisted readers the dispatcher uses, so
// a parameter that would never actually be stripped can never be shown as one.
func TestLearnedRouteFactsHidesWhatTheDispatcherWillNotActOn(t *testing.T) {
	server := learnedServer(t)
	ctx := context.Background()
	modelID := fmt.Sprintf("mdl_learned_off_%d", time.Now().UnixNano())
	t.Cleanup(func() { server.forgetLearnedRouteState(ctx, modelID) })

	// max_tokens is deliberately absent from the strip allowlist: dropping a
	// caller's output cap is never the repair. Writing it straight into the set
	// bypasses rememberCompatibilityParameters' own filter, which is the only way
	// a disallowed name could be in Redis at all.
	if err := server.redis.SAdd(ctx, compatibilityStripKey(modelID), "max_tokens").Err(); err != nil {
		t.Fatalf("seed the strip set: %v", err)
	}
	if err := server.redis.Expire(ctx, compatibilityStripKey(modelID), adaptiveCompatibilityTTL).Err(); err != nil {
		t.Fatalf("expire the strip set: %v", err)
	}
	if facts := server.learnedRouteFacts(ctx, modelID); len(facts) != 0 {
		t.Fatalf("a parameter the dispatcher will not strip was reported as stripped: %v", facts)
	}
}

// TestForgetLearnedRouteStateClearsTheParameterFactsToo is the reason this
// endpoint exists as well as the existing per-edit resets. Editing or re-probing
// a route clears the two endpoint facts and nothing has ever cleared the
// parameter facts, so a rename learned during an outage outlived every correction
// the operator could make.
func TestForgetLearnedRouteStateClearsTheParameterFactsToo(t *testing.T) {
	server := learnedServer(t)
	ctx := context.Background()
	modelID := fmt.Sprintf("mdl_learned_reset_%d", time.Now().UnixNano())
	t.Cleanup(func() { server.forgetLearnedRouteState(ctx, modelID) })

	server.rememberResponsesEndpointPreferred(ctx, modelID)
	server.rememberResponsesEndpointMissing(ctx, modelID)
	server.rememberCompatibilityParameters(ctx, modelID, []string{"stream_options"})
	server.rememberItemFieldStrip(ctx, modelID, itemFieldStrip{Root: "input", Field: "vendor_metadata"})
	server.rememberReplyFloor(ctx, modelID, 16384)
	server.rememberDetachReplayedIDs(ctx, modelID)
	for _, endpoint := range learnedRouteEndpoints {
		server.rememberCompatibilityReplacement(ctx, modelID, endpoint,
			compatibilityReplacement{From: "max_tokens", To: "max_output_tokens"})
	}
	if facts := server.learnedRouteFacts(ctx, modelID); len(facts) != 8 {
		t.Fatalf("expected eight facts before the reset, got %d", len(facts))
	}

	server.forgetLearnedRouteState(ctx, modelID)

	if facts := server.learnedRouteFacts(ctx, modelID); len(facts) != 0 {
		t.Fatalf("the reset left %d facts behind: %v", len(facts), facts)
	}
}
