package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Dispatcher handles async webhook delivery
type Dispatcher struct {
	store      WebhookStore
	client     *http.Client
	workerPool chan struct{}
	wg         sync.WaitGroup
}

// WebhookStore interface for database operations
type WebhookStore interface {
	GetEnabledWebhooksByEvent(ctx context.Context, event string) ([]Webhook, error)
	CreateWebhookLog(ctx context.Context, log *WebhookLog) error
}

// NewDispatcher creates a new webhook dispatcher
func NewDispatcher(store WebhookStore, maxWorkers int) *Dispatcher {
	if maxWorkers <= 0 {
		maxWorkers = 10
	}
	return &Dispatcher{
		store: store,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		workerPool: make(chan struct{}, maxWorkers),
	}
}

// Dispatch sends a webhook event to all registered endpoints
func (d *Dispatcher) Dispatch(ctx context.Context, eventType, tableName string, data, oldData map[string]interface{}) {
	event := BuildEventName(eventType, tableName)

	// Also check for wildcard events
	wildcardEvent := eventType + ":*"

	go func() {
		webhooks, err := d.store.GetEnabledWebhooksByEvent(ctx, event)
		if err != nil {
			return
		}

		// Also get webhooks subscribed to wildcard
		wildcardWebhooks, err := d.store.GetEnabledWebhooksByEvent(ctx, wildcardEvent)
		if err == nil {
			webhooks = append(webhooks, wildcardWebhooks...)
		}

		// Deduplicate webhooks by ID
		seen := make(map[int64]bool)
		unique := make([]Webhook, 0)
		for _, wh := range webhooks {
			if !seen[wh.ID] {
				seen[wh.ID] = true
				unique = append(unique, wh)
			}
		}

		payload := WebhookPayload{
			Event:     event,
			Table:     tableName,
			Timestamp: time.Now().UTC(),
			Data:      data,
			OldData:   oldData,
		}

		for _, webhook := range unique {
			d.wg.Add(1)
			go d.deliver(ctx, webhook, payload)
		}
	}()
}

// deliver sends the webhook with retries
func (d *Dispatcher) deliver(ctx context.Context, webhook Webhook, payload WebhookPayload) {
	defer d.wg.Done()

	// Acquire worker slot
	d.workerPool <- struct{}{}
	defer func() { <-d.workerPool }()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		d.logDelivery(ctx, webhook, payload, 0, "", 1, false, err.Error())
		return
	}

	var lastErr error
	var lastStatus int
	var lastBody string

	// Retry with exponential backoff: 1s, 2s, 4s
	retryDelays := []time.Duration{0, 1 * time.Second, 2 * time.Second, 4 * time.Second}

	for attempt := 1; attempt <= 4; attempt++ {
		if attempt > 1 {
			time.Sleep(retryDelays[attempt-1])
		}

		req, err := http.NewRequestWithContext(ctx, "POST", webhook.URL, bytes.NewReader(payloadBytes))
		if err != nil {
			lastErr = err
			continue
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "RapiBase-Webhook/1.0")
		req.Header.Set("X-Webhook-Event", payload.Event)
		req.Header.Set("X-Webhook-Timestamp", fmt.Sprintf("%d", payload.Timestamp.Unix()))

		// Add signature if secret is set
		if webhook.Secret != "" {
			signature := d.sign(payloadBytes, webhook.Secret)
			req.Header.Set("X-Webhook-Signature", signature)
		}

		// Add custom headers
		for key, value := range webhook.Headers {
			req.Header.Set(key, value)
		}

		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()

		lastStatus = resp.StatusCode
		lastBody = string(body)

		// Success if 2xx
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			d.logDelivery(ctx, webhook, payload, lastStatus, lastBody, attempt, true, "")
			return
		}

		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// All retries failed
	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	d.logDelivery(ctx, webhook, payload, lastStatus, lastBody, 4, false, errMsg)
}

// sign creates HMAC-SHA256 signature
func (d *Dispatcher) sign(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

// logDelivery saves the delivery attempt to database
func (d *Dispatcher) logDelivery(ctx context.Context, webhook Webhook, payload WebhookPayload, status int, body string, attempts int, success bool, errMsg string) {
	payloadBytes, _ := json.Marshal(payload)

	log := &WebhookLog{
		WebhookID:      webhook.ID,
		Event:          payload.Event,
		Payload:        string(payloadBytes),
		ResponseStatus: status,
		ResponseBody:   body,
		Attempts:       attempts,
		Success:        success,
		Error:          errMsg,
		CreatedAt:      time.Now().UTC(),
	}

	d.store.CreateWebhookLog(ctx, log)
}

// Wait waits for all pending deliveries to complete
func (d *Dispatcher) Wait() {
	d.wg.Wait()
}
