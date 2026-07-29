package app

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func openDatabase(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	config.MaxConns = 10
	config.MinConns = 1
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := runMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func scanProvider(row pgx.Row) (Provider, error) {
	var provider Provider
	var extra []byte
	err := row.Scan(
		&provider.ID,
		&provider.Name,
		&provider.Slug,
		&provider.BaseURL,
		&provider.AuthHeader,
		&provider.AuthScheme,
		&extra,
		&provider.TimeoutSeconds,
		&provider.Enabled,
		&provider.AllowPrivateNetwork,
		&provider.CreatedAt,
		&provider.UpdatedAt,
	)
	if err != nil {
		return Provider{}, err
	}
	if err := json.Unmarshal(extra, &provider.ExtraHeaders); err != nil {
		provider.ExtraHeaders = map[string]string{}
	}
	return provider, nil
}

const providerColumns = `
	id, name, slug, base_url, auth_header, auth_scheme, extra_headers,
	timeout_seconds, enabled, allow_private_network, created_at, updated_at
`

func (s *Server) loadRoute(ctx context.Context, alias string) (routeRuntime, error) {
	row := s.db.QueryRow(ctx, `
		SELECT
			m.id, m.provider_id, m.public_alias, m.upstream_model, m.supports_chat,
			m.supports_responses, m.default_max_output_tokens, m.tokenizer,
			m.capture_bodies, m.strip_parameters, m.enabled, m.created_at, m.updated_at,
			p.id, p.name, p.slug, p.base_url, p.auth_header, p.auth_scheme,
			p.extra_headers, p.timeout_seconds, p.enabled, p.allow_private_network,
			p.created_at, p.updated_at
		FROM model_routes m
		JOIN providers p ON p.id = m.provider_id
		WHERE m.public_alias = $1 AND m.enabled = TRUE AND p.enabled = TRUE
	`, alias)

	var route routeRuntime
	var extra []byte
	err := row.Scan(
		&route.Model.ID,
		&route.Model.ProviderID,
		&route.Model.PublicAlias,
		&route.Model.UpstreamModel,
		&route.Model.SupportsChat,
		&route.Model.SupportsResponses,
		&route.Model.DefaultMaxOutputTokens,
		&route.Model.Tokenizer,
		&route.Model.CaptureBodies,
		&route.Model.StripParameters,
		&route.Model.Enabled,
		&route.Model.CreatedAt,
		&route.Model.UpdatedAt,
		&route.Provider.ID,
		&route.Provider.Name,
		&route.Provider.Slug,
		&route.Provider.BaseURL,
		&route.Provider.AuthHeader,
		&route.Provider.AuthScheme,
		&extra,
		&route.Provider.TimeoutSeconds,
		&route.Provider.Enabled,
		&route.Provider.AllowPrivateNetwork,
		&route.Provider.CreatedAt,
		&route.Provider.UpdatedAt,
	)
	if err != nil {
		return routeRuntime{}, err
	}
	if err := json.Unmarshal(extra, &route.Provider.ExtraHeaders); err != nil {
		route.Provider.ExtraHeaders = map[string]string{}
	}
	return route, nil
}

func (s *Server) loadCredentials(ctx context.Context, providerID, modelID string) ([]credentialRuntime, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			c.id, c.provider_id, c.label, c.secret_cipher, c.secret_suffix,
			c.is_primary, c.enabled, c.status, c.cooldown_until,
			c.last_validated_at, c.validation_error, c.created_at, c.updated_at,
			r.scope_key, r.rps, r.rpm, r.rpd, r.tps, r.tpm, r.tpd, r.tpr
		FROM credentials c
		LEFT JOIN rate_policies r
			ON r.credential_id = c.id AND (r.scope_key = '*' OR r.scope_key = $2)
		WHERE c.provider_id = $1 AND c.enabled = TRUE AND c.status <> 'quarantined'
		ORDER BY c.is_primary DESC, c.created_at, c.id, r.scope_key
	`, providerID, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]*credentialRuntime{}
	order := []string{}
	for rows.Next() {
		var (
			id, provider, label, suffix, status string
			ciphertext                          []byte
			isPrimary, enabled                  bool
			cooldown                            *time.Time
			lastValidated                       *time.Time
			validationError                     string
			createdAt, updatedAt                time.Time
			scope                               *string
			policy                              RatePolicy
		)
		if err := rows.Scan(
			&id, &provider, &label, &ciphertext, &suffix,
			&isPrimary, &enabled, &status, &cooldown, &lastValidated, &validationError,
			&createdAt, &updatedAt,
			&scope, &policy.RPS, &policy.RPM, &policy.RPD, &policy.TPS,
			&policy.TPM, &policy.TPD, &policy.TPR,
		); err != nil {
			return nil, err
		}
		entry := byID[id]
		if entry == nil {
			secret, err := s.vault.Decrypt(ciphertext)
			if err != nil {
				return nil, fmt.Errorf("decrypt credential %s: %w", id, err)
			}
			entry = &credentialRuntime{CredentialView: CredentialView{
				ID:              id,
				ProviderID:      provider,
				Label:           label,
				SecretSuffix:    suffix,
				IsPrimary:       isPrimary,
				Enabled:         enabled,
				Status:          status,
				CooldownUntil:   cooldown,
				LastValidatedAt: lastValidated,
				ValidationError: validationError,
				Limits:          RatePolicy{},
				ModelLimits:     map[string]RatePolicy{},
				CreatedAt:       createdAt,
				UpdatedAt:       updatedAt,
			}, Secret: secret}
			byID[id] = entry
			order = append(order, id)
		}
		if scope != nil {
			if *scope == "*" {
				entry.Limits = policy
			} else {
				entry.ModelLimits[*scope] = policy
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]credentialRuntime, 0, len(order))
	for _, id := range order {
		result = append(result, *byID[id])
	}
	return result, nil
}

func (s *Server) settings(ctx context.Context) (AppSettings, []byte, error) {
	var settings AppSettings
	var keyHash []byte
	err := s.db.QueryRow(ctx, `
		SELECT gateway_key_prefix, metadata_retention_days, body_retention_days,
		       max_wait_ms, gateway_key_hash
		FROM app_settings WHERE id = 1
	`).Scan(
		&settings.GatewayKeyPrefix,
		&settings.MetadataRetentionDays,
		&settings.BodyRetentionDays,
		&settings.MaxWaitMS,
		&keyHash,
	)
	return settings, keyHash, err
}

func (s *Server) audit(ctx context.Context, adminID, action, resourceType, resourceID string, detail map[string]any) {
	id, err := newID("aud")
	if err != nil {
		return
	}
	payload, err := json.Marshal(detail)
	if err != nil {
		return
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO audit_logs (id, admin_id, action, resource_type, resource_id, detail)
		VALUES ($1, NULLIF($2, ''), $3, $4, NULLIF($5, ''), $6)
	`, id, adminID, action, resourceType, resourceID, payload)
}

func isNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
