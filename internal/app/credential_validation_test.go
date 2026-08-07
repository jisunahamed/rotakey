package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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
