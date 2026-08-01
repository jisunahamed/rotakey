package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInspectProviderSecretLoadsModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer valid-key" {
			t.Fatalf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"object":"list",
			"data":[
				{"id":"z/model","owned_by":"z"},
				{"id":"a/model","owned_by":"a"},
				{"id":"a/model","owned_by":"duplicate"}
			]
		}`))
	}))
	defer upstream.Close()

	result := inspectProviderSecret(context.Background(), Provider{
		BaseURL: upstream.URL + "/v1", AuthHeader: "Authorization",
		AuthScheme: "Bearer", TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}, []byte("valid-key"))
	if !result.Valid || result.StatusCode != http.StatusOK {
		t.Fatalf("inspection = %#v", result)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "a/model" || result.Models[1].ID != "z/model" {
		t.Fatalf("models = %#v", result.Models)
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
