package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
)

type APIKeyMiddleware struct {
	cfg        *config.Config
	jwtManager *auth.JWTManager
}

func NewAPIKeyMiddleware(cfg *config.Config, jwtManager *auth.JWTManager) *APIKeyMiddleware {
	return &APIKeyMiddleware{cfg: cfg, jwtManager: jwtManager}
}

// RequireAPIKey validates the apikey header for public API access
// - ANON_KEY: Requires valid JWT (for authenticated app users)
// - SERVICE_KEY: Full access without JWT (for backend/admin)
func (m *APIKeyMiddleware) RequireAPIKey() fiber.Handler {
	return func(c *fiber.Ctx) error {
		apiKey := c.Get("apikey")

		if apiKey == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing API key. Include 'apikey' header with your anon or service key.",
			})
		}

		// SERVICE_KEY: Full access without JWT
		if apiKey == m.cfg.ServiceKey {
			c.Locals("apiKeyType", "service")
			return c.Next()
		}

		// ANON_KEY: Requires valid JWT
		if apiKey == m.cfg.AnonKey {
			c.Locals("apiKeyType", "anon")

			// Validate JWT for anon key
			authHeader := c.Get("Authorization")
			if authHeader == "" {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error": "Authorization required. Include 'Authorization: Bearer <token>' header.",
				})
			}

			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error": "Invalid authorization header format. Use 'Bearer <token>'.",
				})
			}

			token := parts[1]
			claims, err := m.jwtManager.ValidateToken(token)
			if err != nil {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error": "Invalid or expired token",
				})
			}

			// Store user info in context
			c.Locals("userID", claims.UserID)
			c.Locals("userEmail", claims.Email)
			c.Locals("userRole", claims.Role)

			return c.Next()
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid API key",
		})
	}
}

// RequireAPIKeyAuthOptional validates API key but JWT is optional
// Used for auth endpoints like signup/signin where user isn't authenticated yet
func (m *APIKeyMiddleware) RequireAPIKeyAuthOptional() fiber.Handler {
	return func(c *fiber.Ctx) error {
		apiKey := c.Get("apikey")

		if apiKey == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing API key. Include 'apikey' header with your anon or service key.",
			})
		}

		if apiKey == m.cfg.AnonKey {
			c.Locals("apiKeyType", "anon")
			return c.Next()
		}

		if apiKey == m.cfg.ServiceKey {
			c.Locals("apiKeyType", "service")
			return c.Next()
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid API key",
		})
	}
}

// RequireServiceKey requires the service key (for admin operations)
func (m *APIKeyMiddleware) RequireServiceKey() fiber.Handler {
	return func(c *fiber.Ctx) error {
		apiKey := c.Get("apikey")
		if apiKey == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing API key. Service key required for this operation.",
			})
		}

		if apiKey != m.cfg.ServiceKey {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "Service key required for this operation",
			})
		}

		c.Locals("apiKeyType", "service")
		return c.Next()
	}
}

// OptionalAPIKey extracts API key info if present, but doesn't require it
func (m *APIKeyMiddleware) OptionalAPIKey() fiber.Handler {
	return func(c *fiber.Ctx) error {
		apiKey := c.Get("apikey")
		if apiKey == "" {
			return c.Next()
		}

		if apiKey == m.cfg.AnonKey {
			c.Locals("apiKeyType", "anon")
		} else if apiKey == m.cfg.ServiceKey {
			c.Locals("apiKeyType", "service")
		}

		return c.Next()
	}
}
