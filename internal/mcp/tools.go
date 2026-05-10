package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/rapibase/rapibase/internal/models"
	"github.com/rapibase/rapibase/internal/webhooks"
)

// registerTools wires every tool into the MCP server. Grouped roughly in
// order of intended discovery (table → CRUD → SQL → DDL → storage) so the
// `tools/list` payload reads like a tutorial.
func (h *Handler) registerTools() {
	h.registerDiscoveryTools()
	h.registerCRUDTools()
	h.registerSQLTools()
	h.registerDDLTools()
	if h.storage != nil {
		h.registerStorageTools()
	}
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

func (h *Handler) registerDiscoveryTools() {
	listTables := mcp.NewTool("list_tables",
		mcp.WithDescription(
			"List every user table in the RapiBase database, with row count, "+
				"primary key and full column definitions (name, type, nullable, "+
				"default, foreign key references).\n\n"+
				"Internal RapiBase tables (prefixed `_rapibase_`) are filtered "+
				"server-side. Call this first when the user asks about data "+
				"without naming a specific table — the response is cheap.",
		),
	)
	h.mcp.AddTool(listTables, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tables, err := h.db.GetTables(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if tables == nil {
			tables = []models.TableInfo{}
		}
		return jsonResult(map[string]any{"tables": tables})
	})

	describeTable := mcp.NewTool("describe_table",
		mcp.WithDescription(
			"Return the full schema of a single table: columns, types, "+
				"nullability, defaults, primary key and foreign-key references. "+
				"Faster than parsing `list_tables` when the agent already knows "+
				"which table it wants. Call this before INSERT/UPDATE so you "+
				"know which columns are required.",
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Exact table name (case-sensitive)."),
		),
	)
	h.mcp.AddTool(describeTable, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		schema, err := h.db.GetTableSchema(ctx, name)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(schema)
	})
}

// ---------------------------------------------------------------------------
// CRUD over the same database layer the REST API uses
// ---------------------------------------------------------------------------

// filterItemSchema is the JSON Schema used as `items` of the `filters` array.
// Defined as a package var so the tool registration block stays readable.
var filterItemSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"column": map[string]any{
			"type":        "string",
			"description": "Column name to filter on.",
		},
		"operator": map[string]any{
			"type":        "string",
			"enum":        []string{"eq", "neq", "gt", "gte", "lt", "lte", "like", "ilike", "is", "in"},
			"description": "Comparison operator.",
		},
		"value": map[string]any{
			"type":        "string",
			"description": "Value as a string. RapiBase casts based on column type.",
		},
	},
	"required":             []string{"column", "operator", "value"},
	"additionalProperties": false,
}

func (h *Handler) registerCRUDTools() {
	queryRows := mcp.NewTool("query_rows",
		mcp.WithDescription(
			"Read paginated rows from a table.\n\n"+
				"Returns `{data, page, page_size, total_rows, total_pages}`.\n\n"+
				"Filter operators (AND-combined): eq, neq, gt, gte, lt, lte, "+
				"like, ilike, is, in.\n\n"+
				"Examples:\n"+
				"  • price > 100: `[{column:\"price\", operator:\"gt\", value:\"100\"}]`\n"+
				"  • name contains phone: `[{column:\"name\", operator:\"ilike\", value:\"%phone%\"}]`\n"+
				"  • not deleted: `[{column:\"deleted_at\", operator:\"is\", value:\"null\"}]`\n\n"+
				"For large tables prefer `page_size <= 100` and a stable "+
				"`order_by` column to keep pagination consistent across calls.",
		),
		mcp.WithString("table",
			mcp.Required(),
			mcp.Description("Table name to read from."),
		),
		mcp.WithNumber("page",
			mcp.Description("1-based page number. Default 1."),
			mcp.DefaultNumber(1),
			mcp.Min(1),
		),
		mcp.WithNumber("page_size",
			mcp.Description("Rows per page. Max 1000. Default 50."),
			mcp.DefaultNumber(50),
			mcp.Min(1),
			mcp.Max(1000),
		),
		mcp.WithString("order_by",
			mcp.Description("Column to sort by. Omit for unsorted (insertion order)."),
		),
		mcp.WithString("order",
			mcp.Description("Sort direction. Default asc."),
			mcp.Enum("asc", "desc"),
			mcp.DefaultString("asc"),
		),
		mcp.WithString("select",
			mcp.Description("Comma-separated columns to return. Omit for all columns."),
		),
		mcp.WithArray("filters",
			mcp.Description("Optional WHERE-style filters. AND-combined."),
			mcp.Items(filterItemSchema),
		),
	)
	h.mcp.AddTool(queryRows, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		table, err := req.RequireString("table")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		params := models.PaginationParams{
			Page:     req.GetInt("page", 1),
			PageSize: req.GetInt("page_size", 50),
			OrderBy:  req.GetString("order_by", ""),
			Order:    req.GetString("order", "asc"),
			Select:   req.GetString("select", ""),
		}
		if raw, ok := req.GetArguments()["filters"].([]any); ok {
			for _, item := range raw {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				params.Filters = append(params.Filters, models.FilterCondition{
					Column:   stringOf(m["column"]),
					Operator: stringOf(m["operator"]),
					Value:    stringOf(m["value"]),
				})
			}
		}
		result, err := h.db.GetRows(ctx, table, params)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)
	})

	insertRow := mcp.NewTool("insert_row",
		mcp.WithDescription(
			"Insert a single row and return the inserted record. Call "+
				"`describe_table` first if you're unsure which columns are "+
				"required. RapiBase fills `id`, `created_at`, `updated_at` "+
				"automatically when the schema defines them.",
		),
		mcp.WithString("table",
			mcp.Required(),
			mcp.Description("Target table."),
		),
		mcp.WithObject("data",
			mcp.Required(),
			mcp.Description("Column → value mapping. Skip auto columns (id, created_at)."),
			mcp.AdditionalProperties(true),
		),
	)
	h.mcp.AddTool(insertRow, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		table, err := req.RequireString("table")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		data, ok := req.GetArguments()["data"].(map[string]any)
		if !ok {
			return mcp.NewToolResultError("'data' must be an object"), nil
		}
		row, err := h.db.InsertRow(ctx, table, data)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if h.webhookDispatcher != nil {
			h.webhookDispatcher.Dispatch(ctx, webhooks.EventInsert, table, row, nil)
		}
		return jsonResult(row)
	})

	updateRow := mcp.NewTool("update_row",
		mcp.WithDescription(
			"Update a single row by primary key. Returns the updated record.",
		),
		mcp.WithString("table",
			mcp.Required(),
			mcp.Description("Target table."),
		),
		mcp.WithString("row_id",
			mcp.Required(),
			mcp.Description("Primary key value of the row to update."),
		),
		mcp.WithObject("data",
			mcp.Required(),
			mcp.Description("Partial column → value mapping. Only include columns to change."),
			mcp.AdditionalProperties(true),
		),
	)
	h.mcp.AddTool(updateRow, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		table, err := req.RequireString("table")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rowID, err := req.RequireString("row_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		data, ok := req.GetArguments()["data"].(map[string]any)
		if !ok {
			return mcp.NewToolResultError("'data' must be an object"), nil
		}
		var oldData map[string]any
		if h.webhookDispatcher != nil {
			oldData, _ = h.db.GetRowByID(ctx, table, rowID)
		}
		row, err := h.db.UpdateRow(ctx, table, rowID, data)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if h.webhookDispatcher != nil {
			h.webhookDispatcher.Dispatch(ctx, webhooks.EventUpdate, table, row, oldData)
		}
		return jsonResult(row)
	})

	deleteRow := mcp.NewTool("delete_row",
		mcp.WithDescription(
			"Delete a single row by primary key. **Destructive** — confirm "+
				"with the user before invoking on production data unless they "+
				"explicitly authorised the deletion.",
		),
		mcp.WithString("table",
			mcp.Required(),
			mcp.Description("Target table."),
		),
		mcp.WithString("row_id",
			mcp.Required(),
			mcp.Description("Primary key of the row to delete."),
		),
	)
	h.mcp.AddTool(deleteRow, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		table, err := req.RequireString("table")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rowID, err := req.RequireString("row_id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var deletedData map[string]any
		if h.webhookDispatcher != nil {
			deletedData, _ = h.db.GetRowByID(ctx, table, rowID)
		}
		if err := h.db.DeleteRow(ctx, table, rowID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if h.webhookDispatcher != nil {
			if deletedData == nil {
				deletedData = map[string]any{"id": rowID}
			}
			h.webhookDispatcher.Dispatch(ctx, webhooks.EventDelete, table, deletedData, nil)
		}
		return jsonResult(map[string]any{"message": "Row deleted successfully"})
	})
}

// ---------------------------------------------------------------------------
// Raw SQL
// ---------------------------------------------------------------------------

func (h *Handler) registerSQLTools() {
	executeSQL := mcp.NewTool("execute_sql",
		mcp.WithDescription(
			"Execute a parameterised Postgres SQL statement.\n\n"+
				"Returns `{columns, rows, rows_affected, duration}`. Reads, "+
				"writes and DDL are all allowed — except touching internal "+
				"`_rapibase_*` tables, which the server blocks.\n\n"+
				"Prefer `query_rows` / `insert_row` / `update_row` / "+
				"`delete_row` for ordinary CRUD and reserve this tool for "+
				"joins, aggregates, migrations or ad-hoc analysis. Use $1, "+
				"$2, … placeholders and pass values via `params` to avoid "+
				"injection.",
		),
		mcp.WithString("sql",
			mcp.Required(),
			mcp.Description("Raw Postgres SQL with $1, $2, … placeholders for parameters."),
		),
		mcp.WithArray("params",
			mcp.Description("Positional parameters substituted for $1, $2, … in the SQL."),
		),
	)
	h.mcp.AddTool(executeSQL, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sql, err := req.RequireString("sql")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Same guardrail as the REST handler — protect internal tables.
		if strings.Contains(strings.ToUpper(sql), "_RAPIBASE_") {
			return mcp.NewToolResultError("Cannot operate on internal RapiBase tables"), nil
		}
		var params []any
		if raw, ok := req.GetArguments()["params"].([]any); ok {
			params = raw
		}
		result, err := h.db.ExecuteQuery(ctx, sql, params)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)
	})
}

// ---------------------------------------------------------------------------
// DDL
// ---------------------------------------------------------------------------

var createColumnSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"name": map[string]any{
			"type":        "string",
			"description": "Column name (snake_case recommended).",
		},
		"type": map[string]any{
			"type":        "string",
			"description": "Postgres type. Common: text, varchar, int, bigint, boolean, timestamp, jsonb, uuid, numeric.",
		},
		"nullable": map[string]any{
			"type":        "boolean",
			"description": "Whether the column allows NULL.",
		},
		"default_value": map[string]any{
			"type":        "string",
			"description": "Default expression, e.g. 'now()' or '0'. Quote literals as needed.",
		},
		"is_primary_key": map[string]any{
			"type":        "boolean",
			"description": "Mark as primary key.",
		},
		"is_unique": map[string]any{
			"type":        "boolean",
			"description": "Add a UNIQUE constraint.",
		},
	},
	"required":             []string{"name", "type"},
	"additionalProperties": false,
}

func (h *Handler) registerDDLTools() {
	createTable := mcp.NewTool("create_table",
		mcp.WithDescription(
			"Create a new table. Schema-changing — confirm with the user "+
				"before adding tables on a shared database. Mark exactly one "+
				"column as primary key.",
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("New table name (snake_case)."),
		),
		mcp.WithArray("columns",
			mcp.Required(),
			mcp.Description("Ordered list of columns."),
			mcp.Items(createColumnSchema),
		),
	)
	h.mcp.AddTool(createTable, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		raw, ok := req.GetArguments()["columns"].([]any)
		if !ok || len(raw) == 0 {
			return mcp.NewToolResultError("'columns' must be a non-empty array"), nil
		}
		columns := make([]models.CreateColumnSpec, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			col := models.CreateColumnSpec{
				Name:         stringOf(m["name"]),
				Type:         stringOf(m["type"]),
				Nullable:     boolOf(m["nullable"], true),
				IsPrimaryKey: boolOf(m["is_primary_key"], false),
				IsUnique:     boolOf(m["is_unique"], false),
			}
			if dv, ok := m["default_value"].(string); ok && dv != "" {
				col.DefaultValue = &dv
			}
			columns = append(columns, col)
		}
		if err := h.db.CreateTable(ctx, models.CreateTableRequest{Name: name, Columns: columns}); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{
			"message": "Table created successfully",
			"name":    name,
		})
	})

	dropTable := mcp.NewTool("drop_table",
		mcp.WithDescription(
			"Drop a table and **all its rows**. Irreversible. Only invoke "+
				"after explicit user confirmation.",
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Exact name of the table to drop."),
		),
	)
	h.mcp.AddTool(dropTable, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := h.db.DropTable(ctx, name); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"message": "Table dropped successfully"})
	})
}

// ---------------------------------------------------------------------------
// Storage (S3-compatible via MinIO)
// ---------------------------------------------------------------------------

func (h *Handler) registerStorageTools() {
	listBuckets := mcp.NewTool("list_buckets",
		mcp.WithDescription("List every storage bucket (S3-compatible, MinIO-backed)."),
	)
	h.mcp.AddTool(listBuckets, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		buckets, err := h.storage.ListBuckets(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"buckets": buckets})
	})

	listObjects := mcp.NewTool("list_objects",
		mcp.WithDescription("List objects (files / folders) inside a bucket."),
		mcp.WithString("bucket",
			mcp.Required(),
			mcp.Description("Bucket name."),
		),
		mcp.WithString("prefix",
			mcp.Description("Optional path prefix, e.g. 'avatars/'. Default: list all."),
		),
	)
	h.mcp.AddTool(listObjects, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		prefix := req.GetString("prefix", "")
		objs, err := h.storage.ListObjects(ctx, bucket, prefix)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"objects": objs, "prefix": prefix})
	})

	downloadObject := mcp.NewTool("download_object",
		mcp.WithDescription(
			"Download an object. Returns content as base64 plus its MIME "+
				"type. For text files the agent can decode and inspect; for "+
				"binaries (images, PDFs) the agent can re-upload to other tools.",
		),
		mcp.WithString("bucket",
			mcp.Required(),
			mcp.Description("Bucket name."),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Object key, e.g. 'avatars/me.png'."),
		),
	)
	h.mcp.AddTool(downloadObject, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		key, err := req.RequireString("key")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		obj, info, err := h.storage.GetObject(ctx, bucket, key)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		defer obj.Close()
		body, err := io.ReadAll(obj)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		contentType := "application/octet-stream"
		if info != nil && info.ContentType != "" {
			contentType = info.ContentType
		}
		return jsonResult(map[string]any{
			"bucket":         bucket,
			"key":            key,
			"size":           len(body),
			"content_type":   contentType,
			"content_base64": base64.StdEncoding.EncodeToString(body),
		})
	})

	uploadObject := mcp.NewTool("upload_object",
		mcp.WithDescription(
			"Upload a file to a bucket. Provide content as base64. Returns "+
				"the stored object's `key`, `size`, `etag` and public URL "+
				"(if the bucket is public).",
		),
		mcp.WithString("bucket",
			mcp.Required(),
			mcp.Description("Target bucket."),
		),
		mcp.WithString("filename",
			mcp.Required(),
			mcp.Description("Filename to use (becomes part of the object key)."),
		),
		mcp.WithString("content_base64",
			mcp.Required(),
			mcp.Description("File contents, base64-encoded."),
		),
		mcp.WithString("path",
			mcp.Description("Optional path prefix, e.g. 'avatars/'. Trailing slash optional."),
		),
		mcp.WithString("content_type",
			mcp.Description("MIME type. Default application/octet-stream."),
			mcp.DefaultString("application/octet-stream"),
		),
	)
	h.mcp.AddTool(uploadObject, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		filename, err := req.RequireString("filename")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		b64, err := req.RequireString("content_base64")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("content_base64 is not valid base64: %v", err)), nil
		}
		path := req.GetString("path", "")
		contentType := req.GetString("content_type", "application/octet-stream")
		key := path + filename
		result, err := h.storage.UploadObject(ctx, bucket, key, strings.NewReader(string(raw)), int64(len(raw)), contentType)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)
	})

	deleteObject := mcp.NewTool("delete_object",
		mcp.WithDescription("Delete an object from a bucket. Irreversible."),
		mcp.WithString("bucket",
			mcp.Required(),
			mcp.Description("Bucket name."),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Object key."),
		),
	)
	h.mcp.AddTool(deleteObject, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		key, err := req.RequireString("key")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := h.storage.DeleteObject(ctx, bucket, key); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"message": "Object deleted successfully"})
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// jsonResult marshals v to JSON and returns it as the tool's text content.
// Tool results are surfaced to the model as text — JSON is the agreed wire
// format for structured payloads.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolOf(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}
