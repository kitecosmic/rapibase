package push

import (
	"time"
)

// Platform types
const (
	PlatformWeb     = "web"
	PlatformIOS     = "ios"
	PlatformAndroid = "android"
)

// PushConfig represents platform-specific push configuration
type PushConfig struct {
	ID        int64                  `json:"id"`
	Platform  string                 `json:"platform"`
	Config    map[string]interface{} `json:"config"`
	Enabled   bool                   `json:"enabled"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// WebPushConfig contains VAPID keys for Web Push
type WebPushConfig struct {
	VAPIDPublicKey  string `json:"vapid_public_key"`
	VAPIDPrivateKey string `json:"vapid_private_key"`
	Subject         string `json:"subject"` // mailto: or https:// URL
}

// APNSConfig contains Apple Push Notification Service configuration
type APNSConfig struct {
	KeyID      string `json:"key_id"`
	TeamID     string `json:"team_id"`
	BundleID   string `json:"bundle_id"`
	PrivateKey string `json:"private_key"` // .p8 file contents
	Production bool   `json:"production"`
}

// FCMConfig contains Firebase Cloud Messaging configuration
type FCMConfig struct {
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	ClientID     string `json:"client_id"`
}

// PushSubscription represents a device subscription
type PushSubscription struct {
	ID        int64                  `json:"id"`
	UserID    string                 `json:"user_id"` // UUID
	Platform  string                 `json:"platform"`
	Token     string                 `json:"token"`
	Endpoint  string                 `json:"endpoint,omitempty"` // For web push
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	ExpiresAt *time.Time             `json:"expires_at,omitempty"`
}

// WebPushSubscription contains Web Push API subscription details
type WebPushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// Notification represents a notification to be sent
type Notification struct {
	ID        int64                  `json:"id"`
	UserID    *string                `json:"user_id,omitempty"` // nil = broadcast
	Title     string                 `json:"title"`
	Body      string                 `json:"body"`
	Data      map[string]interface{} `json:"data,omitempty"`
	SentAt    *time.Time             `json:"sent_at,omitempty"`
	ReadAt    *time.Time             `json:"read_at,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// SendNotificationRequest is the request to send a notification
type SendNotificationRequest struct {
	UserID  *string                `json:"user_id,omitempty"`  // Single user (UUID)
	UserIDs []string               `json:"user_ids,omitempty"` // Multiple users
	Filter  *UserFilter            `json:"filter,omitempty"`   // Filter users by conditions
	Title   string                 `json:"title"`
	Body    string                 `json:"body"`
	Data    map[string]interface{} `json:"data,omitempty"`
}

// UserFilter allows filtering users by conditions
type UserFilter struct {
	// Filter by user metadata in auth_users table
	Role          *string    `json:"role,omitempty"`           // e.g., "premium", "admin"
	EmailVerified *bool      `json:"email_verified,omitempty"` // Only verified users
	CreatedAfter  *time.Time `json:"created_after,omitempty"`  // Users created after date
	CreatedBefore *time.Time `json:"created_before,omitempty"` // Users created before date
	// Custom metadata filters (stored in user_metadata)
	Metadata map[string]interface{} `json:"metadata,omitempty"` // e.g., {"plan": "pro"}
}

// PushMessage is the payload sent to push services
type PushMessage struct {
	Title string                 `json:"title"`
	Body  string                 `json:"body"`
	Icon  string                 `json:"icon,omitempty"`
	Badge string                 `json:"badge,omitempty"`
	Data  map[string]interface{} `json:"data,omitempty"`
}
