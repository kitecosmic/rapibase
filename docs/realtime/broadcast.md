# Broadcast

Ephemeral pub/sub between clients connected to the same channel. Use
it for messages that **must not be persisted**: typing indicators,
cursor positions, ephemeral signals, intra-team coordination.

If you find yourself wanting broadcast to be reliable across
disconnects, use [`postgres-changes.md`](./postgres-changes.md) on a
real table instead.

## Listening

```ts
rt.channel('room:42')
  .onBroadcast<{ userId: number }>('typing', ({ payload, from }) => {
    console.log(`user ${payload.userId} is typing (from ${from?.userId})`)
  })
  .subscribe()
```

## Sending

```ts
await channel.broadcast('typing', { userId: 7 })

// With server-confirmed ack (more latency, rarely useful):
await channel.broadcast('typing', { userId: 7 }, { ack: true })
```

`broadcast()` returns a Promise that resolves when the frame is
written to the socket. With `{ ack: true }`, it resolves only after
the server confirms fan-out completed.

## Self-delivery

By default, **the sender does not receive its own broadcast**. To opt
into self-delivery (useful for state machines where the same code path
handles both local and remote actions):

```ts
.subscribe({ broadcast: { self: true } })
```

After that, every broadcast you send is also delivered back to your
own `onBroadcast` listener.

## The `from` field

Receivers see who sent the broadcast:

```ts
{
  event:   'typing',
  payload: { userId: 7 },
  from: {
    session_id: 'abc123',   // opaque, unique per WS connection
    user_id:    '7',        // present only for authenticated senders
  },
}
```

`from.user_id` is the **server-validated** subject of the sender's
JWT. Never trust `payload.userId` — clients can put anything in
payload; only `from.user_id` is enforced by the server.

## Rate limits

Per connection, broadcast is capped by the transport's rate limiter
(default: 100 frames/s sustained, 200 burst). Excess frames receive:

```json
{
  "type": "error",
  "code": "rate_limited",
  "retryable": true,
  "retry_after_ms": 23
}
```

For very chatty UIs (cursor tracking at 60fps), consider debouncing
to 20-30 Hz client-side. The server will throttle you anyway, but
debouncing keeps your CPU happier.

## Multi-channel routing

A single client subscribed to several channels keeps each one's
broadcasts separate:

```ts
rt.channel('room:42').onBroadcast('typing', forRoom42)
rt.channel('room:99').onBroadcast('typing', forRoom99)
```

The router uses the `channel` field of every inbound frame; there's
no cross-channel leakage.

## What broadcast is NOT

- **Not persisted**: a subscriber who joins after a broadcast was
  sent will never see it. There is no history.
- **Not ordered across senders**: two clients broadcasting
  simultaneously may arrive in different orders for different
  receivers. Within a single sender, order is preserved.
- **Not reliable**: if a receiver's queue is full, the broadcast is
  dropped for that receiver (the connection is also evicted, see
  `troubleshooting.md`). Senders never know.

For ordered + persisted + reliable, write to a DB table and use
postgres_changes.

## Common pattern: "typing" indicator

```ts
let lastSent = 0
input.addEventListener('input', () => {
  const now = Date.now()
  if (now - lastSent < 500) return        // throttle to 2 Hz
  lastSent = now
  channel.broadcast('typing', { userId: me.id })
})

// Server side: nothing to do — broadcasts are stateless.
```

## Common pattern: cursor sync at 30 Hz

```ts
let raf = 0
canvas.addEventListener('mousemove', (e) => {
  cancelAnimationFrame(raf)
  raf = requestAnimationFrame(() => {
    channel.broadcast('cursor', { x: e.clientX, y: e.clientY })
  })
})

channel.onBroadcast<{ x: number, y: number }>('cursor', ({ payload, from }) => {
  drawCursor(from!.user_id!, payload.x, payload.y)
})
```

For 60+ Hz / sub-millisecond requirements, broadcast is fine but
consider whether your users actually perceive the difference. 30 Hz
feels smooth and stays under the rate limit comfortably.
