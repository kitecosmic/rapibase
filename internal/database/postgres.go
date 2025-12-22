package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type DB struct {
	Pool *pgxpool.Pool
}

func New(databaseURL string) (*DB, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Connection pool settings for performance
	config.MaxConns = 25
	config.MinConns = 5
	config.MaxConnLifetime = time.Hour
	config.MaxConnIdleTime = 30 * time.Minute
	config.HealthCheckPeriod = time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{Pool: pool}, nil
}

func (db *DB) Close() {
	db.Pool.Close()
}

func (db *DB) Migrate() error {
	ctx := context.Background()

	migrations := []string{
		// Users table
		`CREATE TABLE IF NOT EXISTS _rapibase_users (
			id BIGSERIAL PRIMARY KEY,
			email VARCHAR(255) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			role VARCHAR(50) NOT NULL DEFAULT 'user',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Password reset tokens
		`CREATE TABLE IF NOT EXISTS _rapibase_password_resets (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES _rapibase_users(id) ON DELETE CASCADE,
			token VARCHAR(255) UNIQUE NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			used BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Refresh tokens
		`CREATE TABLE IF NOT EXISTS _rapibase_refresh_tokens (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES _rapibase_users(id) ON DELETE CASCADE,
			token VARCHAR(255) UNIQUE NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Indexes for internal tables
		`CREATE INDEX IF NOT EXISTS idx_users_email ON _rapibase_users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_password_resets_token ON _rapibase_password_resets(token)`,
		`CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token ON _rapibase_refresh_tokens(token)`,

		// ============================================
		// AUTH USERS - For third-party applications
		// ============================================
		`CREATE TABLE IF NOT EXISTS auth_users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			email VARCHAR(255) UNIQUE NOT NULL,
			password_hash VARCHAR(255) NOT NULL,
			email_verified BOOLEAN DEFAULT FALSE,
			phone VARCHAR(50),
			phone_verified BOOLEAN DEFAULT FALSE,
			full_name VARCHAR(255),
			avatar_url TEXT,
			role VARCHAR(50) DEFAULT 'user',
			metadata JSONB DEFAULT '{}',
			provider VARCHAR(50) DEFAULT 'email',
			provider_id VARCHAR(255),
			last_sign_in TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Add role column if not exists (migration for existing databases)
		`DO $$ BEGIN
			ALTER TABLE auth_users ADD COLUMN IF NOT EXISTS role VARCHAR(50) DEFAULT 'user';
		EXCEPTION WHEN others THEN NULL;
		END $$`,

		// Auth refresh tokens for app users
		`CREATE TABLE IF NOT EXISTS auth_refresh_tokens (
			id BIGSERIAL PRIMARY KEY,
			user_id UUID NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
			token VARCHAR(255) UNIQUE NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Auth tokens (verification, magic link, password reset)
		`CREATE TABLE IF NOT EXISTS auth_tokens (
			id BIGSERIAL PRIMARY KEY,
			user_id UUID NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
			token VARCHAR(255) UNIQUE NOT NULL,
			token_type VARCHAR(50) NOT NULL,
			expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
			used BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Indexes for auth tables
		`CREATE INDEX IF NOT EXISTS idx_auth_users_email ON auth_users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_refresh_tokens_token ON auth_refresh_tokens(token)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_refresh_tokens_user ON auth_refresh_tokens(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_tokens_token ON auth_tokens(token)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_tokens_user ON auth_tokens(user_id)`,

		// ============================================
		// WEBHOOKS
		// ============================================
		`CREATE TABLE IF NOT EXISTS _rapibase_webhooks (
			id BIGSERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			url TEXT NOT NULL,
			secret VARCHAR(255),
			events JSONB NOT NULL DEFAULT '[]',
			headers JSONB DEFAULT '{}',
			enabled BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS _rapibase_webhook_logs (
			id BIGSERIAL PRIMARY KEY,
			webhook_id BIGINT REFERENCES _rapibase_webhooks(id) ON DELETE SET NULL,
			event VARCHAR(255) NOT NULL,
			payload TEXT,
			response_status INT,
			response_body TEXT,
			attempts INT DEFAULT 1,
			success BOOLEAN DEFAULT FALSE,
			error TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		`CREATE INDEX IF NOT EXISTS idx_webhook_logs_webhook ON _rapibase_webhook_logs(webhook_id)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_logs_created ON _rapibase_webhook_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_webhooks_enabled ON _rapibase_webhooks(enabled)`,

		// ============================================
		// PUSH NOTIFICATIONS
		// ============================================
		`CREATE TABLE IF NOT EXISTS _rapibase_push_config (
			id BIGSERIAL PRIMARY KEY,
			platform VARCHAR(50) NOT NULL UNIQUE,
			config JSONB NOT NULL DEFAULT '{}',
			enabled BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS _rapibase_push_subscriptions (
			id BIGSERIAL PRIMARY KEY,
			user_id UUID REFERENCES auth_users(id) ON DELETE CASCADE,
			platform VARCHAR(50) NOT NULL,
			token TEXT NOT NULL,
			endpoint TEXT,
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			expires_at TIMESTAMP WITH TIME ZONE,
			UNIQUE(user_id, platform, token)
		)`,

		`CREATE TABLE IF NOT EXISTS _rapibase_notifications (
			id BIGSERIAL PRIMARY KEY,
			user_id UUID REFERENCES auth_users(id) ON DELETE CASCADE,
			title VARCHAR(255) NOT NULL,
			body TEXT,
			data JSONB DEFAULT '{}',
			sent_at TIMESTAMP WITH TIME ZONE,
			read_at TIMESTAMP WITH TIME ZONE,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		`CREATE INDEX IF NOT EXISTS idx_push_subs_user ON _rapibase_push_subscriptions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_push_subs_platform ON _rapibase_push_subscriptions(platform)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_user ON _rapibase_notifications(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_notifications_read ON _rapibase_notifications(user_id, read_at)`,

		// ============================================
		// STORAGE - File metadata and relationships
		// ============================================
		`CREATE TABLE IF NOT EXISTS storage_buckets (
			id VARCHAR(255) PRIMARY KEY,
			name VARCHAR(255) NOT NULL UNIQUE,
			public BOOLEAN DEFAULT FALSE,
			file_size_limit BIGINT,
			allowed_mime_types TEXT[],
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS storage_objects (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			bucket_id VARCHAR(255) NOT NULL REFERENCES storage_buckets(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			owner UUID REFERENCES auth_users(id) ON DELETE SET NULL,
			size BIGINT NOT NULL DEFAULT 0,
			mime_type VARCHAR(255),
			etag VARCHAR(255),
			path_tokens TEXT[] GENERATED ALWAYS AS (string_to_array(name, '/')) STORED,
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			UNIQUE(bucket_id, name)
		)`,

		`CREATE INDEX IF NOT EXISTS idx_storage_objects_bucket ON storage_objects(bucket_id)`,
		`CREATE INDEX IF NOT EXISTS idx_storage_objects_owner ON storage_objects(owner)`,
		`CREATE INDEX IF NOT EXISTS idx_storage_objects_name ON storage_objects(name)`,
		`CREATE INDEX IF NOT EXISTS idx_storage_objects_path ON storage_objects USING GIN(path_tokens)`,
	}

	for _, migration := range migrations {
		if _, err := db.Pool.Exec(ctx, migration); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

func (db *DB) CreateAdminIfNotExists(email, password string) error {
	ctx := context.Background()

	// Check if admin exists
	var exists bool
	err := db.Pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM _rapibase_users WHERE email = $1)",
		email,
	).Scan(&exists)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	// Hash password
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}

	// Create admin
	_, err = db.Pool.Exec(ctx,
		`INSERT INTO _rapibase_users (email, password_hash, role) VALUES ($1, $2, 'admin')`,
		email, string(hash),
	)

	return err
}
