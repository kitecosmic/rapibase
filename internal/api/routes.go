package api

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/api/handlers"
	"github.com/rapibase/rapibase/internal/api/middleware"
	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
)

func SetupRoutes(app *fiber.App, db *database.DB, cfg *config.Config) *WebhookDispatcher {
	// Initialize components
	jwtManager := auth.NewJWTManagerWithExpiry(cfg.JWTSecret, cfg.JWTExpiry)
	smtpClient := auth.NewSMTPClient(cfg)
	authMiddleware := middleware.NewAuthMiddleware(jwtManager)
	apiKeyMiddleware := middleware.NewAPIKeyMiddleware(cfg, jwtManager)
	rateLimiter := middleware.NewRateLimiter(10, time.Minute) // 10 requests per minute for auth

	// Initialize webhook dispatcher
	webhookDispatcher := NewWebhookDispatcher(db)

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(db, jwtManager, smtpClient, cfg)
	authUsersHandler := handlers.NewAuthUsersHandler(db, jwtManager, smtpClient, cfg)
	tablesHandler := handlers.NewTablesHandler(db, webhookDispatcher)
	queryHandler := handlers.NewQueryHandler(db)
	webhooksHandler := handlers.NewWebhooksHandler(db)
	pushHandler := handlers.NewPushHandler(db)

	// API v1 (Public API for third-party apps)
	api := app.Group("/api/v1")

	// Health check
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"version": "1.0.0",
		})
	})

	// ============================================
	// DASHBOARD API (Admin panel - requires JWT)
	// ============================================
	dashboard := app.Group("/api/dashboard")

	// Dashboard Auth
	dashboardAuth := dashboard.Group("/auth")
	dashboardAuth.Post("/login", rateLimiter.Limit(), authHandler.Login)
	dashboardAuth.Post("/forgot-password", rateLimiter.Limit(), authHandler.ForgotPassword)
	dashboardAuth.Post("/reset-password", rateLimiter.Limit(), authHandler.ResetPassword)
	dashboardAuth.Post("/refresh", authHandler.RefreshToken)
	dashboardAuth.Get("/me", authMiddleware.RequireAuth(), authHandler.Me)

	// Project info endpoint (returns API keys for dashboard)
	dashboard.Get("/project", authMiddleware.RequireAuth(), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"anon_key":    cfg.AnonKey,
			"service_key": cfg.ServiceKey,
			"app_url":     cfg.AppURL,
		})
	})

	// ============================================
	// Email callback endpoints (no API key - accessed from email links)
	// These redirect to the user's app with tokens
	// ============================================
	api.Get("/auth/verify", authUsersHandler.VerifyEmail)
	api.Get("/auth/magic", authUsersHandler.VerifyMagicLink)

	// ============================================
	// AUTH - Public endpoints for third-party apps
	// Requires API key (JWT optional - for signup/signin)
	// ============================================
	authPublic := api.Group("/auth", apiKeyMiddleware.RequireAPIKeyAuthOptional())
	authPublic.Post("/signup", rateLimiter.Limit(), authUsersHandler.SignUp)
	authPublic.Post("/signin", rateLimiter.Limit(), authUsersHandler.SignIn)
	authPublic.Post("/token", authUsersHandler.RefreshToken)
	authPublic.Post("/signout", authUsersHandler.SignOut)

	// Email verification (POST requires API key)
	authPublic.Post("/resend", rateLimiter.Limit(), authUsersHandler.SendVerificationEmail)

	// Magic link (POST requires API key)
	authPublic.Post("/magiclink", rateLimiter.Limit(), authUsersHandler.SendMagicLink)

	// Password reset
	authPublic.Post("/forgot-password", rateLimiter.Limit(), authUsersHandler.ForgotPassword)
	authPublic.Post("/reset-password", authUsersHandler.ResetPassword)

	// User management (SERVICE_KEY only - for external automations)
	authPublic.Get("/users", authUsersHandler.ListUsersAPI)
	authPublic.Post("/users", authUsersHandler.CreateUserAPI)
	authPublic.Put("/users/:id", authUsersHandler.UpdateUserAPI)
	authPublic.Delete("/users/:id", authUsersHandler.DeleteUserAPI)
	authPublic.Post("/users/bulk", authUsersHandler.BulkUpdateUsersAPI)

	// ============================================
	// REST API - Data access for third-party apps
	// - ANON_KEY: Requires JWT (authenticated users only)
	// - SERVICE_KEY: Full access without JWT (admin/backend)
	// ============================================
	restAPI := api.Group("/rest", apiKeyMiddleware.RequireAPIKey())
	restAPI.Get("/:name", tablesHandler.GetRows)          // SELECT
	restAPI.Post("/:name", tablesHandler.InsertRow)       // INSERT
	restAPI.Put("/:name/:id", tablesHandler.UpdateRow)    // UPDATE
	restAPI.Delete("/:name/:id", tablesHandler.DeleteRow) // DELETE

	// ============================================
	// DASHBOARD - Protected routes (require admin JWT)
	// ============================================
	protected := dashboard.Group("", authMiddleware.RequireAuth())

	// Tables routes
	protected.Get("/tables", tablesHandler.ListTables)
	protected.Post("/tables", tablesHandler.CreateTable)
	protected.Get("/tables/:name", tablesHandler.GetTableSchema)
	protected.Delete("/tables/:name", tablesHandler.DropTable)

	// Rows routes (admin panel)
	protected.Get("/tables/:name/rows", tablesHandler.GetRows)
	protected.Post("/tables/:name/rows", tablesHandler.InsertRow)
	protected.Put("/tables/:name/rows/:id", tablesHandler.UpdateRow)
	protected.Delete("/tables/:name/rows/:id", tablesHandler.DeleteRow)

	// Query routes
	protected.Post("/query", queryHandler.ExecuteQuery)

	// Import/Export routes
	protected.Post("/import/sql", queryHandler.ImportSQL)
	protected.Post("/import/json", queryHandler.ImportJSON)
	protected.Post("/import/json/:table", queryHandler.ImportJSON)
	protected.Get("/export/:table", queryHandler.ExportTable)

	// Auth users management (admin panel - uses JWT)
	// Note: API endpoints are in /api/v1/auth/users (uses SERVICE_KEY)
	protected.Get("/users", authUsersHandler.ListUsers)
	protected.Post("/users", authUsersHandler.CreateUser)
	protected.Put("/users/:id", authUsersHandler.UpdateUser)
	protected.Delete("/users/:id", authUsersHandler.DeleteUser)
	protected.Post("/users/bulk", authUsersHandler.BulkUpdateUsers)

	// ============================================
	// WEBHOOKS (admin only)
	// ============================================
	protected.Get("/webhooks", webhooksHandler.ListWebhooks)
	protected.Post("/webhooks", webhooksHandler.CreateWebhook)
	protected.Get("/webhooks/events", webhooksHandler.GetAvailableEvents)
	protected.Get("/webhooks/logs", webhooksHandler.ListWebhookLogs)
	protected.Get("/webhooks/:id", webhooksHandler.GetWebhook)
	protected.Put("/webhooks/:id", webhooksHandler.UpdateWebhook)
	protected.Delete("/webhooks/:id", webhooksHandler.DeleteWebhook)
	protected.Patch("/webhooks/:id/toggle", webhooksHandler.ToggleWebhook)

	// ============================================
	// PUSH NOTIFICATIONS CONFIG (admin only)
	// ============================================
	protected.Get("/push/config", pushHandler.GetPushConfigs)
	protected.Post("/push/config/web", pushHandler.SetupWebPush)
	protected.Post("/push/config/ios", pushHandler.SetupIOSPush)
	protected.Post("/push/config/android", pushHandler.SetupAndroidPush)
	protected.Patch("/push/config/:platform/toggle", pushHandler.TogglePushConfig)
	protected.Post("/push/send", pushHandler.SendNotification)
	protected.Get("/push/notifications", pushHandler.ListNotifications)

	// ============================================
	// PUSH - Client endpoints for third-party apps
	// Requires API key + JWT (authenticated users)
	// ============================================
	pushAPI := api.Group("/push", apiKeyMiddleware.RequireAPIKey())
	pushAPI.Get("/vapid", pushHandler.GetVAPIDPublicKey)
	pushAPI.Post("/subscribe", pushHandler.Subscribe)
	pushAPI.Delete("/subscribe", pushHandler.Unsubscribe)
	pushAPI.Get("/notifications", pushHandler.GetNotifications)
	pushAPI.Put("/notifications/:id/read", pushHandler.MarkRead)
	pushAPI.Put("/notifications/read-all", pushHandler.MarkAllRead)

	// ============================================
	// PUSH - API endpoint for automation (SERVICE_KEY only)
	// Use this from n8n, webhooks, or other automation tools
	// ============================================
	pushAPI.Post("/send", pushHandler.SendNotification)

	return webhookDispatcher
}

// ErrorHandler handles errors globally
func ErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError

	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
	}

	return c.Status(code).JSON(fiber.Map{
		"error": err.Error(),
	})
}
