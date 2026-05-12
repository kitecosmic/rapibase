package hub

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/bus"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// recvWithin reads up to want frames from sub.Outbound() with a timeout
// per frame. Fails the test on timeout.
func recvWithin(t *testing.T, sub *Subscriber, want int, timeout time.Duration) []protocol.Frame {
	t.Helper()
	out := make([]protocol.Frame, 0, want)
	for i := 0; i < want; i++ {
		select {
		case f, ok := <-sub.Outbound():
			if !ok {
				t.Fatalf("subscriber outbound closed early (got %d/%d)", len(out), want)
			}
			out = append(out, f)
		case <-time.After(timeout):
			t.Fatalf("timeout waiting for frame %d/%d", i+1, want)
		}
	}
	return out
}

// drainEmpty asserts no frame arrives within the timeout window.
func drainEmpty(t *testing.T, sub *Subscriber, timeout time.Duration) {
	t.Helper()
	select {
	case f := <-sub.Outbound():
		t.Fatalf("unexpected frame: %+v", f)
	case <-time.After(timeout):
	}
}

func newTestHub(t *testing.T) (*Hub, *bus.Local) {
	t.Helper()
	b := bus.NewLocal(64)
	h := New(Config{Shards: 4, SubscriberQueueSize: 16, ResumeBufferSize: 32}, b)
	return h, b
}

func makeEvent(lsn, schema, table string, evType protocol.EventType, newRow, oldRow wal.Row) wal.Event {
	return wal.Event{
		LSN:      protocol.LSN(lsn),
		CommitTS: time.Now().UTC(),
		Type:     evType,
		Schema:   schema,
		Table:    table,
		New:      newRow,
		Old:      oldRow,
	}
}

func TestHub_Attach_PostgresChanges_Insert(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()

	sub := NewSubscriber("s1", "anon", "7", 16)
	defer sub.Close()

	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{
			Event:  "INSERT",
			Schema: "public",
			Table:  "messages",
			Filter: map[string]any{"column": "room_id", "op": "eq", "value": 42.0},
		}},
	}
	if err := h.Attach(context.Background(), sub, "room:42", cfg); err != nil {
		t.Fatal(err)
	}

	ev := makeEvent("0/1", "public", "messages", protocol.EventInsert,
		wal.Row{"id": 1, "room_id": 42, "text": "hola"}, nil)
	h.PublishLocal(ev)

	frames := recvWithin(t, sub, 1, time.Second)
	f := frames[0]
	if f.Type != protocol.FramePostgresChanges || f.Channel != "room:42" {
		t.Fatalf("unexpected frame: %+v", f)
	}
	if f.DBEvent != protocol.EventInsert || f.Table != "messages" {
		t.Fatalf("event metadata mismatch: %+v", f)
	}
	row, ok := f.New.(map[string]any)
	if !ok || row["text"] != "hola" {
		t.Fatalf("row mismatch: %+v", f.New)
	}
}

func TestHub_FilterRejectsNonMatching(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()
	sub := NewSubscriber("s1", "anon", "7", 16)
	defer sub.Close()

	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{
			Event:  "INSERT",
			Schema: "public",
			Table:  "messages",
			Filter: map[string]any{"column": "room_id", "op": "eq", "value": 42.0},
		}},
	}
	if err := h.Attach(context.Background(), sub, "room:42", cfg); err != nil {
		t.Fatal(err)
	}

	// Different room — filter rejects.
	h.PublishLocal(makeEvent("0/1", "public", "messages", protocol.EventInsert,
		wal.Row{"id": 1, "room_id": 99}, nil))
	// Different table — stream rejects.
	h.PublishLocal(makeEvent("0/2", "public", "users", protocol.EventInsert,
		wal.Row{"id": 1, "room_id": 42}, nil))
	// Different event type — stream rejects.
	h.PublishLocal(makeEvent("0/3", "public", "messages", protocol.EventUpdate,
		wal.Row{"id": 1, "room_id": 42}, nil))

	drainEmpty(t, sub, 50*time.Millisecond)
}

func TestHub_PermissionsFilterColumns(t *testing.T) {
	b := bus.NewLocal(16)
	defer b.Close()

	// Forbid the "secret" column for role=anon.
	checker := denyChecker{deny: map[string]map[string]bool{"anon": {"secret": true}}}
	h := New(Config{Shards: 2, SubscriberQueueSize: 16, ResumeBufferSize: 16, Permissions: checker}, b)

	sub := NewSubscriber("s1", "anon", "", 16)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{
			Event: "*",
			Table: "users",
		}},
	}
	if err := h.Attach(context.Background(), sub, "users", cfg); err != nil {
		t.Fatal(err)
	}

	h.PublishLocal(makeEvent("0/1", "public", "users", protocol.EventUpdate,
		wal.Row{"id": 1, "name": "Joel", "secret": "shhh"},
		wal.Row{"id": 1, "name": "Joel", "secret": "old"},
	))

	frames := recvWithin(t, sub, 1, time.Second)
	row := frames[0].New.(map[string]any)
	if _, leaked := row["secret"]; leaked {
		t.Fatalf("secret leaked: %+v", row)
	}
	if row["name"] != "Joel" {
		t.Fatalf("expected name, got %+v", row)
	}
	if cols := frames[0].Columns; containsStr(cols, "secret") {
		t.Fatalf("secret listed in columns: %v", cols)
	}
}

func TestHub_AttachRejectsFilterOnDeniedColumn(t *testing.T) {
	b := bus.NewLocal(16)
	defer b.Close()
	checker := denyChecker{deny: map[string]map[string]bool{"anon": {"secret": true}}}
	h := New(Config{Shards: 2, Permissions: checker}, b)

	sub := NewSubscriber("s1", "anon", "", 16)
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{
			Event:  "*",
			Table:  "users",
			Filter: map[string]any{"column": "secret", "op": "eq", "value": "x"},
		}},
	}
	err := h.Attach(context.Background(), sub, "users", cfg)
	if err == nil {
		t.Fatalf("attach should reject filter referencing denied column")
	}
}

func TestHub_Broadcast_ExcludesSelfByDefault(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()

	sender := NewSubscriber("s1", "anon", "", 16)
	defer sender.Close()
	listener := NewSubscriber("s2", "anon", "", 16)
	defer listener.Close()

	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}}
	if err := h.Attach(context.Background(), sender, "room:42", cfg); err != nil {
		t.Fatal(err)
	}
	if err := h.Attach(context.Background(), listener, "room:42", cfg); err != nil {
		t.Fatal(err)
	}

	delivered := h.Broadcast("room:42", sender, "typing", map[string]any{"u": 7}, false)
	if delivered != 1 {
		t.Fatalf("expected 1 listener, got %d", delivered)
	}
	frames := recvWithin(t, listener, 1, time.Second)
	if frames[0].Event != "typing" || frames[0].From == nil || frames[0].From.SessionID != "s1" {
		t.Fatalf("broadcast envelope mismatch: %+v from=%+v", frames[0], frames[0].From)
	}
	drainEmpty(t, sender, 50*time.Millisecond)
}

func TestHub_Broadcast_SelfReceivedWhenConfigured(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()
	sender := NewSubscriber("s1", "anon", "", 16)
	defer sender.Close()
	// Sender opts into receiving its own broadcasts.
	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{Self: true}}
	if err := h.Attach(context.Background(), sender, "room:42", cfg); err != nil {
		t.Fatal(err)
	}
	if h.Broadcast("room:42", sender, "ping", nil, false) != 1 {
		t.Fatalf("expected 1 delivery to self")
	}
	frames := recvWithin(t, sender, 1, time.Second)
	if frames[0].Event != "ping" {
		t.Fatalf("got %+v", frames[0])
	}
}

func TestHub_Broadcast_ForceOverridesSelfOptOut(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()
	sender := NewSubscriber("s1", "anon", "", 16)
	defer sender.Close()
	// Sender did NOT opt into self.
	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}}
	if err := h.Attach(context.Background(), sender, "room:42", cfg); err != nil {
		t.Fatal(err)
	}
	// Server-emitted broadcast forces fan-out to everyone.
	if h.Broadcast("room:42", sender, "ping", nil, true) != 1 {
		t.Fatal("force should override self opt-out")
	}
	frames := recvWithin(t, sender, 1, time.Second)
	if frames[0].Event != "ping" {
		t.Fatalf("got %+v", frames[0])
	}
}

func TestHub_DetachReleasesChannel(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()
	sub := NewSubscriber("s1", "anon", "", 16)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}}
	if err := h.Attach(context.Background(), sub, "room:42", cfg); err != nil {
		t.Fatal(err)
	}
	if h.ChannelCount() != 1 {
		t.Fatalf("expected 1 channel, got %d", h.ChannelCount())
	}
	h.Detach(sub, "room:42")
	if h.ChannelCount() != 0 {
		t.Fatalf("expected channel GC'd, got %d", h.ChannelCount())
	}
}

func TestHub_DetachAllReleasesEveryChannel(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()
	sub := NewSubscriber("s1", "anon", "", 16)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}}
	for _, name := range []string{"a", "b", "c"} {
		if err := h.Attach(context.Background(), sub, name, cfg); err != nil {
			t.Fatal(err)
		}
	}
	if h.ChannelCount() != 3 {
		t.Fatalf("got %d", h.ChannelCount())
	}
	h.DetachAll(sub)
	if h.ChannelCount() != 0 {
		t.Fatalf("expected zero after DetachAll, got %d", h.ChannelCount())
	}
}

func TestHub_Resume_ReplaysAfterLSN(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()
	sub := NewSubscriber("s1", "anon", "", 32)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*", Table: "messages"}},
	}
	if err := h.Attach(context.Background(), sub, "room", cfg); err != nil {
		t.Fatal(err)
	}

	for _, lsn := range []string{"0/1", "0/2", "0/3"} {
		h.PublishLocal(makeEvent(lsn, "public", "messages", protocol.EventInsert,
			wal.Row{"id": 1}, nil))
	}
	// Consume the three live deliveries.
	recvWithin(t, sub, 3, time.Second)

	// Resume from 0/1 → should re-deliver 0/2 and 0/3.
	if err := h.Resume(sub, "room", "0/1"); err != nil {
		t.Fatal(err)
	}
	frames := recvWithin(t, sub, 2, time.Second)
	if string(frames[0].LSN) != "0/2" || string(frames[1].LSN) != "0/3" {
		t.Fatalf("resume order wrong: %v %v", frames[0].LSN, frames[1].LSN)
	}
}

func TestHub_Resume_TruncatedReturnsErr(t *testing.T) {
	b := bus.NewLocal(16)
	defer b.Close()
	h := New(Config{Shards: 2, SubscriberQueueSize: 8, ResumeBufferSize: 2}, b) // tiny

	sub := NewSubscriber("s1", "anon", "", 8)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*", Table: "t"}},
	}
	if err := h.Attach(context.Background(), sub, "c", cfg); err != nil {
		t.Fatal(err)
	}
	for _, lsn := range []string{"0/1", "0/2", "0/3"} {
		h.PublishLocal(makeEvent(lsn, "public", "t", protocol.EventInsert, wal.Row{"id": 1}, nil))
	}
	recvWithin(t, sub, 3, time.Second)

	err := h.Resume(sub, "c", "0/1")
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
}

func TestHub_ReValidate_RevokesNewlyForbiddenChannels(t *testing.T) {
	b := bus.NewLocal(16)
	defer b.Close()

	// Two roles: "user" can read both cols, "anon" cannot read "secret".
	checker := denyChecker{deny: map[string]map[string]bool{"anon": {"secret": true}}}
	h := New(Config{Shards: 2, SubscriberQueueSize: 16, Permissions: checker}, b)

	sub := NewSubscriber("s1", "user", "7", 16)
	defer sub.Close()
	// Subscribe with a filter that references "secret" — allowed for "user".
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{
			Event:  "*",
			Table:  "users",
			Filter: map[string]any{"column": "secret", "op": "eq", "value": "x"},
		}},
	}
	if err := h.Attach(context.Background(), sub, "room", cfg); err != nil {
		t.Fatal(err)
	}
	if h.ChannelCount() != 1 {
		t.Fatalf("expected 1 channel, got %d", h.ChannelCount())
	}

	// Downgrade role; "secret" is now denied.
	sub.SetRole("anon", "7")
	revoked := h.ReValidateSubscriber(sub)
	if len(revoked) != 1 || revoked[0] != "room" {
		t.Fatalf("expected ['room'] revoked, got %v", revoked)
	}
	if h.ChannelCount() != 0 {
		t.Fatalf("expected channel detached, got %d remaining", h.ChannelCount())
	}
}

func TestHub_ReValidate_KeepsStillPermittedChannels(t *testing.T) {
	b := bus.NewLocal(16)
	defer b.Close()
	checker := denyChecker{deny: map[string]map[string]bool{"anon": {"secret": true}}}
	h := New(Config{Shards: 2, SubscriberQueueSize: 16, Permissions: checker}, b)

	sub := NewSubscriber("s1", "user", "7", 16)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{
			Event:  "*",
			Table:  "users",
			Filter: map[string]any{"column": "id", "op": "eq", "value": 1.0},
		}},
	}
	if err := h.Attach(context.Background(), sub, "ok", cfg); err != nil {
		t.Fatal(err)
	}

	sub.SetRole("anon", "7")
	revoked := h.ReValidateSubscriber(sub)
	if len(revoked) != 0 {
		t.Fatalf("expected no revokes (filter does not reference denied col), got %v", revoked)
	}
	if h.ChannelCount() != 1 {
		t.Fatalf("channel should stay, got %d", h.ChannelCount())
	}
}

func TestHub_Run_ConsumesFromBus(t *testing.T) {
	h, b := newTestHub(t)
	defer b.Close()
	sub := NewSubscriber("s1", "anon", "", 16)
	defer sub.Close()
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*", Table: "t"}},
	}
	if err := h.Attach(context.Background(), sub, "c", cfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()

	// Wait until Run has actually subscribed to the bus, otherwise the
	// Publish below races and the event is dropped by the bus.
	deadline := time.Now().Add(time.Second)
	for b.SubscriberCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("hub never subscribed to bus")
		}
		time.Sleep(time.Millisecond)
	}

	if err := b.Publish(ctx, makeEvent("0/1", "public", "t", protocol.EventInsert, wal.Row{"id": 1}, nil)); err != nil {
		t.Fatal(err)
	}
	recvWithin(t, sub, 1, time.Second)

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run should return context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after ctx cancel")
	}
}

// --- helpers -------------------------------------------------------

type denyChecker struct {
	deny map[string]map[string]bool
}

func (d denyChecker) CanRead(role, _, _, col string) bool {
	if cols, ok := d.deny[role]; ok && cols[col] {
		return false
	}
	return true
}

func (d denyChecker) ReadableColumns(role, _, _ string, cols []string) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		if d.CanRead(role, "", "", c) {
			out = append(out, c)
		}
	}
	return out
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
