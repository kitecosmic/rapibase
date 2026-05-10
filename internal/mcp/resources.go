package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerResources exposes the table catalogue as MCP resources.
//
// Resources are read by Claude without spending tool budget — perfect for
// "what data is available?" lookups that happen at the start of most
// conversations. The same information is reachable through the
// `list_tables` / `describe_table` tools, but advertising it here lets
// the client surface it eagerly in the UI.
func (h *Handler) registerResources() {
	tables := mcp.NewResource(
		"rapibase://tables",
		"Tables",
		mcp.WithResourceDescription(
			"Live list of user tables in the database, with columns and row "+
				"counts. Refreshed on every read.",
		),
		mcp.WithMIMEType("application/json"),
	)
	h.mcp.AddResource(tables, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		list, err := h.db.GetTables(ctx)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(map[string]any{"tables": list})
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(body),
			},
		}, nil
	})

	tableSchema := mcp.NewResourceTemplate(
		"rapibase://tables/{name}",
		"Table schema",
		mcp.WithTemplateDescription(
			"Schema (columns, types, constraints, primary key, row count) "+
				"for a single table. The {name} segment must match a table "+
				"reported by `rapibase://tables`.",
		),
		mcp.WithTemplateMIMEType("application/json"),
	)
	h.mcp.AddResourceTemplate(tableSchema, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		// The SDK matched the URI against our template but doesn't auto-extract
		// path parameters into req.Params.Arguments — pull `{name}` ourselves.
		name := strings.TrimPrefix(req.Params.URI, "rapibase://tables/")
		schema, err := h.db.GetTableSchema(ctx, name)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(schema)
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(body),
			},
		}, nil
	})
}
