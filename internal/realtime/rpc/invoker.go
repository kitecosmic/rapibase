package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// CallContext carries the metadata needed by an invoker to dispatch a
// single rpc call. Transport builds this from the inbound frame and
// the session state.
type CallContext struct {
	// Function is the registered name to invoke.
	Function string

	// Args is the codec-decoded argument value; pass it through to the
	// handler as-is.
	Args any

	// Role is the caller's effective role for permission checks. Must
	// be set by the transport from the validated JWT.
	Role string

	// UserID is the JWT subject; passed through into the handler's
	// context so handlers can authorize on a per-user basis without
	// re-parsing the token.
	UserID string

	// Timeout, when zero, falls back to the invoker's default.
	Timeout time.Duration
}

// Result is what an invoker returns to the caller. Either OK is true
// and Value contains the function's output, or OK is false and Err
// carries the failure reason. Inspect OK before reading Value.
type Result struct {
	OK    bool
	Value any
	Err   error
}

// Invoker dispatches CallContexts against a Registry, enforcing role
// checks, per-call timeouts and panic recovery. One Invoker is shared
// across every connection in the process.
type Invoker struct {
	reg            *Registry
	defaultTimeout time.Duration
	hook           Hook
}

// Hook is the observability sink for the invoker. Implemented by the
// realtime root package as a thin adapter over metrics.Recorder, so
// the rpc package does not depend on the metrics package directly.
type Hook interface {
	OnComplete(function, status string, duration time.Duration)
}

// nopHook is the default no-op implementation.
type nopHook struct{}

func (nopHook) OnComplete(string, string, time.Duration) {}

// NewInvoker builds an Invoker bound to a registry. When
// defaultTimeout is zero, falls back to 5 seconds.
func NewInvoker(reg *Registry, defaultTimeout time.Duration) *Invoker {
	if defaultTimeout <= 0 {
		defaultTimeout = 5 * time.Second
	}
	return &Invoker{reg: reg, defaultTimeout: defaultTimeout, hook: nopHook{}}
}

// SetHook installs an observability hook. Safe to call after
// construction because Call only reads the hook field at the start
// and end of each invocation.
func (i *Invoker) SetHook(h Hook) {
	if h == nil {
		i.hook = nopHook{}
		return
	}
	i.hook = h
}

// CallerKey, UserIDKey are context keys used by handlers to read the
// caller's identity without re-parsing anything. Exported as typed
// values to avoid string-key collisions.
type ctxKey int

const (
	roleKey ctxKey = iota
	userIDKey
)

// RoleFromContext extracts the caller's role from a handler context.
// Returns "" when called outside an RPC dispatch.
func RoleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(roleKey).(string)
	return v
}

// UserIDFromContext extracts the caller's user id from a handler
// context. Returns "" when called outside an RPC dispatch or for
// anonymous callers.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}

// Call runs the function described by cc and returns its Result.
// Panics inside the handler are recovered and surfaced as a normal
// Result with OK=false and an explanatory error so the transport
// loop never dies on a faulty function.
func (i *Invoker) Call(ctx context.Context, cc CallContext) (result Result) {
	start := time.Now()
	hook := i.hook
	defer func() {
		hook.OnComplete(cc.Function, statusOf(result), time.Since(start))
	}()

	def, ok := i.reg.Lookup(cc.Function)
	if !ok {
		return Result{Err: ErrUnknownFunction}
	}
	if !roleAllowed(def.AllowedRoles, cc.Role) {
		return Result{Err: ErrForbidden}
	}

	timeout := cc.Timeout
	if timeout <= 0 {
		timeout = i.defaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	callCtx = context.WithValue(callCtx, roleKey, cc.Role)
	callCtx = context.WithValue(callCtx, userIDKey, cc.UserID)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			result = Result{Err: fmt.Errorf("rpc panic in %q: %v", cc.Function, r)}
		}
	}()

	value, err := def.Handler(callCtx, cc.Args)
	if err != nil {
		return Result{Err: err}
	}
	return Result{OK: true, Value: value}
}

// statusOf maps a Result to a short status label for observability.
func statusOf(r Result) string {
	if r.OK {
		return "ok"
	}
	switch {
	case errors.Is(r.Err, ErrUnknownFunction):
		return "unknown_function"
	case errors.Is(r.Err, ErrForbidden):
		return "forbidden"
	case errors.Is(r.Err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(r.Err, context.Canceled):
		return "canceled"
	default:
		return "error"
	}
}

func roleAllowed(allowed []string, role string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, r := range allowed {
		if r == role {
			return true
		}
	}
	return false
}

// ErrForbidden indicates the caller's role is not in the AllowedRoles
// set of the function definition. Mapped to protocol.ErrUnauthorized
// by the transport.
var ErrForbidden = errors.New("rpc: forbidden")
