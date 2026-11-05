package webhooks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu    sync.Mutex
	hooks []Webhook
	logs  map[int64]*WebhookLog
	next  int64
}

func (m *memStore) GetEnabledWebhooksByEvent(_ context.Context, event string) ([]Webhook, error) {
	var out []Webhook
	for _, h := range m.hooks {
		for _, e := range h.Events {
			if e == event && h.Enabled {
				out = append(out, h)
			}
		}
	}
	return out, nil
}
func (m *memStore) GetWebhook(_ context.Context, id int64) (*Webhook, error) {
	for _, h := range m.hooks {
		if h.ID == id {
			h := h
			return &h, nil
		}
	}
	return nil, nil
}
func (m *memStore) CreateDelivery(_ context.Context, l *WebhookLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.next++
	l.ID = m.next
	cp := *l
	m.logs[l.ID] = &cp
	return nil
}
func (m *memStore) FinishAttempt(_ context.Context, id int64, r AttemptResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.logs[id]
	l.Status, l.ResponseStatus, l.Attempts, l.Success, l.NextAttemptAt, l.Error = r.Status, r.ResponseStatus, r.Attempts, r.Success, r.NextAttemptAt, r.Error
	return nil
}
func (m *memStore) ClaimDueDeliveries(context.Context, time.Duration, int) ([]WebhookLog, error) {
	return nil, nil
}
func (m *memStore) ClaimDelivery(_ context.Context, id int64) (*WebhookLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *m.logs[id]
	return &cp, nil
}
func (m *memStore) only(t *testing.T) WebhookLog {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.logs) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(m.logs))
	}
	for _, l := range m.logs {
		return *l
	}
	return WebhookLog{}
}

func setup(t *testing.T, status int, events ...string) (*memStore, *Dispatcher, *[]http.Header) {
	var mu sync.Mutex
	var got []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	st := &memStore{logs: map[int64]*WebhookLog{}, hooks: []Webhook{{ID: 1, URL: srv.URL, Secret: "s", Events: events, Enabled: true,
		Headers: map[string]string{"X-Webhook-Signature": "forged"}}}}
	return st, NewDispatcher(st, 2), &got
}

func TestDeliverSuccess(t *testing.T) {
	st, d, got := setup(t, 200, "INSERT:pets")
	d.Dispatch(context.Background(), EventInsert, "pets", map[string]interface{}{"id": 1}, nil)
	d.Wait()
	l := st.only(t)
	if l.Status != StatusDelivered || !l.Success || l.Attempts != 1 {
		t.Fatalf("got %+v", l)
	}
	h := (*got)[0]
	if h.Get("X-Webhook-Id") != l.DeliveryID || !strings.HasPrefix(l.DeliveryID, "evt_") {
		t.Errorf("X-Webhook-Id %q vs %q", h.Get("X-Webhook-Id"), l.DeliveryID)
	}
	if h.Get("X-Webhook-Signature") == "forged" {
		t.Error("a custom header overrode the signature")
	}
	if !strings.Contains(l.Payload, `"id":"`+l.DeliveryID+`"`) {
		t.Errorf("payload lacks the delivery id: %s", l.Payload)
	}
}

func TestServerErrorSchedulesRetry(t *testing.T) {
	st, d, _ := setup(t, 503, "INSERT:pets")
	d.Dispatch(context.Background(), EventInsert, "pets", map[string]interface{}{"id": 1}, nil)
	d.Wait()
	l := st.only(t)
	if l.Status != StatusPending || l.NextAttemptAt == nil {
		t.Fatalf("want pending with next attempt, got %+v", l)
	}
	if wait := time.Until(*l.NextAttemptAt); wait < 50*time.Second || wait > 70*time.Second {
		t.Errorf("first retry in %v, want ~1m", wait)
	}
}

func TestClientErrorIsPermanent(t *testing.T) {
	st, d, _ := setup(t, 400, "INSERT:pets")
	d.Dispatch(context.Background(), EventInsert, "pets", map[string]interface{}{"id": 1}, nil)
	d.Wait()
	if l := st.only(t); l.Status != StatusFailed || l.NextAttemptAt != nil {
		t.Fatalf("got %+v", l)
	}
}

func TestLastAttemptFails(t *testing.T) {
	st, d, _ := setup(t, 500, "INSERT:pets")
	l := &WebhookLog{WebhookID: 1, DeliveryID: "evt_x", Event: "INSERT:pets", Payload: "{}", Attempts: MaxAttempts() - 1, CreatedAt: time.Now()}
	_ = st.CreateDelivery(context.Background(), l)
	d.attempt(l)
	if got := st.only(t); got.Status != StatusFailed || got.Attempts != MaxAttempts() {
		t.Fatalf("got %+v", got)
	}
}

func TestRedeliverKeepsID(t *testing.T) {
	st, d, got := setup(t, 200, "INSERT:pets")
	d.Dispatch(context.Background(), EventInsert, "pets", map[string]interface{}{"id": 1}, nil)
	d.Wait()
	first := st.only(t)
	l, err := d.Redeliver(context.Background(), first.ID)
	if err != nil || l.Attempts != 2 || l.Status != StatusDelivered {
		t.Fatalf("redeliver: %+v %v", l, err)
	}
	if (*got)[1].Get("X-Webhook-Id") != first.DeliveryID || (*got)[1].Get("X-Webhook-Attempt") != "2" {
		t.Errorf("headers on resend: %v", (*got)[1])
	}
}

func TestAuthUsersNotInWildcard(t *testing.T) {
	st, d, _ := setup(t, 200, "INSERT:*")
	d.Dispatch(context.Background(), EventInsert, AuthUsersTable, map[string]interface{}{"id": "u"}, nil)
	d.Wait()
	if len(st.logs) != 0 {
		t.Fatalf("INSERT:* must not receive sign-ups, got %d deliveries", len(st.logs))
	}
	st.hooks[0].Events = []string{"INSERT:auth_users"}
	d.Dispatch(context.Background(), EventInsert, AuthUsersTable, map[string]interface{}{"id": "u"}, nil)
	d.Wait()
	st.only(t)
}
