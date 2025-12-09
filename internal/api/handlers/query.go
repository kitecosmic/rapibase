package handlers

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/models"
)

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
	// Get file from form
	file, err := c.FormFile("file")
	if err != nil {
		// Try to get SQL from body
		var req struct {
			SQL string `json:"sql"`
		}
		if err := c.BodyParser(&req); err != nil || req.SQL == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "SQL file or content is required",
			})
		}

		reader := strings.NewReader(req.SQL)
		affected, err := h.db.ImportSQL(c.Context(), reader)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		return c.JSON(fiber.Map{
			"message":       "Import completed",
			"rows_affected": affected,
		})
	}

	// Open file
	f, err := file.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to open file",
		})
	}
	defer f.Close()

	affected, err := h.db.ImportSQL(c.Context(), f)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message":       "Import completed",
		"rows_affected": affected,
	})
}

// ImportJSON imports data from JSON
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

	// Get file from form
	file, err := c.FormFile("file")
	if err != nil {
		// Try to get JSON from body
		reader := bytes.NewReader(c.Body())
		affected, err := h.db.ImportJSON(c.Context(), tableName, reader)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		return c.JSON(fiber.Map{
			"message":       "Import completed",
			"rows_affected": affected,
		})
	}

	// Open file
	f, err := file.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to open file",
		})
	}
	defer f.Close()

	affected, err := h.db.ImportJSON(c.Context(), tableName, f)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message":       "Import completed",
		"rows_affected": affected,
	})
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
