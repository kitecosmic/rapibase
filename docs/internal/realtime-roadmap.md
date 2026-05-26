# Rapibase Realtime — Roadmap

Lo que **falta**. Lo que ya está implementado y deployado vive en el
estado actual del repo y se valida con `go test ./...` + el dashboard
en `/realtime`.

Para la lista de lo ya hecho (referencia histórica), ver el git log o
`docs/realtime/`.

---

## 1. Pendientes de cierre dev-facing

### 1.1 `bus/nats` para multi-nodo

El contrato `Bus` ya está; `bus.Local` cubre single-node. Para escalar
horizontalmente:

- Wrapper sobre `nats.go` que serializa/deserializa eventos al subject
  `rapibase.realtime.events`.
- **NO** usar queue groups (cada nodo debe ver cada evento para
  fan-outear a sus subscribers locales).
- Embeber `nats-server` opcionalmente para casos donde el operador no
  quiere correr un cluster NATS aparte. Para deployments pequeños es
  un binario más en el mismo proceso.

Hasta que un solo nodo se sature, no hay urgencia. Pero el contrato
ya quedó listo para enchufar cuando llegue el día.

---

## 2. Camino a "muchas operaciones"

El índice por `(schema, table)` (2.1, hecho) cortó el bottleneck #1 —
fan-out es ahora O(matches), no O(canales totales). Lo que sigue son
optimizaciones incrementales sobre esa base.

### 2.2 Fan-out paralelo por shard

`Hub.fanout` itera shards secuencialmente. Con N shards y N cores,
paralelizar saca >5× throughput:

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
canales, el overhead de goroutines domina. Heurística: "si
ChannelCount > 1000, paraleliza".

### 2.3 Pre-compilar el "fan-out plan" por evento

Combinando 2.1 (hecho) y 2.2: cuando un evento entra, el hub arma
**una sola vez** la lista de `(Subscriber, Frame)` a entregar, en
paralelo sobre los canales que aplican, y después hace N `enqueue`
no bloqueantes en serie. La projection y la permission check son CPU
caras; hacerlas en una pasada con CPU paralelizada es óptimo.

### 2.4 Buffer compartido para frames idénticos

**Observación**: cuando 1000 suscriptores piden la misma tabla con
columnas idénticas y el filter aplica para todos, el frame
postgres_changes que reciben es **byte-idéntico**. Codificarlo 1000
veces es desperdicio.

**Optimización**: el hub agrupa suscriptores por
`(projection_columns_hash, role)`. Para cada grupo, codifica el frame
**una vez** y envía los bytes ya serializados a todos. Patrón de
Discord para mil millones de mensajes/día con clusters modestos.

Requiere extender `rawConn.WriteMessage` con una variante `WriteRaw`
que toma bytes ya codificados, y un cache en `Channel` por
`(role, columns)` con TTL corto.

### 2.5 Backpressure adaptativo

Hoy: queue lleno → drop hard, evict subscriber. Mejora:

- **Drop selectivo** según importancia del frame: presence y broadcast
  efímeros caen primero, postgres_changes nunca (preservar resume).
- **Coalesce**: si el queue tiene ya un frame de presence del mismo
  key, el siguiente lo reemplaza en vez de añadirse.
- **Warn antes de evict**: cuando queue > 75%, emitir
  `system code=behind` para que el cliente baje el ritmo (deje de
  trackear cursores, etc.).

### 2.6 LSN-based dedup en el cliente

El protocolo ya emite LSN en cada frame y el SDK lo persiste para
resume. Falta usarlo para **dedup explícito** entre live + resume:
permite ser muy agresivo con retransmisiones del servidor (resume
puede solapar con stream live sin problemas para el cliente).

### 2.7 Compaction de eventos en presence

Cuando un cliente hace `track` muchas veces seguidas (cursor moviéndose
cada 16ms), no es necesario emitir 60 diffs/s — basta con un debounce
de 50ms server-side por (channel, key). Eso libera 90% del tráfico de
presence sin perder UX real. Configurable por canal.

### 2.8 Sharded WAL consumers (futuro, multi-nodo)

Para deployments enormes: en vez de un solo replicator por cluster,
varios slots de replicación particionados por hash de tabla. Cada
slot lo consume un nodo distinto. Postgres lo soporta nativamente.

Requiere coordinador adicional para asignar particiones; no urgente
hasta que un solo nodo se sature (>500k events/s).

### Estimaciones de targets actuales

Estado actual (un solo nodo, 8 cores, 32 GB):
- **Conexiones concurrentes**: estimado 50k-100k (limitado por
  serialización del fan-out, mejora con 2.2-2.4).
- **Latencia p50**: <5 ms WAL→cliente medido empíricamente con el
  test de VPS.

Con 2.2-2.4 implementados, target realista:
- **Conexiones**: 200k-500k.
- **Eventos/s**: 1M+.

Para comparar: Supabase Realtime publica oficialmente 10k
connections/nodo sostenidos.

---

## 3. Diferenciador: realtime como sustrato para IA

Sección estratégica. Los BaaS actuales se diseñaron antes del boom de
agentes y LLM tool use; tienen realtime "para chats". Rapibase puede
posicionarse como el realtime *nativo* para apps AI-first.

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

Análogo a Langfuse/Helicone pero **dentro del mismo BaaS**, sin un
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
es similar a 'problemas de billing'" — no a una tabla entera. El
fan-out es 10× más selectivo y la app es más simple.

### 3.5 Multi-agent broadcast con typing/cursor coordination

Cuando varios agentes (y humanos) operan sobre el mismo documento,
los conflictos comunes son:

- "Estaba editando esta línea" → presence con cursor + lock-light.
- "Agente terminó su tool call" → broadcast con `from.agent_id`.
- "Humano canceló la operación" → broadcast con cancel propagation.

Esto se construye sobre primitives que rapibase ya tiene; pero
documentado y ejemplificado en el SDK abre un caso de uso enorme.

### 3.6 RPC como herramienta MCP

Rapibase ya tiene MCP server (`internal/mcp/`). Integración natural:

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
  afectado vía `Hub.PublishLocal` o `Hub.Broadcast` (un endpoint REST
  nuevo).

Resultado: un usuario inserta una fila → tu agente la procesa → el
usuario ve la respuesta del agente en realtime, todo sin cableado
explícito. El BaaS se vuelve un orquestador de "evento → agente →
respuesta".

---

## 4. Diferenciales — estado vs. competencia

| Capacidad | Supabase | Firebase | Appwrite | Rapibase |
|---|---|---|---|---|
| WS único para DB+broadcast+presence+RPC | Parcial | No | No | **✅ hecho** |
| Codec binario (msgpack) | No | No | No | **✅ hecho (default)** |
| Resume con LSN garantizado | Parcial | No | No | **✅ hecho** |
| Single binary operacional | No (4+ servicios) | N/A | No (~5 servicios) | **✅ hecho** |
| Rate limiting per conexión + per función | Limitado | N/A | Limitado | **✅ hecho** |
| Métricas Prometheus integradas | No | No | No | **✅ hecho** |
| Índice (schema, table) en fan-out | No | N/A | No | **✅ hecho** |
| Streaming RPC | No | No | No | Pendiente (§3.1) |
| Shared state CRDT | No | No | No | Pendiente (§3.2) |
| Observabilidad LLM built-in | No | No | No | Pendiente (§3.3) |
| Filtros semánticos (embeddings) | No | No | No | Pendiente (§3.4) |
| RPCs auto-expuestos como MCP tools | No | No | No | Pendiente (§3.6) |
| Conexiones/nodo sostenibles | ~10k | N/A | ~5k | actual ~50-100k, target 200k+ (§2.2-2.4) |

Columna derecha = la tesis del producto: **el realtime de un BaaS
diseñado en 2026, para apps que son híbridas humano-agente**.

---

## 5. Orden sugerido de ejecución pendiente

1. **RPCs como MCP tools** (3.6) — combina lo que ya hay (MCP +
   rpc.Registry), ganancia enorme en posicionamiento, ~1-2 días.
2. **Streaming RPC** (3.1) — primer diferenciador AI real, base para
   3.2-3.5.
3. **Shared state CRDT** (3.2) — habilita la categoría completa de
   apps colaborativas humano-agente.
4. **Backpressure adaptativo** (2.5) — el más impactante de las
   optimizaciones que aún quedan, mejor UX bajo carga.
5. **Fan-out paralelo + frames compartidos** (2.2 + 2.4) — el push
   final hacia 200k+ conexiones por nodo.
6. **bus/NATS** (1.1) — solo cuando un nodo se sature.
7. Resto en paralelo según necesidades reales de usuarios.

Lo de 3.x son proyectos de 1-2 semanas cada uno; lo de 2.x son días.
