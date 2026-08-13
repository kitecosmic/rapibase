// Login social (OAuth 2.0 authorization code) para los usuarios de la app:
// Google, GitHub y Facebook. El flujo calca el del magic link — el callback
// emite el mismo JWT + refresh token y redirige a la app con los tokens en
// el fragment (#access_token=...), así el frontend no cambia nada.
//
//	GET /api/v1/auth/oauth/providers            → proveedores configurados
//	GET /api/v1/auth/oauth/{provider}           → redirige al proveedor
//	GET /api/v1/auth/oauth/{provider}/callback  → tokens y redirect a la app
//
// Un proveedor se activa configurando su par de variables de entorno
// (OAUTH_GOOGLE_CLIENT_ID/SECRET, OAUTH_GITHUB_..., OAUTH_FACEBOOK_...).
// La URL de callback a registrar en la consola del proveedor es
// {APP_URL}/api/v1/auth/oauth/{provider}/callback.
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"

	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
)

type OAuthHandler struct {
	db         *database.DB
	jwtManager *auth.JWTManager
	cfg        *config.Config
	client     *http.Client
}

func NewOAuthHandler(db *database.DB, jwtManager *auth.JWTManager, cfg *config.Config) *OAuthHandler {
	return &OAuthHandler{
		db:         db,
		jwtManager: jwtManager,
		cfg:        cfg,
		client:     &http.Client{Timeout: 15 * time.Second},
	}
}

// profile es lo único que necesitamos de cualquier proveedor.
type oauthProfile struct {
	Email    string
	Name     string
	Verified bool
}

func (h *OAuthHandler) providerCreds(provider string) (clientID, clientSecret string) {
	switch provider {
	case "google":
		return h.cfg.OAuthGoogleClientID, h.cfg.OAuthGoogleClientSecret
	case "github":
		return h.cfg.OAuthGitHubClientID, h.cfg.OAuthGitHubClientSecret
	case "facebook":
		return h.cfg.OAuthFacebookClientID, h.cfg.OAuthFacebookClientSecret
	}
	return "", ""
}

func (h *OAuthHandler) callbackURL(provider string) string {
	return strings.TrimRight(h.cfg.AppURL, "/") + "/api/v1/auth/oauth/" + provider + "/callback"
}

// Providers lists the configured providers so the app's login UI can render
// only the buttons that will work.
func (h *OAuthHandler) Providers(c *fiber.Ctx) error {
	out := []string{}
	for _, p := range []string{"google", "github", "facebook"} {
		if id, secret := h.providerCreds(p); id != "" && secret != "" {
			out = append(out, p)
		}
	}
	return c.JSON(fiber.Map{"providers": out})
}

// allowedRedirect valida el destino final para no regalar tokens a un
// redirect arbitrario: mismo host que APP_URL o AUTH_REDIRECT_URL,
// cualquier origen listado en CORS_ORIGINS, o localhost para desarrollo.
func (h *OAuthHandler) allowedRedirect(raw string) string {
	fallback := h.cfg.AuthRedirectURL
	if fallback == "" {
		fallback = h.cfg.AppURL
	}
	if raw == "" {
		return fallback
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fallback
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" {
		return raw
	}
	for _, ref := range []string{h.cfg.AppURL, h.cfg.AuthRedirectURL} {
		if ref == "" {
			continue
		}
		if r, err := url.Parse(ref); err == nil && r.Hostname() == host {
			return raw
		}
	}
	for _, o := range strings.Split(h.cfg.CORSOrigins, ",") {
		o = strings.TrimSpace(o)
		if o == "*" {
			return raw
		}
		if r, err := url.Parse(o); err == nil && r.Hostname() == host {
			return raw
		}
	}
	return fallback
}

const oauthStateAudience = "rapibase-oauth-state"

func (h *OAuthHandler) signState(provider, redirect string) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"aud":      oauthStateAudience,
		"provider": provider,
		"redirect": redirect,
		"exp":      time.Now().Add(10 * time.Minute).Unix(),
	})
	return tok.SignedString([]byte(h.cfg.JWTSecret))
}

func (h *OAuthHandler) parseState(state, provider string) (redirect string, err error) {
	tok, err := jwt.Parse(state, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(h.cfg.JWTSecret), nil
	}, jwt.WithAudience(oauthStateAudience))
	if err != nil || !tok.Valid {
		return "", fmt.Errorf("invalid state")
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok || claims["provider"] != provider {
		return "", fmt.Errorf("state/provider mismatch")
	}
	redirect, _ = claims["redirect"].(string)
	return redirect, nil
}

// Start redirects the browser to the provider's consent screen.
func (h *OAuthHandler) Start(c *fiber.Ctx) error {
	provider := c.Params("provider")
	clientID, clientSecret := h.providerCreds(provider)
	if clientID == "" || clientSecret == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": fmt.Sprintf("provider %q not configured (set OAUTH_%s_CLIENT_ID / _CLIENT_SECRET)", provider, strings.ToUpper(provider)),
		})
	}

	redirect := h.allowedRedirect(c.Query("redirect_url"))
	state, err := h.signState(provider, redirect)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "state generation failed"})
	}

	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {h.callbackURL(provider)},
		"state":         {state},
		"response_type": {"code"},
	}
	var authURL string
	switch provider {
	case "google":
		q.Set("scope", "openid email profile")
		authURL = "https://accounts.google.com/o/oauth2/v2/auth"
	case "github":
		q.Set("scope", "read:user user:email")
		authURL = "https://github.com/login/oauth/authorize"
	case "facebook":
		q.Set("scope", "email public_profile")
		authURL = "https://www.facebook.com/v19.0/dialog/oauth"
	}
	return c.Redirect(authURL+"?"+q.Encode(), fiber.StatusFound)
}

// Callback exchanges the code, resolves the profile, find-or-creates the
// app user and hands tokens to the app via URL fragment.
func (h *OAuthHandler) Callback(c *fiber.Ctx) error {
	ctx := context.Background()
	provider := c.Params("provider")

	redirect, err := h.parseState(c.Query("state"), provider)
	if err != nil || redirect == "" {
		fallback := h.allowedRedirect("")
		return c.Redirect(fallback + "#error=invalid_state")
	}
	if e := c.Query("error"); e != "" {
		// el usuario canceló en el proveedor
		return c.Redirect(redirect + "#error=" + url.QueryEscape(e))
	}
	code := c.Query("code")
	if code == "" {
		return c.Redirect(redirect + "#error=missing_code")
	}

	profile, err := h.fetchProfile(ctx, provider, code)
	if err != nil {
		return c.Redirect(redirect + "#error=oauth_exchange_failed")
	}
	if profile.Email == "" {
		// p. ej. cuenta de Facebook sin email, o permiso denegado
		return c.Redirect(redirect + "#error=no_email_from_provider")
	}
	email := strings.ToLower(strings.TrimSpace(profile.Email))

	// find-or-create por email: si ya existe, es el mismo humano (el
	// proveedor verificó el buzón); si no, se crea verificado con una
	// contraseña aleatoria (podrá hacer reset si algún día la quiere).
	var userID string
	user, err := h.db.GetAuthUserByEmail(ctx, email)
	if err == nil && user != nil {
		userID = user.ID
		if !user.EmailVerified && profile.Verified {
			_ = h.db.SetAuthUserEmailVerified(ctx, user.ID, true)
		}
	} else {
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			return c.Redirect(redirect + "#error=signup_failed")
		}
		name := profile.Name
		created, err := h.db.CreateAuthUserAdmin(ctx, email, hex.EncodeToString(buf), &name, true)
		if err != nil {
			return c.Redirect(redirect + "#error=signup_failed")
		}
		userID = created.ID
	}
	_ = h.db.UpdateAuthUserLastSignIn(ctx, userID)

	jwtToken, err := h.jwtManager.GenerateToken(userID, email, "user", auth.AudienceApp)
	if err != nil {
		return c.Redirect(redirect + "#error=token_generation_failed")
	}
	refreshToken, err := h.db.CreateAuthRefreshTokenWithExpiry(ctx, userID, h.cfg.RefreshExpiry)
	if err != nil {
		return c.Redirect(redirect + "#error=token_generation_failed")
	}

	return c.Redirect(redirect +
		"#access_token=" + jwtToken +
		"&refresh_token=" + refreshToken +
		"&expires_in=" + fmt.Sprintf("%d", int(h.cfg.JWTExpiry.Seconds())) +
		"&type=oauth&provider=" + provider)
}

// --- intercambio de code y perfil por proveedor -----------------------------

func (h *OAuthHandler) fetchProfile(ctx context.Context, provider, code string) (*oauthProfile, error) {
	clientID, clientSecret := h.providerCreds(provider)
	switch provider {
	case "google":
		return h.googleProfile(ctx, clientID, clientSecret, code)
	case "github":
		return h.githubProfile(ctx, clientID, clientSecret, code)
	case "facebook":
		return h.facebookProfile(ctx, clientID, clientSecret, code)
	}
	return nil, fmt.Errorf("unknown provider")
}

func (h *OAuthHandler) postForm(ctx context.Context, endpoint string, form url.Values, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (h *OAuthHandler) getJSON(ctx context.Context, endpoint, bearer string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("profile endpoint %d: %s", resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}

func (h *OAuthHandler) googleProfile(ctx context.Context, clientID, clientSecret, code string) (*oauthProfile, error) {
	body, err := h.postForm(ctx, "https://oauth2.googleapis.com/token", url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {h.callbackURL("google")},
		"grant_type":    {"authorization_code"},
	}, "")
	if err != nil {
		return nil, err
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("google: no access token")
	}
	var info struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := h.getJSON(ctx, "https://openidconnect.googleapis.com/v1/userinfo", tok.AccessToken, &info); err != nil {
		return nil, err
	}
	return &oauthProfile{Email: info.Email, Name: info.Name, Verified: info.EmailVerified}, nil
}

func (h *OAuthHandler) githubProfile(ctx context.Context, clientID, clientSecret, code string) (*oauthProfile, error) {
	body, err := h.postForm(ctx, "https://github.com/login/oauth/access_token", url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {h.callbackURL("github")},
	}, "application/json")
	if err != nil {
		return nil, err
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("github: no access token")
	}
	var user struct {
		Name  string `json:"name"`
		Login string `json:"login"`
		Email string `json:"email"`
	}
	if err := h.getJSON(ctx, "https://api.github.com/user", tok.AccessToken, &user); err != nil {
		return nil, err
	}
	name := user.Name
	if name == "" {
		name = user.Login
	}
	email := user.Email
	if email == "" {
		// el email del perfil puede ser privado: pedir la lista y quedarse
		// con el primario verificado
		var emails []struct {
			Email    string `json:"email"`
			Primary  bool   `json:"primary"`
			Verified bool   `json:"verified"`
		}
		if err := h.getJSON(ctx, "https://api.github.com/user/emails", tok.AccessToken, &emails); err == nil {
			for _, e := range emails {
				if e.Primary && e.Verified {
					email = e.Email
					break
				}
			}
			if email == "" {
				for _, e := range emails {
					if e.Verified {
						email = e.Email
						break
					}
				}
			}
		}
	}
	return &oauthProfile{Email: email, Name: name, Verified: true}, nil
}

func (h *OAuthHandler) facebookProfile(ctx context.Context, clientID, clientSecret, code string) (*oauthProfile, error) {
	q := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {h.callbackURL("facebook")},
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := h.getJSON(ctx, "https://graph.facebook.com/v19.0/oauth/access_token?"+q.Encode(), "", &tok); err != nil {
		return nil, err
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("facebook: no access token")
	}
	var me struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := h.getJSON(ctx, "https://graph.facebook.com/v19.0/me?fields=name,email&access_token="+url.QueryEscape(tok.AccessToken), "", &me); err != nil {
		return nil, err
	}
	// Facebook solo entrega emails verificados por ellos
	return &oauthProfile{Email: me.Email, Name: me.Name, Verified: true}, nil
}
