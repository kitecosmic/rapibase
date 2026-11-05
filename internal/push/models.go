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
	UpdatedAt *time.Time             `json:"updated_at,omitempty"` // last (re)registration
	ExpiresAt *time.Time             `json:"expires_at,omitempty"`
}

// MaxDevicesPerUser caps subscriptions per user; registering one more
// drops the least recently refreshed.
const MaxDevicesPerUser = 20

// IsAPNsToken tells a raw APNs device token (hex, 32+ bytes) apart from an
// FCM token / Firebase Installation ID, which iOS apps using Firebase get.
func IsAPNsToken(t string) bool {
	if len(t) < 64 || len(t)%2 != 0 {
		return false
	}
	for _, r := range t {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
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
	// Admin history only: one send = one batch, fanned out to one inbox row
	// per recipient.
	BatchID    *string `json:"batch_id,omitempty"`
	Target     string  `json:"target,omitempty"`
	Recipients *int64  `json:"recipients,omitempty"`
}

// SendNotificationRequest is the request to send a notification.
// Target priority: filter > user_ids > user_id > broadcast (none given).
type SendNotificationRequest struct {
	UserID  *string                `json:"user_id,omitempty"`  // Single user (UUID)
	UserIDs []string               `json:"user_ids,omitempty"` // Multiple users
	Filter  *UserFilter            `json:"filter,omitempty"`   // Filter users by conditions
	Title   string                 `json:"title"`
	Body    string                 `json:"body"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Android *AndroidOptions        `json:"android,omitempty"`
	IOS     *IOSOptions            `json:"ios,omitempty"`
	Web     *WebOptions            `json:"web,omitempty"`
	// Inbox: nil/true stores it in each recipient's in-app inbox; false
	// sends the push only (silent pings, codes, transient alerts).
	Inbox *bool `json:"inbox,omitempty"`
}

// AndroidOptions map to FCM v1 message.android / android.notification.
type AndroidOptions struct {
	ChannelID   string `json:"channel_id,omitempty"`   // Android 8+ notification channel (created by the app)
	Priority    string `json:"priority,omitempty"`     // "high" | "normal"
	Icon        string `json:"icon,omitempty"`         // drawable resource name, e.g. "ic_stat_notify"
	Color       string `json:"color,omitempty"`        // "#rrggbb"
	Sound       string `json:"sound,omitempty"`        // "default" or a res/raw file name
	Tag         string `json:"tag,omitempty"`          // same tag replaces the previous notification
	Image       string `json:"image,omitempty"`        // https URL
	ClickAction string `json:"click_action,omitempty"` // intent filter action
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
}

// IOSOptions map to the APNs aps dictionary.
type IOSOptions struct {
	Sound    string `json:"sound,omitempty"` // default "default"
	Badge    *int   `json:"badge,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	Category string `json:"category,omitempty"`
}

// WebOptions travel inside the Web Push payload the service worker receives.
type WebOptions struct {
	Icon  string `json:"icon,omitempty"`
	Badge string `json:"badge,omitempty"`
	Image string `json:"image,omitempty"`
	URL   string `json:"url,omitempty"`
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

// PushMessage is the payload sent to push services. The JSON form is
// exactly what a web service worker receives in event.data.json().
type PushMessage struct {
	Title   string                 `json:"title"`
	Body    string                 `json:"body"`
	Icon    string                 `json:"icon,omitempty"`
	Badge   string                 `json:"badge,omitempty"`
	Image   string                 `json:"image,omitempty"`
	URL     string                 `json:"url,omitempty"`
	Data    map[string]interface{} `json:"data,omitempty"`
	Android *AndroidOptions        `json:"-"`
	IOS     *IOSOptions            `json:"-"`
}

// DeliveryError describes one device that could not be reached.
type DeliveryError struct {
	SubscriptionID int64  `json:"subscription_id"`
	UserID         string `json:"user_id"`
	Platform       string `json:"platform"`
	Error          string `json:"error"`
	Removed        bool   `json:"removed"` // the provider said the token is dead and it was deleted
}

// DeliveryReport is the per-send summary returned by the API.
type DeliveryReport struct {
	Devices int             `json:"devices"`
	Sent    int             `json:"sent"`
	Failed  int             `json:"failed"`
	Removed int             `json:"removed"`
	Errors  []DeliveryError `json:"errors"`
}

// MaxReportedErrors caps the errors listed in a DeliveryReport.
const MaxReportedErrors = 50
