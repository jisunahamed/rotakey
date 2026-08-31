package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// azureResponsesDemand is the rejection Azure Foundry sends when a request
// carries function tools that its Chat Completions route will not accept. It
// names the endpoint that would work, which is the whole signal.
const azureResponsesDemand = `{"error":{"message":"Function tools with reasoning_effort are not supported for gpt-5.6-sol in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'.","type":"invalid_request_error","param":null,"code":null}}`

// upstreamRejecting answers every request with one status and body, standing in
// for a provider that refuses the request's shape.
func upstreamRejecting(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.Close)
	return upstream
}

// chatAttempt assembles the pieces runAttempt needs for a Chat Completions call
// against a test upstream. The route claims only Chat, which is the shape the
// provider is about to contradict.
func chatAttempt(t *testing.T, upstreamURL string) (*Server, routeCandidate, upstreamPlan) {
	t.Helper()
	server := &Server{
		cfg:     Config{MaxResponseBytes: 16 << 20},
		redis:   unreachableRedis(),
		limiter: newLimiter(unreachableRedis()),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	t.Cleanup(func() {
		_ = server.redis.Close()
		_ = server.limiter.redis.Close()
	})
	candidate := routeCandidate{
		Route: routeRuntime{
			Provider: Provider{ID: "prv_1", Name: "Foundry", BaseURL: upstreamURL, APIFormat: "openai", TimeoutSeconds: 30},
			Model:    ModelRoute{ID: "mdl_1", PublicAlias: "azure/gpt-5.6-sol", UpstreamModel: "gpt-5.6-sol", SupportsChat: true},
		},
		Credential: credentialRuntime{
			CredentialView: CredentialView{ID: "cre_1", Label: "primary"},
			Secret:         []byte("sk-test"),
		},
	}
	plan := upstreamPlan{
		Payload: map[string]any{"model": "gpt-5.6-sol", "reasoning_effort": "medium"},
		Encoded: []byte(`{"model":"gpt-5.6-sol","reasoning_effort":"medium"}`),
		Path:    "/chat/completions",
		Format:  "openai",
	}
	return server, candidate, plan
}

// TestRunAttemptSwitchesToResponsesOnUpstreamDemand is the fix for the failure
// the operator hit: a Foundry model that only accepts function tools at
// /responses used to end the request with a verbatim 400. The attempt must now
// report the switch and write nothing, so the pool can retry the same candidate
// at the endpoint the provider named.
func TestRunAttemptSwitchesToResponsesOnUpstreamDemand(t *testing.T) {
	upstream := upstreamRejecting(t, http.StatusBadRequest, azureResponsesDemand)
	server, candidate, plan := chatAttempt(t, upstream.URL)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req := dispatchRequest{RequestID: "req_1", PublicMode: messageModeChat, Alias: "azure/gpt-5.6-sol", Endpoint: "/v1/chat/completions"}
	outcome := server.runAttempt(recorder, request, req, candidate, plan,
		upstream.Client(), context.Background(), reservation{}, true)

	if !outcome.NativeResponsesPreferred {
		t.Fatalf("the provider's instruction to use /responses was not learned: %#v", outcome.Record)
	}
	if outcome.Done {
		t.Fatal("the attempt finished the request instead of leaving it open for a retry")
	}
	if !outcome.Compatibility || !outcome.ResetSkips {
		t.Fatalf("the retry was not accounted for as a compatibility repair: %#v", outcome)
	}
	if outcome.Record.SwitchedEndpoint != "responses" || !outcome.Record.Retryable {
		t.Fatalf("attempt record = %#v", outcome.Record)
	}
	if outcome.Record.Error != "responses_endpoint_preferred" {
		t.Fatalf("attempt error = %q", outcome.Record.Error)
	}
	// Nothing may reach the client while a retry is still possible.
	if recorder.Body.Len() != 0 || recorder.Flushed {
		t.Fatalf("the rejection was written to the caller: %q", recorder.Body.String())
	}

	// A plan that already knows /responses is absent must not read the same
	// rejection as an invitation to go back there.
	plan.ResponsesUnavailable = true
	settled := server.runAttempt(httptest.NewRecorder(), request, req, candidate, plan,
		upstream.Client(), context.Background(), reservation{}, true)
	if settled.NativeResponsesPreferred {
		t.Fatal("a provider with no Responses endpoint was sent back to it")
	}
}

// TestRunAttemptDeliversARepairableRejectionOnceTheBudgetIsSpent covers the end
// of the retry budget: the caller must still receive the provider's own words
// rather than a gateway-invented error.
func TestRunAttemptDeliversARepairableRejectionOnceTheBudgetIsSpent(t *testing.T) {
	upstream := upstreamRejecting(t, http.StatusBadRequest, azureResponsesDemand)
	server, candidate, plan := chatAttempt(t, upstream.URL)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req := dispatchRequest{RequestID: "req_2", PublicMode: messageModeChat, Alias: "azure/gpt-5.6-sol", Endpoint: "/v1/chat/completions"}
	outcome := server.runAttempt(recorder, request, req, candidate, plan,
		upstream.Client(), context.Background(), reservation{}, false)

	if !outcome.Done || outcome.NativeResponsesPreferred {
		t.Fatalf("a spent budget still tried to repair the request: %#v", outcome)
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "use /v1/responses") {
		t.Fatalf("the provider's own rejection did not reach the caller: %q", recorder.Body.String())
	}
	// The endpoint was not switched, so the record must not claim it was.
	if outcome.Record.SwitchedEndpoint != "" {
		t.Fatalf("attempt record claimed a switch that never happened: %#v", outcome.Record)
	}
}

// TestRunAttemptSparesACredentialFromARequestShapeRejection is the guard for the
// second half of the bug: a 400 the gateway can explain is the request's fault,
// so the key that carried it must not collect a failure strike. Without this a
// caller repeating one unsupported parameter could put every healthy key in the
// pool into cooldown.
func TestRunAttemptSparesACredentialFromARequestShapeRejection(t *testing.T) {
	client := integrationRedis(t)
	upstream := upstreamRejecting(t, http.StatusBadRequest, azureResponsesDemand)
	server, candidate, plan := chatAttempt(t, upstream.URL)
	server.redis = client
	candidate.Credential.CredentialView.ID = fmt.Sprintf("cre_shape_%d", time.Now().UnixNano())
	failures := "failures:" + candidate.Credential.ID
	t.Cleanup(func() { _ = client.Del(context.Background(), failures).Err() })

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
	req := dispatchRequest{RequestID: "req_3", PublicMode: messageModeChat, Alias: "azure/gpt-5.6-sol", Endpoint: "/v1/chat/completions"}
	// The budget is spent, so this rejection is delivered rather than repaired —
	// and that is exactly the path that used to strike the credential.
	server.runAttempt(httptest.NewRecorder(), request, req, candidate, plan,
		upstream.Client(), context.Background(), reservation{}, false)
	if count, err := client.Exists(context.Background(), failures).Result(); err != nil || count != 0 {
		t.Fatalf("a recognised request-shape 400 struck the credential (exists=%d, err=%v)", count, err)
	}

	// A 400 the gateway cannot explain is still charged to the key, because it may
	// genuinely be the key that the provider is objecting to.
	opaque := upstreamRejecting(t, http.StatusBadRequest, `{"error":{"message":"something went wrong"}}`)
	candidate.Route.Provider.BaseURL = opaque.URL
	server.runAttempt(httptest.NewRecorder(), request, req, candidate, plan,
		opaque.Client(), context.Background(), reservation{}, false)
	if count, err := client.Exists(context.Background(), failures).Result(); err != nil || count != 1 {
		t.Fatalf("an unexplained 400 was not charged to the credential (exists=%d, err=%v)", count, err)
	}
}

// TestResponsesEndpointPreferredSurvivesARestart pins the learned preference to
// Redis: the point of remembering it is that the next request, in another
// process, starts at the right endpoint instead of paying the same 400 again.
func TestResponsesEndpointPreferredSurvivesARestart(t *testing.T) {
	client := integrationRedis(t)
	server := &Server{redis: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	modelID := fmt.Sprintf("mdl_prefer_%d", time.Now().UnixNano())
	ctx := context.Background()
	t.Cleanup(func() { _ = client.Del(ctx, responsesPreferredKey(modelID)).Err() })

	if server.responsesEndpointPreferred(ctx, []string{modelID})[modelID] {
		t.Fatal("an unseen route already preferred the Responses endpoint")
	}
	server.rememberResponsesEndpointPreferred(ctx, modelID)
	if !server.responsesEndpointPreferred(ctx, []string{modelID})[modelID] {
		t.Fatal("the learned preference was not reported back")
	}
	ttl, err := client.TTL(ctx, responsesPreferredKey(modelID)).Result()
	if err != nil || ttl <= 0 || ttl > adaptiveCompatibilityTTL {
		t.Fatalf("preference TTL = %v, %v", ttl, err)
	}
	// Editing or re-probing the route must clear it, so a corrected configuration
	// is not overruled by yesterday's error message.
	server.forgetResponsesEndpointPreferred(ctx, modelID)
	if server.responsesEndpointPreferred(ctx, []string{modelID})[modelID] {
		t.Fatal("the preference outlived the route edit that should have cleared it")
	}
}
