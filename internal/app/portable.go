package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// A portable bundle carries the whole provider, model and limit setup in one
// file so it can be replayed on another install. Everything inside is addressed
// by provider slug and public alias instead of database ID, which is what makes
// a bundle valid on a gateway that has never seen these rows.
const (
	exportKind    = "rotakey.config"
	exportVersion = 1
)

type ExportBundle struct {
	Kind            string           `json:"kind"`
	Version         int              `json:"version"`
	ExportedAt      time.Time        `json:"exported_at"`
	AppVersion      string           `json:"app_version"`
	IncludesSecrets bool             `json:"includes_secrets"`
	RoutingMode     string           `json:"routing_mode"`
	Settings        ExportSettings   `json:"settings"`
	Providers       []ExportProvider `json:"providers"`
}

type ExportSettings struct {
	MetadataRetentionDays        int    `json:"metadata_retention_days"`
	BodyRetentionDays            int    `json:"body_retention_days"`
	MaxWaitMS                    int    `json:"max_wait_ms"`
	DefaultProviderTimeoutSecs   int    `json:"default_provider_timeout_seconds"`
	DefaultAnthropicProviderSlug string `json:"default_anthropic_provider_slug,omitempty"`
}

type ExportProvider struct {
	Name                string             `json:"name"`
	Slug                string             `json:"slug"`
	BaseURL             string             `json:"base_url"`
	AuthHeader          string             `json:"auth_header"`
	AuthScheme          string             `json:"auth_scheme"`
	ExtraHeaders        map[string]string  `json:"extra_headers,omitempty"`
	TimeoutSeconds      int                `json:"timeout_seconds"`
	Enabled             bool               `json:"enabled"`
	AllowPrivateNetwork bool               `json:"allow_private_network"`
	APIFormat           string             `json:"api_format"`
	AnthropicVersion    string             `json:"anthropic_version"`
	Models              []ExportModel      `json:"models"`
	Credentials         []ExportCredential `json:"credentials"`
}

type ExportModel struct {
	PublicAlias             string   `json:"public_alias"`
	UpstreamModel           string   `json:"upstream_model"`
	SupportsChat            bool     `json:"supports_chat"`
	SupportsResponses       bool     `json:"supports_responses"`
	SupportsMessages        bool     `json:"supports_messages"`
	DefaultMaxOutputTokens  int      `json:"default_max_output_tokens"`
	InputCostPerMillionUSD  float64  `json:"input_cost_per_million_usd"`
	OutputCostPerMillionUSD float64  `json:"output_cost_per_million_usd"`
	RequestCostUSD          *float64 `json:"request_cost_usd,omitempty"`
	Tokenizer               string   `json:"tokenizer"`
	CaptureBodies           bool     `json:"capture_bodies"`
	StripParameters         []string `json:"strip_parameters,omitempty"`
	Enabled                 bool     `json:"enabled"`
	// CapabilityStatus and CapabilityProfile carry the probe result so an
	// imported route can serve traffic immediately instead of waiting to be
	// re-probed. Only a verified status is honoured on import.
	CapabilityStatus  string            `json:"capability_status,omitempty"`
	CapabilityProfile map[string]string `json:"capability_profile,omitempty"`
}

type ExportCredential struct {
	Label string `json:"label"`
	// Secret is empty when the bundle was exported without keys. An import then
	// recreates everything except the credential itself.
	Secret      string                `json:"secret,omitempty"`
	IsPrimary   bool                  `json:"is_primary"`
	Enabled     bool                  `json:"enabled"`
	Limits      RatePolicy            `json:"limits"`
	ModelLimits map[string]RatePolicy `json:"model_limits,omitempty"`
	// BalanceUSD and BalanceSpentUSD travel together so a restored key keeps the
	// credit it had left rather than appearing freshly topped up.
	BalanceUSD      *float64 `json:"balance_usd,omitempty"`
	BalanceSpentUSD float64  `json:"balance_spent_usd,omitempty"`
}

func (s *Server) registerPortableRoutes(mux *http.ServeMux) {
	admin := func(handler http.HandlerFunc) http.Handler { return s.requireAdmin(handler) }
	// Export is a POST even though it reads nothing: requireAdmin only enforces
	// the CSRF token on unsafe methods, and a cookie-authenticated GET that
	// returns every plaintext API key must not be triggerable cross-site.
	mux.Handle("POST /api/admin/config/export", admin(s.handleExportConfig))
	mux.Handle("POST /api/admin/config/import", admin(s.handleImportConfig))
}

// buildExportBundle assembles the whole configuration in a stable A-to-Z order
// so two exports of the same setup produce the same file.
func (s *Server) buildExportBundle(ctx context.Context, withSecrets bool) (ExportBundle, error) {
	settings, _, err := s.settings(ctx)
	if err != nil {
		return ExportBundle{}, err
	}
	providers, err := s.listProviders(ctx)
	if err != nil {
		return ExportBundle{}, err
	}
	// Per-model rate limits are stored against the model's database ID, which
	// means nothing on another install, so they are re-keyed by public alias.
	aliasByModelID := map[string]string{}
	for _, provider := range providers {
		for _, model := range provider.Models {
			aliasByModelID[model.ID] = model.PublicAlias
		}
	}
	secrets := map[string]string{}
	if withSecrets {
		if secrets, err = s.exportSecrets(ctx); err != nil {
			return ExportBundle{}, err
		}
	}
	bundle := ExportBundle{
		Kind: exportKind, Version: exportVersion, ExportedAt: time.Now().UTC(),
		AppVersion: Version, IncludesSecrets: withSecrets,
		RoutingMode: valueOr(settings.RoutingMode, routingModeProvider),
		Settings: ExportSettings{
			MetadataRetentionDays:      settings.MetadataRetentionDays,
			BodyRetentionDays:          settings.BodyRetentionDays,
			MaxWaitMS:                  settings.MaxWaitMS,
			DefaultProviderTimeoutSecs: settings.DefaultProviderTimeoutSecs,
		},
		Providers: make([]ExportProvider, 0, len(providers)),
	}
	for _, provider := range providers {
		if provider.ID == settings.DefaultAnthropicProviderID {
			bundle.Settings.DefaultAnthropicProviderSlug = provider.Slug
		}
		bundle.Providers = append(bundle.Providers, exportProvider(provider, aliasByModelID, secrets, withSecrets))
	}
	sort.Slice(bundle.Providers, func(left, right int) bool {
		return bundle.Providers[left].Slug < bundle.Providers[right].Slug
	})
	return bundle, nil
}

func exportProvider(provider Provider, aliasByModelID, secrets map[string]string, withSecrets bool) ExportProvider {
	exported := ExportProvider{
		Name: provider.Name, Slug: provider.Slug, BaseURL: provider.BaseURL,
		AuthHeader: provider.AuthHeader, AuthScheme: provider.AuthScheme,
		ExtraHeaders: provider.ExtraHeaders, TimeoutSeconds: provider.TimeoutSeconds,
		Enabled: provider.Enabled, AllowPrivateNetwork: provider.AllowPrivateNetwork,
		APIFormat:        valueOr(provider.APIFormat, "openai"),
		AnthropicVersion: valueOr(provider.AnthropicVersion, "2023-06-01"),
		Models:           make([]ExportModel, 0, len(provider.Models)),
		Credentials:      make([]ExportCredential, 0, len(provider.Credentials)),
	}
	for _, model := range provider.Models {
		exported.Models = append(exported.Models, ExportModel{
			PublicAlias: model.PublicAlias, UpstreamModel: model.UpstreamModel,
			SupportsChat: model.SupportsChat, SupportsResponses: model.SupportsResponses,
			SupportsMessages:        model.SupportsMessages,
			DefaultMaxOutputTokens:  model.DefaultMaxOutputTokens,
			InputCostPerMillionUSD:  model.InputCostPerMillionUSD,
			OutputCostPerMillionUSD: model.OutputCostPerMillionUSD,
			RequestCostUSD:          model.RequestCostUSD, Tokenizer: model.Tokenizer,
			CaptureBodies: model.CaptureBodies, StripParameters: model.StripParameters,
			Enabled:          model.Enabled,
			CapabilityStatus: model.CapabilityStatus, CapabilityProfile: model.CapabilityProfile,
		})
	}
	sort.Slice(exported.Models, func(left, right int) bool {
		return exported.Models[left].PublicAlias < exported.Models[right].PublicAlias
	})
	for _, credential := range provider.Credentials {
		limits := map[string]RatePolicy{}
		for modelID, policy := range credential.ModelLimits {
			if alias := aliasByModelID[modelID]; alias != "" {
				limits[alias] = policy
			}
		}
		entry := ExportCredential{
			Label: credential.Label, IsPrimary: credential.IsPrimary,
			Enabled: credential.Enabled, Limits: credential.Limits, ModelLimits: limits,
			BalanceUSD: credential.BalanceUSD, BalanceSpentUSD: credential.BalanceSpentUSD,
		}
		if withSecrets {
			entry.Secret = secrets[credential.ID]
		}
		exported.Credentials = append(exported.Credentials, entry)
	}
	sort.Slice(exported.Credentials, func(left, right int) bool {
		return exported.Credentials[left].Label < exported.Credentials[right].Label
	})
	return exported
}

// exportSecrets decrypts every stored API key so the bundle can rebuild a
// working gateway. The plaintext exists only inside this response.
func (s *Server) exportSecrets(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT id, secret_cipher FROM credentials`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	secrets := map[string]string{}
	for rows.Next() {
		var id string
		var ciphertext []byte
		if err := rows.Scan(&id, &ciphertext); err != nil {
			return nil, err
		}
		plain, err := s.vault.Decrypt(ciphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt credential %s: %w", id, err)
		}
		secrets[id] = string(plain)
	}
	return secrets, rows.Err()
}

func (s *Server) handleExportConfig(w http.ResponseWriter, r *http.Request) {
	// Keys are included by default because the point of the file is a one-click
	// restore; ?secrets=false produces a shareable bundle instead.
	withSecrets := !strings.EqualFold(r.URL.Query().Get("secrets"), "false")
	bundle, err := s.buildExportBundle(r.Context(), withSecrets)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "export_unavailable", "Configuration could not be exported.")
		return
	}
	body, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "export_failed", "Configuration could not be encoded.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "config.export", "system", "", map[string]any{
		"providers": len(bundle.Providers), "includes_secrets": withSecrets,
	})
	// The console turns this body into the download, so no Content-Disposition is
	// needed; no-store keeps a bundle carrying plaintext keys out of any cache.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
