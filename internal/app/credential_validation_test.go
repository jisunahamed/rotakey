package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInspectProviderSecretLoadsModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer valid-key" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{
			"object":"list",
			"data":[
				{"id":"z/model","owned_by":"z"},
				{"id":"a/model","owned_by":"a"},
				{"id":"a/model","owned_by":"duplicate"}
			]
		}`))
		case "/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"a"}}]}`))
		default:
			t.Fatalf("unexpected path = %q", r.URL.Path)
		}
	}))
	defer upstream.Close()

	result := inspectProviderSecret(context.Background(), Provider{
		BaseURL: upstream.URL + "/v1", AuthHeader: "Authorization",
		AuthScheme: "Bearer", TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}, []byte("valid-key"))
	if !result.Valid || !result.ProtocolVerified || result.StatusCode != http.StatusOK {
		t.Fatalf("inspection = %#v", result)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "a/model" || result.Models[1].ID != "z/model" {
		t.Fatalf("models = %#v", result.Models)
	}
}

func TestInspectProviderSecretRejectsProtocolMismatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-test"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":"message","content":[{"type":"text","text":"a"}]}`))
	}))
	defer upstream.Close()

	result := inspectProviderSecret(context.Background(), Provider{
		BaseURL: upstream.URL + "/v1", APIFormat: "openai", AuthHeader: "Authorization",
		AuthScheme: "Bearer", TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}, []byte("valid-key"))
	if result.Valid || result.DetectedProtocol != "anthropic" || result.Warning == "" {
		t.Fatalf("inspection = %#v", result)
	}
}

func TestInspectProviderSecretDoesNotVerifyOpenAIErrorEnvelope(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"missing-model"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model was not found","code":"model_not_found"}}`))
	}))
	defer upstream.Close()

	result := inspectProviderSecret(context.Background(), Provider{
		BaseURL: upstream.URL + "/v1", APIFormat: "openai", AuthHeader: "Authorization",
		AuthScheme: "Bearer", TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}, []byte("valid-key"))
	if result.Valid || result.ProtocolVerified || result.StatusCode != http.StatusNotFound {
		t.Fatalf("inspection = %#v", result)
	}
	if result.DetectedProtocol != "openai" || result.Warning == "" {
		t.Fatalf("warning did not preserve protocol diagnosis: %#v", result)
	}
}

func TestInspectNVIDIAProviderUsesCatalogAndDefersRouteProbe(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"catalog-only-model"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"model is not available for inference"}`))
	}))
	defer upstream.Close()

	result := inspectProviderSecret(context.Background(), Provider{
		BaseURL: upstream.URL + "/v1", APIFormat: "openai", AuthHeader: "Authorization",
		AuthScheme: "Bearer", TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}, []byte("valid-key"))
	if result.Valid {
		t.Fatal("ordinary provider with an unavailable probe model must not be accepted")
	}

	// The public NVIDIA hostname takes the catalog-only validation path. The
	// hostname predicate is covered separately to keep this test local.
	if !isNVIDIAOpenAIProvider(Provider{BaseURL: "https://integrate.api.nvidia.com/v1", APIFormat: "openai"}) {
		t.Fatal("NVIDIA compatibility URL was not recognized")
	}
}

func TestVerifyProviderProtocolExplainsUnknownOpenAI404(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"model missing"}`))
	}))
	defer upstream.Close()
	provider := Provider{BaseURL: upstream.URL, APIFormat: "openai", TimeoutSeconds: 5, AllowPrivateNetwork: true}
	client, err := upstreamClient(provider)
	if err != nil {
		t.Fatal(err)
	}
	verified, _, status, warning := verifyProviderProtocol(context.Background(), client, provider, []byte("key"), "missing")
	if verified || status != http.StatusNotFound {
		t.Fatalf("result = verified=%v status=%d", verified, status)
	}
	if !strings.Contains(warning, "protocol probe returned HTTP 404") || !strings.Contains(warning, "model missing") {
		t.Fatalf("warning = %q", warning)
	}
}

func TestInspectProviderSecretRejectsInvalidKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer upstream.Close()

	result := inspectProviderSecret(context.Background(), Provider{
		BaseURL: upstream.URL, TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}, []byte("invalid-key"))
	if result.Valid || result.StatusCode != http.StatusUnauthorized || result.Warning == "" {
		t.Fatalf("inspection = %#v", result)
	}
}

func TestMergeDiscoveredModels(t *testing.T) {
	models := mergeDiscoveredModels([]credentialInspection{
		{Models: []DiscoveredModel{{ID: "b"}, {ID: "a"}}},
		{Models: []DiscoveredModel{{ID: "a"}, {ID: "c"}}},
	})
	if len(models) != 3 || models[0].ID != "a" || models[1].ID != "b" || models[2].ID != "c" {
		t.Fatalf("models = %#v", models)
	}
}

func TestModelCapabilityProfileUsesProtocolTranslation(t *testing.T) {
	anthropicInput := modelInput{SupportsChat: false, SupportsResponses: true, SupportsMessages: false}
	anthropic := modelCapabilityProfile(Provider{APIFormat: "anthropic"}, &anthropicInput, "probe")
	if !anthropicInput.SupportsChat || !anthropicInput.SupportsMessages || anthropicInput.SupportsResponses {
		t.Fatalf("Anthropic route flags = %#v", anthropicInput)
	}
	if anthropic["chat"] != "translated" || anthropic["messages"] != "native" || anthropic["streaming"] != "gateway_normalized" {
		t.Fatalf("Anthropic capabilities = %#v", anthropic)
	}

	openAIInput := modelInput{SupportsChat: true, SupportsResponses: false, SupportsMessages: false}
	openAI := modelCapabilityProfile(Provider{APIFormat: "openai"}, &openAIInput, "catalog")
	if !openAIInput.SupportsMessages || openAI["chat"] != "native" || openAI["messages"] != "translated" || openAI["availability"] != "catalog_visible" {
		t.Fatalf("OpenAI capabilities = %#v / %#v", openAI, openAIInput)
	}
}

func TestIsGeminiOpenAIProvider(t *testing.T) {
	valid := []Provider{
		{APIFormat: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai/"},
		{BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai"},
	}
	for _, provider := range valid {
		if !isGeminiOpenAIProvider(provider) {
			t.Fatalf("Gemini provider was not recognized: %#v", provider)
		}
	}
	invalid := []Provider{
		{APIFormat: "anthropic", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai/"},
		{APIFormat: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta"},
		{APIFormat: "openai", BaseURL: "https://example.com/v1beta/openai/"},
	}
	for _, provider := range invalid {
		if isGeminiOpenAIProvider(provider) {
			t.Fatalf("non-Gemini provider was recognized: %#v", provider)
		}
	}
}

func TestUpstreamModelForGeminiOpenAIProvider(t *testing.T) {
	provider := Provider{APIFormat: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai/"}
	if got := upstreamModelForProvider(provider, "models/gemini-2.5-flash"); got != "gemini-2.5-flash" {
		t.Fatalf("Gemini upstream model = %q", got)
	}
	if got := upstreamModelForProvider(provider, "gemini-2.5-flash"); got != "gemini-2.5-flash" {
		t.Fatalf("Gemini upstream model without prefix = %q", got)
	}
	other := Provider{APIFormat: "openai", BaseURL: "https://example.com/v1"}
	if got := upstreamModelForProvider(other, "models/gemini-2.5-flash"); got != "models/gemini-2.5-flash" {
		t.Fatalf("non-Gemini upstream model changed to %q", got)
	}
}

func TestProbeProviderModelWithSecretBoundsOpenAIOutput(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["max_tokens"] != float64(1) {
			t.Fatalf("max_tokens = %#v", payload["max_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"a"}}]}`))
	}))
	defer upstream.Close()

	input := modelInput{UpstreamModel: "large-model", SupportsChat: true}
	status, profile, checkedAt, statusCode, err := probeProviderModelWithSecret(context.Background(), Provider{
		BaseURL: upstream.URL + "/v1", APIFormat: "openai", AuthHeader: "Authorization",
		AuthScheme: "Bearer", TimeoutSeconds: 60, AllowPrivateNetwork: true,
	}, &input, []byte("valid-key"))
	if err != nil || status != "probe_verified" || statusCode != http.StatusOK || checkedAt == nil {
		t.Fatalf("probe = status=%q profile=%#v checked=%v code=%d err=%v", status, profile, checkedAt, statusCode, err)
	}
}

func TestModelProbeTimeoutHonorsProviderLimit(t *testing.T) {
	for _, test := range []struct {
		seconds int
		want    time.Duration
	}{{0, 15 * time.Second}, {60, time.Minute}, {120, 2 * time.Minute}, {600, 2 * time.Minute}} {
		if got := modelProbeTimeout(Provider{TimeoutSeconds: test.seconds}); got != test.want {
			t.Fatalf("timeout(%d) = %s, want %s", test.seconds, got, test.want)
		}
	}
}

func TestRetryModelProbeWithAnotherCredential(t *testing.T) {
	for _, status := range []int{0, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway} {
		if !retryModelProbeWithAnotherCredential(status) {
			t.Fatalf("status %d should try another credential", status)
		}
	}
	for _, status := range []int{-1, http.StatusBadRequest, http.StatusNotFound, http.StatusPaymentRequired} {
		if retryModelProbeWithAnotherCredential(status) {
			t.Fatalf("status %d should preserve the model-specific failure", status)
		}
	}
}
