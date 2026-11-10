// Login nativo con ID token (Google y Apple): la app inicia sesión con el
// SDK del proveedor (google_sign_in, Sign in with Apple en iOS/macOS, Apple
// JS con popup en web), obtiene un id_token y lo canjea por la sesión de
// Rapibase:
//
//	POST /api/v1/auth/oauth/{google|apple}/token   (apikey: ANON_KEY)
//	{"id_token": "...", "nonce": "<raw, si se usó>", "name": "<Apple: solo llega la 1.ª vez>"}
//
// Responde igual que /auth/signin: {token, refresh_token, expires_in, user}.
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"

	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/database"
)

// idTokenAudiences are the client IDs whose tokens this project accepts.
func (h *OAuthHandler) idTokenAudiences(provider string) []string {
	switch provider {
	case "google":
		out := append([]string{}, h.cfg.OAuthGoogleClientIDs...)
		if h.cfg.OAuthGoogleClientID != "" {
			out = append(out, h.cfg.OAuthGoogleClientID)
		}
		return out
	case "apple":
		return h.cfg.OAuthAppleClientIDs
	}
	return nil
}

func idTokenIssuer(provider string) (auth.IDTokenIssuer, bool) {
	switch provider {
	case "google":
		return auth.GoogleIssuer, true
	case "apple":
		return auth.AppleIssuer, true
	}
	return auth.IDTokenIssuer{}, false
}

// TokenSignIn verifies a provider ID token and opens a session.
func (h *OAuthHandler) TokenSignIn(c *fiber.Ctx) error {
	provider := c.Params("provider")
	iss, ok := idTokenIssuer(provider)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "id_token sign-in supports google and apple; github and facebook use the redirect flow (GET /api/v1/auth/oauth/{provider})",
		})
	}
	audiences := h.idTokenAudiences(provider)
	if len(audiences) == 0 {
		env := "OAUTH_GOOGLE_CLIENT_ID / OAUTH_GOOGLE_CLIENT_IDS"
		if provider == "apple" {
			env = "OAUTH_APPLE_CLIENT_IDS"
		}
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": provider + " sign-in is not configured (set " + env + ")",
		})
	}

	var req struct {
		IDToken string `json:"id_token"`
		Nonce   string `json:"nonce"`
		Name    string `json:"name"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body: " + err.Error(), "field": "body"})
	}
	if strings.TrimSpace(req.IDToken) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id_token is required", "field": "id_token"})
	}

	ctx := context.Background()
	ident, err := h.idTokens.Verify(ctx, iss, strings.TrimSpace(req.IDToken), audiences, req.Nonce)
	if err != nil {
		status := fiber.StatusUnauthorized
		if !errors.Is(err, auth.ErrIDToken) {
			status = fiber.StatusBadGateway // could not reach the provider's keys
		}
		return c.Status(status).JSON(fiber.Map{"error": err.Error()})
	}

	userID, err := h.db.GetUserIDByIdentity(ctx, provider, ident.Subject)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "identity lookup failed"})
	}
	if userID == "" {
		// First sign-in with this account: link by verified email, or create.
		if ident.Email == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "the id_token has no email: request the email scope on the first sign-in",
				"field": "id_token",
			})
		}
		if !ident.EmailVerified {
			// An unverified address can't be trusted to claim an account.
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "the provider has not verified this email"})
		}
		user, err := h.db.GetAuthUserByEmail(ctx, ident.Email)
		if err == nil && user != nil {
			userID = user.ID
			if !user.EmailVerified {
				_ = h.db.SetAuthUserEmailVerified(ctx, user.ID, true)
			}
		} else {
			buf := make([]byte, 24)
			if _, err := rand.Read(buf); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "signup failed"})
			}
			name := strings.TrimSpace(req.Name)
			if name == "" {
				name = ident.Name
			}
			if utf8.RuneCountInString(name) > 255 {
				name = string([]rune(name)[:255])
			}
			var namePtr *string
			if name != "" {
				namePtr = &name
			}
			subject := ident.Subject
			created, err := h.db.CreateAuthUserWithProvider(ctx, ident.Email, hex.EncodeToString(buf), namePtr, true, provider, &subject)
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "signup failed"})
			}
			userID = created.ID
		}
	}
	if err := h.db.LinkIdentity(ctx, userID, provider, ident.Subject, ident.Email); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "identity link failed"})
	}
	_ = h.db.UpdateAuthUserLastSignIn(ctx, userID)

	user, err := h.db.GetAuthUserByID(ctx, userID)
	if err != nil || user == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "user lookup failed"})
	}
	token, err := h.jwtManager.GenerateToken(user.ID, user.Email, "user", auth.AudienceApp)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate token"})
	}
	refreshToken, err := h.db.CreateAuthRefreshTokenWithExpiry(ctx, user.ID, h.cfg.RefreshExpiry)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create refresh token"})
	}
	return c.JSON(fiber.Map{
		"token":         token,
		"refresh_token": refreshToken,
		"expires_in":    int(h.cfg.JWTExpiry.Seconds()),
		"provider":      provider,
		"user": database.AuthUserPublic{
			ID:            user.ID,
			Email:         user.Email,
			EmailVerified: user.EmailVerified,
			Phone:         user.Phone,
			PhoneVerified: user.PhoneVerified,
			FullName:      user.FullName,
			AvatarURL:     user.AvatarURL,
			Provider:      user.Provider,
			LastSignIn:    user.LastSignIn,
			CreatedAt:     user.CreatedAt,
		},
	})
}

// ProviderStatus is one row of the dashboard's "Login providers" panel.
type ProviderStatus struct {
	ID          string   `json:"id"`   // google | google_native | apple | github | facebook
	Name        string   `json:"name"` // display name
	Flow        string   `json:"flow"` // "redirect" | "id_token"
	Enabled     bool     `json:"enabled"`
	Env         []string `json:"env"`                    // variables that configure it
	Missing     []string `json:"missing"`                // which of them are still empty
	CallbackURL string   `json:"callback_url,omitempty"` // redirect: register at the provider
	StartURL    string   `json:"start_url,omitempty"`    // redirect: link for the login button
	TokenURL    string   `json:"token_url,omitempty"`    // id_token: POST here
	ClientIDs   []string `json:"client_ids,omitempty"`   // id_token: accepted audiences (public values)
}

// ProvidersStatus lists every provider, configured or not (admin only).
func (h *OAuthHandler) ProvidersStatus(c *fiber.Ctx) error {
	base := strings.TrimRight(h.cfg.AppURL, "/") + "/api/v1/auth/oauth/"
	redirect := func(id, name string) ProviderStatus {
		envID, envSecret := "OAUTH_"+strings.ToUpper(id)+"_CLIENT_ID", "OAUTH_"+strings.ToUpper(id)+"_CLIENT_SECRET"
		cid, secret := h.providerCreds(id)
		missing := []string{}
		if cid == "" {
			missing = append(missing, envID)
		}
		if secret == "" {
			missing = append(missing, envSecret)
		}
		return ProviderStatus{
			ID: id, Name: name, Flow: "redirect", Enabled: len(missing) == 0,
			Env: []string{envID, envSecret}, Missing: missing,
			CallbackURL: base + id + "/callback", StartURL: base + id,
		}
	}
	native := func(id, provider, name string, env []string) ProviderStatus {
		aud := h.idTokenAudiences(provider)
		missing := []string{}
		if len(aud) == 0 {
			missing = env
		}
		return ProviderStatus{
			ID: id, Name: name, Flow: "id_token", Enabled: len(aud) > 0,
			Env: env, Missing: missing, TokenURL: base + provider + "/token", ClientIDs: aud,
		}
	}
	return c.JSON(fiber.Map{"providers": []ProviderStatus{
		redirect("google", "Google"),
		native("google_native", "google", "Google (app nativa)", []string{"OAUTH_GOOGLE_CLIENT_IDS", "OAUTH_GOOGLE_CLIENT_ID"}),
		native("apple", "apple", "Apple", []string{"OAUTH_APPLE_CLIENT_IDS"}),
		redirect("github", "GitHub"),
		redirect("facebook", "Facebook"),
	}})
}
