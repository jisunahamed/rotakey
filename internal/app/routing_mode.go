package app

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// queryer covers the shared query surface of *pgxpool.Pool and pgx.Tx so
// alias planning can run inside the settings transaction.
type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

const (
	routingModeProvider = "provider"
	routingModeModel    = "model"
)

func normalizeRoutingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case routingModeModel:
		return routingModeModel
	case routingModeProvider, "":
		return routingModeProvider
	default:
		return ""
	}
}

// aliasWithoutProviderPrefix drops a leading "<slug>/" so the same model on
// several providers collapses to one public name in model-wise mode.
func aliasWithoutProviderPrefix(alias, slug string) string {
	if slug == "" {
		return alias
	}
	trimmed := strings.TrimPrefix(alias, slug+"/")
	if trimmed == "" {
		return alias
	}
	return trimmed
}

// aliasWithProviderPrefix restores the "<slug>/" form provider-wise mode needs
// so two providers publishing the same model stay addressable separately.
func aliasWithProviderPrefix(alias, slug string) string {
	if slug == "" || alias == "" {
		return alias
	}
	if strings.HasPrefix(alias, slug+"/") {
		return alias
	}
	return slug + "/" + alias
}

type aliasRewrite struct {
	ModelID string
	From    string
	To      string
}

type routeAliasRow struct {
	ModelID      string
	ProviderID   string
	ProviderSlug string
	Alias        string
}

// planAliasRewrites computes the alias renames a routing-mode switch implies.
// A rename is skipped when it would collide with another alias on the same
// provider, because that pair is already addressable and silently merging them
// would change which upstream model a request reaches.
func planAliasRewrites(rows []routeAliasRow, mode string) ([]aliasRewrite, []string) {
	existing := map[string]map[string]string{}
	for _, row := range rows {
		if existing[row.ProviderID] == nil {
			existing[row.ProviderID] = map[string]string{}
		}
		existing[row.ProviderID][row.Alias] = row.ModelID
	}
	rewrites := make([]aliasRewrite, 0, len(rows))
	conflicts := make([]string, 0)
	claimed := map[string]map[string]bool{}
	for _, row := range rows {
		target := row.Alias
		if mode == routingModeModel {
			target = aliasWithoutProviderPrefix(row.Alias, row.ProviderSlug)
		} else {
			target = aliasWithProviderPrefix(row.Alias, row.ProviderSlug)
		}
		if target == row.Alias {
			continue
		}
		if owner, taken := existing[row.ProviderID][target]; taken && owner != row.ModelID {
			conflicts = append(conflicts, row.Alias)
			continue
		}
		if claimed[row.ProviderID][target] {
			conflicts = append(conflicts, row.Alias)
			continue
		}
		if claimed[row.ProviderID] == nil {
			claimed[row.ProviderID] = map[string]bool{}
		}
		claimed[row.ProviderID][target] = true
		rewrites = append(rewrites, aliasRewrite{ModelID: row.ModelID, From: row.Alias, To: target})
	}
	return rewrites, conflicts
}

func (s *Server) routeAliasRows(ctx context.Context, tx queryer) ([]routeAliasRow, error) {
	rows, err := tx.Query(ctx, `
		SELECT m.id, m.provider_id, p.slug, m.public_alias
		FROM model_routes m JOIN providers p ON p.id = m.provider_id
		ORDER BY m.created_at, m.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]routeAliasRow, 0)
	for rows.Next() {
		var row routeAliasRow
		if err := rows.Scan(&row.ModelID, &row.ProviderID, &row.ProviderSlug, &row.Alias); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
