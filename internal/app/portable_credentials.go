package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// importCredentials restores the API keys for one provider. Keys are stored
// encrypted exactly as the admin forms store them, and are not checked against
// the provider here: a bundle can carry hundreds of keys, and an invalid one
// quarantines itself on first use anyway.
func (s *Server) importCredentials(ctx context.Context, tx pgx.Tx, providerID string, provider ExportProvider, modelIDByAlias map[string]string, result *ImportResult) error {
	for _, credential := range provider.Credentials {
		if credential.Secret == "" {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credentials WHERE provider_id=$1 AND label=$2)`, providerID, credential.Label).Scan(&exists); err != nil {
				return fmt.Errorf("API key %q could not be read", credential.Label)
			}
			if !exists {
				// A secret-free bundle cannot create a usable key, so the route is
				// restored and the operator is told which key to paste in.
				result.CredentialsSkipped++
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s · %s: the bundle carried no API key value, so add it manually.", provider.Slug, credential.Label))
				continue
			}
		}
		credentialID, created, err := s.upsertImportedCredential(ctx, tx, providerID, credential)
		if err != nil {
			return err
		}
		if created {
			result.CredentialsCreated++
		} else {
			result.CredentialsUpdated++
		}
		if credential.Secret != "" {
			result.CredentialsUnverified++
		}
		if err := upsertPolicy(ctx, tx, credentialID, "*", credential.Limits); err != nil {
			return fmt.Errorf("rate limits for %q could not be saved", credential.Label)
		}
		for alias, policy := range credential.ModelLimits {
			modelID := modelIDByAlias[alias]
			if modelID == "" {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s · %s: per-model limits for %q were dropped because that model is not in the bundle.", provider.Slug, credential.Label, alias))
				continue
			}
			if err := upsertPolicy(ctx, tx, credentialID, modelID, policy); err != nil {
				return fmt.Errorf("model limits for %q could not be saved", credential.Label)
			}
		}
		if credential.IsPrimary {
			if _, err := tx.Exec(ctx, `
				UPDATE credentials SET is_primary=FALSE, updated_at=NOW()
				WHERE provider_id=$1 AND id<>$2 AND is_primary=TRUE
			`, providerID, credentialID); err != nil {
				return fmt.Errorf("the primary API key for %q could not be set", provider.Slug)
			}
		}
	}
	return nil
}

func (s *Server) upsertImportedCredential(ctx context.Context, tx pgx.Tx, providerID string, credential ExportCredential) (string, bool, error) {
	status := "healthy"
	if !credential.Enabled {
		status = "disabled"
	}
	// The import does not call the provider, so the key is recorded as saved
	// without validation rather than claiming a successful check.
	validationError := ""
	if credential.Secret != "" {
		validationError = "Imported from a configuration bundle without validation."
	}
	var existingID string
	err := tx.QueryRow(ctx, `SELECT id FROM credentials WHERE provider_id=$1 AND label=$2`, providerID, credential.Label).Scan(&existingID)
	if err != nil && !isNotFound(err) {
		return "", false, fmt.Errorf("API key %q could not be read", credential.Label)
	}
	if err == nil && credential.Secret == "" {
		// Keep the existing secret and only refresh the surrounding settings.
		if _, execErr := tx.Exec(ctx, `
			UPDATE credentials SET is_primary=$2, enabled=$3, status=$4,
			    cooldown_until=NULL, consecutive_failures=0, updated_at=NOW()
			WHERE id=$1
		`, existingID, credential.IsPrimary, credential.Enabled, status); execErr != nil {
			return "", false, fmt.Errorf("API key %q could not be updated", credential.Label)
		}
		return existingID, false, nil
	}
	encrypted, encryptErr := s.vault.Encrypt([]byte(credential.Secret))
	if encryptErr != nil {
		return "", false, fmt.Errorf("API key %q could not be encrypted", credential.Label)
	}
	if err == nil {
		if _, execErr := tx.Exec(ctx, `
			UPDATE credentials SET secret_cipher=$2, secret_suffix=$3, is_primary=$4,
			    enabled=$5, status=$6, cooldown_until=NULL, consecutive_failures=0,
			    validation_error=$7, last_validated_at=NULL, updated_at=NOW()
			WHERE id=$1
		`, existingID, encrypted, secretSuffix(credential.Secret), credential.IsPrimary,
			credential.Enabled, status, validationError); execErr != nil {
			return "", false, fmt.Errorf("API key %q could not be updated", credential.Label)
		}
		return existingID, false, nil
	}
	id, _ := newID("key")
	if _, execErr := tx.Exec(ctx, `
		INSERT INTO credentials
		    (id, provider_id, label, secret_cipher, secret_suffix, is_primary,
		     enabled, status, validation_error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, id, providerID, credential.Label, encrypted, secretSuffix(credential.Secret),
		credential.IsPrimary, credential.Enabled, status, validationError); execErr != nil {
		return "", false, fmt.Errorf("API key %q could not be created", credential.Label)
	}
	return id, true, nil
}
