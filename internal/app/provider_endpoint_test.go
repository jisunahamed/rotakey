package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderEndpointOverridesModelAndLearnedRouting(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat"} {
		for _, mode := range []string{messageModeChat, messageModeResponses, messageModeAnthropic} {
			t.Run(endpoint+"/"+mode, func(t *testing.T) {
				payload := map[string]any{"model": "alias", "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": "hello"}}}
				if mode == messageModeResponses {
					payload = map[string]any{"model": "alias", "input": "hello", "max_output_tokens": 32}
				}
				route := routeRuntime{Provider: Provider{APIFormat: "openai", InferenceEndpoint: endpoint}, Model: ModelRoute{ID: "test", UpstreamModel: "test-model", SupportsChat: endpoint == "responses", SupportsResponses: endpoint == "chat", DefaultMaxOutputTokens: 32}}
				s := &Server{redis: unreachableRedis()}
				t.Cleanup(func() { _ = s.redis.Close() })
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				state := dispatchState{NativeResponsesUnavailable: map[string]bool{"test": true}, PreferNativeResponses: map[string]bool{"test": true}}
				plan, err := s.buildPlan(ctx, dispatchRequest{PublicMode: mode, Public: payload}, route, state)
				if err != nil {
					t.Fatal(err)
				}
				want := "/responses"
				if endpoint == "chat" {
					want = "/chat/completions"
				}
				if plan.Path != want {
					t.Fatalf("path %s want %s", plan.Path, want)
				}
				if endpoint == "responses" && plan.Payload["input"] == nil {
					t.Fatal("missing translated input")
				}
				if endpoint == "chat" && plan.Payload["messages"] == nil {
					t.Fatal("missing translated messages")
				}
			})
		}
	}
}

func TestProviderResponsesProtocolAndModelProbes(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/responses" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["input"] == nil || body["messages"] != nil || body["max_output_tokens"] != float64(16) {
			t.Error(body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a"}]}]}`))
	}))
	defer upstream.Close()
	provider := Provider{BaseURL: upstream.URL + "/v1", APIFormat: "openai", InferenceEndpoint: "responses", AllowPrivateNetwork: true, TimeoutSeconds: 5, AuthHeader: "Authorization", AuthScheme: "Bearer"}
	client, err := upstreamClient(provider)
	if err != nil {
		t.Fatal(err)
	}
	valid, protocol, status, warning := verifyProviderProtocol(context.Background(), client, provider, []byte("test-secret"), "test-model")
	if !valid || protocol != "openai" || status != 200 {
		t.Fatal(valid, protocol, status, warning)
	}
	input := modelInput{UpstreamModel: "test-model", SupportsChat: true}
	state, profile, _, status, err := probeProviderModelWithSecret(context.Background(), provider, &input, []byte("test-secret"))
	if err != nil || state != "probe_verified" || status != 200 || profile["responses"] != "native" || input.SupportsChat || !input.SupportsResponses {
		t.Fatal(state, profile, status, err)
	}
	if calls != 2 {
		t.Fatalf("unexpected extra probe: %d", calls)
	}
}

func TestProviderEndpointLeavesAudioAndEmbeddingsNative(t *testing.T) {
	provider := Provider{APIFormat: "openai", InferenceEndpoint: "responses"}
	input := modelInput{UpstreamModel: "embedding", SupportsEmbeddings: true}
	applyProviderInferenceEndpoint(provider, &input)
	if !input.SupportsEmbeddings || input.SupportsResponses {
		t.Fatal(input)
	}
	audio := modelInput{UpstreamModel: "whisper-large-v3"}
	profile := modelCapabilityProfile(provider, &audio, "catalog")
	if profile["audio/transcriptions"] != "native" || audio.SupportsResponses {
		t.Fatal(profile)
	}
}

func TestProviderEndpointConfigurationValidationAndExport(t *testing.T) {
	for _, value := range []string{"auto", "chat", "responses", "invalid"} {
		input := providerInput{Name: "Test", BaseURL: "https://provider.example.com/v1", APIFormat: "openai", InferenceEndpoint: &value}
		err := validateProviderInput(&input)
		if (err != nil) != (value == "invalid") {
			t.Fatalf("%s: %v", value, err)
		}
	}
	provider := exportFixture()
	provider.InferenceEndpoint = "responses"
	exported := exportProvider(provider, map[string]string{}, map[string]string{}, false)
	if exported.InferenceEndpoint == nil || *exported.InferenceEndpoint != "responses" {
		t.Fatal("provider endpoint lost during export")
	}
}

func TestIntegrationProviderResponsesOnlyDoesNotFallbackToChat(t *testing.T) {
	var paths []string
	s, id, _, call := queueFixture(t, 0, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"message":"endpoint missing"}}`))
	})
	if _, err := s.db.Exec(context.Background(), `UPDATE providers SET inference_endpoint='responses' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	w := call(context.Background(), false)
	if w.Code != 404 || len(paths) != 1 || paths[0] != "/responses" {
		t.Fatal(w.Code, paths, w.Body.String())
	}
}

func TestIntegrationProviderEndpointPersistenceAndOldPayload(t *testing.T) {
	s, id, _, _ := queueFixture(t, 0, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(queueAnswer))
	})
	update := func(endpoint *string) {
		provider, err := scanProvider(s.db.QueryRow(context.Background(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, id))
		if err != nil {
			t.Fatal(err)
		}
		input := providerInput{Name: provider.Name, Slug: "endpoint-test", BaseURL: provider.BaseURL, AuthHeader: provider.AuthHeader, AuthScheme: provider.AuthScheme, ExtraHeaders: provider.ExtraHeaders, TimeoutSeconds: provider.TimeoutSeconds, Enabled: provider.Enabled, AllowPrivateNetwork: true, APIFormat: "openai", InferenceEndpoint: endpoint}
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest("PUT", "/api/admin/providers/"+id, strings.NewReader(string(raw)))
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleUpdateProvider(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	endpoint := "responses"
	update(&endpoint)
	update(nil)
	p, err := scanProvider(s.db.QueryRow(context.Background(), `SELECT `+providerColumns+` FROM providers WHERE id=$1`, id))
	if err != nil || p.InferenceEndpoint != "responses" {
		t.Fatal(p, err)
	}
	routes, err := s.resolveRoutes(context.Background(), id+"m", routingModeModel)
	if err != nil || routes[0].Provider.InferenceEndpoint != "responses" {
		t.Fatal(routes, err)
	}
}
