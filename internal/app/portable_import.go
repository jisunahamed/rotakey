package app

import (
	"fmt"
	"net/http"
	"strings"
)

// ImportResult tells the operator exactly what an import changed, because a
// bundle may be a fresh setup on an empty gateway or a restore over one that
// already has providers.
type ImportResult struct {
	RoutingMode           string   `json:"routing_mode"`
	ProvidersCreated      int      `json:"providers_created"`
	ProvidersUpdated      int      `json:"providers_updated"`
	ModelsCreated         int      `json:"models_created"`
	ModelsUpdated         int      `json:"models_updated"`
	CredentialsCreated    int      `json:"credentials_created"`
	CredentialsUpdated    int      `json:"credentials_updated"`
	CredentialsSkipped    int      `json:"credentials_skipped"`
	CredentialsUnverified int      `json:"credentials_unverified"`
	Warnings              []string `json:"warnings"`
}

// verifiedCapabilityStatuses are the values routeFilter accepts. A bundle may
// only restore a status it could have legitimately earned; anything else lands
// as unverified so an unprobed route never starts serving traffic.
var verifiedCapabilityStatuses = map[string]bool{
	"catalog_verified": true, "probe_verified": true,
}

func (s *Server) handleImportConfig(w http.ResponseWriter, r *http.Request) {
	var bundle ExportBundle
	if decodeJSON(w, r, 8<<20, &bundle) != nil {
		return
	}
	if bundle.Kind != exportKind {
		writeError(w, http.StatusBadRequest, "invalid_bundle", "This file is not a rotakey configuration export.")
		return
	}
	if bundle.Version > exportVersion {
		writeError(w, http.StatusBadRequest, "unsupported_bundle_version", fmt.Sprintf("This bundle needs a newer rotakey (bundle version %d, supported %d).", bundle.Version, exportVersion))
		return
	}
	if len(bundle.Providers) == 0 {
		writeError(w, http.StatusBadRequest, "empty_bundle", "The bundle contains no providers.")
		return
	}
	if len(bundle.Providers) > 500 {
		writeError(w, http.StatusBadRequest, "bundle_too_large", "A bundle may contain at most 500 providers.")
		return
	}
	if err := validateImportBundle(&bundle); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_bundle", err.Error())
		return
	}

	result, err := s.applyImportBundle(r.Context(), bundle)
	if err != nil {
		s.logger.Error("configuration import failed", "error", err)
		writeError(w, http.StatusUnprocessableEntity, "import_failed", "The bundle could not be applied: "+err.Error())
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "config.import", "system", "", map[string]any{
		"routing_mode":        result.RoutingMode,
		"providers_created":   result.ProvidersCreated,
		"providers_updated":   result.ProvidersUpdated,
		"models_created":      result.ModelsCreated,
		"models_updated":      result.ModelsUpdated,
		"credentials_created": result.CredentialsCreated,
		"credentials_updated": result.CredentialsUpdated,
		"credentials_skipped": result.CredentialsSkipped,
	})
	writeJSON(w, http.StatusOK, result)
}

// validateImportBundle runs the bundle through the same checks the admin forms
// use, so an import cannot introduce a provider URL or model shape the API
// would have rejected.
func validateImportBundle(bundle *ExportBundle) error {
	bundle.RoutingMode = normalizeRoutingMode(bundle.RoutingMode)
	if bundle.RoutingMode == "" {
		return fmt.Errorf("routing mode must be 'provider' or 'model'")
	}
	settings := &bundle.Settings
	if settings.MetadataRetentionDays == 0 {
		settings.MetadataRetentionDays = 90
	}
	if settings.BodyRetentionDays == 0 {
		settings.BodyRetentionDays = 30
	}
	if settings.DefaultProviderTimeoutSecs == 0 {
		settings.DefaultProviderTimeoutSecs = 120
	}
	if settings.MetadataRetentionDays < 1 || settings.MetadataRetentionDays > 3650 ||
		settings.BodyRetentionDays < 1 || settings.BodyRetentionDays > 365 ||
		settings.MaxWaitMS < 0 || settings.MaxWaitMS > 30000 ||
		settings.DefaultProviderTimeoutSecs < 1 || settings.DefaultProviderTimeoutSecs > 900 {
		return fmt.Errorf("bundle settings are outside the allowed ranges")
	}
	seenSlugs := map[string]bool{}
	for index := range bundle.Providers {
		provider := &bundle.Providers[index]
		input := providerInput{
			Name: provider.Name, Slug: provider.Slug, BaseURL: provider.BaseURL,
			AuthHeader: provider.AuthHeader, AuthScheme: provider.AuthScheme,
			ExtraHeaders: provider.ExtraHeaders, TimeoutSeconds: provider.TimeoutSeconds,
			Enabled: provider.Enabled, AllowPrivateNetwork: provider.AllowPrivateNetwork,
			APIFormat: provider.APIFormat, AnthropicVersion: provider.AnthropicVersion,
		}
		if err := validateProviderInput(&input); err != nil {
			return fmt.Errorf("provider %q: %w", provider.Name, err)
		}
		provider.Name, provider.Slug, provider.BaseURL = input.Name, input.Slug, input.BaseURL
		provider.AuthHeader, provider.AuthScheme = input.AuthHeader, input.AuthScheme
		provider.TimeoutSeconds, provider.APIFormat = input.TimeoutSeconds, input.APIFormat
		provider.AnthropicVersion = input.AnthropicVersion
		if seenSlugs[provider.Slug] {
			return fmt.Errorf("provider identifier %q appears twice in the bundle", provider.Slug)
		}
		seenSlugs[provider.Slug] = true
		if err := validateImportProviderChildren(provider); err != nil {
			return err
		}
	}
	return nil
}

func validateImportProviderChildren(provider *ExportProvider) error {
	if len(provider.Models) > 2000 || len(provider.Credentials) > 500 {
		return fmt.Errorf("provider %q carries too many models or API keys", provider.Slug)
	}
	seenAliases := map[string]bool{}
	for index := range provider.Models {
		model := &provider.Models[index]
		input := modelInput{
			PublicAlias: model.PublicAlias, UpstreamModel: model.UpstreamModel,
			SupportsChat: model.SupportsChat, SupportsResponses: model.SupportsResponses,
			SupportsMessages:        model.SupportsMessages,
			DefaultMaxOutputTokens:  model.DefaultMaxOutputTokens,
			InputCostPerMillionUSD:  model.InputCostPerMillionUSD,
			OutputCostPerMillionUSD: model.OutputCostPerMillionUSD,
			RequestCostUSD:          model.RequestCostUSD, Tokenizer: model.Tokenizer,
			CaptureBodies: model.CaptureBodies, StripParameters: model.StripParameters,
			Enabled: model.Enabled,
		}
		if err := validateModelInput(&input); err != nil {
			return fmt.Errorf("model %q on %q: %w", model.PublicAlias, provider.Slug, err)
		}
		model.PublicAlias, model.UpstreamModel = input.PublicAlias, input.UpstreamModel
		model.DefaultMaxOutputTokens, model.Tokenizer = input.DefaultMaxOutputTokens, input.Tokenizer
		model.StripParameters = input.StripParameters
		if seenAliases[model.PublicAlias] {
			return fmt.Errorf("alias %q appears twice on provider %q", model.PublicAlias, provider.Slug)
		}
		seenAliases[model.PublicAlias] = true
		if !verifiedCapabilityStatuses[model.CapabilityStatus] {
			model.CapabilityStatus = "unverified"
		}
	}
	seenLabels := map[string]bool{}
	for index := range provider.Credentials {
		credential := &provider.Credentials[index]
		credential.Label = strings.TrimSpace(credential.Label)
		credential.Secret = strings.TrimSpace(credential.Secret)
		if credential.Label == "" || len(credential.Label) > 100 {
			return fmt.Errorf("API key label on provider %q is invalid", provider.Slug)
		}
		if credential.Secret != "" && (len(credential.Secret) < 8 || len(credential.Secret) > 8192) {
			return fmt.Errorf("API key %q on provider %q is invalid", credential.Label, provider.Slug)
		}
		if !credential.Limits.Valid() {
			return fmt.Errorf("rate limits for API key %q are invalid", credential.Label)
		}
		if credential.BalanceUSD != nil &&
			(*credential.BalanceUSD < 0 || *credential.BalanceUSD > maxCredentialBalanceUSD) {
			return fmt.Errorf("the balance for API key %q is invalid", credential.Label)
		}
		if credential.BalanceSpentUSD < 0 || credential.BalanceSpentUSD > maxCredentialBalanceUSD {
			return fmt.Errorf("the recorded spend for API key %q is invalid", credential.Label)
		}
		for alias, policy := range credential.ModelLimits {
			if !policy.Valid() {
				return fmt.Errorf("model limits for %q on API key %q are invalid", alias, credential.Label)
			}
		}
		if seenLabels[credential.Label] {
			return fmt.Errorf("API key label %q appears twice on provider %q", credential.Label, provider.Slug)
		}
		seenLabels[credential.Label] = true
	}
	return nil
}
