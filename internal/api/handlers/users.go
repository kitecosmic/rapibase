package handlers

import (
	"context"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
	"golang.org/x/crypto/bcrypt"
)

type UsersHandler struct {
	db *database.DB
}

func NewUsersHandler(db *database.DB) *UsersHandler {
	return &UsersHandler{db: db}
}

// ListUsers returns all users
func (h *UsersHandler) ListUsers(c *fiber.Ctx) error {
	ctx := context.Background()
	users, err := h.db.GetAllUsers(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to fetch users")
	}

	return c.JSON(fiber.Map{
		"users": users,
	})
}

// CreateUser creates a new user
func (h *UsersHandler) CreateUser(c *fiber.Ctx) error {
	ctx := context.Background()
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email and password are required")
	}

	// Check if user exists
	existing, _ := h.db.GetUserByEmail(ctx, req.Email)
	if existing != nil {
		return fiber.NewError(fiber.StatusConflict, "User already exists")
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to hash password")
	}

	// Create user
	user, err := h.db.CreateUserWithRole(ctx, req.Email, string(hashedPassword), req.IsAdmin)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create user")
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"user": user,
	})
}

// UpdateUser updates an existing user
func (h *UsersHandler) UpdateUser(c *fiber.Ctx) error {
	ctx := context.Background()
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		IsAdmin  *bool  `json:"is_admin"`
	}

	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Get existing user
	user, err := h.db.GetUserByID(ctx, id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "User not found")
	}

	// Update fields
	if req.Email != "" {
		user.Email = req.Email
	}

	if req.Password != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to hash password")
		}
		user.PasswordHash = string(hashedPassword)
	}

	if req.IsAdmin != nil {
		if *req.IsAdmin {
			user.Role = "admin"
		} else {
			user.Role = "user"
		}
	}

	// Save changes
	if err := h.db.UpdateUser(ctx, user); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update user")
	}

	return c.JSON(fiber.Map{
		"user": user,
	})
}

// DeleteUser deletes a user
func (h *UsersHandler) DeleteUser(c *fiber.Ctx) error {
	ctx := context.Background()
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid user ID")
	}

	if err := h.db.DeleteUser(ctx, id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete user")
	}

	return c.JSON(fiber.Map{
		"message": "User deleted",
	})
}
