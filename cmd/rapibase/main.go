package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/joho/godotenv"

	"github.com/rapibase/rapibase/internal/api"
	"github.com/rapibase/rapibase/internal/api/middleware"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/realtime"
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
	// Skip the Docker HEALTHCHECK's /api/v1/health probe (every 30s) in the
	// request log — it buries real traffic under thousands of no-op lines.
	app.Use(logger.New(logger.Config{
		Next: func(c *fiber.Ctx) bool { return c.Path() == "/api/v1/health" },
	}))
	// AllowCredentials deliberately omitted: the API authenticates via the
	// apikey/Authorization headers, never cookies, and the spec forbids
	// credentials together with a wildcard origin (Fiber >= 2.52.1 panics
	// on that combination at startup).
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.CORSOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization, apikey",
		AllowMethods: "GET, POST, PUT, DELETE, OPTIONS",
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

	// Admin dashboard (React SPA): always under the reserved /_/ path — the
	// SPA is built with base=/_/ so its assets resolve there too. This frees
	// the root for the user's own frontend.
	app.Static("/_", "./web/dist")
	app.Get("/_/*", func(c *fiber.Ctx) error {
		return c.SendFile("./web/dist/index.html")
	})

	if st, err := os.Stat(cfg.PublicDir); err == nil && st.IsDir() {
		// Public site: PUBLIC_DIR exists → serve it at / with SPA fallback.
		// Same-origin as /api/v1/*: fetch('/api/...') needs no CORS setup.
		log.Printf("🌐 Public site: serving %s at / (admin dashboard at /_/)", cfg.PublicDir)
		app.Static("/", cfg.PublicDir)
		app.Get("/*", func(c *fiber.Ctx) error {
			return c.SendFile(filepath.Join(cfg.PublicDir, "index.html"))
		})
	} else {
		// No public site: send everything to the dashboard, preserving old
		// deep links (/tables → /_/tables) and email links with query strings.
		app.Get("/*", func(c *fiber.Ctx) error {
			return c.Redirect("/_"+c.OriginalURL(), fiber.StatusFound)
		})
	}

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
	log.Printf("🖥️  Admin dashboard: http://localhost:%s/_/", port)
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

// startRealtime builds the realtime.Service and hands Bootstrap + Run to
// their own goroutine. Any failure is logged loudly but never aborts
// startup — the rest of the API keeps working.
//
// Bootstrap is retried instead of attempted once. A single failed attempt
// used to disable postgres_changes for the whole life of the process
// while REST, broadcast and presence carried on serving, so the instance
// looked healthy and only the "live" parts were dead — with one log line
// an hour into the past as the only clue. The causes are all transient or
// operator-fixable (Postgres not accepting connections yet, wal_level
// still on replica, replication rights missing), so the loop keeps trying
// and the instance heals itself once the cause is gone.
func startRealtime(ctx context.Context, app *fiber.App, db *database.DB, cfg *config.Config) {
	svc, err := api.SetupRealtime(ctx, app, db, cfg)
	if err != nil {
		log.Printf("⚠️  Realtime: setup failed, endpoint disabled: %v", err)
		return
	}

	go func() {
		// Until this succeeds the WebSocket endpoint is mounted and
		// broadcast/presence/rpc work; only postgres_changes waits.
		if err := bootstrapWithRetry(ctx, svc, cfg); err != nil {
			return // ctx cancelled — shutting down
		}
		log.Printf("✅ Realtime: bootstrap OK (slot=%s publication=%s)",
			cfg.RealtimeSlotName, cfg.RealtimePublicationName)

		if err := svc.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("⚠️  Realtime: service exited with error: %v", err)
		}
	}()
}

// bootstrapWithRetry runs the realtime Postgres bootstrap until it
// succeeds or ctx is cancelled, with capped exponential backoff.
func bootstrapWithRetry(ctx context.Context, svc *realtime.Service, cfg *config.Config) error {
	const (
		minBackoff = 2 * time.Second
		maxBackoff = 60 * time.Second
	)
	backoff := minBackoff
	for {
		err := svc.Bootstrap(ctx, cfg.DatabaseURL)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("⚠️  Realtime: bootstrap failed, retrying in %s: %v", backoff, err)
		log.Printf("    Hint: set wal_level=logical and grant REPLICATION to the rapibase role.")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}
