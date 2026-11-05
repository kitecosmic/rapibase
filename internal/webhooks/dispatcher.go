package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

// Delivery states stored in _rapibase_webhook_logs.status.
const (
	StatusSending   = "sending"   // claimed by a worker, request in flight
	StatusPending   = "pending"   // failed, waiting for next_attempt_at
	StatusDelivered = "delivered" // got a 2xx
	StatusFailed    = "failed"    // gave up (or webhook deleted / disabled)
)

// RetrySchedule is the wait after each failed attempt. The first attempt is
// immediate; after the last wait fails the delivery is marked failed. It
// lives in the database, so it survives restarts: 6 attempts over ~8.5 h.
var RetrySchedule = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

// MaxAttempts is the total number of automatic attempts per delivery.
func MaxAttempts() int { return len(RetrySchedule) + 1 }

// staleSending: a delivery stuck in "sending" this long belongs to a worker
// that died mid-request (restart, crash) and is picked up again.
const staleSending = 2 * time.Minute

// Dispatcher handles async webhook delivery.
type Dispatcher struct {
	store      WebhookStore
	client     *http.Client
	workerPool chan struct{}
	wg         sync.WaitGroup
	stop       chan struct{}
	stopOnce   sync.Once
}

// WebhookStore is the persistence the dispatcher needs.
type WebhookStore interface {
	GetEnabledWebhooksByEvent(ctx context.Context, event string) ([]Webhook, error)
	GetWebhook(ctx context.Context, id int64) (*Webhook, error)
	// CreateDelivery inserts a log row in StatusSending and sets l.ID.
	CreateDelivery(ctx context.Context, l *WebhookLog) error
	// FinishAttempt records the outcome of one attempt.
	FinishAttempt(ctx context.Context, id int64, r AttemptResult) error
	// ClaimDueDeliveries moves due pending (or stale sending) rows to
	// StatusSending and returns them, using SKIP LOCKED.
	ClaimDueDeliveries(ctx context.Context, staleAfter time.Duration, limit int) ([]WebhookLog, error)
	// ClaimDelivery moves one row to StatusSending for a manual resend.
	ClaimDelivery(ctx context.Context, id int64) (*WebhookLog, error)
}

// AttemptResult is what one HTTP attempt produced.
type AttemptResult struct {
	Status         string
	ResponseStatus int
	ResponseBody   string
	Error          string
	Attempts       int
	Success        bool
	NextAttemptAt  *time.Time
}

// NewDispatcher creates a new webhook dispatcher.
func NewDispatcher(store WebhookStore, maxWorkers int) *Dispatcher {
	if maxWorkers <= 0 {
		maxWorkers = 10
	}
	return &Dispatcher{
		store:      store,
		client:     &http.Client{Timeout: 30 * time.Second},
		workerPool: make(chan struct{}, maxWorkers),
		stop:       make(chan struct{}),
	}
}

// Start runs the retry poller until Stop.
func (d *Dispatcher) Start() {
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-d.stop:
				return
			case <-t.C:
				d.runDue()
			}
		}
	}()
}

// Stop ends the poller (pending rows stay in the database).
func (d *Dispatcher) Stop() { d.stopOnce.Do(func() { close(d.stop) }) }

func (d *Dispatcher) runDue() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	due, err := d.store.ClaimDueDeliveries(ctx, staleSending, 20)
	cancel()
	if err != nil {
		log.Printf("[webhooks] claiming retries: %v", err)
		return
	}
	for i := range due {
		l := due[i]
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.attempt(&l)
		}()
	}
}

// Dispatch sends a webhook event to all registered endpoints. The request
// context is not used past this call: delivery runs on its own.
func (d *Dispatcher) Dispatch(_ context.Context, eventType, tableName string, data, oldData map[string]interface{}) {
	event := BuildEventName(eventType, tableName)
	// System tables (auth_users) only reach webhooks that name them
	// explicitly: "INSERT:*" means every *app* table.
	wildcard := ""
	if !IsSystemTable(tableName) {
		wildcard = eventType + ":*"
	}

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hooks, err := d.store.GetEnabledWebhooksByEvent(ctx, event)
		if err != nil {
			log.Printf("[webhooks] %s: %v", event, err)
			return
		}
		if wildcard != "" {
			if more, err := d.store.GetEnabledWebhooksByEvent(ctx, wildcard); err == nil {
				hooks = append(hooks, more...)
			}
		}
		seen := make(map[int64]bool)
		for _, wh := range hooks {
			if seen[wh.ID] || !wh.MatchesData(data) {
				continue
			}
			seen[wh.ID] = true

			payload := WebhookPayload{
				ID:        newDeliveryID(),
				Event:     event,
				Table:     tableName,
				Timestamp: time.Now().UTC(),
				Data:      data,
				OldData:   oldData,
			}
			body, err := json.Marshal(payload)
			if err != nil {
				continue
			}
			l := &WebhookLog{
				WebhookID:  wh.ID,
				DeliveryID: payload.ID,
				Event:      event,
				Payload:    string(body),
				Status:     StatusSending,
				CreatedAt:  payload.Timestamp,
			}
			if err := d.store.CreateDelivery(ctx, l); err != nil {
				log.Printf("[webhooks] saving delivery for webhook %d: %v", wh.ID, err)
				continue
			}
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				d.attempt(l)
			}()
		}
	}()
}

// Redeliver sends a logged delivery again right away, with the same
// payload and X-Webhook-Id, and returns the updated row. It does not
// schedule automatic retries: the person who clicked sees the outcome.
func (d *Dispatcher) Redeliver(ctx context.Context, logID int64) (*WebhookLog, error) {
	l, err := d.store.ClaimDelivery(ctx, logID)
	if err != nil {
		return nil, err
	}
	res := d.send(ctx, l, true)
	if err := d.store.FinishAttempt(ctx, l.ID, res); err != nil {
		return nil, err
	}
	l.Status, l.ResponseStatus, l.ResponseBody = res.Status, res.ResponseStatus, res.ResponseBody
	l.Error, l.Attempts, l.Success, l.NextAttemptAt = res.Error, res.Attempts, res.Success, res.NextAttemptAt
	return l, nil
}

// attempt makes one HTTP attempt and stores the outcome (with the next
// retry time when it failed and attempts remain).
func (d *Dispatcher) attempt(l *WebhookLog) {
	d.workerPool <- struct{}{}
	defer func() { <-d.workerPool }()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	res := d.send(ctx, l, false)
	if err := d.store.FinishAttempt(ctx, l.ID, res); err != nil {
		log.Printf("[webhooks] saving attempt of delivery %d: %v", l.ID, err)
	}
}

var errWebhookGone = errors.New("webhook deleted or disabled")

func (d *Dispatcher) send(ctx context.Context, l *WebhookLog, manual bool) AttemptResult {
	res := AttemptResult{Attempts: l.Attempts + 1}

	wh, err := d.store.GetWebhook(ctx, l.WebhookID)
	if err != nil || wh == nil || (!wh.Enabled && !manual) {
		res.Status, res.Error = StatusFailed, errWebhookGone.Error()
		return res
	}

	status, body, err := d.post(ctx, wh, l, res.Attempts)
	res.ResponseStatus, res.ResponseBody = status, body
	if err == nil {
		res.Status, res.Success = StatusDelivered, true
		return res
	}
	res.Error = err.Error()
	// 4xx other than 408/429 means the receiver rejected the payload:
	// sending the same bytes again won't change its mind.
	permanent := status >= 400 && status < 500 && status != 408 && status != 429
	if manual || permanent || res.Attempts >= MaxAttempts() {
		res.Status = StatusFailed
		return res
	}
	next := time.Now().UTC().Add(RetrySchedule[res.Attempts-1])
	res.Status, res.NextAttemptAt = StatusPending, &next
	return res
}

func (d *Dispatcher) post(ctx context.Context, wh *Webhook, l *WebhookLog, attempt int) (int, string, error) {
	payload := []byte(l.Payload)
	req, err := http.NewRequestWithContext(ctx, "POST", wh.URL, bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	// Custom headers first, so they can't override the ones below.
	for key, value := range wh.Headers {
		req.Header.Set(key, value)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RapiBase-Webhook/1.0")
	req.Header.Set("X-Webhook-Event", l.Event)
	req.Header.Set("X-Webhook-Timestamp", fmt.Sprintf("%d", l.CreatedAt.Unix()))
	// Same value on every retry and on manual resends: the receiver uses
	// it to ignore a delivery it already processed.
	if l.DeliveryID != "" {
		req.Header.Set("X-Webhook-Id", l.DeliveryID)
	}
	req.Header.Set("X-Webhook-Attempt", fmt.Sprintf("%d", attempt))
	if wh.Secret != "" {
		req.Header.Set("X-Webhook-Signature", sign(payload, wh.Secret))
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, string(body), nil
	}
	return resp.StatusCode, string(body), fmt.Errorf("HTTP %d", resp.StatusCode)
}

// sign creates the HMAC-SHA256 signature of the exact payload bytes.
func sign(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

func newDeliveryID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "evt_" + hex.EncodeToString(b)
}

// Wait waits for in-flight attempts to finish.
func (d *Dispatcher) Wait() {
	d.wg.Wait()
}
