package rpc

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRegistry_RegisterLookup(t *testing.T) {
	r := NewRegistry()
	def := Definition{
		Name:    "ping",
		Handler: func(ctx context.Context, _ any) (any, error) { return "pong", nil },
	}
	r.Register(def)

	got, ok := r.Lookup("ping")
	if !ok {
		t.Fatalf("expected lookup ok")
	}
	if got.Name != "ping" {
		t.Fatalf("name: %q", got.Name)
	}
	if r.Count() != 1 {
		t.Fatalf("count: %d", r.Count())
	}
}

func TestRegistry_Lookup_Unknown(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Lookup("nope"); ok {
		t.Fatalf("unknown lookup should fail")
	}
}

func TestRegistry_Register_Replaces(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Name: "x", RatePerSec: 10})
	r.Register(Definition{Name: "x", RatePerSec: 20})
	got, _ := r.Lookup("x")
	if got.RatePerSec != 20 {
		t.Fatalf("re-register did not replace, got rate=%d", got.RatePerSec)
	}
	if r.Count() != 1 {
		t.Fatalf("count: %d", r.Count())
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{Name: "a"})
	r.Register(Definition{Name: "b"})

	if !r.Unregister("a") {
		t.Fatalf("expected unregister to return true")
	}
	if r.Unregister("a") {
		t.Fatalf("second unregister should be false")
	}
	if _, ok := r.Lookup("a"); ok {
		t.Fatalf("a should be gone")
	}
	if r.Count() != 1 {
		t.Fatalf("count after unregister: %d", r.Count())
	}
}

func TestRegistry_NamesAndList_Sorted(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"c", "a", "b"} {
		r.Register(Definition{Name: n})
	}
	names := r.Names()
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("Names: got %v want %v", names, want)
	}
	defs := r.List()
	for i, d := range defs {
		if d.Name != want[i] {
			t.Fatalf("List[%d] = %s, want %s", i, d.Name, want[i])
		}
	}
}

func TestInvoker_OK_ReturnsValue(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name: "echo",
		Handler: func(_ context.Context, args any) (any, error) {
			return args, nil
		},
	})
	inv := NewInvoker(r, 0)
	res := inv.Call(context.Background(), CallContext{Function: "echo", Args: "hi"})
	if !res.OK || res.Value != "hi" {
		t.Fatalf("expected ok+hi, got %+v", res)
	}
}

func TestInvoker_UnknownFunction(t *testing.T) {
	inv := NewInvoker(NewRegistry(), 0)
	res := inv.Call(context.Background(), CallContext{Function: "nope"})
	if res.OK || !errors.Is(res.Err, ErrUnknownFunction) {
		t.Fatalf("expected ErrUnknownFunction, got %+v", res)
	}
}

func TestInvoker_RoleAllowedSet_ForbidsOthers(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name:         "admin_op",
		AllowedRoles: []string{"admin"},
		Handler:      func(context.Context, any) (any, error) { return "ok", nil },
	})
	inv := NewInvoker(r, 0)

	if res := inv.Call(context.Background(), CallContext{Function: "admin_op", Role: "anon"}); !errors.Is(res.Err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for anon, got %+v", res)
	}
	if res := inv.Call(context.Background(), CallContext{Function: "admin_op", Role: "admin"}); !res.OK {
		t.Fatalf("expected ok for admin, got %+v", res)
	}
}

func TestInvoker_EmptyAllowedRoles_AnyCallerAllowed(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name:    "public",
		Handler: func(context.Context, any) (any, error) { return 1, nil },
	})
	inv := NewInvoker(r, 0)
	if res := inv.Call(context.Background(), CallContext{Function: "public", Role: "anon"}); !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
}

func TestInvoker_HandlerError_PropagatesAsErr(t *testing.T) {
	wanted := errors.New("nope")
	r := NewRegistry()
	r.Register(Definition{
		Name: "boom",
		Handler: func(context.Context, any) (any, error) {
			return nil, wanted
		},
	})
	inv := NewInvoker(r, 0)
	res := inv.Call(context.Background(), CallContext{Function: "boom"})
	if res.OK || !errors.Is(res.Err, wanted) {
		t.Fatalf("expected wrapped error, got %+v", res)
	}
}

func TestInvoker_Panic_RecoveredAsErr(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name:    "crash",
		Handler: func(context.Context, any) (any, error) { panic("oops") },
	})
	inv := NewInvoker(r, 0)
	res := inv.Call(context.Background(), CallContext{Function: "crash"})
	if res.OK || res.Err == nil {
		t.Fatalf("expected recovered error, got %+v", res)
	}
	if !contains(res.Err.Error(), "crash") || !contains(res.Err.Error(), "oops") {
		t.Fatalf("error should mention function name and payload: %v", res.Err)
	}
}

func TestInvoker_Timeout_HonoredViaContext(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name: "slow",
		Handler: func(ctx context.Context, _ any) (any, error) {
			select {
			case <-time.After(time.Second):
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})
	inv := NewInvoker(r, 5*time.Second)
	start := time.Now()
	res := inv.Call(context.Background(), CallContext{Function: "slow", Timeout: 20 * time.Millisecond})
	elapsed := time.Since(start)
	if res.OK {
		t.Fatalf("expected timeout failure, got value %+v", res.Value)
	}
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", res.Err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("timeout did not actually fire: elapsed=%v", elapsed)
	}
}

func TestInvoker_Defaults_UsedWhenTimeoutZero(t *testing.T) {
	r := NewRegistry()
	captured := make(chan time.Time, 1)
	r.Register(Definition{
		Name: "deadline",
		Handler: func(ctx context.Context, _ any) (any, error) {
			d, _ := ctx.Deadline()
			captured <- d
			return nil, nil
		},
	})
	inv := NewInvoker(r, 100*time.Millisecond)
	res := inv.Call(context.Background(), CallContext{Function: "deadline"})
	if !res.OK {
		t.Fatalf("expected ok, got %+v", res)
	}
	d := <-captured
	if time.Until(d) > 100*time.Millisecond || time.Until(d) < -10*time.Millisecond {
		t.Fatalf("deadline not within default window: %v from now", time.Until(d))
	}
}

func TestInvoker_ContextValues_AvailableToHandler(t *testing.T) {
	r := NewRegistry()
	gotRole := make(chan string, 1)
	gotUser := make(chan string, 1)
	r.Register(Definition{
		Name: "whoami",
		Handler: func(ctx context.Context, _ any) (any, error) {
			gotRole <- RoleFromContext(ctx)
			gotUser <- UserIDFromContext(ctx)
			return nil, nil
		},
	})
	inv := NewInvoker(r, 0)
	res := inv.Call(context.Background(), CallContext{Function: "whoami", Role: "user", UserID: "42"})
	if !res.OK {
		t.Fatal(res.Err)
	}
	if r := <-gotRole; r != "user" {
		t.Fatalf("role: %q", r)
	}
	if u := <-gotUser; u != "42" {
		t.Fatalf("user: %q", u)
	}
}

func TestInvoker_ParentContextCanceled_AbortsHandler(t *testing.T) {
	r := NewRegistry()
	r.Register(Definition{
		Name: "wait",
		Handler: func(ctx context.Context, _ any) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	inv := NewInvoker(r, 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		done <- inv.Call(ctx, CallContext{Function: "wait", Timeout: 5 * time.Second})
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case res := <-done:
		if !errors.Is(res.Err, context.Canceled) {
			t.Fatalf("expected canceled, got %v", res.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not honor parent cancellation")
	}
}

func TestRegistry_Concurrent(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := refOf(i)
			r.Register(Definition{Name: name})
			_, _ = r.Lookup(name)
			r.Unregister(name)
		}()
	}
	// Concurrent reader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = r.Names()
		}
	}()
	wg.Wait()
	if r.Count() != 0 {
		t.Fatalf("expected empty, got %d", r.Count())
	}
}

// helpers ----------------------------------------------------------

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func refOf(i int) string {
	const hex = "0123456789abcdef"
	return string([]byte{hex[(i>>4)&0xf], hex[i&0xf]})
}
