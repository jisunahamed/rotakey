package app

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
)

func (s *Server) providerFallbacks(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT primary_id, alternative_id FROM provider_fallbacks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var primary, alternative string
		if err := rows.Scan(&primary, &alternative); err != nil {
			return nil, err
		}
		result[primary] = alternative
	}
	return result, rows.Err()
}

func (s *Server) handleSetProviderFallback(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AlternativeID string `json:"alternative_provider_id"`
	}
	if decodeJSON(w, r, 4096, &input) != nil {
		return
	}
	primary := r.PathValue("id")
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, 503, "database_unavailable", "Fallback settings could not be saved.")
		return
	}
	defer tx.Rollback(r.Context())
	// Serialize topology changes so concurrent saves cannot introduce a cycle.
	if _, err = tx.Exec(r.Context(), `LOCK TABLE provider_fallbacks IN EXCLUSIVE MODE`); err != nil {
		writeError(w, 503, "database_unavailable", "Fallback settings could not be saved.")
		return
	}
	var exists bool
	if err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM providers WHERE id=$1)`, primary).Scan(&exists); err != nil || !exists {
		writeError(w, 404, "provider_not_found", "Provider was not found.")
		return
	}
	if input.AlternativeID != "" {
		var invalid bool
		err = tx.QueryRow(r.Context(), `SELECT $1=$2 OR NOT EXISTS(SELECT 1 FROM providers WHERE id=$2) OR EXISTS(SELECT 1 FROM provider_fallbacks WHERE alternative_id=$1 OR primary_id=$2)`, primary, input.AlternativeID).Scan(&invalid)
		if err != nil || invalid {
			writeError(w, 400, "invalid_fallback", "Choose a different provider. Alternative providers cannot also have an alternative; chains and circular fallbacks are not allowed.")
			return
		}
		_, err = tx.Exec(r.Context(), `INSERT INTO provider_fallbacks(primary_id,alternative_id) VALUES($1,$2) ON CONFLICT(primary_id) DO UPDATE SET alternative_id=excluded.alternative_id`, primary, input.AlternativeID)
	} else {
		_, err = tx.Exec(r.Context(), `DELETE FROM provider_fallbacks WHERE primary_id=$1`, primary)
	}
	if err != nil {
		writeError(w, 503, "database_unavailable", "Fallback settings could not be saved.")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		writeError(w, 503, "database_unavailable", "Fallback settings could not be saved.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "provider.fallback", "provider", primary, map[string]any{"alternative_provider_id": input.AlternativeID})
	writeJSON(w, 200, input)
}

func (s *Server) resolveRoutes(ctx context.Context, alias, mode string) ([]routeRuntime, error) {
	links, err := s.providerFallbacks(ctx)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return s.resolveUnconfiguredRoutes(ctx, alias, mode)
	}
	// Include same-alias alternatives in provider-wise mode as well. A disabled
	// primary remains configured, allowing its alternative to carry its aliases.
	routes, err := s.loadRoutes(ctx, alias)
	if err != nil {
		return nil, err
	}
	configured := map[string]bool{}
	configuredRows, err := s.db.Query(ctx, `SELECT provider_id FROM model_routes WHERE public_alias=$1 AND enabled=TRUE`, routes[0].Model.PublicAlias)
	if err != nil {
		return nil, err
	}
	for configuredRows.Next() {
		var id string
		if err = configuredRows.Scan(&id); err != nil {
			configuredRows.Close()
			return nil, err
		}
		configured[id] = true
	}
	err = configuredRows.Err()
	configuredRows.Close()
	if err != nil {
		return nil, err
	}
	relevant := false
	for primary, alternative := range links {
		if configured[primary] || configured[alternative] {
			relevant = true
		}
	}
	if !relevant {
		return s.resolveUnconfiguredRoutes(ctx, alias, mode)
	}
	result := filterProviderFallbackRoutes(routes, links, configured)
	if len(result) == 0 {
		return nil, pgx.ErrNoRows
	}
	return result, nil
}

func filterProviderFallbackRoutes(routes []routeRuntime, links map[string]string, configured map[string]bool) []routeRuntime {
	targets := map[string]bool{}
	allowed := map[string]bool{}
	for primary, alternative := range links {
		targets[alternative] = true
		if configured[primary] {
			allowed[alternative] = true
		}
	}
	result := make([]routeRuntime, 0, len(routes))
	for _, route := range routes {
		if alternative, primary := links[route.Provider.ID]; primary {
			route.FallbackOnly = false
			route.Provider.AlternativeProviderID = alternative
		}
		if targets[route.Provider.ID] {
			if !allowed[route.Provider.ID] || route.Model.PublicAlias != routes[0].Model.PublicAlias {
				continue
			}
			route.FallbackOnly = true
		}
		result = append(result, route)
	}
	return result
}

// A configured provider alternative is the next choice after a real rejection,
// rather than retrying dozens of keys against the same failing provider first.
func skipFailedPrimary(candidates []routeCandidate, failed routeCandidate, skipped map[string]bool) bool {
	alternative := failed.Route.Provider.AlternativeProviderID
	if alternative == "" {
		return false
	}
	available := false
	for _, c := range candidates {
		if c.Route.Provider.ID == alternative && c.Route.FallbackOnly {
			available = true
		}
	}
	if !available {
		return false
	}
	for _, c := range candidates {
		if c.Route.Provider.ID == failed.Route.Provider.ID {
			skipped[c.key()] = true
		}
	}
	return true
}
