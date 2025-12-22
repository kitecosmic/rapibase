package handlers

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
)

type AuthUsersHandler struct {
	db         *database.DB
	jwtManager *auth.JWTManager
	smtp       *auth.SMTPClient
	cfg        *config.Config
}

func NewAuthUsersHandler(db *database.DB, jwtManager *auth.JWTManager, smtp *auth.SMTPClient, cfg *config.Config) *AuthUsersHandler {
	return &AuthUsersHandler{
		db:         db,
		jwtManager: jwtManager,
		smtp:       smtp,
		cfg:        cfg,
	}
}

// ============================================
// PUBLIC ENDPOINTS - For third-party apps
// ============================================

// SignUp registers a new user (public endpoint)
func (h *AuthUsersHandler) SignUp(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		Email    string  `json:"email"`
		Password string  `json:"password"`
		FullName *string `json:"full_name,omitempty"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email and password are required")
	}

	if len(req.Password) < 6 {
		return fiber.NewError(fiber.StatusBadRequest, "Password must be at least 6 characters")
	}

	// Check if user exists
	existing, _ := h.db.GetAuthUserByEmail(ctx, req.Email)
	if existing != nil {
		return fiber.NewError(fiber.StatusConflict, "User already exists")
	}

	// Create user
	user, err := h.db.CreateAuthUser(ctx, req.Email, req.Password, req.FullName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create user")
	}

	// Generate tokens
	token, err := h.jwtManager.GenerateToken(user.ID, user.Email, "user")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate token")
	}

	refreshToken, err := h.db.CreateAuthRefreshTokenWithExpiry(ctx, user.ID, h.cfg.RefreshExpiry)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create refresh token")
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"token":         token,
		"refresh_token": refreshToken,
		"expires_in":    int(h.cfg.JWTExpiry.Seconds()),
		"user":          user,
	})
}

// SignIn authenticates a user (public endpoint)
func (h *AuthUsersHandler) SignIn(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email and password are required")
	}

	// Get user
	user, err := h.db.GetAuthUserByEmail(ctx, req.Email)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid credentials")
	}

	// Verify password
	if !database.VerifyAuthUserPassword(req.Password, user.PasswordHash) {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid credentials")
	}

	// Update last sign in
	h.db.UpdateAuthUserLastSignIn(ctx, user.ID)

	// Generate tokens
	token, err := h.jwtManager.GenerateToken(user.ID, user.Email, "user")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate token")
	}

	refreshToken, err := h.db.CreateAuthRefreshTokenWithExpiry(ctx, user.ID, h.cfg.RefreshExpiry)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create refresh token")
	}

	// Build public user response
	publicUser := database.AuthUserPublic{
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
	}

	return c.JSON(fiber.Map{
		"token":         token,
		"refresh_token": refreshToken,
		"expires_in":    int(h.cfg.JWTExpiry.Seconds()),
		"user":          publicUser,
	})
}

// RefreshToken refreshes an access token (public endpoint)
func (h *AuthUsersHandler) RefreshToken(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.RefreshToken == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Refresh token is required")
	}

	// Validate refresh token
	user, err := h.db.ValidateAuthRefreshToken(ctx, req.RefreshToken)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}

	// Delete old refresh token (rotative refresh tokens)
	h.db.DeleteAuthRefreshToken(ctx, req.RefreshToken)

	// Generate new tokens
	token, err := h.jwtManager.GenerateToken(user.ID, user.Email, "user")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate token")
	}

	// Create new refresh token (rotation)
	newRefreshToken, err := h.db.CreateAuthRefreshTokenWithExpiry(ctx, user.ID, h.cfg.RefreshExpiry)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create refresh token")
	}

	return c.JSON(fiber.Map{
		"token":         token,
		"refresh_token": newRefreshToken,
		"expires_in":    int(h.cfg.JWTExpiry.Seconds()),
	})
}

// GetUser returns the current authenticated user (requires auth)
func (h *AuthUsersHandler) GetUser(c *fiber.Ctx) error {
	ctx := context.Background()

	userID := c.Locals("userID").(string)

	user, err := h.db.GetAuthUserByID(ctx, userID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "User not found")
	}

	publicUser := database.AuthUserPublic{
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
	}

	return c.JSON(fiber.Map{
		"user": publicUser,
	})
}

// SignOut invalidates the refresh token
func (h *AuthUsersHandler) SignOut(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := c.BodyParser(&req); err == nil && req.RefreshToken != "" {
		h.db.DeleteAuthRefreshToken(ctx, req.RefreshToken)
	}

	return c.JSON(fiber.Map{
		"message": "Signed out successfully",
	})
}

// SendVerificationEmail sends a verification email to the user
func (h *AuthUsersHandler) SendVerificationEmail(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		Email       string `json:"email"`
		RedirectURL string `json:"redirect_url,omitempty"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email is required")
	}

	user, err := h.db.GetAuthUserByEmail(ctx, req.Email)
	if err != nil {
		// Don't reveal if user exists
		return c.JSON(fiber.Map{
			"message": "If the email exists, a verification link has been sent",
		})
	}

	if user.EmailVerified {
		return c.JSON(fiber.Map{
			"message": "Email already verified",
		})
	}

	// Create verification token (24 hours)
	token, err := h.db.CreateAuthToken(ctx, user.ID, database.TokenTypeVerification, 24*time.Hour)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create verification token")
	}

	// Build verification URL
	redirectURL := req.RedirectURL
	if redirectURL == "" {
		redirectURL = h.cfg.AuthRedirectURL
	}
	if redirectURL == "" {
		redirectURL = h.cfg.AppURL
	}
	verifyURL := h.cfg.AppURL + "/api/v1/auth/verify?token=" + token + "&redirect=" + url.QueryEscape(redirectURL)

	// Send email
	if h.cfg.IsSMTPConfigured() {
		err = h.smtp.SendEmail(user.Email, "Verify your email",
			"Click the link to verify your email: "+verifyURL)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to send email")
		}
	}

	return c.JSON(fiber.Map{
		"message": "Verification email sent",
	})
}

// VerifyEmail verifies the user's email with a token
func (h *AuthUsersHandler) VerifyEmail(c *fiber.Ctx) error {
	ctx := context.Background()

	token := c.Query("token")
	redirect := c.Query("redirect")

	// Use default redirect if not provided
	if redirect == "" {
		redirect = h.cfg.AuthRedirectURL
	}
	if redirect == "" {
		redirect = h.cfg.AppURL
	}

	if token == "" {
		return c.Redirect(redirect + "?error=missing_token")
	}

	user, err := h.db.ValidateAuthToken(ctx, token, database.TokenTypeVerification)
	if err != nil {
		return c.Redirect(redirect + "?error=invalid_token")
	}

	// Mark email as verified
	if err := h.db.SetAuthUserEmailVerified(ctx, user.ID, true); err != nil {
		return c.Redirect(redirect + "?error=verification_failed")
	}

	// Mark token as used
	h.db.MarkAuthTokenUsed(ctx, token)

	// Redirect to app with success
	return c.Redirect(redirect + "?verified=true&email=" + url.QueryEscape(user.Email))
}

// SendMagicLink sends a magic link for passwordless signin
func (h *AuthUsersHandler) SendMagicLink(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		Email       string `json:"email"`
		RedirectURL string `json:"redirect_url,omitempty"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email is required")
	}

	user, err := h.db.GetAuthUserByEmail(ctx, req.Email)
	if err != nil {
		// Don't reveal if user exists
		return c.JSON(fiber.Map{
			"message": "If the email exists, a magic link has been sent",
		})
	}

	// Create magic link token (15 minutes)
	token, err := h.db.CreateAuthToken(ctx, user.ID, database.TokenTypeMagicLink, 15*time.Minute)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create magic link")
	}

	// Build magic link URL
	redirectURL := req.RedirectURL
	if redirectURL == "" {
		redirectURL = h.cfg.AuthRedirectURL
	}
	if redirectURL == "" {
		redirectURL = h.cfg.AppURL
	}
	magicURL := h.cfg.AppURL + "/api/v1/auth/magic?token=" + token + "&redirect=" + url.QueryEscape(redirectURL)

	// Send email
	if h.cfg.IsSMTPConfigured() {
		err = h.smtp.SendEmail(user.Email, "Your magic link",
			"Click the link to sign in: "+magicURL+"\n\nThis link expires in 15 minutes.")
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to send email")
		}
	}

	return c.JSON(fiber.Map{
		"message": "Magic link sent",
	})
}

// VerifyMagicLink verifies a magic link and signs in the user
func (h *AuthUsersHandler) VerifyMagicLink(c *fiber.Ctx) error {
	ctx := context.Background()

	token := c.Query("token")
	redirect := c.Query("redirect")

	// Use default redirect if not provided
	if redirect == "" {
		redirect = h.cfg.AuthRedirectURL
	}
	if redirect == "" {
		redirect = h.cfg.AppURL
	}

	if token == "" {
		return c.Redirect(redirect + "#error=missing_token")
	}

	user, err := h.db.ValidateAuthToken(ctx, token, database.TokenTypeMagicLink)
	if err != nil {
		return c.Redirect(redirect + "#error=invalid_token")
	}

	// Mark token as used
	h.db.MarkAuthTokenUsed(ctx, token)

	// Also verify email if not verified
	if !user.EmailVerified {
		h.db.SetAuthUserEmailVerified(ctx, user.ID, true)
	}

	// Update last sign in
	h.db.UpdateAuthUserLastSignIn(ctx, user.ID)

	// Generate tokens
	jwtToken, err := h.jwtManager.GenerateToken(user.ID, user.Email, "user")
	if err != nil {
		return c.Redirect(redirect + "#error=token_generation_failed")
	}

	refreshToken, err := h.db.CreateAuthRefreshTokenWithExpiry(ctx, user.ID, h.cfg.RefreshExpiry)
	if err != nil {
		return c.Redirect(redirect + "#error=token_generation_failed")
	}

	// Redirect with tokens in URL fragment (client-side only, more secure)
	return c.Redirect(redirect + "#access_token=" + jwtToken + "&refresh_token=" + refreshToken + "&expires_in=" + fmt.Sprintf("%d", int(h.cfg.JWTExpiry.Seconds())) + "&type=magiclink")
}

// ForgotPassword sends a password reset email
func (h *AuthUsersHandler) ForgotPassword(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		Email       string `json:"email"`
		RedirectURL string `json:"redirect_url,omitempty"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email is required")
	}

	user, err := h.db.GetAuthUserByEmail(ctx, req.Email)
	if err != nil {
		// Don't reveal if user exists
		return c.JSON(fiber.Map{
			"message": "If the email exists, a password reset link has been sent",
		})
	}

	// Create password reset token (1 hour)
	token, err := h.db.CreateAuthToken(ctx, user.ID, database.TokenTypePasswordReset, time.Hour)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create reset token")
	}

	// Build reset URL - redirect to user's app reset password page
	redirectURL := req.RedirectURL
	if redirectURL == "" && h.cfg.AuthRedirectURL != "" {
		redirectURL = h.cfg.AuthRedirectURL + "/reset-password"
	}
	if redirectURL == "" {
		redirectURL = h.cfg.AppURL + "/reset-password"
	}
	resetURL := redirectURL + "?token=" + token

	// Send email
	if h.cfg.IsSMTPConfigured() {
		err = h.smtp.SendEmail(user.Email, "Reset your password",
			"Click the link to reset your password: "+resetURL+"\n\nThis link expires in 1 hour.")
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to send email")
		}
	}

	return c.JSON(fiber.Map{
		"message": "Password reset email sent",
	})
}

// ResetPassword resets the user's password with a token
func (h *AuthUsersHandler) ResetPassword(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Token == "" || req.NewPassword == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Token and new password are required")
	}

	if len(req.NewPassword) < 6 {
		return fiber.NewError(fiber.StatusBadRequest, "Password must be at least 6 characters")
	}

	user, err := h.db.ValidateAuthToken(ctx, req.Token, database.TokenTypePasswordReset)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// Update password
	if err := h.db.UpdateAuthUserPassword(ctx, user.ID, req.NewPassword); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update password")
	}

	// Mark token as used
	h.db.MarkAuthTokenUsed(ctx, req.Token)

	return c.JSON(fiber.Map{
		"message": "Password updated successfully",
	})
}

// ============================================
// ADMIN ENDPOINTS - For RapiBase admin panel
// ============================================

// ListUsers returns all auth users (admin only)
func (h *AuthUsersHandler) ListUsers(c *fiber.Ctx) error {
	ctx := context.Background()

	users, err := h.db.GetAllAuthUsers(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to fetch users")
	}

	count, _ := h.db.CountAuthUsers(ctx)

	return c.JSON(fiber.Map{
		"users": users,
		"total": count,
	})
}

// CreateUser creates a new auth user (admin only)
func (h *AuthUsersHandler) CreateUser(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		Email         string  `json:"email"`
		Password      string  `json:"password"`
		FullName      *string `json:"full_name,omitempty"`
		EmailVerified bool    `json:"email_verified"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email and password are required")
	}

	// Check if user exists
	existing, _ := h.db.GetAuthUserByEmail(ctx, req.Email)
	if existing != nil {
		return fiber.NewError(fiber.StatusConflict, "User already exists")
	}

	// Create user
	user, err := h.db.CreateAuthUserAdmin(ctx, req.Email, req.Password, req.FullName, req.EmailVerified)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create user")
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"user": user,
	})
}

// UpdateUser updates an auth user (admin only)
func (h *AuthUsersHandler) UpdateUser(c *fiber.Ctx) error {
	ctx := context.Background()
	id := c.Params("id")

	var req struct {
		Email         *string                `json:"email,omitempty"`
		Password      *string                `json:"password,omitempty"`
		FullName      *string                `json:"full_name,omitempty"`
		Phone         *string                `json:"phone,omitempty"`
		Role          *string                `json:"role,omitempty"`
		EmailVerified *bool                  `json:"email_verified,omitempty"`
		Metadata      map[string]interface{} `json:"metadata,omitempty"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Check user exists
	_, err := h.db.GetAuthUserByID(ctx, id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "User not found")
	}

	// Update user
	if err := h.db.UpdateAuthUserFull(ctx, id, req.Email, req.Password, req.FullName, req.Phone, req.Role, req.EmailVerified, req.Metadata); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update user")
	}

	// Get updated user
	user, _ := h.db.GetAuthUserByID(ctx, id)
	publicUser := database.AuthUserPublic{
		ID:            user.ID,
		Email:         user.Email,
		EmailVerified: user.EmailVerified,
		Phone:         user.Phone,
		PhoneVerified: user.PhoneVerified,
		FullName:      user.FullName,
		AvatarURL:     user.AvatarURL,
		Role:          user.Role,
		Metadata:      user.Metadata,
		Provider:      user.Provider,
		LastSignIn:    user.LastSignIn,
		CreatedAt:     user.CreatedAt,
	}

	return c.JSON(fiber.Map{
		"user": publicUser,
	})
}

// DeleteUser deletes an auth user (admin only)
func (h *AuthUsersHandler) DeleteUser(c *fiber.Ctx) error {
	ctx := context.Background()
	id := c.Params("id")

	if err := h.db.DeleteAuthUser(ctx, id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete user")
	}

	return c.JSON(fiber.Map{
		"message": "User deleted",
	})
}

// BulkUpdateUsers updates multiple auth users at once (admin only)
func (h *AuthUsersHandler) BulkUpdateUsers(c *fiber.Ctx) error {
	ctx := context.Background()

	var req struct {
		// Option 1: Update specific users by ID
		Users []struct {
			ID            string                 `json:"id"`
			Role          *string                `json:"role,omitempty"`
			Metadata      map[string]interface{} `json:"metadata,omitempty"`
			EmailVerified *bool                  `json:"email_verified,omitempty"`
		} `json:"users,omitempty"`

		// Option 2: Update all users matching a filter
		Filter *struct {
			Role          *string `json:"role,omitempty"`
			EmailVerified *bool   `json:"email_verified,omitempty"`
		} `json:"filter,omitempty"`
		Update struct {
			Role          *string                `json:"role,omitempty"`
			Metadata      map[string]interface{} `json:"metadata,omitempty"`
			EmailVerified *bool                  `json:"email_verified,omitempty"`
		} `json:"update,omitempty"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	var updated int
	var errors []string

	// Option 1: Update specific users by ID
	if len(req.Users) > 0 {
		for _, u := range req.Users {
			err := h.db.UpdateAuthUserFull(ctx, u.ID, nil, nil, nil, nil, u.Role, u.EmailVerified, u.Metadata)
			if err != nil {
				errors = append(errors, fmt.Sprintf("Failed to update user %s: %v", u.ID, err))
			} else {
				updated++
			}
		}
	}

	// Option 2: Update by filter
	if req.Filter != nil {
		count, err := h.db.BulkUpdateAuthUsers(ctx, req.Filter.Role, req.Filter.EmailVerified, req.Update.Role, req.Update.EmailVerified, req.Update.Metadata)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to bulk update users")
		}
		updated = count
	}

	response := fiber.Map{
		"updated": updated,
		"message": fmt.Sprintf("Updated %d users", updated),
	}
	if len(errors) > 0 {
		response["errors"] = errors
	}

	return c.JSON(response)
}

// ============================================
// API Endpoints (require SERVICE_KEY via apikey header)
// These are for external automations (n8n, scripts, etc.)
// ============================================

// requireServiceKey checks if the request has a valid SERVICE_KEY
func (h *AuthUsersHandler) requireServiceKey(c *fiber.Ctx) error {
	apiKeyType := c.Locals("apiKeyType")
	if apiKeyType != "service" {
		return fiber.NewError(fiber.StatusForbidden, "Service key required for this operation")
	}
	return nil
}

// ListUsersAPI lists all auth users (SERVICE_KEY only)
func (h *AuthUsersHandler) ListUsersAPI(c *fiber.Ctx) error {
	if err := h.requireServiceKey(c); err != nil {
		return err
	}
	return h.ListUsers(c)
}

// CreateUserAPI creates a new auth user (SERVICE_KEY only)
func (h *AuthUsersHandler) CreateUserAPI(c *fiber.Ctx) error {
	if err := h.requireServiceKey(c); err != nil {
		return err
	}
	return h.CreateUser(c)
}

// UpdateUserAPI updates an auth user (SERVICE_KEY only)
func (h *AuthUsersHandler) UpdateUserAPI(c *fiber.Ctx) error {
	if err := h.requireServiceKey(c); err != nil {
		return err
	}
	return h.UpdateUser(c)
}

// DeleteUserAPI deletes an auth user (SERVICE_KEY only)
func (h *AuthUsersHandler) DeleteUserAPI(c *fiber.Ctx) error {
	if err := h.requireServiceKey(c); err != nil {
		return err
	}
	return h.DeleteUser(c)
}

// BulkUpdateUsersAPI updates multiple auth users (SERVICE_KEY only)
func (h *AuthUsersHandler) BulkUpdateUsersAPI(c *fiber.Ctx) error {
	if err := h.requireServiceKey(c); err != nil {
		return err
	}
	return h.BulkUpdateUsers(c)
}
