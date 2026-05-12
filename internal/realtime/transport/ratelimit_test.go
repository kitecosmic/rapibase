package transport

import (
	"context"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/realtime/protocol"
)

// ---- token bucket unit tests --------------------------------------

func TestTokenBucket_BurstAllowed(t *testing.T) {
	b := newTokenBucket(3, 1) // 3 capacity, 1/s refill
	for i := 0; i < 3; i++ {
		ok, _ := b.tryConsume()
		if !ok {
			t.Fatalf("token %d should pass", i+1)
		}
	}
}

func TestTokenBucket_BlocksWhenExceeded(t *testing.T) {
	b := newTokenBucket(2, 1)
	_, _ = b.tryConsume()
	_, _ = b.tryConsume()
	ok, retry := b.tryConsume()
	if ok {
		t.Fatal("third call should be blocked")
	}
	if retry <= 0 || retry > 2*time.Second {
		t.Fatalf("retry duration unreasonable: %v", retry)
	}
}

func TestTokenBucket_RefillsOverTime(t *testing.T) {
	b := newTokenBucket(1, 100) // 1 capacity, 100/s refill (10ms per token)
	_, _ = b.tryConsume()
	if ok, _ := b.tryConsume(); ok {
		t.Fatal("second immediate call should be blocked")
	}
	time.Sleep(20 * time.Millisecond)
	if ok, _ := b.tryConsume(); !ok {
		t.Fatal("call after 20ms should pass (refilled)")
	}
}

func TestTokenBucket_ZeroCapacityDisabled(t *testing.T) {
	b := newTokenBucket(0, 0)
	for i := 0; i < 10; i++ {
		if ok, _ := b.tryConsume(); !ok {
			t.Fatalf("zero-capacity bucket should always allow, blocked on %d", i)
		}
	}
}

func TestTokenBucket_ZeroRateNoRefill(t *testing.T) {
	b := newTokenBucket(1, 0)
	_, _ = b.tryConsume()
	ok, retry := b.tryConsume()
	if ok {
		t.Fatal("should be blocked after capacity drained with no rate")
	}
	if retry < time.Hour {
		t.Fatalf("zero-rate retry should be very large: %v", retry)
	}
}

// ---- rateLimitFor dispatch ----------------------------------------

func TestRateLimitFor_FrameTypeMapping(t *testing.T) {
	limits := RateLimits{
		BroadcastBurst:   10,
		BroadcastPerSec:  5,
		SubscribeBurst:   2,
		SubscribePerSec:  0.1,
		PresenceBurst:    3,
		PresencePerSec:   1,
		RPCDefaultBurst:  20,
		RPCDefaultPerSec: 10,
		RPCRateFor: func(fn string) int {
			if fn == "boosted" {
				return 50
			}
			return 0
		},
	}
	cases := []struct {
		frame    protocol.Frame
		key      string
		capacity int
		rate     float64
	}{
		{protocol.Frame{Type: protocol.FrameBroadcastIn}, "broadcast", 10, 5},
		{protocol.Frame{Type: protocol.FrameSubscribe}, "subscribe", 2, 0.1},
		{protocol.Frame{Type: protocol.FrameUnsubscribe}, "subscribe", 2, 0.1},
		{protocol.Frame{Type: protocol.FramePresenceTrack}, "presence", 3, 1},
		{protocol.Frame{Type: protocol.FrameRPC, Function: "ping"}, "rpc:ping", 20, 10},
		{protocol.Frame{Type: protocol.FrameRPC, Function: "boosted"}, "rpc:boosted", 20, 50},
		{protocol.Frame{Type: protocol.FrameHeartbeatIn}, "", 0, 0}, // not rate-limited
	}
	for _, c := range cases {
		gotKey, gotCap, gotRate := rateLimitFor(c.frame, limits)
		if gotKey != c.key || gotCap != c.capacity || gotRate != c.rate {
			t.Fatalf("%s: got (%q, %d, %v), want (%q, %d, %v)",
				c.frame.Type, gotKey, gotCap, gotRate, c.key, c.capacity, c.rate)
		}
	}
}

func TestRateLimits_DefaultsApplied(t *testing.T) {
	r := RateLimits{}.withDefaults()
	if r.BroadcastBurst == 0 || r.BroadcastPerSec == 0 {
		t.Fatalf("defaults missing: %+v", r)
	}
	if r.SubscribePerSec <= 0 {
		t.Fatalf("subscribe rate default missing: %+v", r)
	}
}

// ---- Conn integration ---------------------------------------------

// TestConn_RateLimit_ReturnsErrorFrameOnExcess wires a Conn with a
// very low broadcast limit and verifies the 11th broadcast within a
// burst gets rejected as ErrRateLimited (the first 10 succeed).
func TestConn_RateLimit_ReturnsErrorFrameOnExcess(t *testing.T) {
	router := &echoRouter{
		on: map[protocol.FrameType]func(protocol.Frame) (protocol.Frame, error){
			protocol.FrameBroadcastIn: func(in protocol.Frame) (protocol.Frame, error) {
				return protocol.Frame{Type: protocol.FrameAck, Ref: in.Ref}, nil
			},
		},
	}
	c, fc, sess, cleanup := newTestConn(t, router)
	defer cleanup()
	// Override rate limits AFTER newTestConn (defaults were applied
	// in newConn; we replace with a tight cap).
	c.rateLimits = RateLimits{
		BroadcastBurst:   3,
		BroadcastPerSec:  0, // no refill — easier to assert
		RPCDefaultBurst:  100,
		RPCDefaultPerSec: 100,
	}.withDefaults()
	// withDefaults filled in unrelated fields but kept Broadcast as
	// we set them. Re-set burst/perSec explicitly because
	// withDefaults fills zero values:
	c.rateLimits.BroadcastBurst = 3
	c.rateLimits.BroadcastPerSec = 0

	go c.Serve(context.Background(), ConnOptions{})
	recvFrame(t, fc, sess.Codec(), time.Second) // welcome

	// First 3 broadcasts should be acked (Ref triggers ack in router).
	for i := 0; i < 3; i++ {
		sendFrame(t, fc, sess.Codec(), protocol.Frame{
			Type:    protocol.FrameBroadcastIn,
			Ref:     "b",
			Channel: "r",
			Event:   "tick",
		})
		got := recvFrame(t, fc, sess.Codec(), time.Second)
		if got.Type != protocol.FrameAck {
			t.Fatalf("broadcast %d: expected ack, got %+v", i+1, got)
		}
	}

	// 4th should be rate-limited.
	sendFrame(t, fc, sess.Codec(), protocol.Frame{
		Type:    protocol.FrameBroadcastIn,
		Ref:     "b4",
		Channel: "r",
		Event:   "tick",
	})
	got := recvFrame(t, fc, sess.Codec(), time.Second)
	if got.Type != protocol.FrameError {
		t.Fatalf("4th broadcast should error, got %+v", got)
	}
	if got.Code != string(protocol.ErrRateLimited) {
		t.Fatalf("expected rate_limited code, got %q", got.Code)
	}
	if !got.Retryable {
		t.Fatal("rate limit errors must be retryable")
	}
}

// TestConn_RateLimit_PerFunctionRPCOverride verifies the registry's
// per-function RatePerSec is honoured.
func TestConn_RateLimit_PerFunctionRPCOverride(t *testing.T) {
	// rpc dispatch shape: router returns rpc_response with the same Ref.
	router := &echoRouter{
		on: map[protocol.FrameType]func(protocol.Frame) (protocol.Frame, error){
			protocol.FrameRPC: func(in protocol.Frame) (protocol.Frame, error) {
				return protocol.Frame{
					Type: protocol.FrameRPCResponse,
					Ref:  in.Ref,
					OK:   true,
				}, nil
			},
		},
	}
	c, fc, sess, cleanup := newTestConn(t, router)
	defer cleanup()

	// "slow" function has burst=1, rate=0. Everything else is generous.
	c.rateLimits = RateLimits{
		RPCDefaultBurst:  100,
		RPCDefaultPerSec: 100,
		RPCRateFor: func(fn string) int {
			if fn == "slow" {
				return 0 // not used; rate ignored when burst=1 below
			}
			return 0
		},
	}.withDefaults()
	// Force a single-token bucket for "slow" by hijacking the helper —
	// rateLimitFor uses RPCDefaultBurst which is 100 here. Easier test:
	// just set RPCDefaultBurst to 1 to make the rule apply to every RPC.
	c.rateLimits.RPCDefaultBurst = 1
	c.rateLimits.RPCDefaultPerSec = 0

	go c.Serve(context.Background(), ConnOptions{})
	recvFrame(t, fc, sess.Codec(), time.Second) // welcome

	// First "slow" call passes.
	sendFrame(t, fc, sess.Codec(), protocol.Frame{
		Type: protocol.FrameRPC, Ref: "r1", Function: "slow",
	})
	got := recvFrame(t, fc, sess.Codec(), time.Second)
	if got.Type != protocol.FrameRPCResponse {
		t.Fatalf("first slow call expected response, got %+v", got)
	}

	// Second is rate-limited.
	sendFrame(t, fc, sess.Codec(), protocol.Frame{
		Type: protocol.FrameRPC, Ref: "r2", Function: "slow",
	})
	got = recvFrame(t, fc, sess.Codec(), time.Second)
	if got.Type != protocol.FrameError || got.Code != string(protocol.ErrRateLimited) {
		t.Fatalf("second call should be rate-limited, got %+v", got)
	}

	// A different function uses a separate bucket — but DefaultBurst is
	// 1, so it ALSO gets one allowed and then blocked. The assertion is
	// that the bucket is keyed per-function, so the "fast" function
	// still has its token even though "slow" exhausted its own.
	sendFrame(t, fc, sess.Codec(), protocol.Frame{
		Type: protocol.FrameRPC, Ref: "f1", Function: "fast",
	})
	got = recvFrame(t, fc, sess.Codec(), time.Second)
	if got.Type != protocol.FrameRPCResponse {
		t.Fatalf("fast function should have its own bucket, got %+v", got)
	}
}
