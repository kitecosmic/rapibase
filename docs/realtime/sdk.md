# Rapibase Realtime — API del SDK (TypeScript)

**Paquete:** `@rapibase/client` — el mismo cliente para REST, Auth, Storage, RPC y Realtime. No hay paquete separado.
**Estado:** especificación de referencia. Implementación contra esta API; cambios requieren bump de versión SemVer.

Este documento define la **superficie pública** del SDK que ven los developers de aplicaciones. El protocolo subyacente (ver `protocol.md`) es un detalle de implementación que el SDK puede cambiar sin romper la API.

---

## 1. Filosofía de diseño

1. **Tipado de extremo a extremo**: tipos generados desde el schema de la DB. Si añades una columna, la autocompletado del editor lo refleja sin pasos manuales.
2. **Una sola conexión, una sola abstracción**: `channel` cubre cambios de DB, broadcast, presence y RPC. El developer no decide entre tres APIs distintas para tres mecanismos relacionados.
3. **Resume transparente**: al reconectar, el SDK persiste el LSN y pide catchup automáticamente. El usuario solo ve "siempre en sync".
4. **Optimistic-first**: el SDK ofrece primitivas para updates optimistas que se reconcilian al recibir el evento real. No es una opción agregada, es central.
5. **Tree-shakeable**: si no usas presence, no entra en tu bundle. Si solo usas REST, el subsistema realtime no se carga.
6. **Errores como valores**: nada se tira como excepción inesperada. Métodos asincrónicos devuelven `Result<T, RealtimeError>` o callbacks tipados.
7. **Sin "magia" oculta**: sin polling oculto, sin reconexiones en bucle infinito sin notificación, sin estado global compartido entre instancias.

---

## 2. Inicialización

```ts
import { createClient } from '@rapibase/client'

const rapi = createClient({
  url: 'https://api.miapp.com',
  apiKey: process.env.RAPIBASE_ANON_KEY!,
  realtime: {
    transport: 'msgpack',          // 'msgpack' | 'json' — default 'msgpack'
    heartbeatIntervalMs: 25000,    // override del default del servidor
    reconnect: {
      enabled: true,               // default true
      backoff: 'exponential',      // 'exponential' | 'linear' | (attempt) => number
      maxDelayMs: 30000,
      maxAttempts: Infinity,
      jitter: true,
    },
    resume: {
      enabled: true,               // persiste LSN en localStorage por canal
      storage: localStorage,       // override para apps móviles, tests, etc.
    },
  },
})
```

`createClient` es **único** por aplicación. Crear varios para el mismo proyecto es un anti-patrón y el SDK emite warning en dev.

### 2.1 Auth dinámica

```ts
rapi.auth.onAuthStateChange((session) => {
  // El SDK rota el JWT en la conexión realtime automáticamente vía set_auth.
  // No requiere reconexión.
})

// Manual:
await rapi.realtime.setAuth(jwt)
```

---

## 3. Channels

```ts
const channel = rapi.channel('room:42')
```

`channel(name)` devuelve **la misma instancia** si se llama dos veces con el mismo nombre dentro de la misma sesión. Esto evita suscripciones duplicadas accidentales.

### 3.1 Ciclo de vida

```ts
channel.subscribe((status, error) => {
  // status: 'subscribing' | 'subscribed' | 'reconnecting' | 'closed' | 'error'
})

await channel.unsubscribe()
```

`subscribe` es **idempotente**: llamar dos veces no duplica el handshake. Devuelve una `Promise<void>` que resuelve cuando el primer `subscribed` ocurre, lo que permite usar `await` cuando se quiere bloquear hasta estar listo.

### 3.2 Estado actual

```ts
channel.status            // 'subscribing' | 'subscribed' | 'reconnecting' | 'closed' | 'error'
channel.lastError         // RealtimeError | null
channel.lastSeenLSN       // string | null
channel.lag               // { queued: number, max: number, behind: boolean }
```

`lag` se actualiza cuando el servidor envía `system code=behind`. Útil para dibujar un indicador "te estás retrasando" en UIs colaborativas.

---

## 4. Postgres Changes

### 4.1 Listener tipado

Asumiendo tipos generados (`@rapibase/client/types` con codegen del schema):

```ts
import type { Database } from './rapibase-types'

type Mensaje = Database['public']['Tables']['messages']['Row']

channel.onChange<Mensaje>({
  event: 'INSERT',
  table: 'messages',
  filter: (q) => q.eq('room_id', 42).is('deleted_at', null),
  columns: ['id', 'user_id', 'text', 'created_at'],
}, (payload) => {
  // payload: { event: 'INSERT', new: Pick<Mensaje, 'id'|'user_id'|'text'|'created_at'>, old: null, lsn: string, commitTs: Date }
})
```

Notas:

- `filter` es una **función con builder tipado**, no string. El builder se compila a JSON estructurado y se manda al servidor. Errores de tipo se atrapan en compile-time, no en runtime.
- `columns` proyecta y **estrecha el tipo** de `payload.new`/`payload.old`. Si pides `['id', 'text']`, TypeScript infiere `Pick<Mensaje, 'id' | 'text'>`.
- `event` puede ser `'INSERT' | 'UPDATE' | 'DELETE' | '*'`. Para `'*'`, el payload es un union discriminado por `event`.

### 4.2 Builder de filtros

```ts
filter: (q) =>
  q.and(
    q.eq('room_id', 42),
    q.in('user_id', [7, 9, 12]),
    q.gte('created_at', new Date('2026-01-01')),
    q.or(
      q.is('archived', false),
      q.eq('owner_id', currentUser.id),
    ),
  )
```

Operadores: `eq`, `neq`, `lt`, `lte`, `gt`, `gte`, `in`, `nin`, `is`, `like`, `ilike`, `contains`, `containedBy`, `match`. Cada uno tipado contra la columna referenciada.

### 4.3 Múltiples listeners

```ts
channel
  .onChange({ event: 'INSERT', table: 'messages', filter: (q) => q.eq('room_id', 42) }, onInsert)
  .onChange({ event: 'UPDATE', table: 'messages', filter: (q) => q.eq('room_id', 42) }, onUpdate)
  .onChange({ event: '*',      table: 'reactions', filter: (q) => q.eq('room_id', 42) }, onReaction)
```

Encadenable. Cada `onChange` es independiente; el SDK consolida en un solo `subscribe` al servidor para minimizar bytes en el wire.

### 4.4 Optimistic updates

```ts
const tx = channel.optimistic({
  table: 'messages',
  insert: { id: '__local-1', room_id: 42, user_id: 7, text: 'hola' },
})

// El listener onChange recibirá el evento con `optimisticRef: '__local-1'`
// cuando llegue el INSERT real del servidor; el SDK reconcilia automáticamente.

tx.commit()    // confirma cuando llegó el INSERT real
tx.rollback()  // revierte (la app debe limpiar UI)
tx.timeoutMs   // timeout opcional; default 10s, después se hace rollback automático
```

Esta es una primitiva de primer nivel, no un patrón que el dev tenga que armar a mano. La integración con `useQuery`/`useState` queda en docs aparte.

---

## 5. Broadcast

### 5.1 Escuchar

```ts
type TypingPayload = { userId: number }

channel.onBroadcast<TypingPayload>('typing', (payload, meta) => {
  // payload: TypingPayload
  // meta: { from: { sessionId: string, userId?: string }, receivedAt: Date }
})
```

### 5.2 Enviar

```ts
await channel.broadcast('typing', { userId: 7 })

// Con ack del servidor (más latencia):
await channel.broadcast('typing', { userId: 7 }, { ack: true })

// Self-broadcast (recibirse a sí mismo):
await channel.broadcast('typing', { userId: 7 }, { self: true })
```

`broadcast` devuelve `Promise<void>`. Sin `ack`, resuelve cuando el frame se escribe al socket. Con `ack`, cuando el servidor confirma fan-out.

---

## 6. Presence

```ts
type PresenceState = { status: 'online' | 'away', cursor?: { x: number, y: number } }

channel.presence<PresenceState>({
  key: () => currentUser.id.toString(),  // función para que se reevalúe en reconexión
})

channel.onPresence('sync', (members) => {
  // members: Map<string, Array<{ ref: string, state: PresenceState, joinedAt: Date }>>
})

channel.onPresence('join', ({ key, current, joined }) => {
  // joined: array de presencias que entraron (puede ser uno o varios)
})

channel.onPresence('leave', ({ key, current, left }) => {})
channel.onPresence('update', ({ key, current, previous }) => {})

// Anunciar / actualizar estado:
await channel.track({ status: 'online', cursor: { x: 120, y: 480 } })
await channel.track({ status: 'away' })   // reemplazo, no merge

// Salir antes de cerrar la conexión:
await channel.untrack()
```

Múltiples presencias con la misma `key` (varias pestañas, varios dispositivos) son ciudadanos de primera clase. `members` siempre devuelve `Array`, no instancia única.

---

## 7. RPC bidireccional

Llamar funciones server-side **sobre el mismo socket**, sin abrir HTTP aparte:

```ts
type SendNotificationArgs = { userId: number, title: string }
type SendNotificationResult = { delivered: number }

const result = await channel.invoke<SendNotificationArgs, SendNotificationResult>(
  'send_notification',
  { userId: 7, title: 'hola' },
  { timeoutMs: 5000 }
)
// result: SendNotificationResult
```

También disponible a nivel global (sin contexto de canal):

```ts
const result = await rapi.realtime.invoke('list_active_users', {}, { timeoutMs: 3000 })
```

Esta unificación es uno de los diferenciales de rapibase: en otros BaaS el RPC vive en HTTP y realtime en WS, lo que duplica state, autenticación y reconexión.

---

## 8. Manejo de errores

```ts
import { RealtimeError } from '@rapibase/client'

channel.onError((err: RealtimeError) => {
  err.code           // 'unauthorized' | 'invalid_filter' | 'rate_limited' | ...
  err.retryable      // boolean
  err.retryAfterMs?  // number, si aplica
  err.message        // human-readable
})
```

Los errores **no se tiran** como excepciones desde callbacks de eventos — siempre llegan vía `onError`. Excepciones solo en métodos `async` (subscribe, broadcast, invoke) cuando el operador llamó incorrectamente la API.

---

## 9. Reconexión y resume

Transparente por defecto:

1. La conexión se cae.
2. SDK intenta reconectar con backoff configurado.
3. Al reconectar, para cada canal suscrito:
   - Si `resume.enabled` y hay LSN persistido, manda `resume from_lsn=…`.
   - Si el servidor responde `slot_truncated`, el SDK emite `onResync` y resuscribe desde cero.
4. El estado del canal pasa por `reconnecting` → `subscribed`.

```ts
channel.onResync((reason) => {
  // reason: 'slot_truncated' | 'auth_changed' | 'forced'
  // La app puede invalidar caches locales aquí.
})

channel.onReconnect((attempt, delayMs) => {
  // Útil para mostrar "Reconectando..." en UI.
})

// Forzar resync manual:
await channel.resync()
```

---

## 10. Tipos generados

```bash
npx @rapibase/cli gen-types --url https://api.miapp.com --out ./rapibase-types.ts
```

Genera tipos a partir del schema actual de la DB. Configurar como step en CI o como git pre-commit hook. El SDK usa esos tipos vía:

```ts
import { createClient } from '@rapibase/client'
import type { Database } from './rapibase-types'

const rapi = createClient<Database>({ ... })
```

Pasar `Database` activa la inferencia tipada en todas las operaciones — REST, Realtime, RPC.

---

## 11. Observabilidad cliente-side

```ts
rapi.realtime.metrics.subscribe((m) => {
  m.connectionState        // 'connecting' | 'open' | 'closed'
  m.channelsActive         // número
  m.bytesReceived          // contador acumulado
  m.bytesSent
  m.eventsReceived
  m.lagBehind              // boolean global
  m.reconnectAttempts
  m.lastReconnectMs
})
```

Útil para integrar con Sentry, Datadog RUM, o un debug panel propio. Sin esto, el operador es ciego a problemas reales en clientes desplegados.

### 11.1 Modo debug

```ts
const rapi = createClient({ ..., realtime: { debug: true } })
```

Activa logging detallado de cada frame en consola, con timestamps y direccion. Solo para desarrollo — el SDK falla en compilación si `debug: true` en builds de producción (vía bandera de bundler).

---

## 12. Ejemplo end-to-end

Chat colaborativo con cursores en vivo y indicador de "está escribiendo":

```ts
import { createClient } from '@rapibase/client'
import type { Database } from './rapibase-types'

const rapi = createClient<Database>({ url, apiKey })

const room = rapi.channel(`room:${roomId}`)

// Mensajes nuevos
room.onChange({
  event: 'INSERT',
  table: 'messages',
  filter: (q) => q.eq('room_id', roomId),
}, ({ new: msg }) => addMessageToUI(msg))

// Quién está escribiendo
room.onBroadcast<{ userId: number }>('typing', ({ userId }) => showTyping(userId))

// Cursores y status
room.presence<{ status: 'online' | 'away', cursor?: { x: number, y: number } }>({
  key: () => currentUser.id.toString(),
})
room.onPresence('sync', (members) => renderCursors(members))

await room.subscribe()
await room.track({ status: 'online' })

// Optimistic send
async function send(text: string) {
  const tx = room.optimistic({
    table: 'messages',
    insert: { id: crypto.randomUUID(), room_id: roomId, user_id: currentUser.id, text },
  })
  try {
    await rapi.from('messages').insert({ room_id: roomId, user_id: currentUser.id, text })
    tx.commit()
  } catch (e) {
    tx.rollback()
    showError(e)
  }
}

// Indicador de typing
let typingTimer: number | undefined
input.addEventListener('input', () => {
  room.broadcast('typing', { userId: currentUser.id })
  clearTimeout(typingTimer)
  typingTimer = window.setTimeout(() => room.broadcast('typing_stop', { userId: currentUser.id }), 2000)
})
```

Sin esto: tendrías que armar a mano WebSocket, reconexión, dedup, optimistic, presence multi-tab, tipos, codegen, y cinco capas de abstracción. Con el SDK es declarativo y tipado.

---

## 13. Compatibilidad

- El SDK garantiza compatibilidad con servidores que anuncien la misma versión mayor del subprotocolo.
- Servidores que anuncien una versión mayor más nueva: el SDK negocia downgrade si lo soporta, o falla con error claro.
- Cambios breaking en el SDK: bump de major SemVer + changelog explícito + codemod cuando sea posible.

---

## 14. Lo que el SDK **no** hace (por diseño)

- **No persiste mensajes localmente**: si necesitas offline-first, integra con tu propia capa (IndexedDB, RxDB, etc.). El SDK expone los hooks (`onChange`, `onResync`) pero no lo asume.
- **No gestiona estado global de aplicación**: úsalo desde Zustand, Redux, Jotai, lo que sea. El SDK es una capa de transporte tipada, no un store.
- **No reintenta operaciones de aplicación**: `broadcast` o `invoke` que fallan llegan al caller. La política de retry de negocio es del developer.

Estas restricciones son intencionales: hacen al SDK predecible, pequeño, y combinable con cualquier stack de UI.
