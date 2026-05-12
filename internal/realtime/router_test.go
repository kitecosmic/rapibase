package realtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/bus"
	"github.com/rapibase/rapibase/internal/realtime/hub"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
	"github.com/rapibase/rapibase/internal/realtime/rpc"
	"github.com/rapibase/rapibase/internal/realtime/transport"
	"github.com/rapibase/rapibase/internal/realtime/wal"
)

// Test fixtures ----------------------------------------------------

type denyChecker struct{ denied map[string]map[string]bool }

func (d denyChecker) CanRead(role, _, _, col string) bool {
	return !d.denied[role][col]
}
func (d denyChecker) ReadableColumns(role, _, _ string, cols []string) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		if !d.denied[role][c] {
			out = append(out, c)
		}
	}
	return out
}

type stubAuth struct {
	role, userID string
	err          error
}

func (s stubAuth) Validate(_, _ string) (string, string, error) {
	if s.err != nil {
		return "", "", s.err
	}
	return s.role, s.userID, nil
}

// newRouter builds a router + the hub it drives, plus a Subscriber and
// Session wired together as a Session would be in production.
func newRouter(t *testing.T, perms denyChecker, auth transport.AuthValidator) (*router, *hub.Hub, *transport.Session, *hub.Subscriber, func()) {
	t.Helper()
	b := bus.NewLocal(16)
	h := hub.New(hub.Config{Shards: 2, SubscriberQueueSize: 16, ResumeBufferSize: 16, Permissions: perms}, b)

	reg := rpc.NewRegistry()
	inv := rpc.NewInvoker(reg, 0)
	r := &router{hub: h, invoker: inv, auth: auth}

	sub := hub.NewSubscriber("sess-1", "user", "7", 16)
	codec := protocol.NewJSONCodec()
	sess := transport.NewSession("sess-1", codec, sub, "test-apikey", "user", "7")

	// Also expose registry through the closure since some tests
	// register functions before invoking.
	t.Cleanup(func() {
		sub.Close()
		_ = b.Close()
	})
	_ = reg
	return r, h, sess, sub, func() { sub.Close(); _ = b.Close() }
}

// drain reads up to N frames off the subscriber's outbound queue with a
// short per-frame timeout. Used to assert fan-out side effects.
func drain(t *testing.T, sub *hub.Subscriber, n int) []protocol.Frame {
	t.Helper()
	out := make([]protocol.Frame, 0, n)
	for i := 0; i < n; i++ {
		select {
		case f := <-sub.Outbound():
			out = append(out, f)
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timeout receiving frame %d/%d", i+1, n)
		}
	}
	return out
}

// Tests ------------------------------------------------------------

func TestRouter_Subscribe_Ack(t *testing.T) {
	r, _, sess, _, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()

	out, err := r.Dispatch(context.Background(), sess, protocol.Frame{
		Type:    protocol.FrameSubscribe,
		Ref:     "s1",
		Channel: "room",
		Config:  &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.Type != protocol.FrameAck || out.Ref != "s1" || out.Channel != "room" {
		t.Fatalf("ack mismatch: %+v", out)
	}
}

func TestRouter_Subscribe_MissingChannel_InvalidPayload(t *testing.T) {
	r, _, sess, _, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()
	_, err := r.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameSubscribe, Ref: "s1"})
	if !isProtocolCode(err, protocol.ErrInvalidPayload) {
		t.Fatalf("expected invalid_payload, got %v", err)
	}
}

func TestRouter_Subscribe_DeniedColumn_ForbiddenFilter(t *testing.T) {
	perms := denyChecker{denied: map[string]map[string]bool{"user": {"secret": true}}}
	r, _, sess, _, cleanup := newRouter(t, perms, stubAuth{})
	defer cleanup()
	_, err := r.Dispatch(context.Background(), sess, protocol.Frame{
		Type:    protocol.FrameSubscribe,
		Ref:     "s1",
		Channel: "room",
		Config: &protocol.SubscribeConfig{
			PostgresChanges: []protocol.PostgresChangesConfig{{
				Event:  "*",
				Table:  "users",
				Filter: map[string]any{"column": "secret", "op": "eq", "value": "x"},
			}},
		},
	})
	if !isProtocolCode(err, protocol.ErrForbiddenFilter) {
		t.Fatalf("expected forbidden_filter, got %v", err)
	}
}

func TestRouter_Unsubscribe_Ack(t *testing.T) {
	r, _, sess, _, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()
	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}}
	_, _ = r.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameSubscribe, Ref: "s1", Channel: "x", Config: cfg})

	out, err := r.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameUnsubscribe, Ref: "u1", Channel: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != protocol.FrameAck || out.Ref != "u1" {
		t.Fatalf("ack mismatch: %+v", out)
	}
}

func TestRouter_Broadcast_FanOutAndOptionalAck(t *testing.T) {
	r, h, sender, senderSub, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()
	// Sender subscribes to broadcast on a channel; with Self=true so it
	// receives its own broadcasts.
	cfg := &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{Self: true}}
	_, _ = r.Dispatch(context.Background(), sender, protocol.Frame{Type: protocol.FrameSubscribe, Channel: "room", Config: cfg})

	// A second subscriber on the same channel.
	other := hub.NewSubscriber("sess-2", "user", "8", 16)
	defer other.Close()
	if err := h.Attach(context.Background(), other, "room", cfg); err != nil {
		t.Fatal(err)
	}

	// No ack requested (Ref empty).
	out, err := r.Dispatch(context.Background(), sender, protocol.Frame{
		Type:    protocol.FrameBroadcastIn,
		Channel: "room",
		Event:   "typing",
		Payload: map[string]any{"user": 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != "" {
		t.Fatalf("expected no reply without ref, got %+v", out)
	}

	// Both subscribers should receive the frame (self enabled).
	got1 := drain(t, senderSub, 1)
	got2 := drain(t, other, 1)
	for i, f := range []protocol.Frame{got1[0], got2[0]} {
		if f.Type != protocol.FrameBroadcastOut || f.Event != "typing" {
			t.Fatalf("subscriber %d got %+v", i, f)
		}
	}

	// Ack requested via Ref.
	out, err = r.Dispatch(context.Background(), sender, protocol.Frame{
		Type:    protocol.FrameBroadcastIn,
		Ref:     "b1",
		Channel: "room",
		Event:   "typing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != protocol.FrameAck || out.Ref != "b1" {
		t.Fatalf("expected ack, got %+v", out)
	}
}

func TestRouter_PresenceTrack_PropagatesDiff(t *testing.T) {
	r, h, sess, sub, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()

	// Two subs on the same channel, both with presence on.
	cfg := &protocol.SubscribeConfig{Presence: &protocol.PresenceConfig{Key: "user_7"}}
	_, _ = r.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameSubscribe, Channel: "room", Config: cfg})

	other := hub.NewSubscriber("sess-2", "user", "8", 16)
	defer other.Close()
	cfg2 := &protocol.SubscribeConfig{Presence: &protocol.PresenceConfig{Key: "user_8"}}
	if err := h.Attach(context.Background(), other, "room", cfg2); err != nil {
		t.Fatal(err)
	}

	// Track for our session.
	if _, err := r.Dispatch(context.Background(), sess, protocol.Frame{
		Type:    protocol.FramePresenceTrack,
		Ref:     "t1",
		Channel: "room",
		Payload: map[string]any{"status": "online"},
	}); err != nil {
		t.Fatal(err)
	}
	// We expect a presence_diff broadcast on both subs (Join).
	got := drain(t, sub, 1)
	if got[0].Type != protocol.FramePresenceDiff || len(got[0].Joins) == 0 {
		t.Fatalf("our sub should see join diff: %+v", got[0])
	}
	got = drain(t, other, 1)
	if got[0].Type != protocol.FramePresenceDiff || len(got[0].Joins) == 0 {
		t.Fatalf("other sub should see join diff: %+v", got[0])
	}
}

func TestRouter_RPC_OK(t *testing.T) {
	r, _, sess, _, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()
	// Register a function on the underlying registry.
	r.invoker = rpc.NewInvoker(func() *rpc.Registry {
		reg := rpc.NewRegistry()
		reg.Register(rpc.Definition{
			Name: "ping",
			Handler: func(_ context.Context, args any) (any, error) {
				return map[string]any{"echo": args}, nil
			},
		})
		return reg
	}(), 0)

	out, err := r.Dispatch(context.Background(), sess, protocol.Frame{
		Type:     protocol.FrameRPC,
		Ref:      "r1",
		Function: "ping",
		Args:     "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != protocol.FrameRPCResponse || !out.OK || out.Ref != "r1" {
		t.Fatalf("rpc response mismatch: %+v", out)
	}
	res, _ := out.Result.(map[string]any)
	if res["echo"] != "hi" {
		t.Fatalf("echo wrong: %+v", out.Result)
	}
}

func TestRouter_RPC_UnknownFunction(t *testing.T) {
	r, _, sess, _, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()
	_, err := r.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameRPC, Function: "missing"})
	if !isProtocolCode(err, protocol.ErrUnknownFunction) {
		t.Fatalf("expected unknown_function, got %v", err)
	}
}

func TestRouter_Resume_Truncated(t *testing.T) {
	r, h, sess, sub, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()
	// Subscribe with tiny resume buffer; push 3 events so the first is
	// truncated; try to resume from before the survivor.
	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{Event: "*", Table: "t"}},
	}
	_, _ = r.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameSubscribe, Channel: "c", Config: cfg})

	// Use a fresh hub with a tiny resume buffer for this test only.
	smallHub := hub.New(hub.Config{Shards: 1, SubscriberQueueSize: 8, ResumeBufferSize: 2, Permissions: denyChecker{}}, bus.NewLocal(8))
	if err := smallHub.Attach(context.Background(), sub, "c", cfg); err != nil {
		t.Fatal(err)
	}
	r2 := &router{hub: smallHub, invoker: rpc.NewInvoker(rpc.NewRegistry(), 0), auth: stubAuth{}}

	for _, lsn := range []string{"0/1", "0/2", "0/3"} {
		smallHub.PublishLocal(wal.Event{LSN: protocol.LSN(lsn), Type: protocol.EventInsert, Table: "t", New: wal.Row{"id": 1}})
	}
	// Drain live deliveries to keep the queue from filling.
	drain(t, sub, 3)
	// Drain the duplicate live deliveries from the original `h` hub
	// too (Subscribe attached us there).
	_ = h
	for i := 0; i < 3; i++ {
		select {
		case <-sub.Outbound():
		case <-time.After(50 * time.Millisecond):
		}
	}

	_, err := r2.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameResume, Channel: "c", FromLSN: "0/1"})
	if !isProtocolCode(err, protocol.ErrSlotTruncated) {
		t.Fatalf("expected slot_truncated, got %v", err)
	}
}

func TestRouter_SetAuth_RotatesAndRevokes(t *testing.T) {
	// Initial role "user" can read "secret"; after set_auth → "anon",
	// the secret-referencing subscription must be revoked.
	perms := denyChecker{denied: map[string]map[string]bool{"anon": {"secret": true}}}
	auth := stubAuth{role: "anon", userID: "7"}
	r, h, sess, _, cleanup := newRouter(t, perms, auth)
	defer cleanup()

	cfg := &protocol.SubscribeConfig{
		PostgresChanges: []protocol.PostgresChangesConfig{{
			Event:  "*",
			Table:  "users",
			Filter: map[string]any{"column": "secret", "op": "eq", "value": "x"},
		}},
	}
	if _, err := r.Dispatch(context.Background(), sess, protocol.Frame{
		Type:    protocol.FrameSubscribe,
		Channel: "users",
		Config:  cfg,
	}); err != nil {
		t.Fatal(err)
	}
	if h.ChannelCount() != 1 {
		t.Fatalf("expected 1 channel, got %d", h.ChannelCount())
	}

	out, err := r.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameSetAuth, Ref: "a1", Token: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != protocol.FrameAck || out.Ref != "a1" {
		t.Fatalf("expected ack, got %+v", out)
	}
	detail, _ := out.Detail.(map[string]any)
	lost, _ := detail["lost_channels"].([]string)
	if len(lost) != 1 || lost[0] != "users" {
		t.Fatalf("expected ['users'] lost, got %v", lost)
	}
	if h.ChannelCount() != 0 {
		t.Fatalf("expected channel revoked, got %d", h.ChannelCount())
	}
}

func TestRouter_SetAuth_InvalidToken(t *testing.T) {
	auth := stubAuth{err: errors.New("expired")}
	r, _, sess, _, cleanup := newRouter(t, denyChecker{}, auth)
	defer cleanup()
	_, err := r.Dispatch(context.Background(), sess, protocol.Frame{Type: protocol.FrameSetAuth, Token: "bogus"})
	if !isProtocolCode(err, protocol.ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}

func TestRouter_UnknownFrameType_InvalidPayload(t *testing.T) {
	r, _, sess, _, cleanup := newRouter(t, denyChecker{}, stubAuth{})
	defer cleanup()
	_, err := r.Dispatch(context.Background(), sess, protocol.Frame{Type: "weird_frame"})
	if !isProtocolCode(err, protocol.ErrInvalidPayload) {
		t.Fatalf("expected invalid_payload, got %v", err)
	}
}

// isProtocolCode inspects err for a *protocol.Error with the given code.
func isProtocolCode(err error, code protocol.ErrorCode) bool {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return pe.Code == code
	}
	return false
}
