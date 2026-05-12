# Quickstart

From zero to receiving a `postgres_changes` event in your browser in
under 5 minutes.

## Prerequisites

- Rapibase running and reachable (locally or on a VPS).
- Your project's **anon key** (visible in the dashboard under
  Project Settings, or in the binary's startup logs as `ANON_KEY`).
- At least one table in your `public` schema.

## 1. Install the SDK

The SDK ships inside rapibase's web client today. For your own apps,
import it the same way the dashboard does — it lives under
`@rapibase/client` (when published to npm) or directly from the
rapibase repo while you self-host:

```ts
import { createRealtimeClient } from '@rapibase/client'
```

Until the npm package lands, copy `web/src/lib/realtime/` into your
project — it's framework-agnostic and has zero runtime dependencies
beyond the standard browser globals.

## 2. Open a connection

```ts
const rt = createRealtimeClient({
  url: 'https://your-rapibase-host.com',  // http or https; SDK upgrades to ws/wss
  apiKey: 'your-anon-key',
})
```

That's it. The socket opens lazily on the first `channel().subscribe()`.

## 3. Subscribe to a channel

```ts
const room = rt
  .channel('room:42')
  .onChange(
    { event: 'INSERT', table: 'messages' },
    ({ new: msg }) => console.log('new message:', msg),
  )

await room.subscribe()
```

`channel(name)` is idempotent — calling it twice returns the same
instance, so two parts of your app can attach handlers to the same
channel without duplicating connections.

## 4. Try it

Insert a row from `psql`, the dashboard's SQL editor, or your REST API:

```sql
INSERT INTO messages (room_id, text) VALUES (42, 'hola');
```

Your callback fires:

```
new message: { id: 901, room_id: 42, text: 'hola', created_at: '...' }
```

## 5. Clean up

```ts
await room.unsubscribe()
rt.close()
```

`close()` is optional in browsers — the socket closes automatically
when the page unloads. Call it explicitly only when you want to tear
down the connection mid-session (logout, route change to a non-realtime
view).

---

## Where to go next

- Filter by row / column → [`postgres-changes.md`](./postgres-changes.md)
- Broadcast non-DB messages → [`broadcast.md`](./broadcast.md)
- Show "X is typing" / cursors → [`presence.md`](./presence.md)
- Call server-side functions → [`rpc.md`](./rpc.md)
- Authenticate as a real user (JWT) → [`auth.md`](./auth.md)

If something doesn't work, [`troubleshooting.md`](./troubleshooting.md)
has the closed list of error codes and what each one means.
