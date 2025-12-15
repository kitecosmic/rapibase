package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/webhooks"
)

type WebhooksHandler struct {
	db *database.DB
}

func NewWebhooksHandler(db *database.DB) *WebhooksHandler {
	return &WebhooksHandler{db: db}
}

// ListWebhooks returns all webhooks
func (h *WebhooksHandler) ListWebhooks(c *fiber.Ctx) error {
	webhookList, err := h.db.ListWebhooks(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to list webhooks",
		})
	}

	// Don't expose secrets in list
	for i := range webhookList {
		if webhookList[i].Secret != "" {
			webhookList[i].Secret = "••••••••"
		}
	}

	return c.JSON(fiber.Map{
		"webhooks": webhookList,
	})
}

// CreateWebhook creates a new webhook
func (h *WebhooksHandler) CreateWebhook(c *fiber.Ctx) error {
	var req struct {
		Name    string            `json:"name"`
		URL     string            `json:"url"`
		Secret  string            `json:"secret"`
		Events  []string          `json:"events"`
		Headers map[string]string `json:"headers"`
		Enabled bool              `json:"enabled"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Name is required",
		})
	}

	if req.URL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "URL is required",
		})
	}

	if len(req.Events) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "At least one event is required",
		})
	}

	webhook := &webhooks.Webhook{
		Name:    req.Name,
		URL:     req.URL,
		Secret:  req.Secret,
		Events:  req.Events,
		Headers: req.Headers,
		Enabled: req.Enabled,
	}

	if err := h.db.CreateWebhook(c.Context(), webhook); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create webhook",
		})
	}

	// Don't expose secret in response
	if webhook.Secret != "" {
		webhook.Secret = "••••••••"
	}

	return c.Status(fiber.StatusCreated).JSON(webhook)
}

// GetWebhook returns a webhook by ID
func (h *WebhooksHandler) GetWebhook(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid webhook ID",
		})
	}

	webhook, err := h.db.GetWebhook(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Webhook not found",
		})
	}

	// Don't expose secret
	if webhook.Secret != "" {
		webhook.Secret = "••••••••"
	}

	return c.JSON(webhook)
}

// UpdateWebhook updates a webhook
func (h *WebhooksHandler) UpdateWebhook(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid webhook ID",
		})
	}

	var req struct {
		Name    string            `json:"name"`
		URL     string            `json:"url"`
		Secret  *string           `json:"secret"`
		Events  []string          `json:"events"`
		Headers map[string]string `json:"headers"`
		Enabled bool              `json:"enabled"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	// Get existing webhook
	existing, err := h.db.GetWebhook(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Webhook not found",
		})
	}

	// Update fields
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.URL != "" {
		existing.URL = req.URL
	}
	if req.Secret != nil {
		existing.Secret = *req.Secret
	}
	if len(req.Events) > 0 {
		existing.Events = req.Events
	}
	if req.Headers != nil {
		existing.Headers = req.Headers
	}
	existing.Enabled = req.Enabled

	if err := h.db.UpdateWebhook(c.Context(), existing); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update webhook",
		})
	}

	// Don't expose secret
	if existing.Secret != "" {
		existing.Secret = "••••••••"
	}

	return c.JSON(existing)
}

// DeleteWebhook deletes a webhook
func (h *WebhooksHandler) DeleteWebhook(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid webhook ID",
		})
	}

	if err := h.db.DeleteWebhook(c.Context(), id); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Webhook not found",
		})
	}

	return c.JSON(fiber.Map{
		"message": "Webhook deleted",
	})
}

// ToggleWebhook enables or disables a webhook
func (h *WebhooksHandler) ToggleWebhook(c *fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid webhook ID",
		})
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if err := h.db.ToggleWebhook(c.Context(), id, req.Enabled); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Webhook not found",
		})
	}

	return c.JSON(fiber.Map{
		"message": "Webhook updated",
		"enabled": req.Enabled,
	})
}

// ListWebhookLogs returns webhook delivery logs
func (h *WebhooksHandler) ListWebhookLogs(c *fiber.Ctx) error {
	var webhookID *int64
	var success *bool

	if idStr := c.Query("webhook_id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil {
			webhookID = &id
		}
	}

	if successStr := c.Query("success"); successStr != "" {
		s := successStr == "true"
		success = &s
	}

	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	logs, err := h.db.ListWebhookLogs(c.Context(), webhookID, success, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to list webhook logs",
		})
	}

	return c.JSON(fiber.Map{
		"logs": logs,
	})
}

// GetAvailableEvents returns the list of available webhook events
func (h *WebhooksHandler) GetAvailableEvents(c *fiber.Ctx) error {
	// Get all tables to build event list
	tables, err := h.db.GetTables(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to list tables",
		})
	}

	events := []string{
		"INSERT:*",
		"UPDATE:*",
		"DELETE:*",
	}

	for _, table := range tables {
		events = append(events,
			"INSERT:"+table.Name,
			"UPDATE:"+table.Name,
			"DELETE:"+table.Name,
		)
	}

	return c.JSON(fiber.Map{
		"events": events,
	})
}
