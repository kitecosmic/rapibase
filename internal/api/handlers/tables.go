package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/models"
)

type TablesHandler struct {
	db *database.DB
}

func NewTablesHandler(db *database.DB) *TablesHandler {
	return &TablesHandler{db: db}
}

// ListTables returns all tables
func (h *TablesHandler) ListTables(c *fiber.Ctx) error {
	tables, err := h.db.GetTables(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if tables == nil {
		tables = []models.TableInfo{}
	}

	return c.JSON(fiber.Map{
		"tables": tables,
	})
}

// GetTableSchema returns schema for a specific table
func (h *TablesHandler) GetTableSchema(c *fiber.Ctx) error {
	tableName := c.Params("name")
	if tableName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name is required",
		})
	}

	schema, err := h.db.GetTableSchema(c.Context(), tableName)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(schema)
}

// CreateTable creates a new table
func (h *TablesHandler) CreateTable(c *fiber.Ctx) error {
	var req models.CreateTableRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name is required",
		})
	}

	if len(req.Columns) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "At least one column is required",
		})
	}

	if err := h.db.CreateTable(c.Context(), req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "Table created successfully",
		"name":    req.Name,
	})
}

// DropTable drops a table
func (h *TablesHandler) DropTable(c *fiber.Ctx) error {
	tableName := c.Params("name")
	if tableName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name is required",
		})
	}

	if err := h.db.DropTable(c.Context(), tableName); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "Table dropped successfully",
	})
}

// GetRows returns paginated rows from a table
func (h *TablesHandler) GetRows(c *fiber.Ctx) error {
	tableName := c.Params("name")
	if tableName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name is required",
		})
	}

	params := models.PaginationParams{
		Page:     c.QueryInt("page", 1),
		PageSize: c.QueryInt("page_size", 50),
		OrderBy:  c.Query("order_by"),
		Order:    c.Query("order", "asc"),
	}

	result, err := h.db.GetRows(c.Context(), tableName, params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(result)
}

// InsertRow inserts a new row
func (h *TablesHandler) InsertRow(c *fiber.Ctx) error {
	tableName := c.Params("name")
	if tableName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name is required",
		})
	}

	var data map[string]interface{}
	if err := c.BodyParser(&data); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	row, err := h.db.InsertRow(c.Context(), tableName, data)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(row)
}

// UpdateRow updates a row
func (h *TablesHandler) UpdateRow(c *fiber.Ctx) error {
	tableName := c.Params("name")
	id := c.Params("id")

	if tableName == "" || id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name and row ID are required",
		})
	}

	var data map[string]interface{}
	if err := c.BodyParser(&data); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	row, err := h.db.UpdateRow(c.Context(), tableName, id, data)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(row)
}

// DeleteRow deletes a row
func (h *TablesHandler) DeleteRow(c *fiber.Ctx) error {
	tableName := c.Params("name")
	id := c.Params("id")

	if tableName == "" || id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Table name and row ID are required",
		})
	}

	if err := h.db.DeleteRow(c.Context(), tableName, id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "Row deleted successfully",
	})
}
