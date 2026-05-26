package handlers

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/models"
)

// parseDelimiter maps the optional ?delimiter= query value to a rune for
// ImportCSV. Empty/"auto" yields 0, signalling the importer to auto-detect.
func parseDelimiter(q string) (rune, error) {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "", "auto":
		return 0, nil
	case "comma", ",":
		return ',', nil
	case "semicolon", ";":
		return ';', nil
	case "tab", "\\t", "\t":
		return '\t', nil
	case "pipe", "|":
		return '|', nil
	default:
		return 0, fmt.Errorf("invalid delimiter %q: use auto, comma, semicolon, tab, or pipe", q)
	}
}

// streamUploadedFile returns an io.Reader over the multipart "file" part of
// the request. It tries two paths, in order:
//
//  1. Network-streaming: when fasthttp exposes the body as a live stream
//     (StreamRequestBody=true AND body is large enough that fasthttp didn't
//     preload it), parse multipart on the fly. No disk spill, no extra RAM.
//
//  2. Buffered fallback: fasthttp preloads small bodies into memory even
//     with stream mode on, so BodyStream() returns nil. Fall back to
//     c.FormFile, which uses fasthttp's parsed multipart form. Large files
//     are spilled to a temp disk file and then streamed from there.
//
// Returns ok=false (without error) only when the request is not multipart
// at all, so the caller can fall back to raw-body parsing for curl-style
// JSON/SQL posts.
func streamUploadedFile(c *fiber.Ctx) (io.Reader, func(), bool, error) {
	ct := c.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	isMultipart := err == nil && strings.HasPrefix(mediaType, "multipart/")
	if !isMultipart {
		return nil, func() {}, false, nil
	}

	if body := c.Request().BodyStream(); body != nil {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, func() {}, false, fmt.Errorf("missing multipart boundary")
		}
		mr := multipart.NewReader(body, boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil, func() {}, false, fmt.Errorf("no file part in multipart upload")
			}
			if err != nil {
				return nil, func() {}, false, fmt.Errorf("multipart parse error: %w", err)
			}
			if part.FormName() == "file" {
				return part, func() { _ = part.Close() }, true, nil
			}
			_ = part.Close()
		}
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return nil, func() {}, false, fmt.Errorf("no file part in multipart upload: %w", err)
	}
	f, err := fh.Open()
	if err != nil {
		return nil, func() {}, false, fmt.Errorf("failed to open uploaded file: %w", err)
	}
	return f, func() { _ = f.Close() }, true, nil
}

type QueryHandler struct {
	db *database.DB
}

func NewQueryHandler(db *database.DB) *QueryHandler {
	return &QueryHandler{db: db}
}

// ExecuteQuery executes a raw SQL query
func (h *QueryHandler) ExecuteQuery(c *fiber.Ctx) error {
	var req models.QueryRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.SQL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "SQL query is required",
		})
	}

	// Block operations on internal tables
	upperSQL := strings.ToUpper(req.SQL)
	if strings.Contains(upperSQL, "_RAPIBASE_") {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Cannot operate on internal tables",
		})
	}

	result, err := h.db.ExecuteQuery(c.Context(), req.SQL, req.Params)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(result)
}

// ImportSQL imports data from SQL
func (h *QueryHandler) ImportSQL(c *fiber.Ctx) error {
	reader, cleanup, ok, err := streamUploadedFile(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		var req struct {
			SQL string `json:"sql"`
		}
		if err := c.BodyParser(&req); err != nil || req.SQL == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "SQL file or content is required",
			})
		}
		affected, err := h.db.ImportSQL(c.Context(), strings.NewReader(req.SQL))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"message": "Import completed", "rows_affected": affected})
	}
	defer cleanup()

	affected, err := h.db.ImportSQL(c.Context(), reader)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "Import completed", "rows_affected": affected})
}

// ImportJSON imports data from JSON with auto-column creation
func (h *QueryHandler) ImportJSON(c *fiber.Ctx) error {
	tableName := c.Params("table")
	if tableName == "" {
		tableName = c.Query("table")
	}
	if tableName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name is required",
		})
	}
	if !database.IsValidIdentifier(tableName) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid table name: only letters, numbers and underscores (max 63 chars, cannot start with a number)",
		})
	}
	autoCreate := c.Query("auto_create", "true") == "true"

	reader, cleanup, ok, err := streamUploadedFile(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		reader = bytes.NewReader(c.Body())
		cleanup = func() {}
	}
	defer cleanup()

	affected, err := h.db.ImportJSON(c.Context(), tableName, reader, autoCreate)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "Import completed", "rows_affected": affected})
}

// ImportCSV imports data from CSV with auto-column creation
func (h *QueryHandler) ImportCSV(c *fiber.Ctx) error {
	tableName := c.Params("table")
	if tableName == "" {
		tableName = c.Query("table")
	}
	if tableName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name is required",
		})
	}
	if !database.IsValidIdentifier(tableName) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid table name: only letters, numbers and underscores (max 63 chars, cannot start with a number)",
		})
	}
	autoCreate := c.Query("auto_create", "true") == "true"

	delim, err := parseDelimiter(c.Query("delimiter"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	reader, cleanup, ok, err := streamUploadedFile(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "CSV file is required (multipart upload)"})
	}
	defer cleanup()

	affected, err := h.db.ImportCSV(c.Context(), tableName, reader, autoCreate, delim)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"message": "Import completed", "rows_affected": affected})
}

// ExportTable exports a table
func (h *QueryHandler) ExportTable(c *fiber.Ctx) error {
	tableName := c.Params("table")
	if tableName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name is required",
		})
	}

	format := c.Query("format", "json")

	var buf bytes.Buffer

	switch format {
	case "json":
		if err := h.db.ExportTableJSON(c.Context(), tableName, &buf); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
		c.Set("Content-Type", "application/json")
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.json", tableName))

	case "sql":
		if err := h.db.ExportTableSQL(c.Context(), tableName, &buf); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}
		c.Set("Content-Type", "application/sql")
		c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.sql", tableName))

	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid format. Use 'json' or 'sql'",
		})
	}

	return c.Send(buf.Bytes())
}
