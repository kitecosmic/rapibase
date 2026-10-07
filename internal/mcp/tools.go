package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/rapibase/rapibase/internal/models"
	"github.com/rapibase/rapibase/internal/storage"
	"github.com/rapibase/rapibase/internal/webhooks"
)

// Tool annotations, per the MCP spec. The mark3labs/mcp-go SDK defaults
// every tool to {readOnly:false, destructive:true, idempotent:false},
// which makes well-behaved clients (Claude Desktop, etc.) ask for human
// confirmation before *every* call — even pure reads. We override that by
// classifying each tool into one of five buckets:
//
//   - readOnlyAnnotation: pure reads. No confirmation needed.
//   - writeAnnotation: mutates state, every call has a fresh effect (insert).
//   - idempotentWriteAnnotation: mutates state, repeated calls converge
//     to the same result (update by PK, overwrite a file).
//   - destructiveAnnotation: irreversible mutation; clients should warn.
//   - destructiveNonIdempotentAnnotation: raw SQL — could be anything,
//     so we pick the most conservative combination.
//
// OpenWorldHint is always true: every tool reaches the database or storage,
// which are external systems from the model's perspective.
var (
	readOnlyAnnotation = mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(true),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
	writeAnnotation = mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(false),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(false),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
	idempotentWriteAnnotation = mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(false),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
	destructiveAnnotation = mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(false),
		DestructiveHint: mcp.ToBoolPtr(true),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
	destructiveNonIdempotentAnnotation = mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(false),
		DestructiveHint: mcp.ToBoolPtr(true),
		IdempotentHint:  mcp.ToBoolPtr(false),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
)

// registerTools wires every tool into the MCP server. Grouped roughly in
// order of intended discovery (table → CRUD → SQL → DDL → storage) so the
// `tools/list` payload reads like a tutorial.
func (h *Handler) registerTools() {
	h.registerDiscoveryTools()
	h.registerCRUDTools()
	h.registerSQLTools()
	h.registerDDLTools()
	h.registerRLSTools()
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
		// WithToolAnnotation replaces the whole struct, so call it first and
		// then layer the title on top.
		mcp.WithToolAnnotation(readOnlyAnnotation),
		mcp.WithTitleAnnotation("List tables"),
	)
	h.mcp.AddTool(listTables, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tables, err := h.db.GetTablesWithColumns(ctx)
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
		mcp.WithToolAnnotation(readOnlyAnnotation),
		mcp.WithTitleAnnotation("Describe table"),
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
		mcp.WithToolAnnotation(readOnlyAnnotation),
		mcp.WithTitleAnnotation("Query rows"),
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
		mcp.WithToolAnnotation(writeAnnotation),
		mcp.WithTitleAnnotation("Insert row"),
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
		mcp.WithToolAnnotation(idempotentWriteAnnotation),
		mcp.WithTitleAnnotation("Update row"),
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
		mcp.WithToolAnnotation(destructiveAnnotation),
		mcp.WithTitleAnnotation("Delete row"),
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
		mcp.WithToolAnnotation(destructiveNonIdempotentAnnotation),
		mcp.WithTitleAnnotation("Execute SQL"),
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
		mcp.WithToolAnnotation(writeAnnotation),
		mcp.WithTitleAnnotation("Create table"),
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
		mcp.WithToolAnnotation(destructiveAnnotation),
		mcp.WithTitleAnnotation("Drop table"),
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
// Storage (buckets and access rules in Postgres, files in the S3 backend)
// ---------------------------------------------------------------------------

// mcpDownloadLimit caps download_object: base64 in a tool result is
// expensive for the model; bigger files go through create_signed_url.
const mcpDownloadLimit = 10 << 20

func storageToolError(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(err.Error()), nil
}

// bucketSettingsFrom reads the optional bucket fields present in the
// call (absent = unchanged).
func bucketSettingsFrom(req mcp.CallToolRequest) storage.BucketSettings {
	args := req.GetArguments()
	var set storage.BucketSettings
	if _, ok := args["public"]; ok {
		v := req.GetBool("public", false)
		set.Public = &v
	}
	if _, ok := args["file_size_limit"]; ok {
		v := int64(req.GetFloat("file_size_limit", 0))
		set.FileSizeLimit = &v
	}
	if _, ok := args["allowed_mime_types"]; ok {
		v := req.GetStringSlice("allowed_mime_types", []string{})
		set.AllowedMimeTypes = &v
	}
	return set
}

func (h *Handler) registerStorageTools() {
	bucketProps := []mcp.ToolOption{
		mcp.WithBoolean("public",
			mcp.Description("Public buckets serve every file to anyone at public_url (no key). Private buckets (default) need the API keys or a signed URL."),
		),
		mcp.WithNumber("file_size_limit",
			mcp.Description("Max size per file in bytes, enforced on upload. 0 removes the limit."),
		),
		mcp.WithArray("allowed_mime_types",
			mcp.Description(`Allowed MIME types, exact or wildcard: ["image/*", "application/pdf"]. Empty array allows every type.`),
			mcp.WithStringItems(),
		),
	}

	listBuckets := mcp.NewTool("list_buckets",
		mcp.WithDescription("List storage buckets with their settings (public, file_size_limit, allowed_mime_types) and usage (object_count, total_size in bytes)."),
		mcp.WithToolAnnotation(readOnlyAnnotation),
		mcp.WithTitleAnnotation("List buckets"),
	)
	h.mcp.AddTool(listBuckets, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		buckets, err := h.storage.ListBuckets(ctx)
		if err != nil {
			return storageToolError(err)
		}
		return jsonResult(map[string]any{"buckets": buckets})
	})

	createBucket := mcp.NewTool("create_bucket", append([]mcp.ToolOption{
		mcp.WithDescription("Create a storage bucket. Names: 3-63 lowercase letters, numbers, dots or hyphens."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Bucket name, e.g. 'avatars'.")),
		mcp.WithToolAnnotation(writeAnnotation),
		mcp.WithTitleAnnotation("Create bucket"),
	}, bucketProps...)...)
	h.mcp.AddTool(createBucket, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return storageToolError(err)
		}
		b, err := h.storage.CreateBucket(ctx, name, bucketSettingsFrom(req))
		if err != nil {
			return storageToolError(err)
		}
		return jsonResult(b)
	})

	updateBucket := mcp.NewTool("update_bucket", append([]mcp.ToolOption{
		mcp.WithDescription("Change a bucket's settings. Only the fields you pass change."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Bucket name.")),
		mcp.WithToolAnnotation(idempotentWriteAnnotation),
		mcp.WithTitleAnnotation("Update bucket"),
	}, bucketProps...)...)
	h.mcp.AddTool(updateBucket, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return storageToolError(err)
		}
		b, err := h.storage.UpdateBucket(ctx, name, bucketSettingsFrom(req))
		if err != nil {
			return storageToolError(err)
		}
		return jsonResult(b)
	})

	deleteBucket := mcp.NewTool("delete_bucket",
		mcp.WithDescription("Delete a bucket. It must be empty unless force=true, which also deletes every file in it. Irreversible."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Bucket name.")),
		mcp.WithBoolean("force", mcp.Description("Delete the bucket's files too. Default false.")),
		mcp.WithToolAnnotation(destructiveAnnotation),
		mcp.WithTitleAnnotation("Delete bucket"),
	)
	h.mcp.AddTool(deleteBucket, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, err := req.RequireString("name")
		if err != nil {
			return storageToolError(err)
		}
		if err := h.storage.DeleteBucket(ctx, name, req.GetBool("force", false)); err != nil {
			return storageToolError(err)
		}
		return jsonResult(map[string]any{"message": "Bucket deleted successfully"})
	})

	listObjects := mcp.NewTool("list_objects",
		mcp.WithDescription("List one folder level of a bucket: files (size, type, owner, and public_url for public buckets) and sub-folders (is_dir)."),
		mcp.WithString("bucket", mcp.Required(), mcp.Description("Bucket name.")),
		mcp.WithString("prefix", mcp.Description("Folder to list, e.g. 'avatars/'. Default: the bucket root.")),
		mcp.WithToolAnnotation(readOnlyAnnotation),
		mcp.WithTitleAnnotation("List objects"),
	)
	h.mcp.AddTool(listObjects, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return storageToolError(err)
		}
		prefix := req.GetString("prefix", "")
		entries, err := h.storage.ListFolder(ctx, bucket, prefix, false)
		if err != nil {
			return storageToolError(err)
		}
		return jsonResult(map[string]any{"objects": entries, "prefix": prefix})
	})

	downloadObject := mcp.NewTool("download_object",
		mcp.WithDescription(
			"Download a file (up to 10 MB) as base64 plus its MIME type. "+
				"For bigger files, or to hand a file to a browser, use create_signed_url.",
		),
		mcp.WithString("bucket", mcp.Required(), mcp.Description("Bucket name.")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Object path, e.g. 'avatars/me.png'.")),
		mcp.WithToolAnnotation(readOnlyAnnotation),
		mcp.WithTitleAnnotation("Download object"),
	)
	h.mcp.AddTool(downloadObject, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return storageToolError(err)
		}
		key, err := req.RequireString("key")
		if err != nil {
			return storageToolError(err)
		}
		st, err := h.storage.Stat(ctx, bucket, key)
		if err != nil {
			return storageToolError(err)
		}
		if st.Size > mcpDownloadLimit {
			return mcp.NewToolResultError(fmt.Sprintf("%s is %d bytes; download_object returns files up to 10 MB. Use create_signed_url to get a link instead.", key, st.Size)), nil
		}
		obj, err := h.storage.Open(ctx, bucket, key, nil)
		if err != nil {
			return storageToolError(err)
		}
		defer obj.Close()
		body, err := io.ReadAll(io.LimitReader(obj, mcpDownloadLimit+1))
		if err != nil {
			return storageToolError(err)
		}
		contentType := st.ContentType
		if contentType == "" {
			contentType = storage.DetectContentType("", key)
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
			"Upload a file (content as base64). Respects the bucket's size and MIME limits. "+
				"Returns the object with `url` (needs the API keys) and `public_url` when the bucket is public.",
		),
		mcp.WithString("bucket", mcp.Required(), mcp.Description("Target bucket.")),
		mcp.WithString("filename", mcp.Required(), mcp.Description("File name (the last part of the object path).")),
		mcp.WithString("content_base64", mcp.Required(), mcp.Description("File contents, base64-encoded.")),
		mcp.WithString("path", mcp.Description("Optional folder, e.g. 'avatars/'.")),
		mcp.WithString("content_type", mcp.Description("MIME type. Default: guessed from the file extension.")),
		mcp.WithString("owner_id", mcp.Description("Optional app-user UUID that will own the file (that user can then read, replace and delete it with their own session).")),
		mcp.WithBoolean("upsert", mcp.Description("Replace an existing file at that path. Default true.")),
		mcp.WithString("cache_control", mcp.Description("Browser cache for public files, in seconds (e.g. '86400'). Default 3600.")),
		mcp.WithToolAnnotation(idempotentWriteAnnotation),
		mcp.WithTitleAnnotation("Upload object"),
	)
	h.mcp.AddTool(uploadObject, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return storageToolError(err)
		}
		filename, err := req.RequireString("filename")
		if err != nil {
			return storageToolError(err)
		}
		b64, err := req.RequireString("content_base64")
		if err != nil {
			return storageToolError(err)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("content_base64 is not valid base64: %v", err)), nil
		}
		cache := req.GetString("cache_control", "")
		if cache != "" && !strings.Contains(cache, "=") {
			cache = "max-age=" + cache
		}
		obj, err := h.storage.Upload(ctx, storage.UploadInput{
			Bucket:       bucket,
			Key:          storage.JoinKey(req.GetString("path", ""), filename),
			Body:         bytes.NewReader(raw),
			Size:         int64(len(raw)),
			ContentType:  req.GetString("content_type", ""),
			CacheControl: cache,
			Owner:        req.GetString("owner_id", ""),
			Upsert:       req.GetBool("upsert", true),
			Actor:        storage.Actor{Service: true},
		})
		if err != nil {
			return storageToolError(err)
		}
		return jsonResult(obj)
	})

	deleteObject := mcp.NewTool("delete_object",
		mcp.WithDescription("Delete a file, or a whole folder when key ends with '/'. Irreversible."),
		mcp.WithString("bucket", mcp.Required(), mcp.Description("Bucket name.")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Object path, or a folder path ending in '/'.")),
		mcp.WithToolAnnotation(destructiveAnnotation),
		mcp.WithTitleAnnotation("Delete object"),
	)
	h.mcp.AddTool(deleteObject, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return storageToolError(err)
		}
		key, err := req.RequireString("key")
		if err != nil {
			return storageToolError(err)
		}
		if strings.HasSuffix(key, "/") {
			n, err := h.storage.DeleteFolder(ctx, bucket, key)
			if err != nil {
				return storageToolError(err)
			}
			return jsonResult(map[string]any{"message": "Folder deleted successfully", "deleted": n})
		}
		if err := h.storage.Delete(ctx, bucket, key); err != nil {
			return storageToolError(err)
		}
		return jsonResult(map[string]any{"message": "Object deleted successfully"})
	})

	signedURL := mcp.NewTool("create_signed_url",
		mcp.WithDescription("Create a temporary link that reads one file without any key (files in private buckets, or to share a file). Max 7 days."),
		mcp.WithString("bucket", mcp.Required(), mcp.Description("Bucket name.")),
		mcp.WithString("key", mcp.Required(), mcp.Description("Object path.")),
		mcp.WithNumber("expires_in", mcp.Description("Seconds until the link expires. Default 3600, max 604800 (7 days).")),
		mcp.WithBoolean("download", mcp.Description("Make browsers download the file instead of showing it. Default false.")),
		mcp.WithToolAnnotation(readOnlyAnnotation),
		mcp.WithTitleAnnotation("Create signed URL"),
	)
	h.mcp.AddTool(signedURL, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		bucket, err := req.RequireString("bucket")
		if err != nil {
			return storageToolError(err)
		}
		key, err := req.RequireString("key")
		if err != nil {
			return storageToolError(err)
		}
		if _, err := h.storage.Stat(ctx, bucket, key); err != nil {
			return storageToolError(err)
		}
		download := ""
		if req.GetBool("download", false) {
			download = "true"
		}
		ttl := time.Duration(req.GetFloat("expires_in", 3600)) * time.Second
		u, exp := h.storage.SignedURL(bucket, key, ttl, download)
		return jsonResult(map[string]any{"signed_url": u, "expires_at": exp.UTC()})
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
