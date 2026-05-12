# Troubleshooting

Closed list of every error and close code the realtime layer emits,
with the typical cause and the fix.

## Server logs at startup

| Log line | Means | Action |
|---|---|---|
| `✅ Realtime: bootstrap OK (slot=… publication=…)` | All clear | None |
| `⚠️ Realtime: bootstrap failed: ... wal_level is "replica"` | Postgres is not configured for logical replication | Set `wal_level=logical` in `postgresql.conf` (the bundled `docker-compose.yaml` already does this). Restart Postgres. |
| `⚠️ Could not grant REPLICATION to current role` | Connecting role lacks REPLICATION attribute and cannot self-grant | Connect as superuser once: `ALTER ROLE my_user REPLICATION;` |
| `⚠️ Realtime: setup failed, endpoint disabled` | Service construction failed (config bug) | Read the wrapped error; usually a missing config field |
| `⚠️ Realtime: service exited with error: ...` | The replicator died and could not recover | Read the wrapped error; common causes: slot was dropped externally, network split |

If the WebSocket endpoint is mounted but Bootstrap failed, broadcast /
presence / RPC still work — only `postgres_changes` is unavailable.

## Application-level error frames

Every error frame the client receives has this shape:

```json
{
  "type":           "error",
  "ref":            "<the request ref, if any>",
  "code":           "<one of below>",
  "message":        "human readable",
  "retryable":      true | false,
  "retry_after_ms": 23
}
```

### `unauthorized`

- **subscribe-time**: JWT was rejected at handshake or `setAuth` was
  called with a bad token. The SDK throws `RealtimeError` from
  `setAuth()`. Action: log the user back in.
- **rpc-time**: the caller's role is not in `Definition.AllowedRoles`.
  Action: gate the UI on the role; the call should not be reachable.

### `forbidden_filter`

A subscribe / resume referenced columns the role cannot read. The
filter or projection mentions a column denied by the permission
checker. Action: drop the column from the filter, or call `setAuth`
with a higher-privilege JWT first.

### `unknown_channel`

`resume` or `unsubscribe` targeted a channel the client never
attached to. Usually a bug in client state tracking. Action: only
call those methods on channels you previously `subscribe`'d.

### `unknown_function`

`rpc` invoked a function name not in the registry. Action: confirm
the operator registered it before starting `Service.Run`.

### `invalid_filter`

Filter JSON has a structural problem: unknown operator, missing
`column`, conditions on a leaf, etc. Action: validate the filter
tree against the operator list in [`filters.md`](./filters.md).

### `invalid_payload`

Frame failed to parse, missing `type`, or used a frame type the
server does not recognize. Action: confirm the SDK version matches
the server's protocol version.

### `slot_truncated`

A `resume` from an LSN older than the server's retention window. The
client missed events while disconnected too long. Action: invalidate
local cache for the affected channel and resubscribe from scratch
— the SDK does this automatically and fires
`onResync('slot_truncated')`.

### `rate_limited`

Per-connection rate limit hit on this frame type or RPC function.
The frame's `retry_after_ms` tells the SDK exactly how long to wait
before retrying. Action: throttle the calling code; cursor sync at
60 Hz almost certainly trips this — use 20-30 Hz instead.

### `quota_exceeded`

Hard project-wide cap hit. Action: contact your operator; not
self-recoverable.

### `internal`

Catch-all for unexpected server-side errors (handler panicked, sink
returned an error, etc.). Action: check server logs.

## WebSocket close codes

When the server hangs up, the close code says why. The SDK exposes
these via `client.onStateChange('closed')` plus the underlying close
event.

| Code | Meaning | Retry? |
|---|---|---|
| `1000` | Normal close (you called `client.close()`) | No |
| `1001` | Going away (browser navigation, etc.) | No |
| `4400` | Unsupported subprotocol version | No — upgrade the client to match server |
| `4401` | Handshake auth invalid (bad apikey or JWT at connect time) | No — fix credentials |
| `4408` | Heartbeat timeout — the client did not respond to a ping for 2× the heartbeat interval | Yes — reconnect immediately |
| `4413` | Frame exceeded `max_payload_bytes` (1 MiB default) | No — chunk the data |
| `4429` | Slow consumer evicted — outbound queue stayed full too long | Yes, but consider why — your client cannot keep up |
| `4500` | Internal server error | Yes — backoff |
| `4503` | Server is shutting down for ordered restart | Yes — backoff |

## Symptoms → causes

### "I subscribed but nothing arrives"

1. **Bootstrap failed?** Check server logs for `✅ Realtime:
   bootstrap OK`. No log line ⇒ no replicator running ⇒ no
   `postgres_changes`. Broadcast / presence / RPC are unaffected.
2. **Wrong table or schema?** The subscribe is case-sensitive.
   Check `pg_publication_tables` includes your table.
3. **Filter is too restrictive?** Drop the filter and verify events
   flow; then add it back piece by piece.
4. **Role can't read any column?** With `columns` omitted, the
   server delivers nothing if no columns are readable. Try an
   explicit `columns: ['id']` to confirm.
5. **The table is not in the publication.** Default publication is
   `FOR ALL TABLES`; if you customised it, add the table.

### "Server sent no subprotocol"

You're using a custom WS client that didn't offer
`rapibase-realtime.v1+json` (or `+msgpack`) in `Sec-WebSocket-Protocol`.
Most libraries take the subprotocol as a constructor arg.

### "WebSocket handshake fails behind nginx/Caddy"

The reverse proxy isn't forwarding upgrade headers. Add:

```nginx
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection "upgrade";
proxy_read_timeout 7d;     # long enough for idle WS
```

For Caddy:

```caddyfile
reverse_proxy localhost:8080 {
    transport http {
        keepalive 30s
    }
}
```

(Caddy auto-handles WS upgrade with the default `reverse_proxy`
directive — no extra config needed.)

### "Tests are flaky in CI"

Integration tests need Postgres up before `go test` runs. Use
`docker compose up -d --wait` (or `up -d` + `sleep 5`).

## Where to look when nothing matches

- **Server logs**: `docker compose logs -f rapibase`. Errors are
  tagged with `⚠️` / `❌`; the warning-only kind (`Warning: ...`) are
  not fatal and the rest of the API keeps working.
- **Postgres state**:
  ```bash
  docker compose exec db psql -U rapibase -d rapibase \
    -c "SELECT * FROM pg_stat_replication;"
  ```
  The slot should appear with state `streaming`.
- **Open a wscat session** with the JSON codec and inspect the raw
  frames. Often the bug becomes obvious in the wire output.
