package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	modelDateSuffix = regexp.MustCompile(`-(?:19|20)\d{2}(?:-?\d{2}){1,2}$`)
	modelMonthDay   = regexp.MustCompile(`-(?:0?[1-9]|1[0-2])(?:0?[1-9]|[12]\d|3[01])$`)
	modelSeparators = regexp.MustCompile(`[^a-z0-9]+`)
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

// modelFamilyKey recognises provider spellings of the same model without
// collapsing meaningful capabilities such as vision or embeddings. Catalogs
// commonly vary only by case, namespace, separators, or a dated deployment
// suffix, so those differences do not need separate caller-facing names.
func modelFamilyKey(modelID string) string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(modelID)), func(r rune) bool { return r == '/' })
	value := "model"
	if len(parts) > 0 {
		value = parts[len(parts)-1]
	}
	value = strings.Trim(modelSeparators.ReplaceAllString(value, "-"), "-")
	value = modelDateSuffix.ReplaceAllString(value, "")
	value = modelMonthDay.ReplaceAllString(value, "")
	value = strings.TrimSuffix(strings.TrimSuffix(value, "-latest"), "-stable")
	value = strings.Trim(value, "-")
	if value == "" {
		return "model"
	}
	return value
}

func normalizedModelLeaf(modelID string) string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(modelID)), func(r rune) bool { return r == '/' })
	if len(parts) == 0 {
		return ""
	}
	return strings.Trim(modelSeparators.ReplaceAllString(parts[len(parts)-1], "-"), "-")
}

// consolidateBulkModels turns duplicate catalog spellings into one route in
// model-wise mode. A provider can serve one member of a model pool; adding a
// dated alias of that same deployment must not make the whole import fail.
func consolidateBulkModels(models []modelInput, mode string) ([]modelInput, int, error) {
	kept := make([]modelInput, 0, len(models))
	byAlias := make(map[string]int, len(models))
	skipped := 0
	for _, model := range models {
		index, duplicate := byAlias[model.PublicAlias]
		if !duplicate {
			byAlias[model.PublicAlias] = len(kept)
			kept = append(kept, model)
			continue
		}
		previous := kept[index]
		if mode != routingModeModel || modelFamilyKey(previous.UpstreamModel) != modelFamilyKey(model.UpstreamModel) {
			return nil, 0, fmt.Errorf("public alias %q points to more than one unrelated model", model.PublicAlias)
		}
		// Prefer the unsuffixed catalog ID when both the base model and a dated
		// deployment alias were returned. Otherwise catalog order is stable.
		family := modelFamilyKey(model.UpstreamModel)
		if normalizedModelLeaf(previous.UpstreamModel) != family && normalizedModelLeaf(model.UpstreamModel) == family {
			kept[index] = model
		}
		skipped++
	}
	return kept, skipped, nil
}

type aliasRewrite struct {
	ModelID string
	From    string
	To      string
}

type routeAliasRow struct {
	ModelID       string
	ProviderID    string
	ProviderSlug  string
	Alias         string
	UpstreamModel string
}

type canonicalAliasPlan struct {
	Rewrites     []aliasRewrite
	RemoveIDs    []string
	Conflicts    []string
	Replacements map[string]string
}

func preferCanonicalRoute(left, right routeAliasRow, family string) bool {
	leftBase := normalizedModelLeaf(left.UpstreamModel) == family
	rightBase := normalizedModelLeaf(right.UpstreamModel) == family
	if leftBase != rightBase {
		return leftBase
	}
	return left.ModelID < right.ModelID
}

// planCanonicalAliases is the bounded executor behind the UI's auto-fix
// button. It pools matching families across providers and removes redundant
// dated/case variants inside one provider. An unrelated route already using the
// target name is left untouched and reported instead of being overwritten.
func planCanonicalAliases(rows []routeAliasRow) canonicalAliasPlan {
	plan := canonicalAliasPlan{Rewrites: []aliasRewrite{}, RemoveIDs: []string{}, Conflicts: []string{}, Replacements: map[string]string{}}
	byProviderAlias := map[string]map[string]routeAliasRow{}
	byFamilyProvider := map[string]map[string][]routeAliasRow{}
	for _, row := range rows {
		if byProviderAlias[row.ProviderID] == nil {
			byProviderAlias[row.ProviderID] = map[string]routeAliasRow{}
		}
		byProviderAlias[row.ProviderID][row.Alias] = row
		family := modelFamilyKey(row.UpstreamModel)
		if byFamilyProvider[family] == nil {
			byFamilyProvider[family] = map[string][]routeAliasRow{}
		}
		byFamilyProvider[family][row.ProviderID] = append(byFamilyProvider[family][row.ProviderID], row)
	}
	for family, providers := range byFamilyProvider {
		for providerID, candidates := range providers {
			if owner, taken := byProviderAlias[providerID][family]; taken && modelFamilyKey(owner.UpstreamModel) != family {
				plan.Conflicts = append(plan.Conflicts, family)
				continue
			}
			winner := candidates[0]
			for _, candidate := range candidates[1:] {
				if preferCanonicalRoute(candidate, winner, family) {
					winner = candidate
				}
			}
			for _, candidate := range candidates {
				if candidate.ModelID != winner.ModelID {
					plan.RemoveIDs = append(plan.RemoveIDs, candidate.ModelID)
					plan.Replacements[candidate.ModelID] = winner.ModelID
				}
			}
			if winner.Alias != family {
				plan.Rewrites = append(plan.Rewrites, aliasRewrite{ModelID: winner.ModelID, From: winner.Alias, To: family})
			}
		}
	}
	sort.Slice(plan.Rewrites, func(i, j int) bool { return plan.Rewrites[i].ModelID < plan.Rewrites[j].ModelID })
	sort.Strings(plan.RemoveIDs)
	sort.Strings(plan.Conflicts)
	if len(plan.Conflicts) > 1 {
		unique := plan.Conflicts[:1]
		for _, conflict := range plan.Conflicts[1:] {
			if conflict != unique[len(unique)-1] {
				unique = append(unique, conflict)
			}
		}
		plan.Conflicts = unique
	}
	return plan
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
		SELECT m.id, m.provider_id, p.slug, m.public_alias, m.upstream_model
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
		if err := rows.Scan(&row.ModelID, &row.ProviderID, &row.ProviderSlug, &row.Alias, &row.UpstreamModel); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *Server) handleNormalizeModelAliases(w http.ResponseWriter, r *http.Request) {
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Model aliases could not be updated.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var mode string
	if err := tx.QueryRow(r.Context(), `SELECT routing_mode FROM app_settings WHERE id=1 FOR UPDATE`).Scan(&mode); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Routing mode could not be loaded.")
		return
	}
	if mode != routingModeModel {
		writeError(w, http.StatusConflict, "model_routing_required", "Switch to model-wise routing before auto-fixing shared aliases.")
		return
	}
	rows, err := s.routeAliasRows(r.Context(), tx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Model aliases could not be read.")
		return
	}
	plan := planCanonicalAliases(rows)
	if len(plan.RemoveIDs) > 0 {
		for removedID, replacementID := range plan.Replacements {
			if _, err := tx.Exec(r.Context(), `
				INSERT INTO rate_policies (credential_id, scope_key, rps, rpm, rpd, tps, tpm, tpd, tpr)
				SELECT credential_id, $2, rps, rpm, rpd, tps, tpm, tpd, tpr
				FROM rate_policies WHERE scope_key=$1
				ON CONFLICT (credential_id, scope_key) DO NOTHING
			`, removedID, replacementID); err != nil {
				writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Duplicate model limits could not be consolidated.")
				return
			}
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM rate_policies WHERE scope_key=ANY($1)`, plan.RemoveIDs); err != nil {
			writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Duplicate model limits could not be consolidated.")
			return
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM model_routes WHERE id=ANY($1)`, plan.RemoveIDs); err != nil {
			writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Duplicate model routes could not be consolidated.")
			return
		}
		var policyRaw []byte
		if err := tx.QueryRow(r.Context(), `SELECT policy FROM repair_policy WHERE id=1 FOR UPDATE`).Scan(&policyRaw); err != nil {
			writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Repair policy references could not be checked.")
			return
		}
		policy := defaultRepairPolicy()
		if json.Unmarshal(policyRaw, &policy) != nil {
			writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Repair policy references could not be read.")
			return
		}
		changed := false
		if replacement, ok := plan.Replacements[policy.ModelID]; ok {
			policy.ModelID, changed = replacement, true
		}
		seenRoutes := map[string]bool{}
		for index, routeID := range policy.RouteIDs {
			if replacement, ok := plan.Replacements[routeID]; ok {
				policy.RouteIDs[index], changed = replacement, true
			}
		}
		if changed {
			uniqueRoutes := policy.RouteIDs[:0]
			for _, routeID := range policy.RouteIDs {
				if !seenRoutes[routeID] {
					seenRoutes[routeID] = true
					uniqueRoutes = append(uniqueRoutes, routeID)
				}
			}
			policy.RouteIDs = uniqueRoutes
			policy.Version = 0
			updatedPolicy, _ := json.Marshal(policy)
			if _, err := tx.Exec(r.Context(), `UPDATE repair_policy SET policy=$1, version=version+1, updated_at=NOW() WHERE id=1`, updatedPolicy); err != nil {
				writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Repair policy references could not be updated.")
				return
			}
		}
	}
	for _, rewrite := range plan.Rewrites {
		if _, err := tx.Exec(r.Context(), `UPDATE model_routes SET public_alias=$2, updated_at=NOW() WHERE id=$1`, rewrite.ModelID, rewrite.To); err != nil {
			writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Model aliases could not be normalized.")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "model_alias_update_failed", "Model aliases could not be updated.")
		return
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "model.aliases_normalize", "system", "", map[string]any{
		"rewritten": len(plan.Rewrites), "removed": len(plan.RemoveIDs), "conflicts": plan.Conflicts,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"rewritten": len(plan.Rewrites), "removed": len(plan.RemoveIDs), "conflicts": plan.Conflicts,
	})
}
