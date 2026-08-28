package app

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type credentialSecretQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type credentialSecretIdentity struct {
	ID     string
	Label  string
	Secret []byte
}

// loadCredentialSecretIdentities decrypts only the keys for one provider. The
// plaintext values never leave this request and let older rows participate in
// duplicate detection without a schema backfill.
func (s *Server) loadCredentialSecretIdentities(
	ctx context.Context,
	querier credentialSecretQuerier,
	providerID string,
) ([]credentialSecretIdentity, error) {
	rows, err := querier.Query(ctx, `
		SELECT id, label, secret_cipher
		FROM credentials
		WHERE provider_id=$1
		ORDER BY created_at, id
	`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	identities := make([]credentialSecretIdentity, 0)
	for rows.Next() {
		var identity credentialSecretIdentity
		var ciphertext []byte
		if err := rows.Scan(&identity.ID, &identity.Label, &ciphertext); err != nil {
			return nil, err
		}
		identity.Secret, err = s.vault.Decrypt(ciphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt credential %s: %w", identity.ID, err)
		}
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

// duplicateCredentialInputs returns the label that already owns each repeated
// secret. Earlier entries in the same paste are treated as owners too.
func duplicateCredentialInputs(
	inputs []credentialInput,
	existing []credentialSecretIdentity,
	excludeID string,
) map[int]string {
	known := make([]credentialSecretIdentity, 0, len(existing)+len(inputs))
	for _, identity := range existing {
		if identity.ID != excludeID {
			known = append(known, identity)
		}
	}

	duplicates := map[int]string{}
	for index, input := range inputs {
		secret := []byte(input.Secret)
		for _, identity := range known {
			if secureEqual(secret, identity.Secret) {
				duplicates[index] = identity.Label
				break
			}
		}
		if _, duplicate := duplicates[index]; !duplicate {
			known = append(known, credentialSecretIdentity{Label: input.Label, Secret: secret})
		}
	}
	return duplicates
}
