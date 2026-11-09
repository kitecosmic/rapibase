package middleware

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
)

type APIKeyMiddleware struct {
	cfg        *config.Config
	jwtManager *auth.JWTManager
}

func NewAPIKeyMiddleware(cfg *config.Config, jwtManager *auth.JWTManager) *APIKeyMiddleware {
	return &APIKeyMiddleware{cfg: cfg, jwtManager: jwtManager}
}

// RequireAPIKey validates the apikey header for public API access
// - ANON_KEY: Requires valid JWT (for authenticated app users). /rest y /rpc
//   usan RequireAPIKeyOrAnonymous, que admite visitantes sin sesión.
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
		kind := m.cfg.KeyKind(apiKey)
		if kind == "service" {
			c.Locals("apiKeyType", "service")
			return c.Next()
		}

		// ANON_KEY: Requires valid JWT
		if kind == "anon" {
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
			claims, err := m.jwtManager.ValidateToken(token, auth.AudienceApp)
			if err != nil {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error": "Invalid or expired token",
				})
			}

			// Store user info in context
			c.Locals("userID", claims.UserID)
			c.Locals("userEmail", claims.Email)
			c.Locals("userRole", claims.Role)

			// Enforce row-level security for authenticated app users:
			// downstream data queries run as rapibase_authenticated with
			// these claims exposed to Postgres (so policies using
			// auth.uid() apply). The service key path does NOT set this
			// and keeps full access.
			claimsJSON, _ := json.Marshal(map[string]string{
				"sub":   claims.UserID,
				"role":  claims.Role,
				"email": claims.Email,
			})
			c.SetUserContext(database.WithAuth(c.Context(), database.AuthContext{
				Enforce:    true,
				ClaimsJSON: string(claimsJSON),
			}))

			return c.Next()
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid API key",
		})
	}
}

// RequireAPIKeyOrAnonymous es RequireAPIKey, pero la clave pública SIN
// header Authorization ya no da 401 en los métodos de anonMethods: la
// request sigue como visitante sin sesión y sus consultas corren como
// rapibase_anon con RLS. Así el modo RLS "public" ("cualquiera puede
// leer") se cumple de verdad. Un Authorization presente pero inválido o
// vencido sigue dando 401: degradar en silencio a anónimo escondería
// una sesión caída.
func (m *APIKeyMiddleware) RequireAPIKeyOrAnonymous(anonMethods ...string) fiber.Handler {
	withJWT := m.RequireAPIKey()
	return func(c *fiber.Ctx) error {
		if c.Get("Authorization") != "" || m.cfg.KeyKind(c.Get("apikey")) != "anon" {
			return withJWT(c)
		}
		allowed := false
		for _, meth := range anonMethods {
			if c.Method() == meth {
				allowed = true
				break
			}
		}
		if !allowed {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Authorization required. Visitors without a session can only read; include 'Authorization: Bearer <token>' to write.",
			})
		}
		c.Locals("apiKeyType", "anon")
		c.Locals("anonymous", true)
		c.SetUserContext(database.WithAuth(c.Context(), database.AuthContext{
			Enforce:    true,
			Anonymous:  true,
			ClaimsJSON: `{"role":"anon"}`,
		}))
		return c.Next()
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

		if kind := m.cfg.KeyKind(apiKey); kind != "" {
			c.Locals("apiKeyType", kind)
			return c.Next()
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid API key",
		})
	}
}

// RequireServiceKey requires the service key. Accepts either:
//   - the legacy `apikey: <key>` header used by the rest of the REST API, or
//   - the standard `Authorization: Bearer <key>` header preferred by MCP
//     clients (Claude Desktop, OpenAI Agents SDK, n8n, …) and most modern
//     HTTP integrations.
//
// Both are checked against cfg.ServiceKey. ANON_KEY is intentionally not
// accepted here — this guard is for endpoints that require admin-level
// access, e.g. /mcp.
func (m *APIKeyMiddleware) RequireServiceKey() fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := c.Get("apikey")
		if key == "" {
			// Fall back to Authorization: Bearer <token>
			if auth := c.Get("Authorization"); auth != "" {
				parts := strings.SplitN(auth, " ", 2)
				if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
					key = strings.TrimSpace(parts[1])
				}
			}
		}

		if key == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing API key. Send the service key as 'apikey: <key>' or 'Authorization: Bearer <key>'.",
			})
		}

		if !config.KeyEquals(key, m.cfg.ServiceKey) {
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

		if kind := m.cfg.KeyKind(apiKey); kind != "" {
			c.Locals("apiKeyType", kind)
		}

		return c.Next()
	}
}
