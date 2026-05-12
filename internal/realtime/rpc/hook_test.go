package rpc

import (
	"context"
	"sync"
	"testing"
	"time"
)

type spyHook struct {
	mu    sync.Mutex
	calls []hookCall
}

type hookCall struct {
	function string
	status   string
	duration time.Duration
}

func (s *spyHook) OnComplete(fn, status string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, hookCall{fn, status, d})
}

func (s *spyHook) last() hookCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

func TestInvoker_Hook_OK(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name:    "ping",
		Handler: func(context.Context, any) (any, error) { return "pong", nil },
	})
	inv := NewInvoker(r, 0)
	spy := &spyHook{}
	inv.SetHook(spy)

	inv.Call(context.Background(), CallContext{Function: "ping"})
	if got := spy.last(); got.function != "ping" || got.status != "ok" {
		t.Fatalf("hook call = %+v", got)
	}
}

func TestInvoker_Hook_StatusForEachError(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name:         "admin",
		AllowedRoles: []string{"admin"},
		Handler:      func(context.Context, any) (any, error) { return nil, nil },
	})
	r.Register(Definition{
		Name: "slow",
		Handler: func(ctx context.Context, _ any) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})

	inv := NewInvoker(r, 0)
	spy := &spyHook{}
	inv.SetHook(spy)

	// unknown_function
	inv.Call(context.Background(), CallContext{Function: "nope"})
	if got := spy.last().status; got != "unknown_function" {
		t.Fatalf("status = %q", got)
	}

	// forbidden
	inv.Call(context.Background(), CallContext{Function: "admin", Role: "anon"})
	if got := spy.last().status; got != "forbidden" {
		t.Fatalf("status = %q", got)
	}

	// timeout
	inv.Call(context.Background(), CallContext{Function: "slow", Timeout: 10 * time.Millisecond})
	if got := spy.last().status; got != "timeout" {
		t.Fatalf("status = %q", got)
	}
}

func TestInvoker_Hook_NilHookIsNoop(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name:    "ok",
		Handler: func(context.Context, any) (any, error) { return nil, nil },
	})
	inv := NewInvoker(r, 0)
	inv.SetHook(nil) // explicit nil — must reset to nopHook, not panic
	inv.Call(context.Background(), CallContext{Function: "ok"})
}
