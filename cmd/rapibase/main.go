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

	// Create admin user if not exists
	if err := db.CreateAdminIfNotExists(cfg.AdminEmail, cfg.AdminPassword); err != nil {
		log.Printf("Warning: Could not create admin user: %v", err)
	}

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "RapiBase",
		BodyLimit:    50 * 1024 * 1024, // 50MB for imports
		ErrorHandler: api.ErrorHandler,
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

	// Setup routes
	_ = api.SetupRoutes(app, db, cfg)

	// Realtime subsystem (WebSocket: postgres_changes + broadcast +
	// presence + rpc). Optional: gated by REALTIME_ENABLED and by the
	// Postgres bootstrap succeeding. Failure to start realtime never
	// blocks the rest of the API.
	rtCtx, rtCancel := context.WithCancel(context.Background())
	defer rtCancel()
	if cfg.RealtimeEnabled {
		startRealtime(rtCtx, app, cfg)
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
	log.Printf("📋 API Keys:")
	log.Printf("   ANON_KEY:    %s", cfg.AnonKey)
	log.Printf("   SERVICE_KEY: %s", cfg.ServiceKey)
	if err := app.Listen(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// startRealtime builds the realtime.Service, runs Bootstrap against
// Postgres, then launches Service.Run on its own goroutine. Any
// failure (bad WAL config, missing slot creation rights, etc.) is
// logged loudly but does not abort startup — the rest of the API
// keeps working.
func startRealtime(ctx context.Context, app *fiber.App, cfg *config.Config) {
	svc, err := api.SetupRealtime(app, cfg)
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
