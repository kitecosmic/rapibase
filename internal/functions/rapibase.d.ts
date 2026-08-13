// Rapibase Functions — type definitions
// Estas son TODAS las APIs disponibles dentro de una function. Son
// SÍNCRONAS (no hay event loop): `const rows = db.query(...)` — sin await.
// Los handlers `async` también funcionan (await sobre valores síncronos es
// válido), pero no hay I/O asíncrono real que esperar.
//
// Los archivos .ts/.js se suben a la instancia:
//   curl -X POST $BASE/api/v1/functions -H "apikey: $SERVICE_KEY" -F "file=@checkout.ts"
// La respuesta incluye los errores de compilación, si los hay.

/** Request que recibe un handler HTTP (fn.http). */
interface FnRequest {
  method: string
  path: string
  /** Query params como mapa plano. */
  query: Record<string, string>
  /** Headers en minúsculas. apikey/authorization se eliminan. */
  headers: Record<string, string>
  /** Cuerpo crudo. */
  body: string
  /** Cuerpo parseado si Content-Type es application/json; si no, null. */
  json: unknown
  /** "service" | "authenticated" | "anon" (anon solo en functions public). */
  role: string
  /** Usuario de la app si llegó ANON_KEY + JWT válido; si no, null. */
  user: { id: string; email: string; role: string } | null
}

/**
 * Respuesta de un handler HTTP. Devuelve:
 *  - un objeto/array cualquiera → JSON 200
 *  - un string → texto plano 200
 *  - null/undefined → 204
 *  - {status?, headers?, json?} o {status?, headers?, body?} → respuesta explícita
 */
type FnResponse =
  | { status?: number; headers?: Record<string, string>; json?: unknown; body?: string }
  | Record<string, unknown>
  | unknown[]
  | string
  | null
  | undefined

declare const fn: {
  /**
   * Registra un endpoint en POST/GET/… /api/fn/{name}.
   * Auth por defecto: SERVICE_KEY, o ANON_KEY + JWT del usuario (req.user).
   * {public: true} lo abre sin credenciales (webhooks de Stripe, etc.).
   */
  http(name: string, handler: (req: FnRequest) => FnResponse, opts?: { public?: boolean }): void
  /** Programa un handler con cron estándar de 5 campos ("0 3 * * *"). */
  cron(name: string, spec: string, handler: () => void): void
  /** Registra un worker; se ejecuta cuando alguien encola jobs con ese nombre. */
  job(name: string, handler: (payload: unknown) => void): void
}

declare const db: {
  /** SQL parametrizado ($1, $2, …). SELECT → filas como objetos. */
  query(sql: string, ...params: unknown[]): Record<string, unknown>[]
  /** SQL de escritura/DDL. Devuelve filas afectadas. */
  exec(sql: string, ...params: unknown[]): number
}

interface FetchResponse {
  status: number
  ok: boolean
  headers: Record<string, string>
  body: string
  /** Parsea body como JSON (lanza si no lo es). */
  json(): unknown
}

/** Cliente HTTP saliente, SÍNCRONO. body objeto → JSON automático. */
declare function fetch(
  url: string,
  opts?: { method?: string; headers?: Record<string, string>; body?: string | object; timeoutMs?: number },
): FetchResponse
declare const http: { fetch: typeof fetch }

declare const env: {
  /** Secrets de functions: SOLO variables con prefijo FN_ (p. ej. FN_STRIPE_KEY). Devuelve "" si no existe. */
  get(name: string): string
}

declare const jobs: {
  /** Encola un trabajo para el worker fn.job(name). Devuelve el id. */
  enqueue(name: string, payload?: unknown, opts?: { delaySeconds?: number }): number
}

declare const console: {
  log(...args: unknown[]): void
  warn(...args: unknown[]): void
  error(...args: unknown[]): void
}
declare function log(...args: unknown[]): void

/** Pausa la ejecución (máx 10000 ms). */
declare function sleep(ms: number): void
