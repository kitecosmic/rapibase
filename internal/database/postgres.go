package database

import (
	"context"
	"fmt"
	"log"
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

	// pgvector (embeddings + búsqueda por similitud) si el Postgres lo trae
	// — la imagen pgvector/pgvector:pg16 lo incluye; en un Postgres sin la
	// extensión esto solo deja un aviso y todo lo demás sigue igual.
	if _, err := db.Pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		log.Printf("pgvector no disponible en este Postgres (opcional): %v", err)
	} else {
		log.Println("pgvector habilitado (tipo `vector` disponible)")
	}

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

		// MFA (TOTP) for dashboard admins.
		`DO $$ BEGIN
			ALTER TABLE _rapibase_users ADD COLUMN IF NOT EXISTS totp_secret VARCHAR(64);
			ALTER TABLE _rapibase_users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN DEFAULT FALSE;
		EXCEPTION WHEN others THEN NULL;
		END $$`,

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

		// Persisted secrets (auto-generated keys: jwt_secret, anon_key,
		// service_key, admin_password). Lets rapibase generate strong
		// secrets on first boot and keep them stable across restarts
		// without any manual key management.
		`CREATE TABLE IF NOT EXISTS _rapibase_secrets (
			key VARCHAR(64) PRIMARY KEY,
			value TEXT NOT NULL,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)`,

		// Access log: one row per API request (IP, identity, status).
		`CREATE TABLE IF NOT EXISTS _rapibase_access_log (
			id BIGSERIAL PRIMARY KEY,
			ts TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
			ip VARCHAR(64),
			method VARCHAR(10),
			path TEXT,
			status INT,
			latency_ms BIGINT,
			key_type VARCHAR(20),
			user_id TEXT,
			user_role VARCHAR(50),
			user_agent TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_access_log_ts ON _rapibase_access_log(ts DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_access_log_ip ON _rapibase_access_log(ip)`,

		// RLS config registry: which tables have row-level security and
		// in which mode. Maintained by SetTableRLS/DisableTableRLS and
		// read by the realtime layer to mirror REST authorization.
		`CREATE TABLE IF NOT EXISTS _rapibase_rls (
			table_name VARCHAR(63) PRIMARY KEY,
			mode VARCHAR(20) NOT NULL,
			owner_column VARCHAR(63),
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
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

		// ============================================
		// ROW-LEVEL SECURITY (RLS) bootstrap
		// ============================================
		// Low-privilege roles the public API switches into per request.
		// Wrapped so a managed Postgres without CREATEROLE logs a clear
		// warning instead of aborting startup — the public API then
		// fails closed until the roles are provisioned by an operator.
		`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rapibase_authenticated') THEN
				CREATE ROLE rapibase_authenticated NOLOGIN NOINHERIT;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rapibase_anon') THEN
				CREATE ROLE rapibase_anon NOLOGIN NOINHERIT;
			END IF;
		EXCEPTION WHEN insufficient_privilege THEN
			RAISE WARNING 'rapibase: cannot create RLS roles (need CREATEROLE/superuser); public API will fail closed until provisioned';
		END $$`,

		// Let the connecting (pooled) role SET ROLE into them and give
		// the roles schema usage. No table privileges are granted here:
		// tables are opt-in via EnableTableRLS, so the default is deny.
		`DO $$
		BEGIN
			EXECUTE format('GRANT rapibase_authenticated TO %I', current_user);
			EXECUTE format('GRANT rapibase_anon TO %I', current_user);
			GRANT USAGE ON SCHEMA public TO rapibase_authenticated, rapibase_anon;
		EXCEPTION WHEN OTHERS THEN
			RAISE WARNING 'rapibase: cannot wire RLS roles to %: %', current_user, SQLERRM;
		END $$`,

		// auth.* helpers expose the request JWT claims to RLS policies,
		// mirroring the well-known Supabase functions. EXECUTE on
		// functions and the schema is available to all roles.
		`CREATE SCHEMA IF NOT EXISTS auth`,
		`GRANT USAGE ON SCHEMA auth TO PUBLIC`,
		`CREATE OR REPLACE FUNCTION auth.jwt() RETURNS jsonb LANGUAGE sql STABLE AS $$
			SELECT COALESCE(NULLIF(current_setting('request.jwt.claims', true), ''), '{}')::jsonb
		$$`,
		`CREATE OR REPLACE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
			SELECT NULLIF(auth.jwt() ->> 'sub', '')::uuid
		$$`,
		`CREATE OR REPLACE FUNCTION auth.role() RETURNS text LANGUAGE sql STABLE AS $$
			SELECT COALESCE(auth.jwt() ->> 'role', 'anon')
		$$`,
		`CREATE OR REPLACE FUNCTION auth.email() RETURNS text LANGUAGE sql STABLE AS $$
			SELECT auth.jwt() ->> 'email'
		$$`,
	}

	for _, migration := range migrations {
		if _, err := db.Pool.Exec(ctx, migration); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	// Realtime requires the connecting role to have the REPLICATION
	// attribute so it can open a logical replication slot. Superusers
	// (the default in the docker-compose) already have it; non-super
	// users do not, and operators rarely think to grant it.
	//
	// We attempt to self-grant here. The operation is intentionally
	// non-fatal: if the role lacks privileges to grant itself
	// REPLICATION (typical when the connecting role is not a
	// superuser and was not created with CREATEROLE), we log a clear
	// warning and continue — the rest of rapibase keeps working, and
	// realtime postgres_changes will fail gracefully at Bootstrap
	// time with an actionable error.
	db.ensureReplicationRole(ctx)

	return nil
}

// ensureReplicationRole grants the REPLICATION attribute to the
// current role if it does not already have it. Idempotent and
// non-fatal — any failure is logged and ignored.
func (db *DB) ensureReplicationRole(ctx context.Context) {
	var hasReplication bool
	err := db.Pool.QueryRow(ctx,
		`SELECT rolreplication FROM pg_roles WHERE rolname = current_user`,
	).Scan(&hasReplication)
	if err != nil {
		log.Printf("⚠️  Could not check role REPLICATION attribute: %v", err)
		return
	}
	if hasReplication {
		return
	}
	if _, err := db.Pool.Exec(ctx,
		`DO $$ BEGIN EXECUTE format('ALTER ROLE %I REPLICATION', current_user); END $$`,
	); err != nil {
		log.Printf("⚠️  Could not grant REPLICATION to current role: %v", err)
		log.Printf("    Realtime postgres_changes will fail until a superuser runs:")
		log.Printf("    ALTER ROLE <your_rapibase_user> REPLICATION;")
		return
	}
	log.Printf("✅ Granted REPLICATION attribute to current role")
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
