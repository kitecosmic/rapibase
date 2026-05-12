# Rapibase Realtime — Roadmap

Este documento captura **lo que falta** para que rapibase realtime no
solo iguale a los BaaS existentes, sino que los supere en dos dimensiones
donde típicamente fracasan: **escala** (cuántas operaciones por segundo
puede sostener un nodo) y **adaptabilidad a IA** (cómo el realtime se
vuelve la columna vertebral de agentes y aplicaciones AI-first).

El estado actual ya cubre el camino feliz: postgres_changes filtrados
por permisos, broadcast, presence, RPC sobre el mismo socket, resume
con LSN, codecs JSON+msgpack, leader election, decoder pgoutput real.
Lo que viene aquí es **producción seria** y **diferenciador competitivo**.

---

## 1. Pendientes inmediatos (cierre del ciclo dev-facing)

Sin esto, rapibase realtime funciona pero no es agradable de usar.

### 1.1 SDK TypeScript

Hoy solo se puede conectar con `wscat` crudo. El SDK convierte el wire
en una API tipada y declarativa que el dev usa en su app.

- Vive en `web/src/lib/realtime/`, listo para extraerse a un paquete
  `@rapibase/client` publicable a npm cuando llegue ese momento.
- Cubre: `createClient`, `channel.subscribe/onChange/broadcast/track/invoke`,
  reconexión transparente con resume, tipos generados desde el schema
  de la DB vía codegen (`@rapibase/cli gen-types`).
- Contrato completo en [`sdk.md`](sdk.md).

### 1.2 Integration tests del WAL contra Postgres real

Decoder, replicator y leader tienen unit tests con fixtures, pero el
camino end-to-end "INSERT en Postgres → frame en el cliente" sólo se
valida operacionalmente al primer deploy. Necesario:

- `wal/integration_test.go` con build tag `integration`.
- `docker-compose.test.yaml` que levanta Postgres con
  `wal_level=logical` + crea publication y slot.
- Suite que mete inserts/updates/deletes/truncates y asserta los
  events que salen del decoder, contra fixtures de relations reales.

### 1.3 `bus/nats` para multi-nodo

El contrato `Bus` ya está. Implementarlo requiere:

- Wrapper sobre `nats.go` que serializa/deserializa eventos al subject
  `rapibase.realtime.events`.
- **NO** usar queue groups (cada nodo debe ver cada evento para
  fan-outear a sus subscribers locales).
- Embeber `nats-server` opcionalmente para casos donde el operador no
  quiere correr un cluster NATS aparte. Para deployments pequeños es
  un binario más en el mismo proceso.

### 1.4 Métricas conectadas

`metrics.Recorder` existe; sólo falta llamarlo en los puntos calientes:

- `hub.fanout` → `MetricEventsPublished` con labels `schema/table`.
- `Channel.deliverEvent` → `MetricEventsDelivered` por delivery exitoso,
  `MetricEventsDropped` por queue full.
- `Subscriber.enqueue` que retorna `false` → `MetricSlowConsumerEvictions`.
- `Replicator.appliedLSN` vs `IdentifySystem.XLogPos` → `MetricWALLagBytes`.
- `Invoker.Call` → `MetricRPCCalls{function,status}` + `MetricRPCDuration`.

Sin estas métricas conectadas, el sistema es operacionalmente ciego en
producción.

### 1.5 Rate limiting por conexión

`rpc.Definition.RatePerSec` está como metadata pero nadie lo aplica.
Necesario en el transport:

- `Session` mantiene un `map[string]*tokenBucket` por función.
- `readLoop`, antes de despachar un `FrameRPC`, consume un token del
  bucket de esa función.
- Similar para `FrameBroadcastIn` con un bucket global per-conexión.
- Excede → `protocol.ErrRateLimited` con `RetryMs`.

---

## 2. Camino a "muchas operaciones" (el desafío que otros BaaS fallan)

Supabase Realtime históricamente ha tenido problemas escalando más
allá de 10-20k conexiones concurrentes por nodo, y Postgres Changes
es especialmente caro cuando hay muchos suscriptores a la misma tabla.
Rapibase puede hacer mejor — pero requiere trabajo deliberado.

### 2.1 Indexar canales por `(schema, table)` en el hub

**Hoy**: `Hub.fanout` itera **todos** los shards y todos los canales,
y cada canal evalúa si tiene streams matching para el evento. Es
O(canales totales × suscriptores por canal) en el peor caso.

**Mejora**: añadir un índice inverso `(schema, table) → []*Channel` en
el hub. Cuando llega un evento, se consultan **solo** los canales que
declararon interés en esa tabla. Es O(canales relevantes × suscriptores).

Para una app con 100k canales activos pero 50 tablas distintas, esto
es **2000× más rápido**. Es la optimización más impactante pendiente.

Archivo: `hub/index.go` con una `sync.Map[tableKey][]*Channel` que se
mantiene en sync con Attach/Detach.

### 2.2 Fan-out paralelo por shard

`Hub.fanout` itera shards secuencialmente. Con 64 shards y 8 cores
modernos, parallelizar por shard saca >5× throughput sin cambios al
modelo conceptual:

```go
func (h *Hub) fanout(ev wal.Event) {
    var wg sync.WaitGroup
    for _, sh := range h.shards {
        sh := sh
        wg.Add(1)
        go func() { defer wg.Done(); sh.forEach(func(c *Channel) { c.PublishEvent(ev) }) }()
    }
    wg.Wait()
}
```

Cuidado: solo vale la pena cuando hay miles de canales. Para 10
canales, el overhead de goroutines domina. Una heurística "si
ChannelCount > 1000, paraleliza" es lo correcto.

### 2.3 Pre-compilar el "fan-out plan" por evento

Combinando 2.1 y 2.2: cuando un evento entra, el hub puede armar **una
sola vez** la lista de `(Subscriber, Frame)` a entregar, en paralelo
sobre los canales que aplican, y después hacer N `enqueue` no
bloqueantes en serie. La projection y la permission check son CPU
caras; hacerlas en una sola pasada con CPU paralelizada es óptimo.

### 2.4 Buffer compartido para frames idénticos

**Observación**: cuando 1000 suscriptores piden la misma tabla con
columnas idénticas y el filter aplica para todos, el frame postgres_changes
que reciben es **byte-idéntico**. Codificarlo 1000 veces es desperdicio.

**Optimización**: el hub puede agrupar suscriptores por
`(projection_columns_hash, role)`. Para cada grupo, codifica el frame
**una vez**, y envía los bytes ya serializados a todos. Esto es lo que
permite a Discord servir miles de millones de mensajes/día con clusters
modestos.

Requiere extender `rawConn.WriteMessage` con una variante `WriteRaw`
que toma bytes ya codificados, y un cache en `Channel` por
`(role, columns)` con TTL corto.

### 2.5 Backpressure adaptativo

Hoy: queue lleno → drop hard, evict subscriber. Mejora:

- **Drop selectivo** según importancia del frame: presence y broadcast
  efímeros caen primero, postgres_changes nunca (preservar resume).
- **Coalesce**: si el queue tiene ya un frame de presence del mismo key,
  el siguiente lo reemplaza en vez de añadirse.
- **Warn antes de evict**: cuando queue > 75%, emitir `system code=behind`
  para que el cliente baje el ritmo (deje de track-ear cursores, etc.).

### 2.6 LSN-based dedup en el cliente

El protocolo ya emite LSN en cada frame; el SDK debe usarlo para
deduplicar entre live + resume. Esto permite ser muy agresivo con
retransmisiones del servidor (resume puede solapar con stream live sin
problemas para el cliente).

### 2.7 Compaction de eventos en presence

Cuando un cliente hace `track` muchas veces seguidas (cursor moviéndose
cada 16ms), no es necesario emitir 60 diffs/s — basta con un debounce
de 50ms server-side por (channel, key). Eso libera 90% del tráfico de
presence sin perder UX real. Configurable por canal.

### 2.8 Sharded WAL consumers (futuro, multi-nodo)

Para deployments enormes: en vez de un solo replicator por cluster,
varios slots de replicación particionados por hash de tabla. Cada slot
lo consume un nodo distinto. Postgres lo soporta nativamente.

Requiere coordinador adicional para asignar particiones; no urgente
hasta que un solo nodo se sature (>500k events/s).

### Estimaciones de targets

Con 1.1-1.4 implementados, un solo nodo (8 cores, 32GB):
- **Conexiones concurrentes**: 200k-500k WebSockets activos.
- **Eventos/s fan-out**: 1M+ (1000 eventos/s × 1000 suscriptores promedio).
- **Latencia p50**: <5ms WAL→cliente, p99 <20ms.

Para comparar: Supabase Realtime publica oficialmente 10k connections/nodo
sostenidos.

---

## 3. Diferenciador: realtime como sustrato para IA

Esta sección es estratégica. Los BaaS actuales se diseñaron antes del
boom de agentes y LLM tool use; tienen realtime "para chats". Rapibase
puede posicionarse como el realtime *nativo* para apps AI-first.

### 3.1 Streaming de tool calls vía RPC

**Hoy**: RPC sobre WS retorna un único valor cuando termina.

**Para IA**: las llamadas a tools en agentes son típicamente **streams**
(tokens del LLM, resultados parciales de búsquedas, progreso de
generación de imagen). Falta extender `rpc/` con un patrón "streaming
RPC":

- Cliente: `channel.stream('generate_image', args, onChunk, onEnd)`.
- Servidor: handler recibe un `chan Chunk` y emite múltiples mensajes
  con el mismo `Ref` antes de un `rpc_response` final.
- Wire: nuevo `FrameRPCChunk` con `Ref`, `Sequence`, `Payload`.

Esto convierte rapibase en un **transport ideal para apps que llaman
agentes**: una sola conexión maneja datos en vivo + tool calls
streaming, sin SSE adicional.

### 3.2 Agent state sync (`shared state` con CRDT)

Apps AI colaborativas (Cursor-style, Replit AI, etc.) necesitan
estado compartido entre humanos y agentes con resolución de conflictos.
Una mejora natural sobre Presence:

- Nuevo tipo de canal: `shared_state`, donde el state vive en el
  servidor como un CRDT (YJS-compatible o Automerge).
- Operaciones merge automáticamente; cada cliente recibe el diff
  como evento.
- Persistido opcionalmente en una tabla `realtime_state` del proyecto.

Permite que un agente que edita código simultáneamente con un humano
no pisotee cambios. Hoy, los devs montan Liveblocks/PartyKit aparte —
rapibase puede ofrecer esto built-in.

### 3.3 Observabilidad de invocaciones LLM

Cada RPC que llama a un LLM puede emitir:
- Modelo usado, tokens consumidos, latencia.
- Tool calls intermedios.
- Costo estimado.

Si rapibase guarda estos meta-eventos en una tabla `realtime_llm_calls`
y el WAL los propaga, los devs obtienen **observabilidad gratis** de
todas las invocaciones de modelo en su app sin instrumentar nada.

Es análogo a Langfuse/Helicone pero **dentro del mismo BaaS**, sin un
segundo servicio. Diferencial fuerte para devs AI-first.

### 3.4 Subscriptions con filtros semánticos

Hoy el `filter` es estructural (`column = value`). Para IA, una
extensión enorme:

- `match_embedding`: `WHERE embedding <-> $query < 0.3` (similarity).
- `match_intent`: el evento se entrega solo si su contenido encaja
  semánticamente con un prompt configurado por el suscriptor.

Estos requieren extender `filter/` con operadores que llaman a
pgvector o a un sidecar de embeddings. El cliente declara intent,
el server filtra en runtime.

Ejemplo: un agente de soporte se suscribe a "tickets cuyo embedding
es similar a 'problemas de billing'" — no a una tabla entera. El fan-out
es 10× más selectivo y la app es más simple.

### 3.5 Multi-agent broadcast con typing/cursor coordination

Cuando varios agentes (y humanos) operan sobre el mismo documento,
los conflictos comunes son:

- "Estaba editando esta línea" → presence con cursor + lock-light.
- "Agente terminó su tool call" → broadcast con `from.agent_id`.
- "Humano canceló la operación" → broadcast con cancel propagation.

Esto se construye sobre primitives que rapibase ya tiene; pero
documentado y ejemplificado en el SDK abre un caso de uso enorme.

### 3.6 RPC como herramienta MCP

Rapibase ya tiene MCP server (`internal/mcp/`). Una integración natural:

- Cada `rpc.Definition` registrada en rapibase es automáticamente
  expuesta como una herramienta MCP.
- Un cliente MCP (Claude Desktop, Cursor, etc.) puede listar y llamar
  RPCs registradas en el rapibase del operador.

Esto convierte rapibase en un **AI-action runtime**: tus RPCs son
disponibles para cualquier modelo MCP-compatible sin escribir
adaptadores. Diferencial competitivo masivo — ningún BaaS actual
ofrece esto.

### 3.7 Webhook → Realtime → Agent pipeline

Combinar lo que ya hay:

- Evento de DB → WAL → realtime (existe).
- Mismo evento → webhook a tu agente (existe en `internal/webhooks/`).
- Webhook responde con frames a inyectar en el canal del usuario
  afectado vía `Hub.PublishLocal` o `Hub.Broadcast` (un endpoint
  REST nuevo).

Resultado: un usuario inserta una fila → tu agente la procesa → el
usuario ve la respuesta del agente en realtime, todo sin cableado
explícito. El BaaS se vuelve un orquestador de "evento → agente →
respuesta".

---

## 4. Resumen ejecutivo: qué nos diferencia

| Capacidad | Supabase | Firebase | Appwrite | Rapibase (objetivo) |
|---|---|---|---|---|
| WS único para DB+broadcast+presence+RPC | Parcial | No | No | **Sí** |
| Codec binario (msgpack) | No | No | No | **Sí (default)** |
| Resume con LSN garantizado | Parcial | No | No | **Sí, doc primer-class** |
| Streaming RPC | No | No | No | **Sí (planeado §3.1)** |
| Shared state CRDT | No | No | No | **Sí (planeado §3.2)** |
| Observabilidad LLM built-in | No | No | No | **Sí (planeado §3.3)** |
| Filtros semánticos | No | No | No | **Sí (planeado §3.4)** |
| RPCs auto-expuestos como MCP tools | No | No | No | **Sí (planeado §3.6)** |
| Single binary (operacional) | No (4+ servicios) | N/A | No (~5 servicios) | **Sí** |
| Conexiones/nodo sostenibles | ~10k | N/A | ~5k | **Target 200k+ (§2)** |

Esa columna derecha es la tesis del producto: **el realtime de un BaaS
diseñado en 2026, para apps que son híbridas humano-agente**.

---

## 5. Orden recomendado de ejecución

1. **SDK TS** (1.1) — desbloquea consumir realtime desde el propio
   dashboard de rapibase y desde apps de devs early adopters.
2. **Métricas conectadas** (1.4) — operacionalmente crítico, barato.
3. **Index por tabla** (2.1) — la optimización con mayor ratio
   impacto/esfuerzo.
4. **Integration tests WAL** (1.2) — antes del primer deploy real.
5. **Rate limiting** (1.5) — protección antes de exponer públicamente.
6. **RPC streaming** (3.1) — primer diferenciador AI real, base para 3.2-3.6.
7. **RPCs como MCP tools** (3.6) — combina lo que ya tienes, ganancia
   enorme en posicionamiento.
8. **Shared state CRDT** (3.2) — habilita la categoría completa de
   apps colaborativas humano-agente.
9. Resto en paralelo según necesidades reales de usuarios.

Cada punto del 1.x se puede entregar en días; los del 3.x son
proyectos de 1-2 semanas cada uno. El conjunto es ambicioso pero
ningún ítem individual lo es.
