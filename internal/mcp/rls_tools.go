// RLS tools: ver y configurar la seguridad por filas desde el MCP, con el
// mismo motor que usa el dashboard (SetTableRLS y compañía). El agente puede
// así dejar una tabla protegida sin pedirle al usuario que abra la UI.
package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

func (h *Handler) registerRLSTools() {
	listRLS := mcp.NewTool("list_rls",
		mcp.WithDescription(
			"Row-level security status. Without arguments: every table with a "+
				"managed RLS mode. With table: full status of that table "+
				"(mode, owner column, policies).",
		),
		mcp.WithString("table",
			mcp.Description("Optional: exact table name to inspect in detail."),
		),
		mcp.WithToolAnnotation(readOnlyAnnotation),
		mcp.WithTitleAnnotation("List RLS config"),
	)
	h.mcp.AddTool(listRLS, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if table := req.GetString("table", ""); table != "" {
			status, err := h.db.GetTableRLSStatus(ctx, table)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return jsonResult(status)
		}
		configs, err := h.db.ListRLSConfig(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"tables": configs})
	})

	setRLS := mcp.NewTool("set_rls",
		mcp.WithDescription(
			"Enable/configure row-level security on a table. Modes: "+
				"'owner' (each app user only sees rows where owner_column = auth.uid(); "+
				"owner_column must be a uuid column and is defaulted to auth.uid() on insert), "+
				"'authenticated' (any logged-in user reads/writes everything), "+
				"'public' (anyone reads, logged-in users write), "+
				"'custom' (RLS enabled with NO policies — default-deny until you add "+
				"your own policies via execute_sql; auth.uid() is available in policy SQL). "+
				"SERVICE_KEY access always bypasses RLS. Idempotent: re-running switches mode.",
		),
		mcp.WithString("table", mcp.Required(), mcp.Description("Exact table name.")),
		mcp.WithString("mode", mcp.Required(), mcp.Description("owner | authenticated | public | custom")),
		mcp.WithString("owner_column",
			mcp.Description("Required for mode 'owner': uuid column that stores the owner's auth.uid()."),
		),
		mcp.WithToolAnnotation(writeAnnotation),
		mcp.WithTitleAnnotation("Set RLS mode"),
	)
	h.mcp.AddTool(setRLS, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		table, err := req.RequireString("table")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		mode, err := req.RequireString("mode")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := h.db.SetTableRLS(ctx, table, mode, req.GetString("owner_column", "")); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		status, err := h.db.GetTableRLSStatus(ctx, table)
		if err != nil {
			return jsonResult(map[string]any{"message": "RLS configured", "table": table, "mode": mode})
		}
		return jsonResult(status)
	})

	disableRLS := mcp.NewTool("disable_rls",
		mcp.WithDescription(
			"Disable row-level security on a table: drops the rapibase policies "+
				"and turns RLS off, so any request with a valid key can access "+
				"every row again. Ask the user before removing protection.",
		),
		mcp.WithString("table", mcp.Required(), mcp.Description("Exact table name.")),
		mcp.WithToolAnnotation(destructiveAnnotation),
		mcp.WithTitleAnnotation("Disable RLS"),
	)
	h.mcp.AddTool(disableRLS, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		table, err := req.RequireString("table")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := h.db.DisableTableRLS(ctx, table); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"message": "RLS disabled", "table": table})
	})
}
