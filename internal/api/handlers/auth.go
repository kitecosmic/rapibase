package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/models"
)

type AuthHandler struct {
	db   *database.DB
	jwt  *auth.JWTManager
	smtp *auth.SMTPClient
	cfg  *config.Config
}

func NewAuthHandler(db *database.DB, jwt *auth.JWTManager, smtp *auth.SMTPClient, cfg *config.Config) *AuthHandler {
	return &AuthHandler{db: db, jwt: jwt, smtp: smtp, cfg: cfg}
}

// Login handles user login
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req models.AuthRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Email and password are required",
		})
	}

	// Get user
	user, err := h.db.GetUserByEmail(c.Context(), req.Email)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid email or password",
		})
	}

	// Verify password
	if !database.VerifyPassword(req.Password, user.PasswordHash) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid email or password",
		})
	}

	// MFA: if the admin has enrolled TOTP, a valid code is mandatory.
	// The mfa_required flag lets the UI prompt for the code without
	// treating the first (code-less) attempt as a wrong password.
	if user.TOTPEnabled {
		if req.TOTPCode == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error":        "MFA code required",
				"mfa_required": true,
			})
		}
		if !auth.ValidateTOTP(user.TOTPSecret, req.TOTPCode) {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Invalid MFA code",
			})
		}
	}

	// Generate tokens
	token, err := h.jwt.GenerateToken(strconv.FormatInt(user.ID, 10), user.Email, user.Role, auth.AudienceDashboard)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate token",
		})
	}

	refreshToken, err := h.db.CreateRefreshToken(c.Context(), user.ID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate refresh token",
		})
	}

	return c.JSON(models.AuthResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         user,
	})
}

// Register handles user registration
func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req models.AuthRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Email and password are required",
		})
	}

	if len(req.Password) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Password must be at least 8 characters",
		})
	}

	// Check if user exists
	existing, _ := h.db.GetUserByEmail(c.Context(), req.Email)
	if existing != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "Email already registered",
		})
	}

	// Create user
	user, err := h.db.CreateUser(c.Context(), req.Email, req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create user",
		})
	}

	// Send welcome email (async, don't fail if it fails)
	go h.smtp.SendWelcomeEmail(user.Email)

	// Generate tokens
	token, err := h.jwt.GenerateToken(strconv.FormatInt(user.ID, 10), user.Email, user.Role, auth.AudienceDashboard)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate token",
		})
	}

	refreshToken, err := h.db.CreateRefreshToken(c.Context(), user.ID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate refresh token",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(models.AuthResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         user,
	})
}

// ForgotPassword handles forgot password requests
func (h *AuthHandler) ForgotPassword(c *fiber.Ctx) error {
	var req models.ForgotPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Email is required",
		})
	}

	// Always return success to prevent email enumeration
	user, err := h.db.GetUserByEmail(c.Context(), req.Email)
	if err != nil {
		return c.JSON(fiber.Map{
			"message": "If the email exists, a reset link has been sent",
		})
	}

	// Create reset token
	token, err := h.db.CreatePasswordResetToken(c.Context(), user.ID)
	if err != nil {
		return c.JSON(fiber.Map{
			"message": "If the email exists, a reset link has been sent",
		})
	}

	// Send email
	if err := h.smtp.SendPasswordResetEmail(user.Email, token); err != nil {
		// Log error but don't expose to user
		return c.JSON(fiber.Map{
			"message": "If the email exists, a reset link has been sent",
		})
	}

	return c.JSON(fiber.Map{
		"message": "If the email exists, a reset link has been sent",
	})
}

// ResetPassword handles password reset
func (h *AuthHandler) ResetPassword(c *fiber.Ctx) error {
	var req models.ResetPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Token == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Token and password are required",
		})
	}

	if len(req.Password) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Password must be at least 8 characters",
		})
	}

	if err := h.db.ResetPassword(c.Context(), req.Token, req.Password); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "Password reset successfully",
	})
}

// RefreshToken handles token refresh
func (h *AuthHandler) RefreshToken(c *fiber.Ctx) error {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.RefreshToken == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Refresh token is required",
		})
	}

	// Validate refresh token
	user, err := h.db.ValidateRefreshToken(c.Context(), req.RefreshToken)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Delete old refresh token
	h.db.DeleteRefreshToken(c.Context(), req.RefreshToken)

	// Generate new tokens
	token, err := h.jwt.GenerateToken(strconv.FormatInt(user.ID, 10), user.Email, user.Role, auth.AudienceDashboard)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate token",
		})
	}

	refreshToken, err := h.db.CreateRefreshToken(c.Context(), user.ID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate refresh token",
		})
	}

	return c.JSON(models.AuthResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         user,
	})
}

// ============================================
// MFA (TOTP) — dashboard admin only
// ============================================

func (h *AuthHandler) currentUserID(c *fiber.Ctx) (int64, bool) {
	s, ok := c.Locals("userID").(string)
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// MFAStatus reports whether MFA is enabled for the current admin.
func (h *AuthHandler) MFAStatus(c *fiber.Ctx) error {
	id, ok := h.currentUserID(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	user, err := h.db.GetUserByID(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	return c.JSON(fiber.Map{"enabled": user.TOTPEnabled})
}

// MFASetup generates a fresh secret and returns the otpauth URI for QR
// enrollment. MFA is not active until a code is verified.
func (h *AuthHandler) MFASetup(c *fiber.Ctx) error {
	id, ok := h.currentUserID(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	user, err := h.db.GetUserByID(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	if user.TOTPEnabled {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "MFA already enabled; disable it first to re-enroll"})
	}

	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to generate secret"})
	}
	if err := h.db.SetUserTOTPSecret(c.Context(), id, secret); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to store secret"})
	}
	return c.JSON(fiber.Map{
		"secret":      secret,
		"otpauth_url": auth.TOTPProvisioningURI(secret, user.Email, "RapiBase"),
	})
}

// MFAVerify confirms a code against the pending secret and enables MFA.
func (h *AuthHandler) MFAVerify(c *fiber.Ctx) error {
	id, ok := h.currentUserID(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil || req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Code is required"})
	}
	user, err := h.db.GetUserByID(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	if user.TOTPSecret == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "No pending MFA setup; call setup first"})
	}
	if !auth.ValidateTOTP(user.TOTPSecret, req.Code) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid code"})
	}
	if err := h.db.SetUserTOTPEnabled(c.Context(), id, true); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to enable MFA"})
	}
	return c.JSON(fiber.Map{"message": "MFA enabled"})
}

// MFADisable turns MFA off after re-checking the password and a current
// code.
func (h *AuthHandler) MFADisable(c *fiber.Ctx) error {
	id, ok := h.currentUserID(c)
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Unauthorized"})
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	user, err := h.db.GetUserByID(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "User not found"})
	}
	if !database.VerifyPassword(req.Password, user.PasswordHash) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Invalid password"})
	}
	if user.TOTPEnabled && !auth.ValidateTOTP(user.TOTPSecret, req.Code) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid code"})
	}
	if err := h.db.ClearUserTOTP(c.Context(), id); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to disable MFA"})
	}
	return c.JSON(fiber.Map{"message": "MFA disabled"})
}

// Me returns the current user
func (h *AuthHandler) Me(c *fiber.Ctx) error {
	userIDStr := c.Locals("userID").(string)
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid user ID",
		})
	}

	user, err := h.db.GetUserByID(c.Context(), userID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "User not found",
		})
	}

	return c.JSON(user)
}
