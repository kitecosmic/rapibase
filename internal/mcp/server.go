// Package mcp embeds a Model Context Protocol server inside RapiBase.
//
// The server exposes the existing database and storage layers to AI agents
// via the streamable-http transport. It is mounted in the same Fiber app
// behind the standard apikey middleware, so the agent connects with one URL
// and one SERVICE_KEY — there are no extra processes, ports, or env vars.
//
// Design notes:
//
//   - **Tools call the data layer directly.** No internal REST hop, no
//     double JSON serialisation, no per-request HTTP client. A panic in a
//     tool is caught by `server.WithRecovery()` and surfaced as a tool
//     error, so the dashboard keeps serving.
//
//   - **Generic tools, not per-table tools.** The agent passes `table` as
//     an argument; new tables show up immediately without a restart. This
//     is the pattern Anthropic recommends in the MCP design guidelines.
//
//   - **Stateless transport.** Every request is processed standalone. No
//     server-side session storage, no sticky sessions for load balancers.
//     Authentication is whatever Fiber middleware ran in front.
//
//   - **Errors are tool results, not protocol errors.** A failed query
//     comes back as `mcp.NewToolResultError(...)` so the model reads the
//     message and self-corrects, instead of the whole call aborting.
package mcp

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/server"

	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/storage"
)

const (
	serverName    = "rapibase"
	serverVersion = "1.0.0"

	serverInstructions = `RapiBase MCP — talk to a RapiBase instance (Postgres + S3 storage).

Recommended flow:

1. Start by reading the resource ` + "`rapibase://tables`" + ` (or call ` + "`list_tables`" + `)
   to discover what data is available. Each entry includes columns, types
   and primary key — enough to plan a CRUD operation without guessing.
2. Use ` + "`query_rows`" + ` to read data. The ` + "`filters`" + ` argument supports the
   operators eq, neq, gt, gte, lt, lte, like, ilike, is, in (AND-combined).
3. Use ` + "`insert_row`" + ` / ` + "`update_row`" + ` / ` + "`delete_row`" + ` to mutate. Pass the
   primary-key value as ` + "`row_id`" + ` for update/delete.
4. ` + "`execute_sql`" + ` runs raw Postgres against the DB. Prefer parameterised
   queries with $1, $2, ... placeholders. Internal RapiBase tables
   (prefixed _rapibase_) are blocked server-side.
5. ` + "`create_table`" + ` / ` + "`drop_table`" + ` change the schema. Only invoke when the
   user explicitly asks to add or remove a table.
6. Storage tools (list_buckets, list_objects, upload_object, ...) talk to
   the bundled MinIO. Files are exchanged as base64 strings.

Beyond MCP, the same instance serves the full HTTP API the app you are
building will consume: auth for end users at /api/v1/auth/*, REST CRUD at
/api/v1/rest/{table} (filters as column.op=value query params), Postgres
functions at /api/v1/rpc/{fn}, and storage at /api/v1/storage/*.

Realtime: the instance also has a WebSocket for live data (postgres
changes, broadcast, presence) at /api/realtime/v1 — auth via ?apikey=
(+ optional user ?token=), and the subprotocol rapibase-realtime.v1+json
is REQUIRED. Full protocol docs: GET /api/realtime/docs (markdown). Use it
when the app needs live updates instead of polling query_rows.

Functions: server-side TypeScript running inside the instance — custom
HTTP endpoints (/api/fn/{name}), cron schedules and queue workers, with
sync APIs (db.query, fetch, env.get("FN_*") secrets, jobs.enqueue).
Deploy a file with POST /api/v1/functions (multipart "file", service key);
compile errors come back in the response. Full type definitions:
GET /api/v1/functions/types. Use functions for logic that must stay
server-side: third-party API keys, webhooks, background work.

pgvector: the "vector" type is available for embeddings — create a column
"embedding vector(1536)", an HNSW index with vector_cosine_ops, and query
nearest neighbours with ORDER BY embedding <=> $1::vector. If
CREATE EXTENSION failed at boot (older Postgres image), the startup log
says so.

If this instance is managed by Rapibase Cloud, the project's console
(Conectar tab) serves a per-project SKILL.md with these URLs and keys
pre-filled, plus deploy instructions for publishing the frontend.

Authentication is handled by the API key sent with the request. The agent
never sees credentials.`
)

// WebhookDispatcher matches the dispatcher in internal/api so MCP-driven
// mutations fire the same hooks as REST API calls. Decoupled via interface
// to keep this package free of cyclic imports against internal/api.
type WebhookDispatcher interface {
	Dispatch(ctx context.Context, eventType, tableName string, data, oldData map[string]interface{})
}

// Handler bundles the MCP server with its HTTP transport so it can be
// mounted by the parent Fiber app via adaptor.HTTPHandler.
type Handler struct {
	mcp  *server.MCPServer
	http http.Handler

	db                *database.DB
	storage           *storage.Client
	webhookDispatcher WebhookDispatcher
}

// NewHandler builds an MCP server with every tool and resource registered.
//
// The streamable-http transport is configured stateless (no per-client
// session) — auth is the upstream Fiber middleware's job, and the server
// holds no state between requests.
func NewHandler(db *database.DB, storageClient *storage.Client, dispatcher WebhookDispatcher) *Handler {
	mcpSrv := server.NewMCPServer(
		serverName,
		serverVersion,
		server.WithInstructions(serverInstructions),
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithRecovery(),
	)

	h := &Handler{
		mcp:               mcpSrv,
		db:                db,
		storage:           storageClient,
		webhookDispatcher: dispatcher,
	}

	h.registerTools()
	h.registerResources()

	h.http = server.NewStreamableHTTPServer(
		mcpSrv,
		server.WithStateLess(true),
		server.WithEndpointPath("/mcp"),
	)

	return h
}

// ServeHTTP delegates to the streamable-http transport. Implements
// net/http.Handler so the parent app can mount it via adaptor.HTTPHandler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.http.ServeHTTP(w, r)
}
