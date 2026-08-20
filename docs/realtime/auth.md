# Auth

How rapibase realtime decides what a connected client is allowed to
do, and how the client can rotate credentials without dropping the
WebSocket.

## The two credentials

Every connection presents two values at handshake:

| Credential | Purpose | Where it goes |
|---|---|---|
| **API key** | Identifies the *project*. Always required. | Query: `?apikey=...` or header: `Authorization: Bearer <apikey>` |
| **JWT** | Identifies the *end user*. Optional — sets role and userID. | Query: `?token=...` |

Two valid API keys exist per project:

- **`ANON_KEY`** — what your frontend ships with. Treats unauthenticated
  callers as `role = "anon"`. If a JWT is present, role comes from
  `claims.role`.
- **`SERVICE_KEY`** — server-to-server only. Grants `role =
  "service_role"` regardless of JWT (the JWT is captured for `userId`
  but does not affect role).

Find them in the dashboard under Project Settings or in the binary's
startup logs.

## What the role controls

Realtime always passes the role to `PermissionChecker.CanRead` /
`ReadableColumns` before fanning out a `postgres_changes` event:

- A subscriber's filter can only reference columns the role can read
  (validated at subscribe time, rejected with `forbidden_filter` if not).
- A subscriber's projection (`columns: [...]`) is clipped to the
  readable subset at fan-out — denied columns simply disappear from
  the frame.

In the default rapibase build the `PermissiveChecker` lets every role
read every column: column-level permissions are not implemented yet.

Row-level security **is**, and it runs after this step — see below.

## Row-level security

Every `postgres_changes` event is checked per subscriber against the
table's RLS mode (the same `_rapibase_rls` configuration the REST API
uses, set from the dashboard or the `set_rls` MCP tool). The rule
mirrors the REST guard: **the anon key alone never reads table data —
it needs a user JWT.**

| Table's RLS mode | `anon` (no JWT) | Authenticated user | Service key |
|---|---|---|---|
| *not configured* | ✗ | ✓ | ✓ |
| `public` | ✓ | ✓ | ✓ |
| `authenticated` | ✗ | ✓ | ✓ |
| `owner` | ✗ | only rows where `owner_column = auth.uid()` | ✓ |
| `custom` | ✗ | ✗ | ✓ |

Notes:

- **A table you have not configured still needs a login.** It reaches
  authenticated subscribers — matching what REST does once RLS is off —
  but never anonymous ones. Creating a table does not publish it.
- **`public` is the opt-out for login-free feeds** (order tracking, live
  scoreboards). It is deliberate, not a default.
- **`custom` is dropped for every non-service subscriber.** Its policies
  are arbitrary SQL evaluated by Postgres on the REST path; realtime
  cannot run them, and guessing would leak the table. If you need a
  custom-policy table live, expose it through a `public`/`owner` view or
  push the change yourself with `broadcast` from a function.
- `owner` tables are switched to `REPLICA IDENTITY FULL` automatically so
  DELETE events carry the owner column. Without it Postgres only ships
  the primary key on a delete and the event could not be scoped.
- The mode snapshot refreshes every 30s, so an RLS change takes up to
  half a minute to reach live subscribers.

## Token rotation: `setAuth`

JWTs expire. A long-lived WS connection needs a way to refresh
without reconnecting:

```ts
await rt.setAuth(newJWT)
```

Server-side, this:

1. Validates the new JWT against the connection's apikey.
2. Updates the session's role + userID atomically.
3. Walks every channel the subscriber holds and re-validates filter
   columns against the new role.
4. **Detaches** the subscriber from any channel that now exceeds
   permissions (filter references a denied column, or projection
   includes one).
5. Responds with an `ack` containing `detail.lost_channels`.

The SDK fires `channel.onResync('auth_changed')` on each affected
channel so the app can invalidate caches and resubscribe with a
permitted filter.

## What happens if the new token is bad

If `setAuth` is called with an invalid JWT, **the session keeps its
previous role**. The server returns `unauthorized` and the SDK throws
`RealtimeError`:

```ts
try {
  await rt.setAuth(maybeExpiredJWT)
} catch (e) {
  if (e instanceof RealtimeError && e.code === 'unauthorized') {
    redirectToLogin()
  }
}
```

This is intentional — a transient client bug shouldn't drop a
working session.

## Service key + JWT

When connecting with the service key, the JWT is **optional**:

- No JWT: role = `service_role`, userID = `""`.
- Valid JWT: role = `service_role` (unchanged), userID = `claims.sub`.

This lets a backend service connect once and act on behalf of
multiple users sequentially:

```ts
const rt = createRealtimeClient({
  url: 'https://rapibase.example.com',
  apiKey: SERVICE_KEY,
})

// Later, for each user being served:
await rt.setAuth(userJWT)
// All subsequent invokes log this user in handler.UserIDFromContext.
```

## Endpoint-level rejection

If the handshake fails (invalid apikey, missing subprotocol header,
invalid JWT under anon key), the server returns HTTP 401 / 400
**before** the WS upgrade — the client never sees a welcome frame.
The SDK fires `onError` and stops reconnecting (these are non-retryable
errors).

## Common pitfalls

- **Frontend embedding `SERVICE_KEY`**: never. The service key
  bypasses every per-user check. Treat it like a database password
  and keep it server-side.
- **Forgetting to call `setAuth` after login**: the connection is
  still anonymous until you do. New filters / actions will be denied
  with the old role.
- **JWT lifetime shorter than session**: set `JWT_EXPIRY` in your
  rapibase config to cover typical session durations (default 1h
  works for chat-like apps; bump to 4h-12h for SaaS dashboards).

## Wire details

The handshake reads, in order:

1. `Sec-WebSocket-Protocol` for codec negotiation
   (`rapibase-realtime.v1+msgpack` preferred, JSON fallback).
2. `apikey` query param (or `Authorization: Bearer`).
3. `token` query param (optional).

Wire-level details for custom client implementations live in
[`protocol.md`](./protocol.md).
