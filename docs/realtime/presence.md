# Presence

Live awareness of who is connected to a channel and what their state
is. Powers "online" indicators, viewer lists in collaborative
editors, "you and 3 others viewing this", multi-cursor displays.

## Subscribing

```ts
type PresenceState = {
  status: 'online' | 'away'
  cursor?: { x: number, y: number }
}

const room = rt.channel('doc:42')
  .onPresence<PresenceState>('sync',   ({ members }) => render(members))
  .onPresence<PresenceState>('join',   ({ joins })   => log('joined', joins))
  .onPresence<PresenceState>('leave',  ({ leaves })  => log('left', leaves))
  .onPresence<PresenceState>('update', ({ updates }) => render(updates))

await room.subscribe({ presence: { key: currentUser.id.toString() } })
```

Right after `subscribe()` you receive a `sync` event with the full
current state. Subsequent changes arrive as `join` / `leave` /
`update` diffs.

## Tracking yourself

```ts
await room.track({ status: 'online' })
// later
await room.track({ status: 'away', cursor: { x: 120, y: 480 } })
// on logout
await room.untrack()
```

Each `track()` call **replaces** your state (LWW semantics). You can
call it as often as needed; the server debounces nothing internally
but the rate limiter caps you at 10/s by default — enough for
status changes, not enough for 60-fps cursor updates (use
[`broadcast.md`](./broadcast.md) for those).

## The `key` parameter

Identifies a "member". Every entry under the same key belongs to the
same logical user. You decide what goes in: typically the JWT user id
(so logging in from two tabs shows up as the same member).

```ts
.subscribe({ presence: { key: jwtClaims.sub } })
```

If omitted, the server uses the session id, which means every tab
appears as a separate member. That's wrong for "online users" but
intentional for some use cases (kanban cursor per browser tab).

## Multi-tab / multi-device

A single `key` can have multiple `PresenceEntry` values. Each entry
represents one active session of that member.

```ts
{
  "user_7": [
    { ref: "sess_aaa", joined_at: "2026-05-12T10:00:00Z", state: { status: "online" } },
    { ref: "sess_bbb", joined_at: "2026-05-12T11:30:00Z", state: { status: "away" } },
  ],
  "user_8": [
    { ref: "sess_ccc", joined_at: "2026-05-12T12:00:00Z", state: { status: "online" } },
  ],
}
```

Closing one tab removes one entry, not the whole member. The user
disappears from the list only when their last entry leaves.

## The diff event shapes

```ts
type PresenceDiff<S> = {
  joins:   Record<string, PresenceEntry<S>[]>
  leaves:  Record<string, PresenceEntry<S>[]>
  updates: Record<string, PresenceEntry<S>[]>
}
```

Listen for `'diff'` if you want all three at once:

```ts
room.onPresence<PresenceState>('diff', ({ joins, leaves, updates }) => {
  for (const [key, entries] of Object.entries(joins))   addMember(key, entries)
  for (const [key, entries] of Object.entries(leaves))  removeMember(key, entries)
  for (const [key, entries] of Object.entries(updates)) updateMember(key, entries)
})
```

## When state is preserved across reconnect

It isn't — by design. Presence is **ephemeral**. When your client
reconnects after a network blip:

1. The SDK resubscribes the channel.
2. If you had a `track()` state, the SDK calls it again automatically.
3. Other members see a `leave` + `join` for you. They cannot tell
   reconnection apart from a brand-new session.

If "user looked away for 5 seconds" is not what you want to render,
debounce your leave handlers (wait 10s before removing a member from
the UI; if they reappear in that window, treat it as continuous).

## Common pattern: viewer list

```tsx
function ViewerList({ channel }: { channel: Channel }) {
  const [members, setMembers] = useState<Record<string, PresenceEntry[]>>({})

  useEffect(() => {
    channel
      .onPresence('sync',   ({ members }) => setMembers(members))
      .onPresence('diff',   ({ joins, leaves }) => {
        setMembers((prev) => {
          const next = { ...prev }
          for (const [k, v] of Object.entries(joins))  next[k] = v
          for (const k of Object.keys(leaves))         delete next[k]
          return next
        })
      })
    channel.subscribe({ presence: { key: me.id } })
    channel.track({ status: 'online' })
    return () => { channel.untrack(); channel.unsubscribe() }
  }, [channel])

  return <ul>{Object.keys(members).map(id => <li key={id}>{id}</li>)}</ul>
}
```

## What presence is NOT

- **Not authoritative for billing or auditing** — it reflects who's
  *currently connected*, not who *logged in this hour*. Use auth logs
  for that.
- **Not durable**: server restart wipes presence (it lives in
  process memory). After a restart, members reconnect and re-track.
- **Not transactional**: there's no "presence_track + DB insert
  atomically" — use RPC ([`rpc.md`](./rpc.md)) if you need both.

## See also

- [`broadcast.md`](./broadcast.md) — for cursor-rate or sub-second
  updates that are too chatty for presence's LWW semantics.
- [`auth.md`](./auth.md) — how `set_auth` interacts with presence
  (presence is **not** automatically revoked on role change; you
  decide whether to drop members manually).
