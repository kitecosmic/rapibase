# RPC — Server-side functions over the same socket

Call a function registered on the server. Unlike HTTP, the response
arrives on the same WebSocket — no extra TCP handshake, no separate
auth, no separate keep-alive. Ideal for high-frequency operations
(agent tool calls, sliding window queries, server-validated actions).

## Registering a function (server-side, Go)

```go
import "github.com/rapibase/rapibase/internal/realtime/rpc"

reg := rpc.NewRegistry()
reg.Register(rpc.Definition{
    Name: "send_notification",
    AllowedRoles: []string{"authenticated", "service_role"},
    RatePerSec: 30,  // per-connection cap; 0 = use transport default
    Handler: func(ctx context.Context, args any) (any, error) {
        a := args.(map[string]any)
        // ...do work, returning result or error...
        return map[string]any{"delivered": 1}, nil
    },
})

// Then pass reg to realtime.Config.RPC at service construction.
```

The registry is process-wide; register all functions at startup,
before `Service.Run`.

## Invoking from a client

```ts
const result = await channel.invoke<
  { userId: number, title: string },   // args type
  { delivered: number }                // result type
>('send_notification', {
  userId: 7,
  title: 'hola',
})

console.log(result.delivered)  // 1
```

The `<Args, Result>` generics are optional — they only add type safety
on the call site. With codegen (see [`sdk.md`](./sdk.md)) the types
come from a generated registry file.

## Channel-scoped vs global invokes

```ts
// Scoped to a channel — useful when the function semantically belongs
// to that channel (e.g. "ban_user" inside a chat room):
await rt.channel('room:42').invoke('ban_user', { userId: 5 })

// Global — for functions that don't relate to a specific channel:
await rt.realtime.invoke('list_active_users', {})
```

Both arrive at the same registry; the channel scope is just metadata
the handler can read from context if it cares.

## What the handler receives

Inside the handler, the caller's identity is in the context:

```go
import "github.com/rapibase/rapibase/internal/realtime/rpc"

handler := func(ctx context.Context, args any) (any, error) {
    role   := rpc.RoleFromContext(ctx)    // "authenticated", "service_role", ...
    userID := rpc.UserIDFromContext(ctx)  // JWT subject, or "" for anonymous
    // role and userID come from the validated handshake.
    // The channel name (if any) is on the inbound frame the
    // transport saw; the handler does not see it directly today.
    // ...
}
```

## Errors

If the handler returns an error, the caller receives:

```json
{
  "type":    "error",
  "ref":     "<the request ref>",
  "code":    "internal",
  "message": "<your error string>"
}
```

The SDK translates this into a `RealtimeError` thrown from `invoke()`:

```ts
try {
  await channel.invoke('send_notification', { ... })
} catch (e) {
  if (e instanceof RealtimeError && e.code === 'unauthorized') {
    // role doesn't match Definition.AllowedRoles
  }
}
```

Closed error codes the invoker emits:

| Code | When |
|---|---|
| `unknown_function` | name not in the registry |
| `unauthorized` | caller's role not in `AllowedRoles` |
| `rate_limited` | per-connection rate exceeded |
| `internal` | handler returned a non-typed error, panicked, or timed out |

## Timeouts

Set per call from the client:

```ts
await channel.invoke('slow_op', args, { timeoutMs: 30_000 })
```

If unset, the server's `Invoker.defaultTimeout` applies (5 seconds).
Handlers should respect `ctx.Done()`:

```go
Handler: func(ctx context.Context, args any) (any, error) {
    select {
    case <-time.After(2 * time.Second):
        return "ok", nil
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}
```

Server-side panics in handlers are caught and converted to error
responses — they never tear down the transport loop.

## Streaming (planned)

The single-response model above covers ~95% of cases. For LLM token
streams or progressive results, a planned **streaming RPC** mode will
let a handler emit multiple frames with the same `ref` before a final
response. Not available yet.

## When to use RPC vs HTTP REST

| Choose RPC when | Choose REST when |
|---|---|
| The caller already has a realtime connection open | First-time stateless call from an unauthenticated client |
| Latency matters more than discoverability | You need a public, browseable API surface |
| You want one auth path for everything | You want HTTP caching / CDN edges |
| The operation is short (< 5s) | The operation is long-running (uploads, batch jobs) |

For a typical app, **both coexist**: REST for crud + auth onboarding,
realtime + RPC for the live UX.
