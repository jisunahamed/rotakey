package app

import "testing"

func importFixture() ExportBundle {
	return ExportBundle{
		Kind: exportKind, Version: exportVersion, RoutingMode: routingModeModel,
		Settings: ExportSettings{
			MetadataRetentionDays: 90, BodyRetentionDays: 30,
			MaxWaitMS: 5000, DefaultProviderTimeoutSecs: 120,
		},
		Providers: []ExportProvider{{
			Name: "Azure", Slug: "azure", BaseURL: "https://azure.example.com/v1",
			AuthHeader: "Authorization", AuthScheme: "Bearer", TimeoutSeconds: 120,
			Enabled: true, APIFormat: "openai",
			Models: []ExportModel{{
				PublicAlias: "opus-5", UpstreamModel: "opus", SupportsChat: true,
				DefaultMaxOutputTokens: 1024, Tokenizer: "heuristic", Enabled: true,
				CapabilityStatus: "probe_verified",
			}},
			Credentials: []ExportCredential{{
				Label: "primary", Secret: "sk-primary-value", IsPrimary: true, Enabled: true,
			}},
		}},
	}
}

func TestValidateImportBundleAcceptsAWholeSetup(t *testing.T) {
	bundle := importFixture()
	if err := validateImportBundle(&bundle); err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}
	if bundle.Providers[0].AnthropicVersion != "2023-06-01" {
		t.Fatalf("the Anthropic version default was not filled in: %q", bundle.Providers[0].AnthropicVersion)
	}
}

func TestValidateImportBundleFillsSettingDefaults(t *testing.T) {
	bundle := importFixture()
	bundle.Settings = ExportSettings{}
	if err := validateImportBundle(&bundle); err != nil {
		t.Fatalf("a bundle with empty settings was rejected: %v", err)
	}
	if bundle.Settings.MetadataRetentionDays != 90 || bundle.Settings.BodyRetentionDays != 30 ||
		bundle.Settings.DefaultProviderTimeoutSecs != 120 {
		t.Fatalf("settings defaults = %#v", bundle.Settings)
	}
}

// TestValidateImportBundleDowngradesUnearnedCapabilities is the guard against a
// hand-edited bundle marking an unprobed route as live: routeFilter only serves
// verified statuses, so anything else must land as unverified.
func TestValidateImportBundleDowngradesUnearnedCapabilities(t *testing.T) {
	bundle := importFixture()
	bundle.Providers[0].Models[0].CapabilityStatus = "totally_fine_trust_me"
	if err := validateImportBundle(&bundle); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := bundle.Providers[0].Models[0].CapabilityStatus; got != "unverified" {
		t.Fatalf("capability status = %q, want unverified", got)
	}
	for _, status := range []string{"catalog_verified", "probe_verified"} {
		bundle.Providers[0].Models[0].CapabilityStatus = status
		if err := validateImportBundle(&bundle); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if got := bundle.Providers[0].Models[0].CapabilityStatus; got != status {
			t.Fatalf("a legitimately verified status was downgraded: %q", got)
		}
	}
}

func TestValidateImportBundleRejectsBrokenBundles(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ExportBundle)
	}{
		{"unknown routing mode", func(b *ExportBundle) { b.RoutingMode = "pool" }},
		{"private base URL", func(b *ExportBundle) { b.Providers[0].BaseURL = "https://127.0.0.1/v1" }},
		{"plain HTTP base URL", func(b *ExportBundle) { b.Providers[0].BaseURL = "http://azure.example.com/v1" }},
		{"unsupported api format", func(b *ExportBundle) { b.Providers[0].APIFormat = "gemini" }},
		{"duplicate provider slug", func(b *ExportBundle) {
			b.Providers = append(b.Providers, b.Providers[0])
		}},
		{"duplicate alias on one provider", func(b *ExportBundle) {
			b.Providers[0].Models = append(b.Providers[0].Models, b.Providers[0].Models[0])
		}},
		{"duplicate credential label", func(b *ExportBundle) {
			b.Providers[0].Credentials = append(b.Providers[0].Credentials, b.Providers[0].Credentials[0])
		}},
		{"model with no endpoint", func(b *ExportBundle) {
			b.Providers[0].Models[0].SupportsChat = false
		}},
		{"negative rate limit", func(b *ExportBundle) {
			zero := int64(0)
			b.Providers[0].Credentials[0].Limits = RatePolicy{RPM: &zero}
		}},
		{"invalid per-model limit", func(b *ExportBundle) {
			negative := int64(-1)
			b.Providers[0].Credentials[0].ModelLimits = map[string]RatePolicy{"opus-5": {TPM: &negative}}
		}},
		{"secret too short", func(b *ExportBundle) { b.Providers[0].Credentials[0].Secret = "short" }},
		{"retention out of range", func(b *ExportBundle) { b.Settings.BodyRetentionDays = 4000 }},
		{"forbidden extra header", func(b *ExportBundle) {
			b.Providers[0].ExtraHeaders = map[string]string{"Authorization": "Bearer leaked"}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bundle := importFixture()
			test.mutate(&bundle)
			if err := validateImportBundle(&bundle); err == nil {
				t.Fatal("a broken bundle was accepted")
			}
		})
	}
}

// TestValidateImportBundleAllowsAKeylessBundle covers the shareable export: the
// providers and routes still validate with no secret present.
func TestValidateImportBundleAllowsAKeylessBundle(t *testing.T) {
	bundle := importFixture()
	bundle.IncludesSecrets = false
	bundle.Providers[0].Credentials[0].Secret = ""
	if err := validateImportBundle(&bundle); err != nil {
		t.Fatalf("a keyless bundle was rejected: %v", err)
	}
}
