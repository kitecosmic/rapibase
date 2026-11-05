package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetUserIDByIdentity returns the app user linked to provider+subject, or
// "" when there is none.
func (db *DB) GetUserIDByIdentity(ctx context.Context, provider, subject string) (string, error) {
	var id string
	err := db.Pool.QueryRow(ctx, `
		UPDATE auth_identities SET last_sign_in = NOW()
		WHERE provider = $1 AND subject = $2
		RETURNING user_id::text
	`, provider, subject).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// LinkIdentity links provider+subject to an app user (idempotent).
func (db *DB) LinkIdentity(ctx context.Context, userID, provider, subject, email string) error {
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO auth_identities (user_id, provider, subject, email)
		VALUES ($1::uuid, $2, $3, NULLIF($4, ''))
		ON CONFLICT (provider, subject) DO UPDATE SET
			email = COALESCE(EXCLUDED.email, auth_identities.email),
			last_sign_in = NOW()
	`, userID, provider, subject, email)
	return err
}
