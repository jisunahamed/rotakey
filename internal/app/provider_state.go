package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// A provider is turned off through its own endpoint rather than the provider
// form. Switching traffic off must not depend on every other field validating,
// and it must not trigger the upstream re-check a base URL change runs, because
// the reason for turning a provider off is often that the upstream is broken.
type providerStateInput struct {
	Enabled bool `json:"enabled"`
}

type providerStateResult struct {
	Enabled bool `json:"enabled"`
	// AliasesStranded lists public aliases no other enabled provider can serve
	// once this one is off. That is the difference between draining one member of
	// a pooled model and removing the model from the gateway entirely.
	AliasesStranded []string `json:"aliases_stranded"`
	Warnings        []string `json:"warnings"`
}

func (s *Server) handleSetProviderEnabled(w http.ResponseWriter, r *http.Request) {
	var input providerStateInput
	if decodeJSON(w, r, 1<<10, &input) != nil {
		return
	}
	providerID := r.PathValue("id")
	var name string
	if err := s.db.QueryRow(r.Context(), `SELECT name FROM providers WHERE id=$1`, providerID).Scan(&name); err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}
	result := providerStateResult{Enabled: input.Enabled, AliasesStranded: []string{}, Warnings: []string{}}
	// The impact is only reported when turning off; turning back on can only add
	// capacity, so there is nothing to warn about.
	if !input.Enabled {
		stranded, err := s.strandedAliases(r.Context(), providerID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "The routing impact could not be checked.")
			return
		}
		settings, _, err := s.settings(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Settings could not be read.")
			return
		}
		result.AliasesStranded = stranded
		result.Warnings = providerDisableWarnings(name, stranded, settings.DefaultAnthropicProviderID == providerID)
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE providers SET enabled=$2, updated_at=NOW() WHERE id=$1`, providerID, input.Enabled); err != nil {
		writeError(w, http.StatusServiceUnavailable, "provider_state_failed", "The provider state could not be changed.")
		return
	}
	action := "provider.disable"
	if input.Enabled {
		action = "provider.enable"
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), action, "provider", providerID, map[string]any{
		"name": name, "aliases_stranded": len(result.AliasesStranded),
	})
	writeJSON(w, http.StatusOK, result)
}

// strandedAliases returns the provider's live aliases that nothing else can
// serve. The NOT EXISTS clause repeats routeFilter's eligibility rules against
// the other providers, so the answer matches what the gateway would actually
// find after this provider stops being a candidate.
//
// Like routeFilter, it asks about configuration rather than today's key health.
// Counting a quarantined key as "cannot serve" made the warning flicker with
// upstream state: the same provider was reported as stranding six aliases one
// minute and none the next, when nothing about the routing had changed.
func (s *Server) strandedAliases(ctx context.Context, providerID string) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT m.public_alias
		FROM model_routes m
		WHERE m.provider_id = $1 AND m.enabled = TRUE
		  AND NOT EXISTS (
		    SELECT 1
		    FROM model_routes o
		    JOIN providers p ON p.id = o.provider_id
		    WHERE o.public_alias = m.public_alias AND o.provider_id <> $1
		      AND o.enabled = TRUE AND p.enabled = TRUE
		      AND o.capability_status IN ('catalog_verified', 'probe_verified')
		  )
		ORDER BY m.public_alias
	`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	aliases := []string{}
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	return aliases, rows.Err()
}

// providerDisableWarnings turns the routing impact into operator-facing text. It
// is separate from the query so the wording stays unit-testable.
func providerDisableWarnings(name string, stranded []string, isDefaultAnthropic bool) []string {
	warnings := []string{}
	if len(stranded) > 0 {
		// A long list is truncated because a provider can carry hundreds of
		// aliases and the count is the part that matters.
		listed, extra := stranded, ""
		if len(listed) > 6 {
			listed, extra = listed[:6], fmt.Sprintf(" and %d more", len(stranded)-6)
		}
		noun, reason := "aliases", "no other enabled provider serves them"
		if len(stranded) == 1 {
			noun, reason = "alias", "no other enabled provider serves it"
		}
		warnings = append(warnings, fmt.Sprintf("%d model %s will stop serving because %s: %s%s.",
			len(stranded), noun, reason, strings.Join(listed, ", "), extra))
	}
	if isDefaultAnthropic {
		warnings = append(warnings, fmt.Sprintf(
			"%s is the default Anthropic resource provider, so Files and Batches will fail until another provider is chosen in System settings.", name))
	}
	return warnings
}
