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

func SetupRoutes(app *fiber.App, db *database.DB, cfg *config.Config) {
	// Initialize components
	jwtManager := auth.NewJWTManagerWithExpiry(cfg.JWTSecret, cfg.JWTExpiry)
	smtpClient := auth.NewSMTPClient(cfg)
	authMiddleware := middleware.NewAuthMiddleware(jwtManager)
	apiKeyMiddleware := middleware.NewAPIKeyMiddleware(cfg, jwtManager)
	rateLimiter := middleware.NewRateLimiter(10, time.Minute) // 10 requests per minute for auth

	// Initialize handlers
	authHandler := handlers.NewAuthHandler(db, jwtManager, smtpClient, cfg)
	authUsersHandler := handlers.NewAuthUsersHandler(db, jwtManager, smtpClient, cfg)
	tablesHandler := handlers.NewTablesHandler(db)
	queryHandler := handlers.NewQueryHandler(db)

	// API v1
	api := app.Group("/api/v1")

	// Health check
	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"version": "1.0.0",
		})
	})

	// Project info endpoint (returns API keys for dashboard)
	api.Get("/project", authMiddleware.RequireAuth(), func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"anon_key":    cfg.AnonKey,
			"service_key": cfg.ServiceKey,
			"app_url":     cfg.AppURL,
		})
	})

	// Auth routes (rate limited)
	authRoutes := api.Group("/auth")
	authRoutes.Post("/login", rateLimiter.Limit(), authHandler.Login)
	authRoutes.Post("/forgot-password", rateLimiter.Limit(), authHandler.ForgotPassword)
	authRoutes.Post("/reset-password", rateLimiter.Limit(), authHandler.ResetPassword)
	authRoutes.Post("/refresh", authHandler.RefreshToken)
	authRoutes.Get("/me", authMiddleware.RequireAuth(), authHandler.Me)

	// ============================================
	// Email callback endpoints (no API key - accessed from email links)
	// These redirect to the user's app with tokens
	// IMPORTANT: Must be registered BEFORE the /auth/v1 group with middleware
	// ============================================
	api.Get("/auth/v1/verify", authUsersHandler.VerifyEmail)
	api.Get("/auth/v1/magic", authUsersHandler.VerifyMagicLink)

	// ============================================
	// AUTH - Public endpoints for third-party apps
	// Requires API key (JWT optional - for signup/signin)
	// ============================================
	authPublic := api.Group("/auth/v1", apiKeyMiddleware.RequireAPIKeyAuthOptional())
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

	// ============================================
	// REST API - Data access for third-party apps
	// - ANON_KEY: Requires JWT (authenticated users only)
	// - SERVICE_KEY: Full access without JWT (admin/backend)
	// ============================================
	restAPI := api.Group("/rest/v1", apiKeyMiddleware.RequireAPIKey())
	restAPI.Get("/:name", tablesHandler.GetRows)          // SELECT
	restAPI.Post("/:name", tablesHandler.InsertRow)       // INSERT
	restAPI.Put("/:name/:id", tablesHandler.UpdateRow)    // UPDATE
	restAPI.Delete("/:name/:id", tablesHandler.DeleteRow) // DELETE

	// ============================================
	// Protected routes (require admin JWT)
	// ============================================
	protected := api.Group("", authMiddleware.RequireAuth())

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

	// Auth users management (admin only)
	protected.Get("/auth/users", authUsersHandler.ListUsers)
	protected.Post("/auth/users", authUsersHandler.CreateUser)
	protected.Put("/auth/users/:id", authUsersHandler.UpdateUser)
	protected.Delete("/auth/users/:id", authUsersHandler.DeleteUser)
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
