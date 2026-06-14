package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetSecret returns a persisted secret by key. ok is false when the key
// has never been stored (not an error — the caller decides whether to
// generate one).
func (db *DB) GetSecret(ctx context.Context, key string) (value string, ok bool, err error) {
	err = db.Pool.QueryRow(ctx,
		`SELECT value FROM _rapibase_secrets WHERE key = $1`, key,
	).Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return value, true, nil
}

// SetSecret stores (or overwrites) a secret value for key.
func (db *DB) SetSecret(ctx context.Context, key, value string) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO _rapibase_secrets (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, value,
	)
	return err
}
