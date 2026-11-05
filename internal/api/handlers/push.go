package handlers

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
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

// SendNotification sends a notification (dashboard admin or SERVICE_KEY).
// Body: {user_id | user_ids | filter (none = broadcast), title, body, data,
// android, ios, web}. Responds 201 with a DeliveryReport: devices, sent,
// failed, removed and the per-device errors.
func (h *PushHandler) SendNotification(c *fiber.Ctx) error {
	var req push.SendNotificationRequest
	if err := c.BodyParser(&req); err != nil {
		return pushBadRequest(c, "body", "invalid JSON body: "+err.Error()+
			` — expected {"user_id"|"user_ids"|"filter", "title", "body", "data": {...}, "android": {...}, "ios": {...}, "web": {...}}`)
	}

	if strings.TrimSpace(req.Title) == "" {
		return pushBadRequest(c, "title", "title is required")
	}
	if utf8.RuneCountInString(req.Title) > 255 {
		return pushBadRequest(c, "title", "title is limited to 255 characters")
	}
	if len(req.UserIDs) > 1000 {
		return pushBadRequest(c, "user_ids", "user_ids is limited to 1000 per send; use filter for larger audiences")
	}
	if req.UserID != nil {
		if err := validateUUID(*req.UserID); err != nil {
			return pushBadRequest(c, "user_id", "user_id must be a UUID")
		}
	}
	for i, id := range req.UserIDs {
		if err := validateUUID(id); err != nil {
			return pushBadRequest(c, "user_ids", fmt.Sprintf("user_ids[%d] must be a UUID", i))
		}
	}
	if a := req.Android; a != nil {
		if p := strings.ToLower(a.Priority); p != "" && p != "high" && p != "normal" {
			return pushBadRequest(c, "android.priority", `android.priority must be "high" or "normal"`)
		}
		if a.TTLSeconds < 0 {
			return pushBadRequest(c, "android.ttl_seconds", "android.ttl_seconds must be >= 0")
		}
	}

	now := time.Now()
	msg := push.PushMessage{
		Title:   req.Title,
		Body:    req.Body,
		Data:    req.Data,
		Android: req.Android,
		IOS:     req.IOS,
	}
	if w := req.Web; w != nil {
		msg.Icon, msg.Badge, msg.Image, msg.URL = w.Icon, w.Badge, w.Image, w.URL
	}

	// One send = one batch: an inbox row per recipient (every matching app
	// user, so someone without a device still finds it in the app), unless
	// inbox:false. The dashboard history shows the batch once.
	t := database.NotificationTarget{Inbox: req.Inbox == nil || *req.Inbox}
	switch {
	case req.Filter != nil:
		t.Label, t.Filter = "filter", req.Filter
	case len(req.UserIDs) > 0:
		t.Label, t.UserIDs = "user_ids", req.UserIDs
	case req.UserID != nil:
		t.Label, t.UserIDs = "user_id", []string{*req.UserID}
	default:
		t.Label, t.All = "broadcast", true
	}
	if req.UserID != nil && req.Filter == nil && len(req.UserIDs) == 0 {
		exists, err := h.db.AuthUserExists(c.Context(), *req.UserID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		if !exists {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found: " + *req.UserID, "field": "user_id"})
		}
	}
	notif := &push.Notification{Title: req.Title, Body: req.Body, Data: req.Data, SentAt: &now}
	batchID, inboxRows, err := h.db.CreateNotificationBatch(c.Context(), notif, t)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to store notification: " + err.Error(),
		})
	}

	var report push.DeliveryReport
	switch t.Label {
	case "filter":
		report, err = h.sender.SendToFilter(c.Context(), req.Filter, msg)
	case "user_ids":
		report, err = h.sender.SendToUsers(c.Context(), req.UserIDs, msg)
	case "user_id":
		report, err = h.sender.SendToUser(c.Context(), *req.UserID, msg)
	default:
		report, err = h.sender.Broadcast(c.Context(), msg)
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to load subscriptions: " + err.Error(),
		})
	}

	response := fiber.Map{
		"message":  "Notification sent",
		"target":   t.Label,
		"batch_id": batchID,
		"inbox":    inboxRows,
		"devices":  report.Devices,
		"sent":             report.Sent,
		"failed":           report.Failed,
		"removed":          report.Removed,
		"errors":           report.Errors,
	}
	switch {
	case report.Devices == 0:
		response["push_warning"] = "no registered devices for this target"
	case report.Failed > 0 && len(report.Errors) > 0:
		response["push_warning"] = report.Errors[0].Error
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
//
// Auth (RequireAPIKey on the group):
//   - ANON_KEY + "Authorization: Bearer <user JWT>": acts on the user in
//     the JWT; a different user_id in the request is rejected (403).
//   - SERVICE_KEY: acts on the user_id given in the body or ?user_id=.
// ============================================

// pushBadRequest answers 400 naming the offending field.
func pushBadRequest(c *fiber.Ctx, field, msg string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg, "field": field})
}

func validateUUID(s string) error {
	_, err := uuid.Parse(s)
	return err
}

// pushUser resolves which app user a client call acts on. required=false
// lets the service key act on every user (returns ""). When ok is false
// the response has already been written and err is what to return.
func (h *PushHandler) pushUser(c *fiber.Ctx, given string, required bool) (userID string, ok bool, err error) {
	given = strings.TrimSpace(given)
	if c.Locals("apiKeyType") != "service" {
		uid, _ := c.Locals("userID").(string)
		if uid == "" {
			return "", false, c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "user JWT required: send 'apikey: <ANON_KEY>' plus 'Authorization: Bearer <access_token>'",
			})
		}
		if given != "" && given != uid {
			return "", false, c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "user_id does not match the JWT; with the anon key the user always comes from the token",
				"field": "user_id",
			})
		}
		return uid, true, nil
	}
	if given == "" {
		if !required {
			return "", true, nil
		}
		return "", false, pushBadRequest(c, "user_id", "user_id is required with the service key (body or ?user_id=)")
	}
	if validateUUID(given) != nil {
		return "", false, pushBadRequest(c, "user_id", "user_id must be a UUID")
	}
	exists, err := h.db.AuthUserExists(c.Context(), given)
	if err != nil {
		return "", false, c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if !exists {
		return "", false, c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found: " + given, "field": "user_id"})
	}
	return given, true, nil
}

// Subscribe registers (or refreshes) a device for a user.
//
//	android / ios: {"platform", "token", "metadata"?}
//	web:           {"platform":"web", "endpoint", "keys":{"p256dh","auth"}, "metadata"?}
//	               (the browser's PushSubscription.toJSON() plus platform;
//	               token defaults to the endpoint)
//	service key:   add "user_id"
//
// 201 when the device is new, 200 when it was already registered for this
// user. A token registered to another user moves to this one.
func (h *PushHandler) Subscribe(c *fiber.Ctx) error {
	var req struct {
		UserID   string                 `json:"user_id"`
		Platform string                 `json:"platform"`
		Token    string                 `json:"token"`
		Endpoint string                 `json:"endpoint"`
		Keys     map[string]interface{} `json:"keys"`
		Metadata map[string]interface{} `json:"metadata"`
	}
	if err := c.BodyParser(&req); err != nil {
		return pushBadRequest(c, "body", "invalid JSON body: "+err.Error())
	}

	req.Platform = strings.ToLower(strings.TrimSpace(req.Platform))
	req.Token = strings.TrimSpace(req.Token)
	switch req.Platform {
	case "":
		return pushBadRequest(c, "platform", `platform is required: "android", "ios" or "web"`)
	case push.PlatformAndroid, push.PlatformIOS:
		if req.Token == "" {
			hint := "the FCM registration token or the Firebase Installation ID"
			if req.Platform == push.PlatformIOS {
				hint = "the APNs device token (hex)"
			}
			return pushBadRequest(c, "token", "token is required for "+req.Platform+": "+hint)
		}
	case push.PlatformWeb:
		if req.Endpoint == "" {
			return pushBadRequest(c, "endpoint", "endpoint is required for web (PushSubscription.endpoint)")
		}
		if err := push.ValidateWebPushEndpoint(req.Endpoint); err != nil {
			return pushBadRequest(c, "endpoint", "invalid web push endpoint: "+err.Error())
		}
		for _, k := range []string{"p256dh", "auth"} {
			if v, _ := req.Keys[k].(string); v == "" {
				return pushBadRequest(c, "keys."+k, "keys."+k+" is required for web (PushSubscription.toJSON().keys)")
			}
		}
		if req.Token == "" {
			req.Token = req.Endpoint
		}
	default:
		return pushBadRequest(c, "platform", `platform must be "android", "ios" or "web"`)
	}

	userID, ok, err := h.pushUser(c, req.UserID, true)
	if !ok {
		return err
	}

	metadata := map[string]interface{}{}
	for k, v := range req.Metadata {
		if k != "keys" {
			metadata[k] = v
		}
	}
	if req.Platform == push.PlatformWeb {
		metadata["keys"] = req.Keys
	}

	sub := &push.PushSubscription{
		UserID:   userID,
		Platform: req.Platform,
		Token:    req.Token,
		Endpoint: req.Endpoint,
		Metadata: metadata,
	}
	created, reassigned, err := h.db.CreatePushSubscription(c.Context(), sub)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to save subscription: " + err.Error(),
		})
	}

	status, message := fiber.StatusOK, "Subscription updated"
	if created {
		status, message = fiber.StatusCreated, "Subscribed successfully"
	}
	return c.Status(status).JSON(fiber.Map{
		"message":         message,
		"created":         created,
		"reassigned_from": reassigned,
		"subscription":    sub,
	})
}

// Unsubscribe removes a device: DELETE /push/subscribe (or POST
// /push/unsubscribe) with {"token"} — or ?token= for clients that cannot
// send a DELETE body. For web the token is the endpoint. With the service
// key, user_id is optional: without it the token is removed from whoever
// holds it. Idempotent: an unknown token answers 200 with deleted:0.
func (h *PushHandler) Unsubscribe(c *fiber.Ctx) error {
	var req struct {
		UserID   string `json:"user_id"`
		Token    string `json:"token"`
		Endpoint string `json:"endpoint"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&req); err != nil {
			return pushBadRequest(c, "body", "invalid JSON body: "+err.Error())
		}
	}
	if req.Token == "" {
		req.Token = c.Query("token")
	}
	if req.Token == "" {
		req.Token = req.Endpoint
	}
	if req.UserID == "" {
		req.UserID = c.Query("user_id")
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		return pushBadRequest(c, "token", "token is required (for web: the endpoint)")
	}

	userID, ok, err := h.pushUser(c, req.UserID, false)
	if !ok {
		return err
	}

	n, err := h.db.DeleteSubscriptionsByToken(c.Context(), userID, req.Token)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to unsubscribe: " + err.Error(),
		})
	}
	return c.JSON(fiber.Map{
		"message": "Unsubscribed",
		"deleted": n,
	})
}

// ListSubscriptions shows registered devices: with the anon key + JWT the
// caller's own; with the service key ?user_id= (or every user, newest
// first). ?limit= up to 500 (default 100).
func (h *PushHandler) ListSubscriptions(c *fiber.Ctx) error {
	userID, ok, err := h.pushUser(c, c.Query("user_id"), false)
	if !ok {
		return err
	}
	subs, err := h.db.ListSubscriptions(c.Context(), userID, c.QueryInt("limit", 100))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"subscriptions": subs, "count": len(subs)})
}

// GetNotifications returns a user's inbox (?unread=true, ?limit=, max 100).
func (h *PushHandler) GetNotifications(c *fiber.Ctx) error {
	userID, ok, err := h.pushUser(c, c.Query("user_id"), true)
	if !ok {
		return err
	}
	unreadOnly := c.Query("unread") == "true"
	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	notifs, err := h.db.GetUserNotifications(c.Context(), userID, unreadOnly, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to get notifications",
		})
	}
	if notifs == nil {
		notifs = []push.Notification{}
	}

	unreadCount, _ := h.db.GetUnreadNotificationCount(c.Context(), userID)

	return c.JSON(fiber.Map{
		"notifications": notifs,
		"unread_count":  unreadCount,
	})
}

// MarkRead marks a notification as read
func (h *PushHandler) MarkRead(c *fiber.Ctx) error {
	userID, ok, err := h.pushUser(c, c.Query("user_id"), true)
	if !ok {
		return err
	}
	notifID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return pushBadRequest(c, "id", "notification id must be an integer")
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
	userID, ok, err := h.pushUser(c, c.Query("user_id"), true)
	if !ok {
		return err
	}

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
