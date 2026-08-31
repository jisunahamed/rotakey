package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKeepCatalogRouteAvailable(t *testing.T) {
	profile, err := json.Marshal(map[string]string{"verification": "catalog", "availability": "catalog_visible"})
	if err != nil {
		t.Fatal(err)
	}
	if !keepCatalogRouteAvailable("catalog_verified", nil) {
		t.Fatal("catalog-verified route was not retained")
	}
	if !keepCatalogRouteAvailable("failed", profile) {
		t.Fatal("failed route with catalog origin was not retained")
	}
	if keepCatalogRouteAvailable("failed", []byte(`{"verification":"probe"}`)) {
		t.Fatal("probe-only failed route was retained")
	}
}

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

func TestNormalizeGeminiCompatibilityURL(t *testing.T) {
	want := "https://generativelanguage.googleapis.com/v1beta/openai/"
	for _, input := range []string{
		"https://generativelanguage.googleapis.com/v1beta",
		"https://generativelanguage.googleapis.com/v1beta/",
	} {
		if got := normalizeProviderCompatibilityURL(input, "openai"); got != want {
			t.Fatalf("normalizeProviderCompatibilityURL(%q) = %q, want %q", input, got, want)
		}
	}
	if got := normalizeProviderCompatibilityURL(want, "openai"); got != want {
		t.Fatalf("compatible URL changed to %q", got)
	}
	native := "https://generativelanguage.googleapis.com/v1beta"
	if got := normalizeProviderCompatibilityURL(native, "anthropic"); got != native {
		t.Fatalf("Anthropic provider URL changed to %q", got)
	}
}

func TestNormalizeOfficialProviderCompatibilityURLs(t *testing.T) {
	tests := []struct {
		name, input, protocol, want string
	}{
		{"openai root", "https://api.openai.com", "openai", "https://api.openai.com/v1"},
		{"openai model endpoint", "https://api.openai.com/v1/models", "openai", "https://api.openai.com/v1"},
		{"openai chat endpoint", "https://api.openai.com/v1/chat/completions", "openai", "https://api.openai.com/v1"},
		{"anthropic root", "https://api.anthropic.com", "anthropic", "https://api.anthropic.com/v1"},
		{"anthropic model endpoint", "https://api.anthropic.com/v1/models", "anthropic", "https://api.anthropic.com/v1"},
		{"anthropic messages endpoint", "https://api.anthropic.com/v1/messages", "anthropic", "https://api.anthropic.com/v1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeProviderCompatibilityURL(test.input, test.protocol); got != test.want {
				t.Fatalf("normalizeProviderCompatibilityURL(%q, %q) = %q, want %q", test.input, test.protocol, got, test.want)
			}
		})
	}
}

// TestNormalizeAzureFoundryCompatibilityURLs covers what an operator actually
// pastes: Azure's portal shows a "Target URI" naming the exact endpoint and
// carrying an ?api-version= query. Left alone it produced a base URL that
// appended a second /messages and 404ed on every request.
func TestNormalizeAzureFoundryCompatibilityURLs(t *testing.T) {
	const foundry = "https://my-resource.services.ai.azure.com/anthropic/v1"
	const azureOpenAI = "https://my-resource.openai.azure.com/openai/v1"
	tests := []struct {
		name, input, protocol, want string
	}{
		{"foundry resource root", "https://my-resource.services.ai.azure.com", "anthropic", foundry},
		{"foundry anthropic prefix", "https://my-resource.services.ai.azure.com/anthropic", "anthropic", foundry},
		{"foundry base with slash", "https://my-resource.services.ai.azure.com/anthropic/v1/", "anthropic", foundry},
		{"foundry target uri", "https://my-resource.services.ai.azure.com/anthropic/v1/messages?api-version=2024-10-21", "anthropic", foundry},
		{"azure openai root", "https://my-resource.openai.azure.com", "openai", azureOpenAI},
		{"azure openai chat endpoint", "https://my-resource.openai.azure.com/openai/v1/chat/completions", "openai", azureOpenAI},
		{"azure openai responses endpoint", "https://my-resource.openai.azure.com/openai/v1/responses?api-version=preview", "openai", azureOpenAI},
		// The protocols do not borrow each other's prefix, and an unfamiliar path
		// is left exactly as typed rather than guessed at.
		{"foundry anthropic path under openai format", "https://my-resource.services.ai.azure.com/anthropic/v1", "openai", "https://my-resource.services.ai.azure.com/anthropic/v1"},
		{"unknown azure path", "https://my-resource.services.ai.azure.com/models/chat", "anthropic", "https://my-resource.services.ai.azure.com/models/chat"},
		{"unrelated host", "https://example.com/anthropic/v1/messages?api-version=1", "anthropic", "https://example.com/anthropic/v1/messages?api-version=1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeProviderCompatibilityURL(test.input, test.protocol); got != test.want {
				t.Fatalf("normalizeProviderCompatibilityURL(%q, %q) = %q, want %q", test.input, test.protocol, got, test.want)
			}
		})
	}
}

// TestValidateProviderURLNamesTheQueryString guards the message an operator sees
// when a pasted Target URI is rejected: the generic "scheme, host, port, path"
// wording never said which part to delete.
func TestValidateProviderURLNamesTheQueryString(t *testing.T) {
	_, err := validateProviderURL("https://my-resource.services.ai.azure.com/anthropic/v1?api-version=2024-10-21", false)
	if err == nil {
		t.Fatal("a base URL with a query string was accepted")
	}
	if !strings.Contains(err.Error(), "query string") || !strings.Contains(err.Error(), "api-version") {
		t.Fatalf("query string rejection = %q", err)
	}
	// The other malformed-URL rejections keep their own wording.
	if _, err := validateProviderURL("https://user:pass@example.com/v1", false); err == nil ||
		strings.Contains(err.Error(), "query string") {
		t.Fatalf("userinfo rejection = %v", err)
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

func TestDuplicateCredentialInputsChecksSavedAndPastedSecrets(t *testing.T) {
	existing := []credentialSecretIdentity{
		{ID: "key_saved", Label: "Production", Secret: []byte("secret-existing")},
	}
	inputs := []credentialInput{
		{Label: "Key 2", Secret: "secret-existing"},
		{Label: "Key 3", Secret: "secret-new"},
		{Label: "Key 4", Secret: "secret-new"},
	}

	duplicates := duplicateCredentialInputs(inputs, existing, "")
	if duplicates[0] != "Production" {
		t.Fatalf("saved duplicate owner = %q, want Production", duplicates[0])
	}
	if _, duplicate := duplicates[1]; duplicate {
		t.Fatal("first new secret was marked duplicate")
	}
	if duplicates[2] != "Key 3" {
		t.Fatalf("pasted duplicate owner = %q, want Key 3", duplicates[2])
	}
}

func TestDuplicateCredentialInputsCanExcludeEditedCredential(t *testing.T) {
	existing := []credentialSecretIdentity{
		{ID: "key_current", Label: "Current", Secret: []byte("secret-current")},
		{ID: "key_other", Label: "Other", Secret: []byte("secret-other")},
	}
	inputs := []credentialInput{{Label: "Current", Secret: "secret-current"}}
	if duplicates := duplicateCredentialInputs(inputs, existing, "key_current"); len(duplicates) != 0 {
		t.Fatalf("unchanged edited key was marked duplicate: %#v", duplicates)
	}
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

// TestApplyBalanceRequiresAnAmount covers the setting that took a whole
// provider offline: a balance of zero means "spent", so applying a blank one to
// every existing key stopped them all from routing while they still looked fine.
func TestApplyBalanceRequiresAnAmount(t *testing.T) {
	base := providerInput{
		Name: "Test provider", BaseURL: "http://127.0.0.1:9000/v1",
		APIFormat: "openai", AuthHeader: "Authorization", AuthScheme: "Bearer",
		TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}
	blank := base
	blank.ApplyBalanceToExistingKeys = true
	if err := validateProviderInput(&blank); err == nil {
		t.Fatal("applying a balance with no amount was accepted")
	}

	zero := base
	zero.ApplyBalanceToExistingKeys = true
	zero.DefaultKeyBalanceUSD = usd(0)
	if err := validateProviderInput(&zero); err == nil {
		t.Fatal("applying a zero balance was accepted, which stops every key routing")
	}

	funded := base
	funded.ApplyBalanceToExistingKeys = true
	funded.DefaultKeyBalanceUSD = usd(25)
	if err := validateProviderInput(&funded); err != nil {
		t.Fatalf("a real top-up was refused: %v", err)
	}

	// Setting a per-key default without applying it is untouched: the zero there
	// only means "new keys start untracked", which is the shipped default.
	unapplied := base
	unapplied.DefaultKeyBalanceUSD = usd(0)
	if err := validateProviderInput(&unapplied); err != nil {
		t.Fatalf("a zero default that is not applied was refused: %v", err)
	}
}

// TestUnusableCredentialPredicateStaysNarrow pins the definition the delete
// button acts on. Widening it is how an operator ends up deleting keys that
// work: a saved validation note is also written for a key stored without a
// successful check or imported from a bundle, and cooldown clears itself.
func TestUnusableCredentialPredicateStaysNarrow(t *testing.T) {
	for _, fragment := range []string{"c.status = 'quarantined'", "c.balance_usd IS NOT NULL", "c.balance_usd - c.balance_spent_usd <= 0"} {
		if !strings.Contains(unusableCredentialPredicate, fragment) {
			t.Fatalf("the predicate no longer tests %q", fragment)
		}
	}
	for _, forbidden := range []string{"validation_error", "cooldown", "enabled"} {
		if strings.Contains(unusableCredentialPredicate, forbidden) {
			t.Fatalf("the predicate widened to include %q, which does not mean a key cannot serve", forbidden)
		}
	}
	// The SQL must agree with BalanceExhausted, which is what the router itself
	// uses, so the console, the router and the delete button count one pool.
	tracked := []struct {
		balance   *float64
		spent     float64
		exhausted bool
	}{
		{balance: nil, spent: 999, exhausted: false},
		{balance: usd(10), spent: 0, exhausted: false},
		{balance: usd(10), spent: 10, exhausted: true},
		{balance: usd(10), spent: 12, exhausted: true},
		{balance: usd(0), spent: 0, exhausted: true},
	}
	for _, test := range tracked {
		credential := balanceCredential(test.balance, test.spent)
		// This mirrors the SQL by hand: NULL is "not tracked", otherwise the
		// remaining figure decides.
		sqlSays := test.balance != nil && *test.balance-test.spent <= 0
		if sqlSays != test.exhausted || credential.BalanceExhausted() != test.exhausted {
			t.Fatalf("balance %v spent %v: sql = %v, router = %v, want %v",
				test.balance, test.spent, sqlSays, credential.BalanceExhausted(), test.exhausted)
		}
	}
}
