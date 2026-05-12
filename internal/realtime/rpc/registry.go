// Package rpc implements server-side function invocations carried over
// the same WebSocket connection used for subscriptions, broadcast and
// presence.
//
// A Function is any handler registered against a name. The transport
// dispatches incoming rpc frames to the Invoker, which routes them by
// name and enforces role checks. Rate limiting and concurrency caps
// are deliberately NOT implemented here — they belong in the transport
// layer (per-connection limiter) because they need session-level state
// that the rpc package should not know about.
package rpc

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// Function is the contract for a server-side RPC handler. The context
// carries the caller's auth identity (role, user id) and the
// invocation deadline; handlers must respect both. Args and the
// returned result are codec-agnostic Go values (typically map[string]any
// decoded from the wire); handlers can json.Marshal them back if they
// need bytes.
type Function func(ctx context.Context, args any) (result any, err error)

// Definition describes a registered RPC, including metadata used for
// permission checks and external discovery (dashboards listing
// available functions).
type Definition struct {
	// Name is the wire-visible identifier callers use in the "function"
	// field of an rpc frame.
	Name string

	// AllowedRoles, when non-empty, restricts the function to callers
	// whose role appears in the set. Empty means any authenticated
	// caller is allowed; the transport layer enforces that the caller
	// is authenticated before reaching the registry.
	AllowedRoles []string

	// RatePerSec is the per-connection rate limit *intent*. The
	// registry stores it as metadata; the transport's rate limiter
	// reads this field when configuring its per-session bucket. Zero
	// means "use the transport's global default".
	RatePerSec int

	// Handler is the function body. Must be safe for concurrent use.
	Handler Function
}

// Registry stores RPC definitions and exposes lookup / iteration.
// Functions are typically registered once at startup but the lookup
// path is hot — Registry uses RWMutex so reads do not contend.
type Registry struct {
	mu        sync.RWMutex
	functions map[string]Definition
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{functions: make(map[string]Definition)}
}

// Register adds a definition. Calling Register twice with the same name
// replaces the previous definition.
func (r *Registry) Register(def Definition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.functions[def.Name] = def
}

// Unregister removes a definition. Returns true if a function with
// that name was actually present.
func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.functions[name]; !ok {
		return false
	}
	delete(r.functions, name)
	return true
}

// Lookup returns the Definition for a name, or false if no function
// with that name is registered.
func (r *Registry) Lookup(name string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.functions[name]
	return d, ok
}

// Names returns the sorted set of registered function names. Useful
// for /admin endpoints listing available RPCs.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.functions))
	for n := range r.functions {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// List returns a snapshot of every registered Definition, sorted by
// name. Handlers are included in the snapshot, so callers can hand the
// result to documentation generators without re-querying.
func (r *Registry) List() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Definition, 0, len(r.functions))
	for _, d := range r.functions {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Count returns the number of registered functions.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.functions)
}

// ErrUnknownFunction is returned by the Invoker when no definition
// exists for the requested name. Surfaced to the wire as
// protocol.ErrUnknownFunction.
var ErrUnknownFunction = errors.New("rpc: unknown function")
