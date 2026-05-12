# Filters

Server-side row filtering for `postgres_changes` subscriptions.
Rejected rows never hit the wire, never count toward your rate limit,
never need to be re-filtered client-side.

The filter is a JSON tree the server compiles into a Go predicate
once per `subscribe` and evaluates against every event.

## Using the builder

```ts
import { q } from '@rapibase/client'

channel.onChange({
  event: 'INSERT',
  table: 'messages',
  filter: (q) => q.and(
    q.eq('room_id', 42),
    q.is('deleted_at', null),
    q.or(
      q.eq('author_id', currentUser.id),
      q.in('visibility', ['public', 'shared']),
    ),
  ),
}, handler)
```

## Leaf operators

Every leaf is `{ column, op, value }`. The builder produces the same
shape with shorter call sites.

| Op | Builder | SQL analogue | Notes |
|---|---|---|---|
| `eq` | `q.eq(col, v)` | `col = v` | Type-coerced (int ↔ float ↔ json.Number) |
| `neq` | `q.neq(col, v)` | `col <> v` | |
| `lt`, `lte`, `gt`, `gte` | `q.lt(col, v)` etc. | `<`, `<=`, `>`, `>=` | Numbers, strings, timestamps |
| `in` | `q.in(col, [v1, v2])` | `col IN (...)` | Array of any supported scalar |
| `nin` | `q.nin(col, [v1, v2])` | `col NOT IN (...)` | |
| `is` | `q.is(col, null)` | `col IS NULL` | Also `true`, `false` |
| `like` | `q.like(col, 'A%')` | `col LIKE 'A%'` | `%` and `_`; `\\%` for literal `%` |
| `ilike` | `q.ilike(col, '%foo%')` | `col ILIKE '%foo%'` | Case-insensitive |
| `contains` | `q.contains(col, val)` | `col @> val` | jsonb / array |
| `containedBy` | `q.containedBy(col, val)` | `col <@ val` | jsonb / array |
| `match` | `q.match(col, 'needle')` | (approx) | Case-insensitive substring; **not** full tsvector — see below |

## Composite operators

```ts
// AND
q.and(q.eq('a', 1), q.eq('b', 2))

// OR
q.or(q.eq('status', 'open'), q.eq('status', 'pending'))

// NOT (exactly one child)
q.not(q.is('deleted_at', null))
```

Nesting is unrestricted:

```ts
q.and(
  q.eq('tenant_id', tenantId),
  q.or(
    q.and(q.eq('role', 'admin'), q.eq('region', 'EU')),
    q.eq('role', 'super_admin'),
  ),
)
```

## Time comparisons

Pass either a `Date`, an RFC3339 string, or a number (Unix ms — the
server treats it as a number, which only matches numeric columns).

```ts
q.gte('created_at', new Date('2026-01-01'))    // server compares as time
q.gte('created_at', '2026-01-01T00:00:00Z')    // also OK
```

## JSONB filters

`contains` / `containedBy` implement Postgres jsonb `@>` / `<@`
semantics, recursively:

```ts
// metadata @> '{"owner": {"id": 7}}'
q.contains('metadata', { owner: { id: 7 } })

// metadata <@ '{"a": 1, "b": 2, "c": 3}'
q.containedBy('metadata', { a: 1, b: 2, c: 3 })
```

For arrays:

```ts
q.contains('tags', ['blue', 'sale'])           // tags contains both
q.contains('tags', 'blue')                     // tags contains "blue"
```

## What `match` actually does

It's **substring case-insensitive**, not a real tsvector match. Real
FTS would require duplicating Postgres's text search engine inside the
filter compiler — out of scope. Workarounds:

- **Coarse pre-filter with `match`**, exact match on the client: cheap.
- **Filter on a normalized column** the application maintains
  (`title_lower`) using `ilike` for predictable behavior.
- **Run a full-text query on the client side** after an event arrives,
  using the row as input to a `to_tsquery` you compute locally.

## Permission interaction

Filters reference columns. The server validates at subscribe time
that the caller's role can read every referenced column:

```ts
// If role 'anon' cannot read `secret_score`, this subscribe fails
// with code = forbidden_filter:
channel.onChange({
  event: '*',
  table: 'users',
  filter: (q) => q.gt('secret_score', 90),
}, handler)
```

That's not just sanity — it's a leak prevention. Without the check,
a client could enumerate denied column values by trying many filters.

If `set_auth` later downgrades the role and a filter now references a
denied column, the entire channel subscription is revoked and the SDK
fires `onResync('auth_changed')`. See [`auth.md`](./auth.md).

## Common pitfalls

- **`q.eq('count', '5')`** vs **`q.eq('count', 5)`**: the server is
  forgiving (string `"5"` matches int `5` numerically) but be explicit
  to avoid surprises in deserialised JSON.
- **`q.in('status', 'active')`**: typo — `in` requires an array.
  `q.in('status', ['active'])` is the right call.
- **Filtering on columns the table doesn't have**: no compile-time
  check; the server returns `invalid_filter` at subscribe.
- **`q.contains` with the wrong type**: jsonb `@>` semantics require
  both sides to be the same shape (both objects, or both arrays).
  Mixing yields `false` per filter eval, not an error — so it looks
  like nothing arrives.

## Raw JSON (if you're not using the SDK)

The filter is JSON over the wire. Builders are just sugar:

```json
{
  "op": "and",
  "conditions": [
    {"column": "room_id", "op": "eq", "value": 42},
    {"op": "or", "conditions": [
      {"column": "status", "op": "eq", "value": "pending"},
      {"column": "status", "op": "eq", "value": "open"}
    ]}
  ]
}
```

Send that as the `filter` field of any `PostgresChangesConfig`.
