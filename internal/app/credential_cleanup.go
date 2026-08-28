package app

import (
	"net/http"
)

// An API key that cannot serve is dead weight in the pool: it is skipped on every
// request, it colours the console, and it hides the keys that do work. Clearing
// them one panel at a time is the tedium this endpoint removes.
//
// "Cannot serve" is deliberately narrow, and matches credentialPoolState in the
// console exactly so the count in the banner is the count that gets deleted:
//
//	quarantined  — the provider rejected it
//	out of balance — its tracked credit is spent
//
// A saved validation note is not included: it is also written when a key is
// stored without a successful check or imported from a config bundle, and those
// keys route normally. Cooldown is not included either, because it clears itself.
const unusableCredentialPredicate = `
	(c.status = 'quarantined'
	 OR (c.balance_usd IS NOT NULL AND c.balance_usd - c.balance_spent_usd <= 0))
`

type deleteUnusableInput struct {
	// CredentialIDs is the exact set the operator confirmed. Sending ids rather
	// than "delete whatever is red now" means a key that turned red between the
	// dialog opening and the click is not deleted without being shown.
	CredentialIDs []string `json:"credential_ids"`
}

type deletedCredential struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type skippedCredential struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

type deleteUnusableResult struct {
	Deleted []deletedCredential `json:"deleted"`
	Skipped []skippedCredential `json:"skipped"`
	// Remaining is how many keys the provider has left, so the console can warn
	// that the provider now has none rather than leaving the operator to count.
	Remaining int `json:"remaining"`
}

func (s *Server) handleDeleteUnusableCredentials(w http.ResponseWriter, r *http.Request) {
	var input deleteUnusableInput
	if decodeJSON(w, r, 64<<10, &input) != nil {
		return
	}
	if len(input.CredentialIDs) == 0 || len(input.CredentialIDs) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_credentials", "Name between 1 and 200 API keys to delete.")
		return
	}
	providerID := r.PathValue("id")
	var providerName string
	if err := s.db.QueryRow(r.Context(), `SELECT name FROM providers WHERE id=$1`, providerID).Scan(&providerName); err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "Provider was not found.")
		return
	}

	tx, err := s.db.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "The API keys could not be deleted.")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	// Every named key is read back with the state that decides its fate, so a key
	// the operator listed but that is no longer unusable is reported rather than
	// deleted, and one pinned resource cannot fail the whole batch.
	rows, err := tx.Query(r.Context(), `
		SELECT c.id, c.label, c.is_primary,
		       `+unusableCredentialPredicate+` AS unusable,
		       (SELECT COUNT(*) FROM anthropic_resources a WHERE a.credential_id = c.id) AS pinned
		FROM credentials c
		WHERE c.provider_id = $1 AND c.id = ANY($2::text[])
		ORDER BY c.created_at, c.id
	`, providerID, input.CredentialIDs)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "The API keys could not be read.")
		return
	}
	result := deleteUnusableResult{Deleted: []deletedCredential{}, Skipped: []skippedCredential{}}
	removeIDs := []string{}
	primaryRemoved := false
	found := map[string]bool{}
	for rows.Next() {
		var id, label string
		var isPrimary, unusable bool
		var pinned int
		if err := rows.Scan(&id, &label, &isPrimary, &unusable, &pinned); err != nil {
			rows.Close()
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "The API keys could not be read.")
			return
		}
		found[id] = true
		switch {
		case !unusable:
			result.Skipped = append(result.Skipped, skippedCredential{
				ID: id, Label: label, Reason: "it can serve requests again",
			})
		case pinned > 0:
			result.Skipped = append(result.Skipped, skippedCredential{
				ID: id, Label: label, Reason: "Anthropic files or batches are pinned to it",
			})
		default:
			removeIDs = append(removeIDs, id)
			result.Deleted = append(result.Deleted, deletedCredential{ID: id, Label: label})
			primaryRemoved = primaryRemoved || isPrimary
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "The API keys could not be read.")
		return
	}
	for _, id := range input.CredentialIDs {
		if !found[id] {
			result.Skipped = append(result.Skipped, skippedCredential{
				ID: id, Label: id, Reason: "it no longer exists",
			})
		}
	}

	if len(removeIDs) > 0 {
		if _, err := tx.Exec(r.Context(), `
			DELETE FROM credentials WHERE provider_id = $1 AND id = ANY($2::text[])
		`, providerID, removeIDs); err != nil {
			writeError(w, http.StatusConflict, "credential_delete_failed", "The API keys could not be deleted.")
			return
		}
	}
	// A provider with no primary key still routes, but the console marks one and
	// the rotation prefers it, so the oldest survivor takes the role rather than
	// leaving the provider without one.
	if primaryRemoved {
		if _, err := tx.Exec(r.Context(), `
			UPDATE credentials SET is_primary = TRUE, updated_at = NOW()
			WHERE id = (
				SELECT id FROM credentials WHERE provider_id = $1
				ORDER BY created_at, id LIMIT 1
			)
		`, providerID); err != nil {
			writeError(w, http.StatusInternalServerError, "credential_delete_failed", "The primary API key could not be reassigned.")
			return
		}
	}
	if err := tx.QueryRow(r.Context(), `SELECT COUNT(*) FROM credentials WHERE provider_id=$1`, providerID).Scan(&result.Remaining); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "The remaining API keys could not be counted.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "credential_delete_failed", "The API keys could not be deleted.")
		return
	}

	// Redis holds the cooldown and failure counters under the key's own id. They
	// are cleared after the commit for the same reason markCredentialSuccess does
	// it: a stale counter would apply to whatever id is issued next.
	for _, id := range removeIDs {
		_ = s.redis.Del(r.Context(), "cooldown:"+id, "failures:"+id).Err()
	}
	// Labels are recorded, not just a count: a count cannot answer "which key went"
	// when someone reads the audit trail back a week later.
	labels := make([]string, 0, len(result.Deleted))
	for _, deleted := range result.Deleted {
		labels = append(labels, deleted.Label)
	}
	s.audit(r.Context(), adminIDFromContext(r.Context()), "credential.bulk_delete", "provider", providerID, map[string]any{
		"provider": providerName, "count": len(result.Deleted),
		"labels": labels, "skipped": len(result.Skipped), "remaining": result.Remaining,
	})
	writeJSON(w, http.StatusOK, result)
}
