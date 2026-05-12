# Postgres Changes

Subscribe to live database events: every INSERT, UPDATE, DELETE or
TRUNCATE on a table arrives at your client within milliseconds of the
commit. Powered by Postgres logical replication; no triggers required.

## Minimum example

```ts
rt.channel('messages-feed')
  .onChange(
    { event: 'INSERT', table: 'messages' },
    (payload) => console.log(payload.new),
  )
  .subscribe()
```

## The `onChange` config

```ts
{
  event:   'INSERT' | 'UPDATE' | 'DELETE' | '*',
  schema?: string,             // defaults to any schema
  table:   string,             // required for non-wildcard subs
  columns?: string[],          // projection — receive only these
  filter?: (q) => FilterNode,  // server-side row filtering
}
```

- **`event: '*'`** delivers every change type on the table.
- **`schema` omitted** matches any schema. In a single-schema app
  (the common case), you can leave it out.
- **`columns` omitted** delivers every column the caller's role can
  read.

## The payload your callback receives

```ts
{
  event:    'INSERT' | 'UPDATE' | 'DELETE' | 'TRUNCATE',
  schema:   string,
  table:    string,
  lsn:      string,         // monotonic per channel; persist for resume
  commitTs: Date,
  columns:  string[],       // the columns actually delivered
  new:      Row | null,     // post-image (INSERT, UPDATE)
  old:      Row | null,     // pre-image  (UPDATE replica-identity, DELETE)
}
```

`Row` is a plain `Record<string, unknown>` typed against your schema
when you use the codegen-generated `Database` type (see
[`sdk.md`](./sdk.md)).

## Field semantics

| Event    | `new` | `old` |
|----------|-------|-------|
| INSERT   | full row | `null` |
| UPDATE   | full row | only replica-identity columns (PK by default) |
| DELETE   | `null` | replica-identity columns |
| TRUNCATE | `null` | `null` |

If you need the full pre-image on UPDATE, run `ALTER TABLE X REPLICA
IDENTITY FULL` once. The cost is bigger WAL records — acceptable for
small tables, expensive for write-heavy ones.

## Filtering

Server-side. Rapibase compiles a typed filter tree into a predicate
that runs **before** fan-out, so unwanted rows never hit the wire:

```ts
import { q } from '@rapibase/client'

channel.onChange({
  event: 'UPDATE',
  table: 'orders',
  filter: (q) => q.and(
    q.eq('status', 'shipped'),
    q.gte('total', 100),
  ),
}, handler)
```

Full operator reference: [`filters.md`](./filters.md).

## Projection (`columns`)

When you only need a few columns, pass them explicitly. Saves bytes
on the wire and prevents accidentally subscribing to columns your role
can't read (which would emit a `forbidden_filter` error at subscribe
time).

```ts
.onChange({
  event: '*',
  table: 'orders',
  columns: ['id', 'status'],  // only these two
}, handler)
```

The server **always** enforces column-level permissions. If your role
loses access to a column after `set_auth`, the SDK fires
`onResync('auth_changed')` and you re-fetch the affected data.

## Multiple subscriptions on one channel

A channel can hold many `onChange` configs at once:

```ts
rt.channel('orders-feed')
  .onChange({ event: 'INSERT', table: 'orders' }, onCreate)
  .onChange({ event: 'UPDATE', table: 'orders' }, onUpdate)
  .onChange({ event: '*', table: 'order_items' }, onItemChange)
  .subscribe()
```

All three streams flow over the same WebSocket frame envelope —
nothing duplicates.

## Resume after disconnect

Every `postgres_changes` frame carries an `lsn`. The SDK persists the
last seen LSN per channel in `sessionStorage` and, on reconnect, asks
the server to replay events that came after it:

```
client: { type: 'resume', channel: 'orders-feed', from_lsn: '0/27B1A68' }
server: <replays missed events> + { type: 'ack' }
```

This is automatic — you don't write code for it. If the requested LSN
fell out of the server's resume buffer (default 4096 events per
channel, ~30s on a busy app), the SDK emits
`onResync('slot_truncated')` and you should invalidate any local
cache because gaps are possible.

## What you cannot do (yet)

- **Joins across tables**: subscribe to each table separately and
  reconstruct on the client. Cross-table aggregation is on the
  roadmap; for now, a single `onChange` is one table.
- **Aggregates** (`COUNT`, `SUM`): not exposed via realtime. Run them
  via a `select` or wrap them in an RPC ([`rpc.md`](./rpc.md)).
- **DDL events** (CREATE TABLE, ALTER, ...): not streamed. Realtime
  publishes only DML on tables in the configured publication.

## Common pitfalls

- **`wal_level` not `logical`**: bootstrap fails with a clear log. The
  default `docker-compose.yaml` already sets it; if you're on a
  managed Postgres, enable it in your provider's settings panel.
- **Role lacks `REPLICATION`**: rapibase tries to self-grant on
  startup but fails silently if the role can't `ALTER`. Connect as
  superuser once: `ALTER ROLE my_user REPLICATION;`.
- **`REPLICA IDENTITY` is `NOTHING`**: UPDATE/DELETE events arrive
  with empty `old`. Default is `DEFAULT` (= PK), which is enough for
  most use cases.

See [`troubleshooting.md`](./troubleshooting.md) for the full error
catalog.
