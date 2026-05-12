package transport

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/bus"
	"github.com/rapibase/rapibase/internal/realtime/hub"
	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// fakeConn is a controllable rawConn used to drive the Conn from tests.
//
// It pairs two channels: writes done by the Conn go onto `outgoing`
// (tests pop them to assert), and tests push frames onto `incoming`
// that the Conn will read. Close signals EOF on incoming.
type fakeConn struct {
	incoming chan []byte
	outgoing chan []byte
	closed   chan struct{}

	mu       sync.Mutex
	closedFn bool
	deadline time.Time

	// readErr, if set, is returned from the next ReadMessage.
	readErr error
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		incoming: make(chan []byte, 16),
		outgoing: make(chan []byte, 16),
		closed:   make(chan struct{}),
	}
}

func (f *fakeConn) ReadMessage() ([]byte, error) {
	f.mu.Lock()
	if f.readErr != nil {
		err := f.readErr
		f.mu.Unlock()
		return nil, err
	}
	deadline := f.deadline
	f.mu.Unlock()

	var timer *time.Timer
	var deadlineC <-chan time.Time
	if !deadline.IsZero() {
		timer = time.NewTimer(time.Until(deadline))
		deadlineC = timer.C
		defer timer.Stop()
	}

	select {
	case data, ok := <-f.incoming:
		if !ok {
			return nil, errClosedFake
		}
		return data, nil
	case <-deadlineC:
		return nil, timeoutErr{}
	case <-f.closed:
		return nil, errClosedFake
	}
}

func (f *fakeConn) WriteMessage(data []byte) error {
	select {
	case f.outgoing <- append([]byte(nil), data...):
		return nil
	case <-f.closed:
		return errClosedFake
	}
}

func (f *fakeConn) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadline = t
	f.mu.Unlock()
	return nil
}

func (f *fakeConn) Close(code int, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closedFn {
		return nil
	}
	f.closedFn = true
	close(f.closed)
	return nil
}

// timeoutErr satisfies net.Error's Timeout() bool detection.
type timeoutErr struct{}

func (timeoutErr) Error() string { return "fake timeout" }
func (timeoutErr) Timeout() bool { return true }

var errClosedFake = errors.New("fake conn closed")

// echoRouter is a Router stub that returns canned responses keyed by
// inbound frame type.
type echoRouter struct {
	on map[protocol.FrameType]func(in protocol.Frame) (protocol.Frame, error)
}

func (e *echoRouter) Dispatch(_ context.Context, _ *Session, in protocol.Frame) (protocol.Frame, error) {
	if fn := e.on[in.Type]; fn != nil {
		return fn(in)
	}
	return protocol.Frame{}, nil
}

// newTestConn wires a fakeConn to a Conn using the JSON codec and a
// permissive heartbeat policy. Returns the Conn, the fakeConn, the
// session, and a cleanup func.
func newTestConn(t *testing.T, router Router) (*Conn, *fakeConn, *Session, func()) {
	t.Helper()
	fc := newFakeConn()
	codec := protocol.NewJSONCodec()
	sub := hub.NewSubscriber("sess-1", "anon", "u", 16)
	sess := NewSession("sess-1", codec, sub, "test-apikey", "anon", "u")
	c := newConn(fc, sess, router, ConnOptions{
		HeartbeatInterval:      50 * time.Millisecond,
		HeartbeatTimeoutFactor: 4,
		MaxPayloadBytes:        4096,
	})
	cleanup := func() {
		fc.Close(0, "")
		sub.Close()
	}
	return c, fc, sess, cleanup
}

func sendFrame(t *testing.T, fc *fakeConn, codec protocol.Codec, f protocol.Frame) {
	t.Helper()
	bs, err := codec.Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	fc.incoming <- bs
}

func recvFrame(t *testing.T, fc *fakeConn, codec protocol.Codec, timeout time.Duration) protocol.Frame {
	t.Helper()
	select {
	case bs := <-fc.outgoing:
		f, err := codec.Decode(bs)
		if err != nil {
			t.Fatalf("decode: %v body=%s", err, bs)
		}
		return f
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for outbound frame")
		return protocol.Frame{}
	}
}

// --- tests ---------------------------------------------------------

func TestConn_SendsWelcomeFirst(t *testing.T) {
	c, fc, _, cleanup := newTestConn(t, &echoRouter{})
	defer cleanup()

	done := make(chan error, 1)
	go func() {
		done <- c.Serve(context.Background(), ConnOptions{ServerVersion: "test", StartingLSN: "0/1"})
	}()

	codec := protocol.NewJSONCodec()
	f := recvFrame(t, fc, codec, time.Second)
	if f.Type != protocol.FrameWelcome {
		t.Fatalf("first frame should be welcome, got %s", f.Type)
	}
	if f.ServerVersion != "test" || f.HeartbeatIntervalMs != 50 || f.LSN != "0/1" {
		t.Fatalf("welcome wrong: %+v", f)
	}

	fc.Close(0, "")
	<-done
}

func TestConn_DispatchesInboundAndSendsReply(t *testing.T) {
	router := &echoRouter{
		on: map[protocol.FrameType]func(protocol.Frame) (protocol.Frame, error){
			protocol.FrameSubscribe: func(in protocol.Frame) (protocol.Frame, error) {
				return protocol.Frame{Type: protocol.FrameAck, Ref: in.Ref}, nil
			},
		},
	}
	c, fc, sess, cleanup := newTestConn(t, router)
	defer cleanup()

	go c.Serve(context.Background(), ConnOptions{ServerVersion: "test"})

	// Drain welcome.
	recvFrame(t, fc, sess.Codec(), time.Second)

	sendFrame(t, fc, sess.Codec(), protocol.Frame{Type: protocol.FrameSubscribe, Ref: "s1", Channel: "x"})
	f := recvFrame(t, fc, sess.Codec(), time.Second)
	if f.Type != protocol.FrameAck || f.Ref != "s1" {
		t.Fatalf("expected ack for s1, got %+v", f)
	}
}

func TestConn_RouterError_TranslatesToErrorFrame(t *testing.T) {
	router := &echoRouter{
		on: map[protocol.FrameType]func(protocol.Frame) (protocol.Frame, error){
			protocol.FrameSubscribe: func(in protocol.Frame) (protocol.Frame, error) {
				return protocol.Frame{}, protocol.NewError(protocol.ErrUnauthorized, "no")
			},
		},
	}
	c, fc, sess, cleanup := newTestConn(t, router)
	defer cleanup()

	go c.Serve(context.Background(), ConnOptions{})
	recvFrame(t, fc, sess.Codec(), time.Second) // welcome

	sendFrame(t, fc, sess.Codec(), protocol.Frame{Type: protocol.FrameSubscribe, Ref: "r1"})
	f := recvFrame(t, fc, sess.Codec(), time.Second)
	if f.Type != protocol.FrameError || f.Code != string(protocol.ErrUnauthorized) || f.Ref != "r1" {
		t.Fatalf("expected mapped error, got %+v", f)
	}
}

func TestConn_HeartbeatInbound_GetsAck_NoRouterCall(t *testing.T) {
	called := 0
	router := &echoRouter{
		on: map[protocol.FrameType]func(protocol.Frame) (protocol.Frame, error){
			protocol.FrameHeartbeatIn: func(protocol.Frame) (protocol.Frame, error) {
				called++
				return protocol.Frame{}, nil
			},
		},
	}
	c, fc, sess, cleanup := newTestConn(t, router)
	defer cleanup()

	go c.Serve(context.Background(), ConnOptions{})
	recvFrame(t, fc, sess.Codec(), time.Second) // welcome

	sendFrame(t, fc, sess.Codec(), protocol.Frame{Type: protocol.FrameHeartbeatIn, Ref: "h1"})
	f := recvFrame(t, fc, sess.Codec(), time.Second)
	if f.Type != protocol.FrameAck || f.Ref != "h1" {
		t.Fatalf("expected ack, got %+v", f)
	}
	if called != 0 {
		t.Fatalf("router should not see heartbeat, got %d calls", called)
	}
}

func TestConn_HeartbeatOutbound_TickerFires(t *testing.T) {
	c, fc, sess, cleanup := newTestConn(t, &echoRouter{})
	defer cleanup()

	go c.Serve(context.Background(), ConnOptions{})
	recvFrame(t, fc, sess.Codec(), time.Second) // welcome

	// Wait for at least one heartbeat tick.
	f := recvFrame(t, fc, sess.Codec(), 500*time.Millisecond)
	if f.Type != protocol.FrameHeartbeatOut {
		t.Fatalf("expected outbound heartbeat, got %+v", f)
	}
}

func TestConn_HeartbeatTimeout_ClosesConnection(t *testing.T) {
	c, fc, sess, cleanup := newTestConn(t, &echoRouter{})
	defer cleanup()

	done := make(chan error, 1)
	go func() {
		done <- c.Serve(context.Background(), ConnOptions{})
	}()

	recvFrame(t, fc, sess.Codec(), time.Second) // welcome

	// Don't send anything. Deadline = 50ms * 4 = 200ms.
	select {
	case err := <-done:
		if !errors.Is(err, errReadTimeout) {
			t.Fatalf("expected errReadTimeout, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return on heartbeat timeout")
	}
}

func TestConn_FrameTooLarge_ClosesConnection(t *testing.T) {
	c, fc, sess, cleanup := newTestConn(t, &echoRouter{})
	defer cleanup()

	done := make(chan error, 1)
	go func() { done <- c.Serve(context.Background(), ConnOptions{}) }()
	recvFrame(t, fc, sess.Codec(), time.Second) // welcome

	big := make([]byte, 5000) // > 4096 cap
	for i := range big {
		big[i] = 'a'
	}
	fc.incoming <- big

	select {
	case err := <-done:
		if !errors.Is(err, errFrameTooLarge) {
			t.Fatalf("expected frame-too-large, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestConn_CodecError_RepliesWithErrorFrame_NotFatal(t *testing.T) {
	c, fc, sess, cleanup := newTestConn(t, &echoRouter{})
	defer cleanup()

	go c.Serve(context.Background(), ConnOptions{})
	recvFrame(t, fc, sess.Codec(), time.Second) // welcome

	// Send malformed JSON.
	fc.incoming <- []byte(`{"type":`)
	f := recvFrame(t, fc, sess.Codec(), time.Second)
	if f.Type != protocol.FrameError || f.Code != string(protocol.ErrInvalidPayload) {
		t.Fatalf("expected invalid_payload error, got %+v", f)
	}

	// Connection still alive — heartbeat should follow.
	next := recvFrame(t, fc, sess.Codec(), 500*time.Millisecond)
	if next.Type != protocol.FrameHeartbeatOut {
		t.Fatalf("expected next frame to be heartbeat, got %+v", next)
	}
}

func TestConn_ContextCancel_ReturnsCanceled(t *testing.T) {
	c, fc, sess, cleanup := newTestConn(t, &echoRouter{})
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Serve(ctx, ConnOptions{}) }()

	recvFrame(t, fc, sess.Codec(), time.Second) // welcome
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return on cancel")
	}
}

func TestHandshake_NegotiateSubprotocol_PrefersMsgpack(t *testing.T) {
	c := negotiateSubprotocol(protocol.SubprotocolJSON + ", " + protocol.SubprotocolMsgpack)
	if c == nil || c.Subprotocol() != protocol.SubprotocolMsgpack {
		t.Fatalf("expected msgpack preference, got %v", c)
	}
}

func TestHandshake_NegotiateSubprotocol_FallsBackToJSON(t *testing.T) {
	c := negotiateSubprotocol(protocol.SubprotocolJSON)
	if c == nil || c.Subprotocol() != protocol.SubprotocolJSON {
		t.Fatalf("expected JSON, got %v", c)
	}
}

func TestHandshake_NegotiateSubprotocol_UnknownReturnsNil(t *testing.T) {
	if c := negotiateSubprotocol("bogus.v0+json"); c != nil {
		t.Fatalf("expected nil for unknown subprotocol, got %v", c)
	}
}

// TestHandle_FullCycle wires the hub to verify subscribe + event +
// fan-out works end-to-end through the transport.
func TestHandle_FullCycle(t *testing.T) {
	b := bus.NewLocal(16)
	defer b.Close()
	h := hub.New(hub.Config{Shards: 2, SubscriberQueueSize: 16, ResumeBufferSize: 16}, b)

	router := &simpleRouter{hub: h}

	deps := HandlerDeps{
		Hub:                 h,
		Router:              router,
		Auth:                &fixedAuth{role: "anon"},
		SubscriberQueueSize: 16,
		ServerVersion:       "test",
		HeartbeatPolicy: ConnOptions{
			HeartbeatInterval:      50 * time.Millisecond,
			HeartbeatTimeoutFactor: 10,
			MaxPayloadBytes:        4096,
		},
	}
	hdl := NewHandler(deps)

	fc := newFakeConn()
	codec := protocol.NewJSONCodec()
	done := make(chan error, 1)
	go func() { done <- hdl.Handle(context.Background(), fc, "test-apikey", "anon", "u", codec) }()

	recvFrame(t, fc, codec, time.Second) // welcome

	// Subscribe to a channel with broadcast.
	sendFrame(t, fc, codec, protocol.Frame{
		Type:    protocol.FrameSubscribe,
		Ref:     "s1",
		Channel: "room:42",
		Config:  &protocol.SubscribeConfig{Broadcast: &protocol.BroadcastConfig{}},
	})
	ack := recvFrame(t, fc, codec, time.Second)
	if ack.Type != protocol.FrameAck || ack.Ref != "s1" {
		t.Fatalf("expected ack, got %+v", ack)
	}

	// Broadcast through the hub directly (as if another conn fired one).
	h.Broadcast("room:42", nil, "ping", "hi", false)

	got := recvFrame(t, fc, codec, time.Second)
	if got.Type != protocol.FrameBroadcastOut || got.Channel != "room:42" {
		t.Fatalf("expected broadcast forward, got %+v", got)
	}

	fc.Close(0, "")
	<-done
}

// simpleRouter is a minimal Router that maps each inbound frame to
// the right hub call. Used only by TestHandle_FullCycle.
type simpleRouter struct{ hub *hub.Hub }

func (r *simpleRouter) Dispatch(ctx context.Context, sess *Session, in protocol.Frame) (protocol.Frame, error) {
	switch in.Type {
	case protocol.FrameSubscribe:
		if err := r.hub.Attach(ctx, sess.Subscriber(), in.Channel, in.Config); err != nil {
			return protocol.Frame{}, err
		}
		return protocol.Frame{Type: protocol.FrameAck, Ref: in.Ref}, nil
	case protocol.FrameUnsubscribe:
		r.hub.Detach(sess.Subscriber(), in.Channel)
		return protocol.Frame{Type: protocol.FrameAck, Ref: in.Ref}, nil
	}
	return protocol.Frame{}, fmt.Errorf("unknown frame %s", in.Type)
}

// fixedAuth always returns the same role/user.
type fixedAuth struct{ role, user string }

func (f *fixedAuth) Validate(_, _ string) (string, string, error) {
	return f.role, f.user, nil
}

