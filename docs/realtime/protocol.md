# Rapibase Realtime — Protocolo WebSocket

**Versión:** `rapibase-realtime.v1`
**Estado:** especificación de referencia — fuente de verdad para implementaciones del servidor y de cualquier SDK cliente.

Este documento define el contrato de comunicación entre los clientes y el servicio Realtime de rapibase. Servidor y SDKs implementan **contra este documento**, no contra la otra implementación. Cambios al protocolo requieren bump de versión (`v2`, `v3`, …) y soporte simultáneo de la versión anterior por al menos un ciclo de release.

---

## 1. Objetivos de diseño

1. **Una sola conexión, todo encima**: subscripciones a cambios de DB, broadcast efímero, presence y RPC bidireccional comparten un único WebSocket. Esto reduce sockets, handshakes TLS y consumo de batería en clientes móviles.
2. **At-least-once con resume**: cada evento de DB lleva un LSN. Tras reconexión, el cliente puede pedir catchup desde el último LSN visto y no perder eventos dentro de la ventana de retención.
3. **Order-preserving por canal**: los eventos llegan en el mismo orden que se commitearon en Postgres.
4. **Codificación binaria por defecto**: MessagePack para reducir bytes y CPU en parse; JSON disponible para debug y compatibilidad.
5. **Versionado explícito**: el subprotocolo WS lleva la versión, lo que permite romper compatibilidad sin tumbar clientes desplegados.
6. **Backpressure visible**: el cliente sabe cuando se está retrasando y puede actuar, en vez de simplemente recibir mensajes obsoletos.
7. **Auth dinámica**: el JWT se puede rotar sin cerrar la conexión.
8. **Errores cerrados y tipados**: códigos enumerados, no cadenas libres.

---

## 2. Conexión y handshake

### 2.1 URL

```
wss://<host>/api/realtime/v1
```

La versión del protocolo va en la **ruta**, no en query params. Esto permite enrutar a binarios diferentes en el futuro sin afectar el balanceo.

### 2.2 Subprotocolo

El cliente **debe** enviar el header WebSocket:

```
Sec-WebSocket-Protocol: rapibase-realtime.v1+msgpack
```

o, para clientes que no pueden usar binario:

```
Sec-WebSocket-Protocol: rapibase-realtime.v1+json
```

El servidor responde con el mismo subprotocolo elegido. Si el cliente ofrece varios, el servidor prefiere `msgpack`. Si el cliente ofrece una versión no soportada, el servidor cierra con código `4400` antes del upgrade.

### 2.3 Autenticación inicial

El JWT se envía en el query string del handshake:

```
wss://<host>/api/realtime/v1?apikey=<anon_or_service_key>&token=<jwt>
```

- `apikey` es **obligatorio** y identifica el proyecto/tenant (igual que el resto de la API).
- `token` es **opcional**. Sin token, la conexión opera con permisos de rol anónimo (definidos por el `apikey`).

El servidor valida ambos antes de aceptar el upgrade. Si fallan, responde HTTP `401` antes del upgrade WebSocket — no se establece conexión.

### 2.4 Frame inicial del servidor

Inmediatamente después del upgrade, el servidor envía un frame `welcome`:

```jsonc
{
  "type": "welcome",
  "session_id": "5f3c8a2e-...",
  "server_version": "1.4.2",
  "heartbeat_interval_ms": 25000,
  "max_payload_bytes": 1048576,
  "max_channels_per_connection": 100,
  "msgpack": true,
  "lsn": "0/16B3748"
}
```

`session_id` es opaco y se usa en logs. `lsn` es el LSN actual del WAL al momento de conectar — útil como punto de partida para suscriptores que no quieran historial.

---

## 3. Codificación

### 3.1 MessagePack (default)

Los frames se serializan como mapas MessagePack. Las claves son strings. Los tipos primitivos se mapean directamente a sus equivalentes msgpack. Timestamps van como string ISO-8601 (no msgpack ext type, para mantener interop con clientes que no implementen ext).

### 3.2 JSON

Mismos campos, mismas semánticas. Permitido para debug y para clientes minimalistas. El servidor debe aceptar ambos según el subprotocolo negociado, sin mezcla en una misma conexión.

### 3.3 Tamaño máximo

El servidor anuncia `max_payload_bytes` en el `welcome`. Frames del cliente que excedan ese tamaño son rechazados con cierre `4413`.

---

## 4. Frames cliente → servidor

Todos los frames cliente→servidor que esperan respuesta llevan un `ref` opaco elegido por el cliente. El servidor lo devuelve en el `ack` o `error` correspondiente. `ref` no tiene que ser único globalmente, solo único entre frames pendientes.

### 4.1 `subscribe`

Se suscribe a uno o más streams en un canal. Un canal es un nombre lógico libre elegido por el cliente; varios clientes pueden compartir el mismo nombre y recibir broadcast/presence entre sí.

```jsonc
{
  "type": "subscribe",
  "ref": "s1",
  "channel": "room:42",
  "config": {
    "postgres_changes": [
      {
        "event": "INSERT",          // INSERT | UPDATE | DELETE | "*"
        "schema": "public",
        "table": "messages",
        "filter": {                 // estructurado, no DSL string
          "op": "and",
          "conditions": [
            { "column": "room_id", "op": "eq", "value": 42 },
            { "column": "deleted_at", "op": "is", "value": null }
          ]
        },
        "columns": ["id", "user_id", "text", "created_at"]  // proyección opcional
      }
    ],
    "broadcast": { "self": false, "ack": false },
    "presence": { "key": "user_7" }
  }
}
```

Notas:

- `filter` es **JSON estructurado**. No usamos DSL tipo `"room.eq.42"` heredado de PostgREST: parseo ambiguo, sin tipos. El esquema completo de operadores está en §10.
- `columns` permite que el servidor solo envíe las columnas que el cliente necesita, reduciendo bytes en el wire.
- `broadcast.self` controla si el cliente recibe sus propios broadcasts. Default `false`.
- `broadcast.ack` activa acks por mensaje (a costa de latencia).
- `presence.key` es el identificador estable del miembro. Usualmente el `user_id` del JWT, pero el cliente puede anular para casos como múltiples pestañas.

Respuesta del servidor: `ack` (§5.1) o `error` (§5.6).

### 4.2 `unsubscribe`

```jsonc
{ "type": "unsubscribe", "ref": "s2", "channel": "room:42" }
```

Cancela todos los streams del canal.

### 4.3 `broadcast`

Mensaje efímero a todos los suscriptores del canal (excepto a uno mismo si `self=false`):

```jsonc
{
  "type": "broadcast",
  "channel": "room:42",
  "event": "typing",
  "payload": { "user_id": 7 },
  "ref": "b1"           // opcional; solo si se pidió ack=true en subscribe
}
```

No persiste. No se reentrega tras reconexión.

### 4.4 `presence_track`

Anuncia o actualiza el estado del miembro en el canal:

```jsonc
{
  "type": "presence_track",
  "channel": "room:42",
  "payload": { "status": "online", "cursor": { "x": 120, "y": 480 } }
}
```

El estado vive mientras la conexión esté abierta. Una segunda llamada **reemplaza** el estado anterior (semántica LWW por miembro).

### 4.5 `presence_untrack`

```jsonc
{ "type": "presence_untrack", "channel": "room:42" }
```

Elimina al miembro inmediatamente, sin esperar a que cierre la conexión.

### 4.6 `rpc`

Invoca una función server-side registrada por el operador del proyecto. Bidirectional req/response sobre el mismo socket — sin abrir HTTP aparte:

```jsonc
{
  "type": "rpc",
  "ref": "r1",
  "channel": "room:42",   // opcional; permite scope contextual
  "function": "send_notification",
  "args": { "user_id": 7, "title": "hola" }
}
```

Respuesta: `rpc_response` (§5.7) o `error`.

Las funciones RPC son la integración con el mecanismo de "Edge Functions" / Functions de rapibase. Un solo socket sirve para subs y RPC, eliminando la separación que en otros BaaS te obliga a abrir HTTP+WS por separado.

### 4.7 `resume`

Tras una reconexión, el cliente puede pedir catchup en un canal:

```jsonc
{
  "type": "resume",
  "ref": "rs1",
  "channel": "room:42",
  "from_lsn": "0/16B3748",
  "config": { /* mismo formato que subscribe.config */ }
}
```

El servidor:

1. Si `from_lsn` está dentro de la ventana de retención, reenvía todos los eventos de DB del canal con `lsn > from_lsn` y luego responde `ack`.
2. Si `from_lsn` ya fue truncado del buffer, responde con `error` código `slot_truncated`. El cliente debe reentrar como `subscribe` normal y aceptar que perdió eventos.

Broadcast y presence **no** se reentregan en resume — son efímeros por diseño.

### 4.8 `set_auth`

Rota el JWT en una conexión viva:

```jsonc
{ "type": "set_auth", "ref": "a1", "token": "eyJhbGc..." }
```

El servidor valida y, si es válido, **reaplica permisos a todas las suscripciones existentes**: si el nuevo rol pierde acceso a una tabla, deja de recibir eventos de ella; si gana acceso, los recibe a partir de ese momento (no retroactivo).

Respuesta: `ack` con `{ "applied": true, "lost_channels": [...] }` listando suscripciones que dejaron de recibir.

### 4.9 `heartbeat`

```jsonc
{ "type": "heartbeat", "ref": "h1" }
```

Cliente debe enviarlo cada `heartbeat_interval_ms` (anunciado en `welcome`). Si el servidor no recibe heartbeat por 2× ese intervalo, cierra la conexión con código `4408`.

El servidor también envía heartbeats al cliente con el mismo formato; el cliente debe responder con `ack` o cerrar si no llega.

---

## 5. Frames servidor → cliente

### 5.1 `ack`

Respuesta exitosa a un frame del cliente:

```jsonc
{ "type": "ack", "ref": "s1", "result": { ... } }
```

El campo `result` es opcional y depende del frame correspondiente.

### 5.2 `postgres_changes`

```jsonc
{
  "type": "postgres_changes",
  "channel": "room:42",
  "lsn": "0/16B3F40",
  "commit_ts": "2026-05-10T12:34:56.123Z",
  "event": "INSERT",
  "schema": "public",
  "table": "messages",
  "new": { "id": 901, "room_id": 42, "user_id": 7, "text": "hola" },
  "old": null,
  "columns": ["id", "room_id", "user_id", "text"]   // las que el cliente pidió
}
```

- `new` está presente en INSERT y UPDATE.
- `old` está presente en UPDATE (solo columnas con REPLICA IDENTITY) y DELETE.
- `lsn` es monótono creciente por canal — el cliente lo guarda y lo usa en `resume`.

### 5.3 `broadcast`

Mismo formato que el `broadcast` cliente→server, más metadatos del emisor:

```jsonc
{
  "type": "broadcast",
  "channel": "room:42",
  "event": "typing",
  "payload": { "user_id": 7 },
  "from": { "session_id": "5f3c...", "user_id": "7" }
}
```

`from.user_id` solo se incluye si la conexión origen estaba autenticada.

### 5.4 `presence_state`

Snapshot completo del estado de presence. Se envía justo después del `ack` de `subscribe` cuando la config incluye `presence`:

```jsonc
{
  "type": "presence_state",
  "channel": "room:42",
  "members": {
    "user_3": [ { "ref": "s5...", "joined_at": "...", "state": { "status": "online" } } ],
    "user_7": [ { "ref": "5f3c...", "joined_at": "...", "state": { "status": "away" } } ]
  }
}
```

Un mismo `key` puede tener varias presencias (varias pestañas, varios dispositivos).

### 5.5 `presence_diff`

Cambios incrementales:

```jsonc
{
  "type": "presence_diff",
  "channel": "room:42",
  "joins": { "user_9": [ { "ref": "...", "state": { ... } } ] },
  "leaves": { "user_3": [ { "ref": "...", "state": { ... } } ] },
  "updates": { "user_7": [ { "ref": "...", "state": { "status": "online" } } ] }
}
```

### 5.6 `error`

```jsonc
{
  "type": "error",
  "ref": "s1",
  "code": "unauthorized",
  "message": "JWT does not have read access to table messages",
  "retryable": false
}
```

Códigos cerrados (lista completa en §9).

### 5.7 `rpc_response`

```jsonc
{
  "type": "rpc_response",
  "ref": "r1",
  "ok": true,
  "result": { "delivered": 3 }
}
```

En caso de error, llega un `error` frame con el mismo `ref`.

### 5.8 `system`

Mensajes del servidor sobre el estado de la conexión, no asociados a un `ref`:

```jsonc
{ "type": "system", "code": "behind", "channel": "room:42",
  "detail": { "queued": 1024, "max": 2048 } }

{ "type": "system", "code": "lsn_advance", "lsn": "0/17F0000" }

{ "type": "system", "code": "quota", "detail": { "rate_limited_until_ms": 1500 } }
```

Códigos en §9.2.

---

## 6. Resume y ventana de retención

El servidor mantiene un **buffer en memoria por canal** con los últimos N eventos de DB y su LSN. Cuando un cliente reconecta y manda `resume from_lsn=X`:

- Si `X >= primer_lsn_en_buffer`, el servidor reenvía `(X, último]` en orden y responde `ack`.
- Si `X < primer_lsn_en_buffer`, responde `error code=slot_truncated`.

Tamaño y retención del buffer son configurables por proyecto, con defaults documentados en el README operacional. La intención es que reconexiones de hasta 30s sean siempre recuperables sin pérdida.

`resume` solo aplica a `postgres_changes`. Broadcast y presence siempre requieren resync (presence se reenvía como `presence_state` automáticamente).

---

## 7. Backpressure

Cada suscriptor tiene una cola de salida bounded en el servidor. Política:

1. Cuando la cola supera el 50%, el servidor envía `system code=behind` con métricas. El cliente debería considerar pedir resync o desuscribirse de canales no críticos.
2. Cuando la cola se llena, el servidor cierra la conexión con código `4429` (`slow_consumer`). El cliente debe reconectar y resumir.

No hay drop silencioso de mensajes — o llegan en orden, o se cierra la conexión y se requiere resume. Esto preserva la garantía de orden y at-least-once.

---

## 8. Rate limiting

Aplicado por conexión, integrado con el rate limiter global (`internal/api/middleware/ratelimit.go`):

| Categoría | Default |
|---|---|
| `subscribe`/`unsubscribe` | 30/min |
| `broadcast` | 100/s |
| `presence_track` | 10/s |
| `rpc` | 60/s (configurable por función) |
| `set_auth` | 6/min |

Cuando se excede, el servidor responde con `error code=rate_limited` y `retry_after_ms`. Si el cliente persiste, conexión cerrada con `4429`.

---

## 9. Errores

### 9.1 Códigos de aplicación (en frames `error`)

| Código | Retryable | Significado |
|---|---|---|
| `unauthorized` | no | JWT inválido o sin permisos para la operación |
| `forbidden_filter` | no | Filtro referencia columnas que el rol no puede leer |
| `unknown_channel` | no | `unsubscribe` o `broadcast` a canal no suscrito |
| `unknown_function` | no | RPC a función no registrada |
| `invalid_filter` | no | Sintaxis o tipos del filtro inválidos |
| `invalid_payload` | no | Frame mal formado |
| `slot_truncated` | sí (con resync) | LSN solicitado fuera de ventana |
| `rate_limited` | sí | Acompañado de `retry_after_ms` |
| `quota_exceeded` | no | Límite duro por proyecto |
| `internal` | sí | Error inesperado del servidor |

### 9.2 Códigos de sistema (en frames `system`)

| Código | Significado |
|---|---|
| `behind` | Cola creciendo, riesgo de cierre |
| `lsn_advance` | Notifica avance de LSN aun sin eventos suscritos (útil para clientes que persisten LSN como heartbeat) |
| `quota` | Información de cuota disponible |
| `auth_expired` | JWT venció; cliente debe llamar `set_auth` |
| `server_shutdown` | Servidor cerrando ordenadamente; cliente debería reconectar a otro nodo |

### 9.3 Códigos de cierre WebSocket

| Código | Significado |
|---|---|
| `4400` | Versión de protocolo no soportada |
| `4401` | Auth inicial inválida |
| `4408` | Heartbeat timeout |
| `4413` | Frame excede `max_payload_bytes` |
| `4429` | Slow consumer / rate limit duro |
| `4500` | Error interno del servidor |
| `4503` | Servidor en shutdown |

Códigos `1000` y `1001` son cierres normales iniciados por cualquiera de los dos lados.

---

## 10. Lenguaje de filtros

El filtro de `postgres_changes` es un árbol JSON. Nodo hoja:

```jsonc
{ "column": "<col>", "op": "<op>", "value": <json> }
```

Nodo compuesto:

```jsonc
{ "op": "and" | "or" | "not", "conditions": [ ... ] }
```

Operadores soportados:

| Operador | Tipos | Semántica |
|---|---|---|
| `eq`, `neq` | cualquiera | Igualdad / desigualdad |
| `lt`, `lte`, `gt`, `gte` | numéricos, fechas, texto | Comparación |
| `in`, `nin` | array | Pertenencia |
| `is` | `null`, `true`, `false` | `IS NULL` / `IS TRUE` / etc. |
| `like`, `ilike` | texto | Patrón con `%` y `_` |
| `contains` | jsonb, array | `@>` |
| `contained_by` | jsonb, array | `<@` |
| `match` | tsvector | Full-text search |

El filtro se compila a un predicado Go una vez al `subscribe`. Validación estática: tipos de columna y operador deben ser compatibles, o se devuelve `invalid_filter`.

---

## 11. Garantías

- **Orden**: eventos de `postgres_changes` llegan en orden de commit por canal.
- **At-least-once con resume**: dentro de la ventana de retención, ningún evento se pierde tras reconexión.
- **Permisos en runtime**: cada evento se filtra contra los permisos vigentes del JWT al momento del fan-out, no al momento del subscribe.
- **Aislamiento entre proyectos**: una conexión solo ve eventos del proyecto identificado por su `apikey`.

No se garantiza:

- Exactly-once (el cliente debe ser idempotente, idealmente usando `lsn` como dedup key).
- Entrega de broadcast/presence tras desconexión.
- Orden entre canales distintos.

---

## 12. Compatibilidad y evolución

- Agregar campos nuevos a frames existentes es **no-breaking**. Los clientes deben ignorar campos desconocidos.
- Agregar tipos de frame nuevos es **no-breaking**. Los clientes deben ignorar tipos desconocidos (con un warning local).
- Cambiar semántica o quitar campos es **breaking** — requiere bump a `v2`.
- El servidor debe soportar `v1` y `v2` simultáneamente durante el periodo de deprecation (mínimo un release minor de margen).
