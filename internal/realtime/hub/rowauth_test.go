package hub

import (
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/metrics"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// fakeRowAuth scopes events to the subscriber whose userID matches the
// configured owner column (service role bypasses).
type fakeRowAuth struct{ ownerCol string }

func (f fakeRowAuth) Authorize(role, userID, _, _ string, row map[string]any) bool {
	if role == "service_role" {
		return true
	}
	v, ok := row[f.ownerCol]
	if !ok {
		return false
	}
	s, _ := v.(string)
	return s == userID
}

// With a RowAuthorizer configured, an owner-scoped event reaches only
// the owning subscriber; others on the same stream are filtered out.
func TestChannel_RowAuth_OwnerScoping(t *testing.T) {
	cfg := Config{
		SubscriberQueueSize: 16,
		ResumeBufferSize:    32,
		Permissions:         openPermissions{},
		Metrics:             metrics.NoOp{},
		RowAuth:             fakeRowAuth{ownerCol: "owner"},
	}
	ch := newChannel("c", cfg)

	subA := NewSubscriber("a", "authenticated", "userA", 16)
	subB := NewSubscriber("b", "authenticated", "userB", 16)
	defer subA.Close()
	defer subB.Close()

	sc := &protocol.SubscribeConfig{PostgresChanges: []protocol.PostgresChangesConfig{{
		Event: "INSERT", Schema: "public", Table: "ordenes",
	}}}
	if err := ch.Attach(subA, sc); err != nil {
		t.Fatal(err)
	}
	if err := ch.Attach(subB, sc); err != nil {
		t.Fatal(err)
	}

	ev := makeEvent("1", "public", "ordenes", protocol.EventInsert,
		wal.Row{"owner": "userA", "amount": 10.0}, nil)
	ch.PublishEvent(ev)

	got := recvWithin(t, subA, 1, time.Second)
	if got[0].Table != "ordenes" {
		t.Fatalf("owner A should receive the event, got %+v", got[0])
	}
	drainEmpty(t, subB, 200*time.Millisecond)
}

// A nil RowAuthorizer must preserve the previous behaviour: every
// matching subscriber receives the event (realtime is not broken for
// tables without RLS config).
func TestChannel_RowAuth_NilDeliversToAll(t *testing.T) {
	cfg := Config{
		SubscriberQueueSize: 16,
		ResumeBufferSize:    32,
		Permissions:         openPermissions{},
		Metrics:             metrics.NoOp{},
		// RowAuth deliberately nil.
	}
	ch := newChannel("c", cfg)

	subA := NewSubscriber("a", "authenticated", "userA", 16)
	subB := NewSubscriber("b", "authenticated", "userB", 16)
	defer subA.Close()
	defer subB.Close()

	sc := &protocol.SubscribeConfig{PostgresChanges: []protocol.PostgresChangesConfig{{
		Event: "INSERT", Schema: "public", Table: "ordenes",
	}}}
	_ = ch.Attach(subA, sc)
	_ = ch.Attach(subB, sc)

	ev := makeEvent("1", "public", "ordenes", protocol.EventInsert,
		wal.Row{"owner": "userA"}, nil)
	ch.PublishEvent(ev)

	recvWithin(t, subA, 1, time.Second)
	recvWithin(t, subB, 1, time.Second)
}
