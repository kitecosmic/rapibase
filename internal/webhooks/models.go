package webhooks

import (
	"time"
)

// Webhook represents a registered webhook endpoint
type Webhook struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Secret    string            `json:"secret,omitempty"`
	Events    []string          `json:"events"`
	Headers   map[string]string `json:"headers,omitempty"`
	Enabled   bool              `json:"enabled"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// WebhookLog represents a webhook delivery attempt
type WebhookLog struct {
	ID             int64     `json:"id"`
	WebhookID      int64     `json:"webhook_id"`
	WebhookName    string    `json:"webhook_name,omitempty"`
	Event          string    `json:"event"`
	Payload        string    `json:"payload"`
	ResponseStatus int       `json:"response_status"`
	ResponseBody   string    `json:"response_body"`
	Attempts       int       `json:"attempts"`
	Success        bool      `json:"success"`
	Error          string    `json:"error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// WebhookPayload is the structure sent to webhook endpoints
type WebhookPayload struct {
	Event     string                 `json:"event"`
	Table     string                 `json:"table"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
	OldData   map[string]interface{} `json:"old_data,omitempty"`
}

// Event types
const (
	EventInsert = "INSERT"
	EventUpdate = "UPDATE"
	EventDelete = "DELETE"
)

// BuildEventName creates an event name like "INSERT:users"
func BuildEventName(eventType, tableName string) string {
	return eventType + ":" + tableName
}
