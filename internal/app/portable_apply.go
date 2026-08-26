package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// applyImportBundle replays the bundle in one transaction: either the whole
// configuration lands or nothing does, so a failed import never leaves a
// half-built provider that the gateway would try to route to.
func (s *Server) applyImportBundle(ctx context.Context, bundle ExportBundle) (ImportResult, error) {
	result := ImportResult{RoutingMode: bundle.RoutingMode, Warnings: []string{}}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("the database is unavailable")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	providerIDBySlug := map[string]string{}
	for _, provider := range bundle.Providers {
		providerID, created, err := upsertImportedProvider(ctx, tx, provider)
		if err != nil {
			return result, err
		}
		providerIDBySlug[provider.Slug] = providerID
		if created {
			result.ProvidersCreated++
		} else {
			result.ProvidersUpdated++
		}
		modelIDByAlias := map[string]string{}
		for _, model := range provider.Models {
			modelID, modelCreated, err := upsertImportedModel(ctx, tx, providerID, model)
			if err != nil {
				return result, err
			}
			modelIDByAlias[model.PublicAlias] = modelID
			if modelCreated {
				result.ModelsCreated++
			} else {
				result.ModelsUpdated++
			}
		}
		if err := s.importCredentials(ctx, tx, providerID, provider, modelIDByAlias, &result); err != nil {
			return result, err
		}
	}

	defaultAnthropicID := providerIDBySlug[bundle.Settings.DefaultAnthropicProviderSlug]
	if bundle.Settings.DefaultAnthropicProviderSlug != "" && defaultAnthropicID == "" {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Default Anthropic provider %q was not in the bundle, so it was left unset.", bundle.Settings.DefaultAnthropicProviderSlug))
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_settings SET metadata_retention_days=$1, body_retention_days=$2,
		    max_wait_ms=$3, default_provider_timeout_seconds=$4,
		    default_anthropic_provider_id=NULLIF($5,''), routing_mode=$6, updated_at=NOW()
		WHERE id=1
	`, bundle.Settings.MetadataRetentionDays, bundle.Settings.BodyRetentionDays,
		bundle.Settings.MaxWaitMS, bundle.Settings.DefaultProviderTimeoutSecs,
		defaultAnthropicID, bundle.RoutingMode); err != nil {
		return result, fmt.Errorf("settings could not be applied")
	}
	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("the import could not be committed")
	}
	return result, nil
}

// upsertImportedProvider matches on slug, which is the bundle's stable identity
// for a provider, and returns the row's database ID.
func upsertImportedProvider(ctx context.Context, tx pgx.Tx, provider ExportProvider) (string, bool, error) {
	headers, _ := json.Marshal(provider.ExtraHeaders)
	var existingID string
	err := tx.QueryRow(ctx, `SELECT id FROM providers WHERE slug=$1`, provider.Slug).Scan(&existingID)
	if err == nil {
		if _, err := tx.Exec(ctx, `
			UPDATE providers SET name=$2, base_url=$3, auth_header=$4, auth_scheme=$5,
			    extra_headers=$6, timeout_seconds=$7, enabled=$8, allow_private_network=$9,
			    api_format=$10, anthropic_version=$11,
			    default_key_balance_usd=$12, balance_spent_usd=$13, updated_at=NOW()
			WHERE id=$1
		`, existingID, provider.Name, provider.BaseURL, provider.AuthHeader, provider.AuthScheme,
			headers, provider.TimeoutSeconds, provider.Enabled, provider.AllowPrivateNetwork,
			provider.APIFormat, provider.AnthropicVersion,
			provider.DefaultKeyBalanceUSD, provider.BalanceSpentUSD); err != nil {
			return "", false, fmt.Errorf("provider %q could not be updated", provider.Slug)
		}
		return existingID, false, nil
	}
	if !isNotFound(err) {
		return "", false, fmt.Errorf("provider %q could not be read", provider.Slug)
	}
	id, _ := newID("prv")
	if _, err := tx.Exec(ctx, `
		INSERT INTO providers
		    (id, name, slug, base_url, auth_header, auth_scheme, extra_headers,
		     timeout_seconds, enabled, allow_private_network, api_format, anthropic_version,
		     default_key_balance_usd, balance_spent_usd)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, id, provider.Name, provider.Slug, provider.BaseURL, provider.AuthHeader,
		provider.AuthScheme, headers, provider.TimeoutSeconds, provider.Enabled,
		provider.AllowPrivateNetwork, provider.APIFormat, provider.AnthropicVersion,
		provider.DefaultKeyBalanceUSD, provider.BalanceSpentUSD); err != nil {
		return "", false, fmt.Errorf("provider %q could not be created", provider.Slug)
	}
	return id, true, nil
}

// upsertImportedModel matches on (provider, alias), the pair the schema makes
// unique, so re-importing the same bundle updates rather than duplicates.
func upsertImportedModel(ctx context.Context, tx pgx.Tx, providerID string, model ExportModel) (string, bool, error) {
	profile, _ := json.Marshal(valueOrEmptyMap(model.CapabilityProfile))
	var existingID string
	err := tx.QueryRow(ctx, `SELECT id FROM model_routes WHERE provider_id=$1 AND public_alias=$2`, providerID, model.PublicAlias).Scan(&existingID)
	if err == nil {
		if _, err := tx.Exec(ctx, `
			UPDATE model_routes SET upstream_model=$2, supports_chat=$3, supports_responses=$4,
			    supports_messages=$5, default_max_output_tokens=$6, tokenizer=$7,
			    input_cost_per_million_usd=$8, output_cost_per_million_usd=$9, request_cost_usd=$10,
			    capture_bodies=$11, strip_parameters=$12, capability_status=$13,
			    capability_profile=$14, enabled=$15, updated_at=NOW()
			WHERE id=$1
		`, existingID, model.UpstreamModel, model.SupportsChat, model.SupportsResponses,
			model.SupportsMessages, model.DefaultMaxOutputTokens, model.Tokenizer,
			model.InputCostPerMillionUSD, model.OutputCostPerMillionUSD, model.RequestCostUSD,
			model.CaptureBodies, model.StripParameters, model.CapabilityStatus, profile,
			model.Enabled); err != nil {
			return "", false, fmt.Errorf("model %q could not be updated", model.PublicAlias)
		}
		return existingID, false, nil
	}
	if !isNotFound(err) {
		return "", false, fmt.Errorf("model %q could not be read", model.PublicAlias)
	}
	id, _ := newID("mdl")
	if _, err := tx.Exec(ctx, `
		INSERT INTO model_routes
		    (id, provider_id, public_alias, upstream_model, supports_chat,
		     supports_responses, supports_messages, default_max_output_tokens, tokenizer,
		     input_cost_per_million_usd, output_cost_per_million_usd, request_cost_usd,
		     capture_bodies, strip_parameters, capability_status, capability_profile,
		     capability_error, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,'',$17)
	`, id, providerID, model.PublicAlias, model.UpstreamModel, model.SupportsChat,
		model.SupportsResponses, model.SupportsMessages, model.DefaultMaxOutputTokens,
		model.Tokenizer, model.InputCostPerMillionUSD, model.OutputCostPerMillionUSD,
		model.RequestCostUSD, model.CaptureBodies, model.StripParameters,
		model.CapabilityStatus, profile, model.Enabled); err != nil {
		return "", false, fmt.Errorf("model %q could not be created", model.PublicAlias)
	}
	return id, true, nil
}

func valueOrEmptyMap(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	return value
}
