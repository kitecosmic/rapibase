package handlers

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/push"
)

type PushHandler struct {
	db     *database.DB
	sender *push.Sender
}

func NewPushHandler(db *database.DB) *PushHandler {
	return &PushHandler{
		db:     db,
		sender: push.NewSender(db),
	}
}

// ============================================
// ADMIN ENDPOINTS
// ============================================

// GetPushConfigs returns all push configurations
func (h *PushHandler) GetPushConfigs(c *fiber.Ctx) error {
	configs, err := h.db.ListPushConfigs(c.Context())
	if err != nil {
		configs = []push.PushConfig{}
	}

	// Create default configs if not exist
	platforms := []string{push.PlatformWeb, push.PlatformIOS, push.PlatformAndroid}
	configMap := make(map[string]push.PushConfig)
	for _, cfg := range configs {
		configMap[cfg.Platform] = cfg
	}

	result := make([]fiber.Map, 0, 3)
	for _, platform := range platforms {
		if cfg, exists := configMap[platform]; exists {
			// Hide sensitive data
			safeConfig := fiber.Map{
				"id":         cfg.ID,
				"platform":   cfg.Platform,
				"enabled":    cfg.Enabled,
				"created_at": cfg.CreatedAt,
				"updated_at": cfg.UpdatedAt,
				"configured": len(cfg.Config) > 0,
			}
			// For web, expose public key
			if platform == push.PlatformWeb {
				if pubKey, ok := cfg.Config["vapid_public_key"].(string); ok {
					safeConfig["vapid_public_key"] = pubKey
				}
			}
			result = append(result, safeConfig)
		} else {
			result = append(result, fiber.Map{
				"platform":   platform,
				"enabled":    false,
				"configured": false,
			})
		}
	}

	return c.JSON(fiber.Map{
		"configs": result,
	})
}

// SetupWebPush generates VAPID keys and enables web push
func (h *PushHandler) SetupWebPush(c *fiber.Ctx) error {
	var req struct {
		Subject string `json:"subject"` // mailto: or https:// URL
		Force   bool   `json:"force"`   // true = regenerar aunque existan keys
	}
	c.BodyParser(&req)

	if req.Subject == "" {
		req.Subject = "mailto:admin@rapibase.local"
	}

	// Idempotente: si ya hay VAPID keys, se reutilizan (regenerarlas
	// invalidaría todas las suscripciones existentes). Con force:true se
	// regeneran a sabiendas.
	if !req.Force {
		if existing, err := h.db.GetPushConfig(c.Context(), push.PlatformWeb); err == nil {
			if pub, ok := existing.Config["vapid_public_key"].(string); ok && pub != "" {
				if !existing.Enabled {
					existing.Enabled = true
					_ = h.db.UpsertPushConfig(c.Context(), existing)
				}
				return c.JSON(fiber.Map{
					"message":          "Web Push already configured (pass force:true to regenerate keys)",
					"vapid_public_key": pub,
				})
			}
		}
	}

	// Generate new VAPID keys
	vapidConfig, err := push.GenerateVAPIDKeys()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to generate VAPID keys",
		})
	}
	vapidConfig.Subject = req.Subject

	config := &push.PushConfig{
		Platform: push.PlatformWeb,
		Config: map[string]interface{}{
			"vapid_public_key":  vapidConfig.VAPIDPublicKey,
			"vapid_private_key": vapidConfig.VAPIDPrivateKey,
			"subject":           vapidConfig.Subject,
		},
		Enabled: true,
	}

	if err := h.db.UpsertPushConfig(c.Context(), config); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save configuration",
		})
	}

	return c.JSON(fiber.Map{
		"message":          "Web Push configured successfully",
		"vapid_public_key": vapidConfig.VAPIDPublicKey,
	})
}

// SetupIOSPush configures APNS
func (h *PushHandler) SetupIOSPush(c *fiber.Ctx) error {
	var req struct {
		KeyID      string `json:"key_id"`
		TeamID     string `json:"team_id"`
		BundleID   string `json:"bundle_id"`
		PrivateKey string `json:"private_key"`
		Production bool   `json:"production"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.KeyID == "" || req.TeamID == "" || req.BundleID == "" || req.PrivateKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "key_id, team_id, bundle_id, and private_key are required",
		})
	}

	config := &push.PushConfig{
		Platform: push.PlatformIOS,
		Config: map[string]interface{}{
			"key_id":      req.KeyID,
			"team_id":     req.TeamID,
			"bundle_id":   req.BundleID,
			"private_key": req.PrivateKey,
			"production":  req.Production,
		},
		Enabled: true,
	}

	if err := h.db.UpsertPushConfig(c.Context(), config); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save configuration",
		})
	}

	return c.JSON(fiber.Map{
		"message": "iOS Push configured successfully",
	})
}

// SetupAndroidPush configures FCM
func (h *PushHandler) SetupAndroidPush(c *fiber.Ctx) error {
	var req struct {
		ProjectID    string `json:"project_id"`
		PrivateKeyID string `json:"private_key_id"`
		PrivateKey   string `json:"private_key"`
		ClientEmail  string `json:"client_email"`
		ClientID     string `json:"client_id"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.ProjectID == "" || req.PrivateKey == "" || req.ClientEmail == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "project_id, private_key, and client_email are required",
		})
	}

	config := &push.PushConfig{
		Platform: push.PlatformAndroid,
		Config: map[string]interface{}{
			"project_id":     req.ProjectID,
			"private_key_id": req.PrivateKeyID,
			"private_key":    req.PrivateKey,
			"client_email":   req.ClientEmail,
			"client_id":      req.ClientID,
		},
		Enabled: true,
	}

	if err := h.db.UpsertPushConfig(c.Context(), config); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save configuration",
		})
	}

	return c.JSON(fiber.Map{
		"message": "Android Push configured successfully",
	})
}

// TogglePushConfig enables or disables a push platform
func (h *PushHandler) TogglePushConfig(c *fiber.Ctx) error {
	platform := c.Params("platform")
	if platform != push.PlatformWeb && platform != push.PlatformIOS && platform != push.PlatformAndroid {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid platform",
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

	config, err := h.db.GetPushConfig(c.Context(), platform)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Platform not configured",
		})
	}

	config.Enabled = req.Enabled
	if err := h.db.UpsertPushConfig(c.Context(), config); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to update configuration",
		})
	}

	return c.JSON(fiber.Map{
		"message": "Configuration updated",
		"enabled": req.Enabled,
	})
}

// SendNotification sends a notification (admin or API)
func (h *PushHandler) SendNotification(c *fiber.Ctx) error {
	var req push.SendNotificationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Title == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Title is required",
		})
	}

	now := time.Now()
	notif := &push.Notification{
		UserID: req.UserID,
		Title:  req.Title,
		Body:   req.Body,
		Data:   req.Data,
		SentAt: &now,
	}

	// Save to database (in-app notification)
	if err := h.db.CreateNotification(c.Context(), notif); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create notification",
		})
	}

	// Send push notification
	msg := push.PushMessage{
		Title: req.Title,
		Body:  req.Body,
		Data:  req.Data,
	}

	var pushErr error
	var targetCount int

	// Priority: Filter > UserIDs > UserID > Broadcast
	if req.Filter != nil {
		// Send to users matching filter conditions
		pushErr = h.sender.SendToFilter(c.Context(), req.Filter, msg)
		targetCount = -1 // Unknown count for filter
	} else if len(req.UserIDs) > 0 {
		// Send to multiple specific users
		pushErr = h.sender.SendToUsers(c.Context(), req.UserIDs, msg)
		targetCount = len(req.UserIDs)
	} else if req.UserID != nil {
		// Send to single user
		pushErr = h.sender.SendToUser(c.Context(), *req.UserID, msg)
		targetCount = 1
	} else {
		// Broadcast to all
		pushErr = h.sender.Broadcast(c.Context(), msg)
		targetCount = -1 // Unknown count for broadcast
	}

	response := fiber.Map{
		"message":      "Notification sent",
		"notification": notif,
	}
	if targetCount > 0 {
		response["target_users"] = targetCount
	}
	if pushErr != nil {
		response["push_warning"] = pushErr.Error()
	}

	return c.Status(fiber.StatusCreated).JSON(response)
}

// ListNotifications returns all notifications (admin)
func (h *PushHandler) ListNotifications(c *fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	notifs, err := h.db.ListAllNotifications(c.Context(), limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to list notifications",
		})
	}

	return c.JSON(fiber.Map{
		"notifications": notifs,
	})
}

// ============================================
// CLIENT ENDPOINTS (for app users)
// ============================================

// Subscribe registers a push subscription for the authenticated user
func (h *PushHandler) Subscribe(c *fiber.Ctx) error {
	userID := c.Locals("userID").(string)

	var req struct {
		Platform string                 `json:"platform"`
		Token    string                 `json:"token"`
		Endpoint string                 `json:"endpoint"`
		Keys     map[string]interface{} `json:"keys"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Platform == "" || req.Token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Platform and token are required",
		})
	}

	sub := &push.PushSubscription{
		UserID:   userID,
		Platform: req.Platform,
		Token:    req.Token,
		Endpoint: req.Endpoint,
		Metadata: map[string]interface{}{
			"keys": req.Keys,
		},
	}

	if err := h.db.CreatePushSubscription(c.Context(), sub); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to create subscription",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message":      "Subscribed successfully",
		"subscription": sub,
	})
}

// Unsubscribe removes a push subscription
func (h *PushHandler) Unsubscribe(c *fiber.Ctx) error {
	userID := c.Locals("userID").(string)

	var req struct {
		Token string `json:"token"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Token is required",
		})
	}

	if err := h.db.DeleteUserSubscription(c.Context(), userID, req.Token); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to unsubscribe",
		})
	}

	return c.JSON(fiber.Map{
		"message": "Unsubscribed successfully",
	})
}

// GetNotifications returns notifications for the authenticated user
func (h *PushHandler) GetNotifications(c *fiber.Ctx) error {
	userID := c.Locals("userID").(string)
	unreadOnly := c.Query("unread") == "true"
	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	notifs, err := h.db.GetUserNotifications(c.Context(), userID, unreadOnly, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get notifications",
		})
	}

	unreadCount, _ := h.db.GetUnreadNotificationCount(c.Context(), userID)

	return c.JSON(fiber.Map{
		"notifications": notifs,
		"unread_count":  unreadCount,
	})
}

// MarkRead marks a notification as read
func (h *PushHandler) MarkRead(c *fiber.Ctx) error {
	userID := c.Locals("userID").(string)
	notifID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid notification ID",
		})
	}

	if err := h.db.MarkNotificationRead(c.Context(), userID, notifID); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Notification not found or already read",
		})
	}

	return c.JSON(fiber.Map{
		"message": "Notification marked as read",
	})
}

// MarkAllRead marks all notifications as read
func (h *PushHandler) MarkAllRead(c *fiber.Ctx) error {
	userID := c.Locals("userID").(string)

	count, err := h.db.MarkAllNotificationsRead(c.Context(), userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to mark notifications as read",
		})
	}

	return c.JSON(fiber.Map{
		"message": "All notifications marked as read",
		"count":   count,
	})
}

// GetVAPIDPublicKey returns the VAPID public key for web push subscription
func (h *PushHandler) GetVAPIDPublicKey(c *fiber.Ctx) error {
	config, err := h.db.GetPushConfig(c.Context(), push.PlatformWeb)
	if err != nil || !config.Enabled {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Web Push not configured",
		})
	}

	pubKey, ok := config.Config["vapid_public_key"].(string)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "VAPID key not found",
		})
	}

	return c.JSON(fiber.Map{
		"vapid_public_key": pubKey,
	})
}
