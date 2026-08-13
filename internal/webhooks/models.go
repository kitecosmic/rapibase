package webhooks

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FilterCond is one condition a row must meet for the webhook to fire.
// All conditions on a webhook are AND-combined and evaluated against the
// row data of the event (for DELETE, the deleted row).
type FilterCond struct {
	Column string      `json:"column"`
	Op     string      `json:"op"` // eq neq gt gte lt lte contains is_null not_null
	Value  interface{} `json:"value,omitempty"`
}

// Webhook represents a registered webhook endpoint
type Webhook struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Secret    string            `json:"secret,omitempty"`
	Events    []string          `json:"events"`
	Headers   map[string]string `json:"headers,omitempty"`
	Filter    []FilterCond      `json:"filter,omitempty"`
	Enabled   bool              `json:"enabled"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// MatchesData reports whether the row satisfies every filter condition.
// Sin condiciones → siempre true. Comparación numérica cuando ambos lados
// parsean como número; si no, comparación de strings.
func (w Webhook) MatchesData(data map[string]interface{}) bool {
	if len(w.Filter) == 0 {
		return true
	}
	if data == nil {
		return false
	}
	for _, c := range w.Filter {
		v, exists := data[c.Column]
		switch c.Op {
		case "is_null":
			if exists && v != nil {
				return false
			}
			continue
		case "not_null":
			if !exists || v == nil {
				return false
			}
			continue
		}
		if !exists || v == nil {
			return false
		}
		got := fmt.Sprint(v)
		want := fmt.Sprint(c.Value)
		gn, gErr := strconv.ParseFloat(got, 64)
		wn, wErr := strconv.ParseFloat(want, 64)
		numeric := gErr == nil && wErr == nil

		ok := false
		switch c.Op {
		case "eq":
			ok = (numeric && gn == wn) || (!numeric && got == want)
		case "neq":
			ok = (numeric && gn != wn) || (!numeric && got != want)
		case "gt":
			ok = (numeric && gn > wn) || (!numeric && got > want)
		case "gte":
			ok = (numeric && gn >= wn) || (!numeric && got >= want)
		case "lt":
			ok = (numeric && gn < wn) || (!numeric && got < want)
		case "lte":
			ok = (numeric && gn <= wn) || (!numeric && got <= want)
		case "contains":
			ok = strings.Contains(strings.ToLower(got), strings.ToLower(want))
		default:
			// operador desconocido: mejor no filtrar de más
			ok = true
		}
		if !ok {
			return false
		}
	}
	return true
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
