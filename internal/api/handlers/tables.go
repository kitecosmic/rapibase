package handlers

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/models"
	"github.com/rapibase/rapibase/internal/webhooks"
)

// WebhookDispatcher interface for dispatching webhook events
type WebhookDispatcher interface {
	Dispatch(ctx context.Context, eventType, tableName string, data, oldData map[string]interface{})
}

type TablesHandler struct {
	db                *database.DB
	webhookDispatcher WebhookDispatcher
}

func NewTablesHandler(db *database.DB, webhookDispatcher WebhookDispatcher) *TablesHandler {
	return &TablesHandler{db: db, webhookDispatcher: webhookDispatcher}
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

	// Dispatch webhook event
	if h.webhookDispatcher != nil {
		h.webhookDispatcher.Dispatch(c.Context(), webhooks.EventInsert, tableName, row, nil)
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

	// Get old data BEFORE update for webhook
	var oldData map[string]interface{}
	if h.webhookDispatcher != nil {
		oldData, _ = h.db.GetRowByID(c.Context(), tableName, id)
	}

	row, err := h.db.UpdateRow(c.Context(), tableName, id, data)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Dispatch webhook event with old data
	if h.webhookDispatcher != nil {
		h.webhookDispatcher.Dispatch(c.Context(), webhooks.EventUpdate, tableName, row, oldData)
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

	// Get row data before deletion for webhook
	var deletedData map[string]interface{}
	if h.webhookDispatcher != nil {
		deletedData, _ = h.db.GetRowByID(c.Context(), tableName, id)
	}

	if err := h.db.DeleteRow(c.Context(), tableName, id); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Dispatch webhook event
	if h.webhookDispatcher != nil {
		if deletedData == nil {
			deletedData = map[string]interface{}{"id": id}
		}
		h.webhookDispatcher.Dispatch(c.Context(), webhooks.EventDelete, tableName, deletedData, nil)
	}

	return c.JSON(fiber.Map{
		"message": "Row deleted successfully",
	})
}
