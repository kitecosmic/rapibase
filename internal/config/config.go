package config

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"time"
)

type Config struct {
	// Database
	DatabaseURL string

	// Server
	Port        string
	AppURL      string
	CORSOrigins string

	// Auth
	JWTSecret     string
	AdminEmail    string
	AdminPassword string

	// API Keys
	AnonKey    string
	ServiceKey string

	// Token Expiration
	JWTExpiry     time.Duration
	RefreshExpiry time.Duration

	// SMTP
	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPass     string
	SMTPFrom     string
	SMTPFromName string

	// Auth Redirects (for third-party apps)
	AuthRedirectURL string // Where to redirect after email verification, magic link, etc.

	// Storage (MinIO)
	StorageEnabled   bool
	StorageEndpoint  string
	StorageAccessKey string
	StorageSecretKey string
	StorageUseSSL    bool
	StoragePublicURL string
}

func Load() *Config {
	return &Config{
		// Database
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/rapibase?sslmode=disable"),

		// Server
		Port:        getEnv("PORT", "8080"),
		AppURL:      getEnv("APP_URL", "http://localhost:8080"),
		CORSOrigins: getEnv("CORS_ORIGINS", "*"),

		// Auth
		JWTSecret:     getEnv("JWT_SECRET", "change-this-secret-in-production"),
		AdminEmail:    getEnv("ADMIN_EMAIL", "admin@rapibase.local"),
		AdminPassword: getEnv("ADMIN_PASSWORD", "admin123"),

		// API Keys - generate random ones if not set
		AnonKey:    getEnvOrGenerate("ANON_KEY"),
		ServiceKey: getEnvOrGenerate("SERVICE_KEY"),

		// Token Expiration
		JWTExpiry:     parseDuration(getEnv("JWT_EXPIRY", "1h"), time.Hour),
		RefreshExpiry: parseDuration(getEnv("REFRESH_EXPIRY", "7d"), 7*24*time.Hour),

		// SMTP
		SMTPHost:     getEnv("SMTP_HOST", ""),
		SMTPPort:     getEnv("SMTP_PORT", "587"),
		SMTPUser:     getEnv("SMTP_USER", ""),
		SMTPPass:     getEnv("SMTP_PASS", ""),
		SMTPFrom:     getEnv("SMTP_FROM", ""),
		SMTPFromName: getEnv("SMTP_FROM_NAME", "RapiBase"),

		// Auth Redirects - defaults to APP_URL if not set
		AuthRedirectURL: getEnv("AUTH_REDIRECT_URL", ""),

		// Storage (MinIO)
		StorageEnabled:   getEnvBool("STORAGE_ENABLED", true),
		StorageEndpoint:  getEnv("STORAGE_ENDPOINT", "localhost:9000"),
		StorageAccessKey: getEnv("STORAGE_ACCESS_KEY", "minioadmin"),
		StorageSecretKey: getEnv("STORAGE_SECRET_KEY", "minioadmin"),
		StorageUseSSL:    getEnvBool("STORAGE_USE_SSL", false),
		StoragePublicURL: getEnv("STORAGE_PUBLIC_URL", "http://localhost:9000"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return strings.TrimSpace(value)
	}
	return defaultValue
}

func getEnvOrGenerate(key string) string {
	if value := os.Getenv(key); value != "" {
		return strings.TrimSpace(value)
	}
	// Generate a random 32-byte key
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return base64.URLEncoding.EncodeToString(bytes)
}

func (c *Config) IsSMTPConfigured() bool {
	return c.SMTPHost != "" && c.SMTPUser != "" && c.SMTPPass != ""
}

func (c *Config) IsStorageEnabled() bool {
	return c.StorageEnabled && c.StorageEndpoint != "" && c.StorageAccessKey != "" && c.StorageSecretKey != ""
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return strings.ToLower(strings.TrimSpace(value)) == "true" || value == "1"
	}
	return defaultValue
}

// parseDuration parses duration strings like "1h", "24h", "7d", "30d"
func parseDuration(s string, defaultVal time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultVal
	}

	// Handle days specially (not supported by time.ParseDuration)
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		var d int
		if _, err := strings.NewReader(days).Read([]byte{}); err == nil {
			for _, c := range days {
				if c >= '0' && c <= '9' {
					d = d*10 + int(c-'0')
				}
			}
			if d > 0 {
				return time.Duration(d) * 24 * time.Hour
			}
		}
		return defaultVal
	}

	duration, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return duration
}
