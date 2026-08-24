package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func exportFixture() Provider {
	cost := 1.5
	return Provider{
		ID: "p1", Name: "Azure", Slug: "azure", BaseURL: "https://azure.example.com/v1",
		AuthHeader: "Authorization", AuthScheme: "Bearer", TimeoutSeconds: 120,
		Enabled: true, APIFormat: "openai", AnthropicVersion: "2023-06-01",
		Models: []ModelRoute{
			{
				ID: "m2", ProviderID: "p1", PublicAlias: "sonnet-5", UpstreamModel: "sonnet",
				SupportsChat: true, DefaultMaxOutputTokens: 2048, Tokenizer: "heuristic",
				CapabilityStatus: "probe_verified", Enabled: true,
			},
			{
				ID: "m1", ProviderID: "p1", PublicAlias: "opus-5", UpstreamModel: "opus",
				SupportsChat: true, SupportsMessages: true, DefaultMaxOutputTokens: 1024,
				InputCostPerMillionUSD: 3, OutputCostPerMillionUSD: 15, RequestCostUSD: &cost,
				Tokenizer: "o200k_base", StripParameters: []string{"thinking"},
				CapabilityStatus: "catalog_verified", Enabled: true,
			},
		},
		Credentials: []CredentialView{
			{
				ID: "k2", Label: "spare", Enabled: true,
				Limits: RatePolicy{}, ModelLimits: map[string]RatePolicy{},
			},
			{
				ID: "k1", Label: "primary", IsPrimary: true, Enabled: true,
				Limits:      RatePolicy{RPM: ptr64(60)},
				ModelLimits: map[string]RatePolicy{"m1": {TPM: ptr64(1000)}, "gone": {TPM: ptr64(5)}},
			},
		},
	}
}

func ptr64(value int64) *int64 { return &value }

// TestExportProviderIsStableAndAliasKeyed pins the two properties that make a
// bundle replayable elsewhere: a deterministic order, and per-model limits
// addressed by public alias instead of the local database ID.
func TestExportProviderIsStableAndAliasKeyed(t *testing.T) {
	provider := exportFixture()
	aliases := map[string]string{"m1": "opus-5", "m2": "sonnet-5"}
	secrets := map[string]string{"k1": "sk-primary-value", "k2": "sk-spare-value"}

	exported := exportProvider(provider, aliases, secrets, true)
	if exported.Models[0].PublicAlias != "opus-5" || exported.Models[1].PublicAlias != "sonnet-5" {
		t.Fatalf("models were not sorted by alias: %#v", exported.Models)
	}
	if exported.Credentials[0].Label != "primary" || exported.Credentials[1].Label != "spare" {
		t.Fatalf("credentials were not sorted by label: %#v", exported.Credentials)
	}
	if got := exported.Credentials[0].Secret; got != "sk-primary-value" {
		t.Fatalf("secret = %q, want the decrypted value", got)
	}
	limits := exported.Credentials[0].ModelLimits
	if _, keyed := limits["opus-5"]; !keyed {
		t.Fatalf("model limits were not re-keyed by alias: %#v", limits)
	}
	// A limit pointing at a model that is not being exported would be
	// unresolvable on import, so it is dropped rather than carried as a dead ID.
	if _, kept := limits["gone"]; kept {
		t.Fatalf("a limit for an unexported model survived: %#v", limits)
	}
	if len(limits) != 1 {
		t.Fatalf("model limits = %#v, want exactly one entry", limits)
	}
}

// TestExportProviderOmitsSecretsWhenNotRequested is the guard for the shareable
// bundle: everything else is present, only the key values are gone.
func TestExportProviderOmitsSecretsWhenNotRequested(t *testing.T) {
	exported := exportProvider(exportFixture(), map[string]string{"m1": "opus-5"}, map[string]string{"k1": "sk-live"}, false)
	for _, credential := range exported.Credentials {
		if credential.Secret != "" {
			t.Fatalf("credential %q leaked a secret in a keyless export", credential.Label)
		}
	}
	if len(exported.Models) != 2 {
		t.Fatalf("a keyless export dropped model routes: %#v", exported.Models)
	}
	body, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "sk-live") {
		t.Fatalf("the encoded bundle still contains the secret: %s", body)
	}
}

func TestExportBundleRoundTripsThroughValidation(t *testing.T) {
	bundle := ExportBundle{
		Kind: exportKind, Version: exportVersion, ExportedAt: time.Now().UTC(),
		RoutingMode: routingModeModel,
		Settings: ExportSettings{
			MetadataRetentionDays: 90, BodyRetentionDays: 30,
			MaxWaitMS: 5000, DefaultProviderTimeoutSecs: 120,
		},
		Providers: []ExportProvider{exportProvider(exportFixture(), map[string]string{"m1": "opus-5", "m2": "sonnet-5"}, map[string]string{"k1": "sk-primary-value", "k2": "sk-spare-value"}, true)},
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded ExportBundle
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := validateImportBundle(&decoded); err != nil {
		t.Fatalf("a freshly exported bundle failed import validation: %v", err)
	}
	if decoded.RoutingMode != routingModeModel {
		t.Fatalf("routing mode = %q", decoded.RoutingMode)
	}
	if len(decoded.Providers[0].Models) != 2 || len(decoded.Providers[0].Credentials) != 2 {
		t.Fatalf("the round trip lost children: %#v", decoded.Providers[0])
	}
}
