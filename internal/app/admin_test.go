package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderSlugFromName(t *testing.T) {
	tests := map[string]string{
		"NVIDIA":              "nvidia",
		"Groq Production":     "groq-production",
		"  OpenAI / Primary ": "openai-primary",
		"🤖":                   "provider",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := providerSlugFromName(input); got != want {
				t.Fatalf("providerSlugFromName(%q) = %q, want %q", input, got, want)
			}
		})
	}

	long := providerSlugFromName(strings.Repeat("provider-", 20))
	if len(long) > 63 || !slugPattern.MatchString(long) {
		t.Fatalf("long generated slug %q is invalid", long)
	}
}

func TestProviderSlugCandidate(t *testing.T) {
	if got := providerSlugCandidate("nvidia", 0); got != "nvidia" {
		t.Fatalf("first candidate = %q", got)
	}
	if got := providerSlugCandidate("nvidia", 1); got != "nvidia-2" {
		t.Fatalf("duplicate candidate = %q", got)
	}
	long := providerSlugCandidate(strings.Repeat("a", 63), 9)
	if len(long) > 63 || !slugPattern.MatchString(long) {
		t.Fatalf("long candidate %q is invalid", long)
	}
}

func TestProviderJSONIncludesEmptyCollections(t *testing.T) {
	raw, err := json.Marshal(Provider{
		Models:      []ModelRoute{},
		Credentials: []CredentialView{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"models", "credentials"} {
		value, exists := decoded[field]
		if !exists {
			t.Fatalf("%s is missing from provider JSON: %s", field, raw)
		}
		items, ok := value.([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("%s = %#v, want an empty array", field, value)
		}
	}
}

func TestCredentialSelectionOrder(t *testing.T) {
	credentials := []credentialRuntime{
		{CredentialView: CredentialView{ID: "one"}},
		{CredentialView: CredentialView{ID: "two"}},
		{CredentialView: CredentialView{ID: "three"}},
	}
	assertOrder := func(want []int, cursor int64) {
		t.Helper()
		got := credentialSelectionOrder(credentials, cursor)
		if len(got) != len(want) {
			t.Fatalf("cursor %d order = %v, want %v", cursor, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("cursor %d order = %v, want %v", cursor, got, want)
			}
		}
	}
	assertOrder([]int{0, 1, 2}, 1)
	assertOrder([]int{1, 2, 0}, 2)

	credentials[1].IsPrimary = true
	assertOrder([]int{1, 0, 2}, 1)
	assertOrder([]int{1, 2, 0}, 2)
}

func TestValidateModelCompatibilityParameters(t *testing.T) {
	input := modelInput{
		PublicAlias:     "nvidia/deepseek",
		UpstreamModel:   "deepseek-ai/deepseek-v4-flash",
		SupportsChat:    true,
		StripParameters: []string{" thinking ", "thinking", "reasoning_effort"},
	}
	if err := validateModelInput(&input); err != nil {
		t.Fatal(err)
	}
	if len(input.StripParameters) != 2 || input.StripParameters[0] != "thinking" {
		t.Fatalf("strip parameters = %#v", input.StripParameters)
	}
	input.StripParameters = []string{"messages"}
	if err := validateModelInput(&input); err == nil {
		t.Fatal("protected messages field should not be removable")
	}
}

func TestStripTopLevelParameters(t *testing.T) {
	payload := map[string]any{
		"model": "deepseek", "messages": []any{}, "thinking": map[string]any{"type": "enabled"},
	}
	stripped := stripTopLevelParameters(payload, []string{"thinking", "missing"})
	if len(stripped) != 1 || stripped[0] != "thinking" {
		t.Fatalf("stripped = %#v", stripped)
	}
	if _, exists := payload["thinking"]; exists {
		t.Fatal("thinking parameter was not removed")
	}
	if _, exists := payload["messages"]; !exists {
		t.Fatal("messages parameter was removed")
	}
}
