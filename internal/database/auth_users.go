package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// AuthUser represents a user for third-party applications
type AuthUser struct {
	ID            string                 `json:"id"`
	Email         string                 `json:"email"`
	PasswordHash  string                 `json:"-"`
	EmailVerified bool                   `json:"email_verified"`
	Phone         *string                `json:"phone,omitempty"`
	PhoneVerified bool                   `json:"phone_verified"`
	FullName      *string                `json:"full_name,omitempty"`
	AvatarURL     *string                `json:"avatar_url,omitempty"`
	Role          string                 `json:"role"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	Provider      string                 `json:"provider"`
	ProviderID    *string                `json:"provider_id,omitempty"`
	LastSignIn    *time.Time             `json:"last_sign_in,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

// AuthUserPublic is the public representation of an auth user
type AuthUserPublic struct {
	ID            string                 `json:"id"`
	Email         string                 `json:"email"`
	EmailVerified bool                   `json:"email_verified"`
	Phone         *string                `json:"phone,omitempty"`
	PhoneVerified bool                   `json:"phone_verified"`
	FullName      *string                `json:"full_name,omitempty"`
	AvatarURL     *string                `json:"avatar_url,omitempty"`
	Role          string                 `json:"role"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	Provider      string                 `json:"provider"`
	LastSignIn    *time.Time             `json:"last_sign_in,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
}

// GetAllAuthUsers returns all auth users for the admin panel
func (db *DB) GetAllAuthUsers(ctx context.Context) ([]AuthUserPublic, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT id, email, email_verified, phone, phone_verified, full_name, avatar_url, 
		        COALESCE(role, 'user') as role, COALESCE(metadata, '{}'::jsonb) as metadata, provider, last_sign_in, created_at 
		 FROM auth_users ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []AuthUserPublic
	for rows.Next() {
		var u AuthUserPublic
		if err := rows.Scan(&u.ID, &u.Email, &u.EmailVerified, &u.Phone, &u.PhoneVerified,
			&u.FullName, &u.AvatarURL, &u.Role, &u.Metadata, &u.Provider, &u.LastSignIn, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}

	return users, nil
}

// GetAuthUserByID retrieves an auth user by ID
func (db *DB) GetAuthUserByID(ctx context.Context, id string) (*AuthUser, error) {
	var user AuthUser
	err := db.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, email_verified, phone, phone_verified, 
		        full_name, avatar_url, COALESCE(role, 'user') as role, COALESCE(metadata, '{}'::jsonb) as metadata, provider, provider_id, last_sign_in, created_at, updated_at 
		 FROM auth_users WHERE id = $1`,
		id,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.EmailVerified, &user.Phone,
		&user.PhoneVerified, &user.FullName, &user.AvatarURL, &user.Role, &user.Metadata, &user.Provider,
		&user.ProviderID, &user.LastSignIn, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

// GetAuthUserByEmail retrieves an auth user by email
func (db *DB) GetAuthUserByEmail(ctx context.Context, email string) (*AuthUser, error) {
	var user AuthUser
	err := db.Pool.QueryRow(ctx,
		`SELECT id, email, password_hash, email_verified, phone, phone_verified, 
		        full_name, avatar_url, provider, provider_id, last_sign_in, created_at, updated_at 
		 FROM auth_users WHERE email = $1`,
		email,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.EmailVerified, &user.Phone,
		&user.PhoneVerified, &user.FullName, &user.AvatarURL, &user.Provider,
		&user.ProviderID, &user.LastSignIn, &user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateAuthUser creates a new auth user
func (db *DB) CreateAuthUser(ctx context.Context, email, password string, fullName *string) (*AuthUserPublic, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}

	var user AuthUserPublic
	err = db.Pool.QueryRow(ctx,
		`INSERT INTO auth_users (email, password_hash, full_name) 
		 VALUES ($1, $2, $3) 
		 RETURNING id, email, email_verified, phone, phone_verified, full_name, avatar_url, provider, last_sign_in, created_at`,
		email, string(hash), fullName,
	).Scan(&user.ID, &user.Email, &user.EmailVerified, &user.Phone, &user.PhoneVerified,
		&user.FullName, &user.AvatarURL, &user.Provider, &user.LastSignIn, &user.CreatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

// CreateAuthUserAdmin creates a new auth user from admin panel (with more options)
func (db *DB) CreateAuthUserAdmin(ctx context.Context, email, password string, fullName *string, emailVerified bool) (*AuthUserPublic, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, err
	}

	var user AuthUserPublic
	err = db.Pool.QueryRow(ctx,
		`INSERT INTO auth_users (email, password_hash, full_name, email_verified) 
		 VALUES ($1, $2, $3, $4) 
		 RETURNING id, email, email_verified, phone, phone_verified, full_name, avatar_url, provider, last_sign_in, created_at`,
		email, string(hash), fullName, emailVerified,
	).Scan(&user.ID, &user.Email, &user.EmailVerified, &user.Phone, &user.PhoneVerified,
		&user.FullName, &user.AvatarURL, &user.Provider, &user.LastSignIn, &user.CreatedAt)

	if err != nil {
		return nil, err
	}
	return &user, nil
}

// UpdateAuthUser updates an auth user
func (db *DB) UpdateAuthUser(ctx context.Context, id string, email *string, password *string, fullName *string, emailVerified *bool) error {
	// Build dynamic update query
	query := "UPDATE auth_users SET updated_at = NOW()"
	args := []interface{}{}
	argNum := 1

	if email != nil {
		query += fmt.Sprintf(", email = $%d", argNum)
		args = append(args, *email)
		argNum++
	}

	if password != nil && *password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), 12)
		if err != nil {
			return err
		}
		query += fmt.Sprintf(", password_hash = $%d", argNum)
		args = append(args, string(hash))
		argNum++
	}

	if fullName != nil {
		query += fmt.Sprintf(", full_name = $%d", argNum)
		args = append(args, *fullName)
		argNum++
	}

	if emailVerified != nil {
		query += fmt.Sprintf(", email_verified = $%d", argNum)
		args = append(args, *emailVerified)
		argNum++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argNum)
	args = append(args, id)

	_, err := db.Pool.Exec(ctx, query, args...)
	return err
}

// DeleteAuthUser deletes an auth user
func (db *DB) DeleteAuthUser(ctx context.Context, id string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM auth_users WHERE id = $1`, id)
	return err
}

// UpdateAuthUserFull updates an auth user with all fields including role and metadata
func (db *DB) UpdateAuthUserFull(ctx context.Context, id string, email *string, password *string, fullName *string, phone *string, role *string, emailVerified *bool, metadata map[string]interface{}) error {
	// Build dynamic update query
	query := "UPDATE auth_users SET updated_at = NOW()"
	args := []interface{}{}
	argNum := 1

	if email != nil {
		query += fmt.Sprintf(", email = $%d", argNum)
		args = append(args, *email)
		argNum++
	}

	if password != nil && *password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), 12)
		if err != nil {
			return err
		}
		query += fmt.Sprintf(", password_hash = $%d", argNum)
		args = append(args, string(hash))
		argNum++
	}

	if fullName != nil {
		query += fmt.Sprintf(", full_name = $%d", argNum)
		args = append(args, *fullName)
		argNum++
	}

	if phone != nil {
		query += fmt.Sprintf(", phone = $%d", argNum)
		args = append(args, *phone)
		argNum++
	}

	if role != nil {
		query += fmt.Sprintf(", role = $%d", argNum)
		args = append(args, *role)
		argNum++
	}

	if emailVerified != nil {
		query += fmt.Sprintf(", email_verified = $%d", argNum)
		args = append(args, *emailVerified)
		argNum++
	}

	if metadata != nil {
		query += fmt.Sprintf(", metadata = $%d", argNum)
		args = append(args, metadata)
		argNum++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argNum)
	args = append(args, id)

	_, err := db.Pool.Exec(ctx, query, args...)
	return err
}

// UpdateAuthUserLastSignIn updates the last sign in time
func (db *DB) UpdateAuthUserLastSignIn(ctx context.Context, id string) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE auth_users SET last_sign_in = NOW(), updated_at = NOW() WHERE id = $1`,
		id)
	return err
}

// BulkUpdateAuthUsers updates multiple users matching a filter
func (db *DB) BulkUpdateAuthUsers(ctx context.Context, filterRole *string, filterEmailVerified *bool, updateRole *string, updateEmailVerified *bool, updateMetadata map[string]interface{}) (int, error) {
	// Build WHERE clause
	whereClause := "WHERE 1=1"
	whereArgs := []interface{}{}
	argNum := 1

	if filterRole != nil {
		whereClause += fmt.Sprintf(" AND role = $%d", argNum)
		whereArgs = append(whereArgs, *filterRole)
		argNum++
	}

	if filterEmailVerified != nil {
		whereClause += fmt.Sprintf(" AND email_verified = $%d", argNum)
		whereArgs = append(whereArgs, *filterEmailVerified)
		argNum++
	}

	// Build SET clause
	setClause := "SET updated_at = NOW()"
	setArgs := []interface{}{}

	if updateRole != nil {
		setClause += fmt.Sprintf(", role = $%d", argNum)
		setArgs = append(setArgs, *updateRole)
		argNum++
	}

	if updateEmailVerified != nil {
		setClause += fmt.Sprintf(", email_verified = $%d", argNum)
		setArgs = append(setArgs, *updateEmailVerified)
		argNum++
	}

	if updateMetadata != nil {
		setClause += fmt.Sprintf(", metadata = $%d", argNum)
		setArgs = append(setArgs, updateMetadata)
		argNum++
	}

	// Combine args
	allArgs := append(whereArgs, setArgs...)

	query := fmt.Sprintf("UPDATE auth_users %s %s", setClause, whereClause)
	result, err := db.Pool.Exec(ctx, query, allArgs...)
	if err != nil {
		return 0, err
	}

	return int(result.RowsAffected()), nil
}

// VerifyAuthUserPassword verifies a password against the stored hash
func VerifyAuthUserPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// CreateAuthRefreshToken creates a refresh token for an auth user
// Uses default 7 days expiration
func (db *DB) CreateAuthRefreshToken(ctx context.Context, userID string) (string, error) {
	return db.CreateAuthRefreshTokenWithExpiry(ctx, userID, 7*24*time.Hour)
}

// CreateAuthRefreshTokenWithExpiry creates a refresh token with custom expiration
func (db *DB) CreateAuthRefreshTokenWithExpiry(ctx context.Context, userID string, expiry time.Duration) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	_, err := db.Pool.Exec(ctx,
		`INSERT INTO auth_refresh_tokens (user_id, token, expires_at) VALUES ($1, $2, $3)`,
		userID, token, time.Now().Add(expiry),
	)
	if err != nil {
		return "", err
	}

	return token, nil
}

// ValidateAuthRefreshToken validates a refresh token and returns the user
func (db *DB) ValidateAuthRefreshToken(ctx context.Context, token string) (*AuthUser, error) {
	var userID string
	var expiresAt time.Time

	err := db.Pool.QueryRow(ctx,
		`SELECT user_id, expires_at FROM auth_refresh_tokens WHERE token = $1`,
		token,
	).Scan(&userID, &expiresAt)

	if err != nil {
		return nil, fmt.Errorf("invalid refresh token")
	}

	if time.Now().After(expiresAt) {
		db.Pool.Exec(ctx, `DELETE FROM auth_refresh_tokens WHERE token = $1`, token)
		return nil, fmt.Errorf("refresh token expired")
	}

	return db.GetAuthUserByID(ctx, userID)
}

// DeleteAuthRefreshToken deletes a refresh token
func (db *DB) DeleteAuthRefreshToken(ctx context.Context, token string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM auth_refresh_tokens WHERE token = $1`, token)
	return err
}

// CountAuthUsers returns the total number of auth users
func (db *DB) CountAuthUsers(ctx context.Context) (int64, error) {
	var count int64
	err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM auth_users`).Scan(&count)
	return count, err
}

// Token types
const (
	TokenTypeVerification  = "verification"
	TokenTypeMagicLink     = "magic_link"
	TokenTypePasswordReset = "password_reset"
)

// CreateAuthToken creates a token for verification, magic link, or password reset
func (db *DB) CreateAuthToken(ctx context.Context, userID string, tokenType string, expiry time.Duration) (string, error) {
	// Delete any existing tokens of this type for this user
	db.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE user_id = $1 AND token_type = $2`, userID, tokenType)

	// Generate new token
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	_, err := db.Pool.Exec(ctx,
		`INSERT INTO auth_tokens (user_id, token, token_type, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, token, tokenType, time.Now().Add(expiry),
	)
	if err != nil {
		return "", err
	}

	return token, nil
}

// ValidateAuthToken validates a token and returns the user
func (db *DB) ValidateAuthToken(ctx context.Context, token string, tokenType string) (*AuthUser, error) {
	var userID string
	var expiresAt time.Time
	var used bool

	err := db.Pool.QueryRow(ctx,
		`SELECT user_id, expires_at, used FROM auth_tokens WHERE token = $1 AND token_type = $2`,
		token, tokenType,
	).Scan(&userID, &expiresAt, &used)

	if err != nil {
		return nil, fmt.Errorf("invalid token")
	}

	if used {
		return nil, fmt.Errorf("token already used")
	}

	if time.Now().After(expiresAt) {
		db.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE token = $1`, token)
		return nil, fmt.Errorf("token expired")
	}

	return db.GetAuthUserByID(ctx, userID)
}

// MarkAuthTokenUsed marks a token as used
func (db *DB) MarkAuthTokenUsed(ctx context.Context, token string) error {
	_, err := db.Pool.Exec(ctx, `UPDATE auth_tokens SET used = TRUE WHERE token = $1`, token)
	return err
}

// DeleteAuthToken deletes a token
func (db *DB) DeleteAuthToken(ctx context.Context, token string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM auth_tokens WHERE token = $1`, token)
	return err
}

// SetAuthUserEmailVerified sets the email_verified flag for a user
func (db *DB) SetAuthUserEmailVerified(ctx context.Context, userID string, verified bool) error {
	_, err := db.Pool.Exec(ctx,
		`UPDATE auth_users SET email_verified = $1, updated_at = NOW() WHERE id = $2`,
		verified, userID)
	return err
}

// UpdateAuthUserPassword updates a user's password
func (db *DB) UpdateAuthUserPassword(ctx context.Context, userID string, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Pool.Exec(ctx,
		`UPDATE auth_users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		string(hash), userID)
	return err
}
