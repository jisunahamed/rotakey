package app

import (
	"context"
	"crypto/sha256"
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
	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock(hashtext('rotakey-schema-migrations'))`); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer connection.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('rotakey-schema-migrations'))`)
	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(body))
		var recorded string
		err = connection.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE filename=$1`, entry.Name()).Scan(&recorded)
		if err == nil {
			if recorded != checksum {
				return fmt.Errorf("migration %s checksum changed after it was applied", entry.Name())
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read migration ledger for %s: %w", entry.Name(), err)
		}
		tx, err := connection.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (filename, checksum) VALUES ($1, $2)`, entry.Name(), checksum); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
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
		&provider.APIFormat,
		&provider.AnthropicVersion,
		&provider.DefaultKeyBalanceUSD,
		&provider.BalanceSpentUSD,
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
	timeout_seconds, enabled, allow_private_network, api_format, anthropic_version,
	default_key_balance_usd::float8, balance_spent_usd::float8,
	created_at, updated_at
`

const routeColumns = `
		m.id, m.provider_id, m.public_alias, m.upstream_model, m.supports_chat,
		m.supports_responses, m.supports_messages, m.default_max_output_tokens, m.tokenizer,
		m.input_cost_per_million_usd::float8, m.output_cost_per_million_usd::float8, m.request_cost_usd::float8,
		m.capture_bodies, m.strip_parameters, m.capability_status, m.capability_profile,
		m.capabilities_checked_at, m.capability_error, m.enabled, m.created_at, m.updated_at,
		p.id, p.name, p.slug, p.base_url, p.auth_header, p.auth_scheme,
		p.extra_headers, p.timeout_seconds, p.enabled, p.allow_private_network,
		p.api_format, p.anthropic_version,
		p.default_key_balance_usd::float8, p.balance_spent_usd::float8,
		p.created_at, p.updated_at
`

// routeFilter keeps the eligibility rules identical between single-route and
// pooled lookups: the route and its provider are switched on, and the route's
// capabilities have been verified.
//
// Key health is deliberately not part of this. It used to require a credential
// that was not quarantined, which meant one upstream 401 took every alias on the
// provider out of /v1/models and turned its requests into 404 model_not_found —
// a client asking "which models do you have" was told the model did not exist.
// Whether a key can serve is decided at selection time instead, where the reason
// is known and can be reported: see selectPoolCandidate and the 503 in
// servePooled. A 404 from here now means only what it says, that no such route is
// configured.
const routeFilter = `
		m.enabled = TRUE AND p.enabled = TRUE
		  AND m.capability_status IN ('catalog_verified', 'probe_verified')
`

func scanRoute(row pgx.Row) (routeRuntime, error) {
	var route routeRuntime
	var extra []byte
	var capabilityProfile []byte
	err := row.Scan(
		&route.Model.ID,
		&route.Model.ProviderID,
		&route.Model.PublicAlias,
		&route.Model.UpstreamModel,
		&route.Model.SupportsChat,
		&route.Model.SupportsResponses,
		&route.Model.SupportsMessages,
		&route.Model.DefaultMaxOutputTokens,
		&route.Model.Tokenizer,
		&route.Model.InputCostPerMillionUSD,
		&route.Model.OutputCostPerMillionUSD,
		&route.Model.RequestCostUSD,
		&route.Model.CaptureBodies,
		&route.Model.StripParameters,
		&route.Model.CapabilityStatus,
		&capabilityProfile,
		&route.Model.CapabilitiesCheckedAt,
		&route.Model.CapabilityError,
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
		&route.Provider.APIFormat,
		&route.Provider.AnthropicVersion,
		&route.Provider.DefaultKeyBalanceUSD,
		&route.Provider.BalanceSpentUSD,
		&route.Provider.CreatedAt,
		&route.Provider.UpdatedAt,
	)
	if err != nil {
		return routeRuntime{}, err
	}
	if json.Unmarshal(capabilityProfile, &route.Model.CapabilityProfile) != nil {
		route.Model.CapabilityProfile = map[string]string{}
	}
	if err := json.Unmarshal(extra, &route.Provider.ExtraHeaders); err != nil {
		route.Provider.ExtraHeaders = map[string]string{}
	}
	return route, nil
}

// loadRoute returns the single best route for an alias. In model-wise mode the
// pool may hold several providers, so callers that need failover should use
// loadRoutes instead.
func (s *Server) loadRoute(ctx context.Context, alias string) (routeRuntime, error) {
	routes, err := s.loadRoutes(ctx, alias)
	if err != nil {
		return routeRuntime{}, err
	}
	return routes[0], nil
}

// loadRoutes resolves an alias to every route that can serve it. Provider-wise
// mode yields at most one route; model-wise mode yields one route per provider
// that publishes the same public alias, ordered oldest first so the rotation
// cursor stays stable across restarts.
func (s *Server) loadRoutes(ctx context.Context, alias string) ([]routeRuntime, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+routeColumns+`
		FROM model_routes m
		JOIN providers p ON p.id = m.provider_id
		WHERE m.public_alias = $1 AND `+routeFilter+`
		ORDER BY m.created_at, m.id
	`, alias)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := make([]routeRuntime, 0, 1)
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, pgx.ErrNoRows
	}
	return routes, nil
}

func (s *Server) loadCredentials(ctx context.Context, providerID, modelID string) ([]credentialRuntime, error) {
	return s.loadCredentialsForModels(ctx, providerID, []string{modelID})
}

// loadCredentialsForModels loads the provider's usable keys with their limits.
//
// Quarantined keys are loaded rather than filtered out in SQL, because the
// selection ladder is what decides usability and it is the only place that can
// say why a key was passed over. Filtering here made a quarantine invisible in
// the request log's routing decisions — unlike a spent balance, which was always
// reported — and left the caller with nothing to act on. A disabled key is still
// excluded: that is an operator's own decision, not a fault to explain.
func (s *Server) loadCredentialsForModels(ctx context.Context, providerID string, modelIDs []string) ([]credentialRuntime, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			c.id, c.provider_id, c.label, c.secret_cipher, c.secret_suffix,
			c.is_primary, c.enabled, c.status, c.cooldown_until,
			c.last_validated_at, c.validation_error, c.created_at, c.updated_at,
			c.balance_usd::float8, c.balance_spent_usd::float8,
			r.scope_key, r.rps, r.rpm, r.rpd, r.tps, r.tpm, r.tpd, r.tpr
		FROM credentials c
		LEFT JOIN rate_policies r
			ON r.credential_id = c.id AND (r.scope_key = '*' OR r.scope_key = ANY($2::text[]))
		WHERE c.provider_id = $1 AND c.enabled = TRUE
		ORDER BY c.is_primary DESC, c.created_at, c.id, r.scope_key
	`, providerID, modelIDs)
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
			balance                             *float64
			spent                               float64
			scope                               *string
			policy                              RatePolicy
		)
		if err := rows.Scan(
			&id, &provider, &label, &ciphertext, &suffix,
			&isPrimary, &enabled, &status, &cooldown, &lastValidated, &validationError,
			&createdAt, &updatedAt, &balance, &spent,
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
				BalanceUSD:      balance,
				BalanceSpentUSD: spent,
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
		       max_wait_ms, default_provider_timeout_seconds,
		       COALESCE(default_anthropic_provider_id, ''), routing_mode, gateway_key_hash
		FROM app_settings WHERE id = 1
	`).Scan(
		&settings.GatewayKeyPrefix,
		&settings.MetadataRetentionDays,
		&settings.BodyRetentionDays,
		&settings.MaxWaitMS,
		&settings.DefaultProviderTimeoutSecs,
		&settings.DefaultAnthropicProviderID,
		&settings.RoutingMode,
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
