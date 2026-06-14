package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/rapibase/rapibase/internal/models"
	"golang.org/x/crypto/bcrypt"
)

// GetUserByEmail retrieves a user by email
func (db *DB) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User
	err := db.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, role, COALESCE(totp_secret,''), COALESCE(totp_enabled,false), created_at, updated_at
		FROM _rapibase_users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.TOTPSecret, &user.TOTPEnabled, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUserByID retrieves a user by ID
func (db *DB) GetUserByID(ctx context.Context, id int64) (*models.User, error) {
	var user models.User
	err := db.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, role, COALESCE(totp_secret,''), COALESCE(totp_enabled,false), created_at, updated_at
		FROM _rapibase_users WHERE id = $1`,
		id,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.TOTPSecret, &user.TOTPEnabled, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateUser creates a new user
func (db *DB) CreateUser(ctx context.Context, email, password string) (*models.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}

	var user models.User
	err = db.Pool.QueryRow(ctx,
		`INSERT INTO _rapibase_users (email, password_hash, role) 
		VALUES ($1, $2, 'user') 
		RETURNING id, email, password_hash, role, created_at, updated_at`,
		email, string(hash),
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

// SetUserTOTPSecret stores a pending TOTP secret (not yet enabled) for a
// dashboard user, replacing any previous one.
func (db *DB) SetUserTOTPSecret(ctx context.Context, userID int64, secret string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE _rapibase_users SET totp_secret = $1, totp_enabled = false, updated_at = NOW() WHERE id = $2`,
		secret, userID,
	)
	return err
}

// SetUserTOTPEnabled flips the enabled flag once a code has been verified.
func (db *DB) SetUserTOTPEnabled(ctx context.Context, userID int64, enabled bool) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE _rapibase_users SET totp_enabled = $1, updated_at = NOW() WHERE id = $2`,
		enabled, userID,
	)
	return err
}

// ClearUserTOTP removes MFA entirely for a user.
func (db *DB) ClearUserTOTP(ctx context.Context, userID int64) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE _rapibase_users SET totp_secret = NULL, totp_enabled = false, updated_at = NOW() WHERE id = $1`,
		userID,
	)
	return err
}

// VerifyPassword verifies a password against a hash
func VerifyPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// CreatePasswordResetToken creates a password reset token
func (db *DB) CreatePasswordResetToken(ctx context.Context, userID int64) (string, error) {
	// Generate random token
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	// Invalidate existing tokens
	_, err := db.Pool.Exec(ctx,
		`UPDATE _rapibase_password_resets SET used = true WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		return "", err
	}

	// Create new token (expires in 1 hour)
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO _rapibase_password_resets (user_id, token, expires_at) 
		VALUES ($1, $2, $3)`,
		userID, token, time.Now().Add(time.Hour),
	)
	if err != nil {
		return "", err
	}

	return token, nil
}

// ValidatePasswordResetToken validates a password reset token
func (db *DB) ValidatePasswordResetToken(ctx context.Context, token string) (*models.User, error) {
	var userID int64
	var expiresAt time.Time
	var used bool

	err := db.Pool.QueryRow(ctx,
		`SELECT user_id, expires_at, used FROM _rapibase_password_resets WHERE token = $1`,
		token,
	).Scan(&userID, &expiresAt, &used)

	if err != nil {
		return nil, fmt.Errorf("invalid token")
	}

	if used {
		return nil, fmt.Errorf("token already used")
	}

	if time.Now().After(expiresAt) {
		return nil, fmt.Errorf("token expired")
	}

	return db.GetUserByID(ctx, userID)
}

// ResetPassword resets a user's password using a token
func (db *DB) ResetPassword(ctx context.Context, token, newPassword string) error {
	user, err := db.ValidatePasswordResetToken(ctx, token)
	if err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), 12)
	if err != nil {
		return err
	}

	// Update password
	_, err = db.Pool.Exec(ctx,
		`UPDATE _rapibase_users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		string(hash), user.ID,
	)
	if err != nil {
		return err
	}

	// Mark token as used
	_, err = db.Pool.Exec(ctx,
		`UPDATE _rapibase_password_resets SET used = true WHERE token = $1`,
		token,
	)

	return err
}

// CreateRefreshToken creates a refresh token
func (db *DB) CreateRefreshToken(ctx context.Context, userID int64) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	// Expires in 7 days
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO _rapibase_refresh_tokens (user_id, token, expires_at) 
		VALUES ($1, $2, $3)`,
		userID, token, time.Now().Add(7*24*time.Hour),
	)
	if err != nil {
		return "", err
	}

	return token, nil
}

// ValidateRefreshToken validates a refresh token and returns the user
func (db *DB) ValidateRefreshToken(ctx context.Context, token string) (*models.User, error) {
	var userID int64
	var expiresAt time.Time

	err := db.Pool.QueryRow(ctx,
		`SELECT user_id, expires_at FROM _rapibase_refresh_tokens WHERE token = $1`,
		token,
	).Scan(&userID, &expiresAt)

	if err != nil {
		return nil, fmt.Errorf("invalid refresh token")
	}

	if time.Now().After(expiresAt) {
		// Delete expired token
		db.Pool.Exec(ctx, `DELETE FROM _rapibase_refresh_tokens WHERE token = $1`, token)
		return nil, fmt.Errorf("refresh token expired")
	}

	return db.GetUserByID(ctx, userID)
}

// DeleteRefreshToken deletes a refresh token
func (db *DB) DeleteRefreshToken(ctx context.Context, token string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM _rapibase_refresh_tokens WHERE token = $1`, token)
	return err
}

// GetAllUsers returns all users
func (db *DB) GetAllUsers(ctx context.Context) ([]models.UserPublic, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, email, role, created_at FROM _rapibase_users ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.UserPublic
	for rows.Next() {
		var u models.UserPublic
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		u.IsAdmin = u.Role == "admin"
		users = append(users, u)
	}

	return users, nil
}

// CreateUserWithRole creates a user with specified role
func (db *DB) CreateUserWithRole(ctx context.Context, email, passwordHash string, isAdmin bool) (*models.UserPublic, error) {
	role := "user"
	if isAdmin {
		role = "admin"
	}

	var user models.UserPublic
	err := db.Pool.QueryRow(ctx,
		`INSERT INTO _rapibase_users (email, password_hash, role) 
		VALUES ($1, $2, $3) 
		RETURNING id, email, role, created_at`,
		email, passwordHash, role,
	).Scan(&user.ID, &user.Email, &user.Role, &user.CreatedAt)

	if err != nil {
		return nil, err
	}
	user.IsAdmin = user.Role == "admin"
	return &user, nil
}

// UpdateUser updates a user
func (db *DB) UpdateUser(ctx context.Context, user *models.User) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE _rapibase_users SET email = $1, password_hash = $2, role = $3, updated_at = NOW() WHERE id = $4`,
		user.Email, user.PasswordHash, user.Role, user.ID,
	)
	return err
}

// DeleteUser deletes a user
func (db *DB) DeleteUser(ctx context.Context, id int) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM _rapibase_users WHERE id = $1`, id)
	return err
}
