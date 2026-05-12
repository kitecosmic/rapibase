# Rapibase Realtime — Index

> Entry point for both human readers and LLM-assisted tools. Start
> here, jump to the file you need.

Rapibase Realtime is the **WebSocket layer** of rapibase. One persistent
connection lets your app:

- **Stream database changes** in real time (INSERT / UPDATE / DELETE /
  TRUNCATE on any table) with row-level filtering.
- **Broadcast ephemeral messages** between connected clients
  (typing indicators, cursors, signals).
- **Track presence** — who is currently in a channel and what their
  state is. Multi-tab / multi-device first class.
- **Invoke server-side RPC functions** over the same connection (no
  separate HTTP needed).

Everything runs on a **single Go binary** with **one Postgres
connection** (logical replication). No separate realtime service to
operate.

---

## Capability map

| I want to... | Read |
|---|---|
| Connect for the first time | [`quickstart.md`](./quickstart.md) |
| Subscribe to DB changes | [`postgres-changes.md`](./postgres-changes.md) |
| Send messages between clients | [`broadcast.md`](./broadcast.md) |
| Track who's online and their cursor / status | [`presence.md`](./presence.md) |
| Call a server function over the same socket | [`rpc.md`](./rpc.md) |
| Filter events server-side (eq / in / like / contains / …) | [`filters.md`](./filters.md) |
| Understand apikey + JWT + token rotation | [`auth.md`](./auth.md) |
| Diagnose an error or close code | [`troubleshooting.md`](./troubleshooting.md) |
| Implement a client in another language | [`protocol.md`](./protocol.md) (wire spec) |
| Read the full SDK reference | [`sdk.md`](./sdk.md) |

---

## Mental model in 30 seconds

```
┌──────────┐ INSERT  ┌──────────┐  WAL  ┌──────────┐  WS  ┌──────────┐
│ Postgres │────────▶│  Replic. │──────▶│   Hub    │─────▶│  Client  │
└──────────┘         └──────────┘       └──────────┘      └──────────┘
                                          │   ▲
                                          ▼   │
                                       (filter,
                                       permis-
                                       sions,
                                       projection)
```

1. Your app writes to Postgres (REST, SQL, anything).
2. Postgres emits the change to a logical replication slot.
3. Rapibase's `Replicator` decodes it via `pgoutput`.
4. The `Hub` looks up which channels declared interest in that
   `(schema, table)` and fans the event out, applying filters and
   role-based column projection per subscriber.
5. The subscriber's WebSocket receives a `postgres_changes` frame.

Everything is **bidirectional on the same socket** — clients can also
send `broadcast`, `presence_track`, `rpc`, and rotate auth with
`set_auth`.

---

## Endpoint at a glance

```
URL:           wss://<your-host>/api/realtime/v1
Subprotocols:  rapibase-realtime.v1+msgpack  (preferred)
               rapibase-realtime.v1+json     (fallback)
Auth:          ?apikey=<anon_or_service_key>&token=<jwt>
               or Authorization: Bearer <apikey>
```

The JS / TS SDK handles all of this transparently — start at
[`quickstart.md`](./quickstart.md) if that's your target.

For custom clients (Swift, Kotlin, Rust, anything else), the wire
spec is in [`protocol.md`](./protocol.md). Frames are JSON or
MessagePack envelopes with a `type` field.

---

## Conventions you'll see in every doc

- Code blocks show **client TS** by default. Server-side Go appears
  with a `[server]` comment.
- "Channel" is an opaque string the client picks (`"room:42"`,
  `"orders"`, anything). Multiple clients sharing the same channel
  name see each other's broadcast / presence.
- "Filter" always means the structured JSON tree, not a SQL string.
- LSN values are opaque strings (`"0/27B1A68"`) — never parse them
  client-side.

---

## What this doc is NOT

- Not a Postgres tutorial — it assumes you have rapibase running and
  a table you can write to.
- Not a deployment guide — see the main `README.md` for `docker
  compose up`.
- Not a copy of Supabase Realtime — APIs are intentionally similar
  enough to be familiar, but the implementation, scale targets, and
  feature roadmap diverge.
