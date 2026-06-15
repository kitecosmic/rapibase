package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"

	"github.com/rapibase/rapibase/internal/api"
	"github.com/rapibase/rapibase/internal/api/middleware"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
)

func main() {
	// Load .env file if exists
	godotenv.Load()

	// Load configuration
	cfg := config.Load()

	// Initialize database
	db, err := database.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := db.Migrate(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Finalize secrets: generate+persist any left blank, and refuse to
	// boot if an operator explicitly configured a known weak default.
	// Runs after Migrate (needs the _rapibase_secrets table) and before
	// anything that consumes the keys.
	if err := cfg.ResolveSecrets(context.Background(), db); err != nil {
		log.Fatalf("Secret configuration error: %v", err)
	}

	// Create admin user if not exists
	if err := db.CreateAdminIfNotExists(cfg.AdminEmail, cfg.AdminPassword); err != nil {
		log.Printf("Warning: Could not create admin user: %v", err)
	}

	// Initialize Fiber app.
	//
	// BodyLimit is intentionally large (5 GiB) to support bulk data imports
	// (CSV/JSON/SQL). StreamRequestBody avoids buffering the whole request
	// body in memory before invoking the handler: multipart files are
	// streamed directly into the import pipeline, which then streams them
	// into Postgres via COPY FROM STDIN (CSV) or chunked batches (JSON).
	app := fiber.New(fiber.Config{
		AppName:           "RapiBase",
		BodyLimit:         5 * 1024 * 1024 * 1024,
		StreamRequestBody: true,
		ErrorHandler:      api.ErrorHandler,
		// Behind Caddy/any reverse proxy the real client IP arrives in
		// X-Forwarded-For; make c.IP() resolve to it so the access log
		// records the actual caller, not the proxy.
		ProxyHeader: fiber.HeaderXForwardedFor,
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORSOrigins,
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization, apikey",
		AllowMethods:     "GET, POST, PUT, DELETE, OPTIONS",
		AllowCredentials: true,
	}))

	// Access log: records every /api and /mcp request with the real
	// client IP and caller identity. Registered before the routes so it
	// wraps them; the DB writer goroutine is started further down.
	accessLogger := middleware.NewAccessLogger(db, cfg.AccessLogRetentionDays)
	if cfg.AccessLogEnabled {
		app.Use(accessLogger.Middleware())
	}

	// Setup routes
	_ = api.SetupRoutes(app, db, cfg)

	// Realtime subsystem (WebSocket: postgres_changes + broadcast +
	// presence + rpc). Optional: gated by REALTIME_ENABLED and by the
	// Postgres bootstrap succeeding. Failure to start realtime never
	// blocks the rest of the API.
	rtCtx, rtCancel := context.WithCancel(context.Background())
	defer rtCancel()

	// Start the access-log DB writer (flushes buffered entries, prunes
	// old rows). Shares the realtime context so it stops on shutdown.
	if cfg.AccessLogEnabled {
		go accessLogger.Run(rtCtx)
	}

	if cfg.RealtimeEnabled {
		startRealtime(rtCtx, app, db, cfg)
	} else {
		log.Println("Realtime: disabled by REALTIME_ENABLED=false")
	}

	// Serve static files (React SPA)
	app.Static("/", "./web/dist")
	app.Get("/*", func(c *fiber.Ctx) error {
		return c.SendFile("./web/dist/index.html")
	})

	// Graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-shutdown
		log.Println("Gracefully shutting down...")
		rtCancel() // signal realtime to stop first
		_ = app.Shutdown()
	}()

	// Start server
	port := cfg.Port
	if port == "" {
		port = "8080"
	}

	log.Printf("🚀 RapiBase running on http://localhost:%s", port)
	// ANON_KEY is public by design (it ships in client apps and the docs
	// snippets), so logging it in full is harmless and saves a dashboard
	// trip. SERVICE_KEY grants full, JWT-bypassing access — it must NEVER
	// be written to logs, which end up in `docker logs`, journald and log
	// aggregators (a far wider audience than the admin-only dashboard).
	// Operators read/copy it from the dashboard → Auth → API keys
	// (protected by RequireAdmin). We print only the last few characters so
	// the active key can be identified when debugging, without exposing it.
	log.Printf("📋 API Keys:")
	log.Printf("   ANON_KEY:    %s", cfg.AnonKey)
	log.Printf("   SERVICE_KEY: configured (ends …%s) — not logged in full; copy it from the dashboard → Auth → API keys", keyTail(cfg.ServiceKey))
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// keyTail returns the last few characters of a secret so logs can point at
// which key is active without ever exposing the secret itself.
func keyTail(s string) string {
	const n = 4
	if len(s) <= n {
		return "****"
	}
	return s[len(s)-n:]
}

// startRealtime builds the realtime.Service, runs Bootstrap against
// Postgres, then launches Service.Run on its own goroutine. Any
// failure (bad WAL config, missing slot creation rights, etc.) is
// logged loudly but does not abort startup — the rest of the API
// keeps working.
func startRealtime(ctx context.Context, app *fiber.App, db *database.DB, cfg *config.Config) {
	svc, err := api.SetupRealtime(ctx, app, db, cfg)
	if err != nil {
		log.Printf("⚠️  Realtime: setup failed, endpoint disabled: %v", err)
		return
	}

	if err := svc.Bootstrap(ctx, cfg.DatabaseURL); err != nil {
		log.Printf("⚠️  Realtime: bootstrap failed, replicator will not start: %v", err)
		log.Printf("    Hint: set wal_level=logical and grant REPLICATION to the rapibase role.")
		// The WebSocket endpoint is mounted but the replicator stays
		// off; clients can still subscribe to broadcast/presence/rpc
		// channels — only postgres_changes is unavailable.
		return
	}

	log.Printf("✅ Realtime: bootstrap OK (slot=%s publication=%s)",
		cfg.RealtimeSlotName, cfg.RealtimePublicationName)

	go func() {
		if err := svc.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("⚠️  Realtime: service exited with error: %v", err)
		}
	}()
}
