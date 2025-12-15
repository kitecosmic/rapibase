package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rapibase/rapibase/internal/models"
)

// GetRows returns paginated rows from a table
func (db *DB) GetRows(ctx context.Context, tableName string, params models.PaginationParams) (*models.PaginatedResponse, error) {
	if !isValidIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}

	// Defaults
	if params.Page < 1 {
		params.Page = 1
	}
	if params.PageSize < 1 || params.PageSize > 1000 {
		params.PageSize = 50
	}
	if params.Order != "asc" && params.Order != "desc" {
		params.Order = "asc"
	}

	offset := (params.Page - 1) * params.PageSize

	// Get total count
	var totalRows int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s", quoteIdentifier(tableName))
	if err := db.Pool.QueryRow(ctx, countQuery).Scan(&totalRows); err != nil {
		return nil, err
	}

	// Build query
	query := fmt.Sprintf("SELECT * FROM %s", quoteIdentifier(tableName))

	if params.OrderBy != "" && isValidIdentifier(params.OrderBy) {
		query += fmt.Sprintf(" ORDER BY %s %s", quoteIdentifier(params.OrderBy), strings.ToUpper(params.Order))
	}

	query += fmt.Sprintf(" LIMIT %d OFFSET %d", params.PageSize, offset)

	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	data, err := pgxRowsToMaps(rows)
	if err != nil {
		return nil, err
	}

	totalPages := int(totalRows) / params.PageSize
	if int(totalRows)%params.PageSize > 0 {
		totalPages++
	}

	return &models.PaginatedResponse{
		Data:       data,
		Page:       params.Page,
		PageSize:   params.PageSize,
		TotalRows:  totalRows,
		TotalPages: totalPages,
	}, nil
}

// InsertRow inserts a new row into a table
func (db *DB) InsertRow(ctx context.Context, tableName string, data map[string]interface{}) (map[string]interface{}, error) {
	if !isValidIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}

	var columns []string
	var placeholders []string
	var values []interface{}
	i := 1

	for col, val := range data {
		if !isValidIdentifier(col) {
			return nil, fmt.Errorf("invalid column name: %s", col)
		}
		columns = append(columns, quoteIdentifier(col))
		placeholders = append(placeholders, fmt.Sprintf("$%d", i))
		values = append(values, val)
		i++
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		quoteIdentifier(tableName),
		strings.Join(columns, ", "),
		strings.Join(placeholders, ", "),
	)

	rows, err := db.Pool.Query(ctx, query, values...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results, err := pgxRowsToMaps(rows)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("insert failed")
	}

	return results[0], nil
}

// GetRowByID returns a single row by its primary key
func (db *DB) GetRowByID(ctx context.Context, tableName string, id interface{}) (map[string]interface{}, error) {
	if !isValidIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}

	// Get primary key column
	schema, err := db.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, err
	}
	if schema.PrimaryKey == "" {
		return nil, fmt.Errorf("table has no primary key")
	}

	query := fmt.Sprintf(
		"SELECT * FROM %s WHERE %s = $1",
		quoteIdentifier(tableName),
		quoteIdentifier(schema.PrimaryKey),
	)

	rows, err := db.Pool.Query(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results, err := pgxRowsToMaps(rows)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("row not found")
	}

	return results[0], nil
}

// UpdateRow updates a row in a table
func (db *DB) UpdateRow(ctx context.Context, tableName string, id interface{}, data map[string]interface{}) (map[string]interface{}, error) {
	if !isValidIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}

	// Get primary key column
	schema, err := db.GetTableSchema(ctx, tableName)
	if err != nil {
		return nil, err
	}
	if schema.PrimaryKey == "" {
		return nil, fmt.Errorf("table has no primary key")
	}

	var setClauses []string
	var values []interface{}
	i := 1

	for col, val := range data {
		if !isValidIdentifier(col) {
			return nil, fmt.Errorf("invalid column name: %s", col)
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", quoteIdentifier(col), i))
		values = append(values, val)
		i++
	}

	values = append(values, id)

	query := fmt.Sprintf(
		"UPDATE %s SET %s WHERE %s = $%d RETURNING *",
		quoteIdentifier(tableName),
		strings.Join(setClauses, ", "),
		quoteIdentifier(schema.PrimaryKey),
		i,
	)

	rows, err := db.Pool.Query(ctx, query, values...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results, err := pgxRowsToMaps(rows)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("row not found")
	}

	return results[0], nil
}

// DeleteRow deletes a row from a table
func (db *DB) DeleteRow(ctx context.Context, tableName string, id interface{}) error {
	if !isValidIdentifier(tableName) {
		return fmt.Errorf("invalid table name")
	}

	schema, err := db.GetTableSchema(ctx, tableName)
	if err != nil {
		return err
	}
	if schema.PrimaryKey == "" {
		return fmt.Errorf("table has no primary key")
	}

	query := fmt.Sprintf(
		"DELETE FROM %s WHERE %s = $1",
		quoteIdentifier(tableName),
		quoteIdentifier(schema.PrimaryKey),
	)

	result, err := db.Pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("row not found")
	}

	return nil
}

// ExecuteQuery executes a raw SQL query
func (db *DB) ExecuteQuery(ctx context.Context, sql string, params []interface{}) (*models.QueryResult, error) {
	start := time.Now()

	// Determine if it's a SELECT query
	trimmedSQL := strings.TrimSpace(strings.ToUpper(sql))
	isSelect := strings.HasPrefix(trimmedSQL, "SELECT") ||
		strings.HasPrefix(trimmedSQL, "WITH") ||
		strings.HasPrefix(trimmedSQL, "SHOW") ||
		strings.HasPrefix(trimmedSQL, "EXPLAIN")

	if isSelect {
		rows, err := db.Pool.Query(ctx, sql, params...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		data, err := pgxRowsToMaps(rows)
		if err != nil {
			return nil, err
		}

		var columns []string
		for _, fd := range rows.FieldDescriptions() {
			columns = append(columns, string(fd.Name))
		}

		return &models.QueryResult{
			Columns:  columns,
			Rows:     data,
			Duration: time.Since(start).String(),
		}, nil
	}

	// Non-SELECT query
	result, err := db.Pool.Exec(ctx, sql, params...)
	if err != nil {
		return nil, err
	}

	return &models.QueryResult{
		RowsAffected: result.RowsAffected(),
		Duration:     time.Since(start).String(),
	}, nil
}

// Helper to convert pgx rows to maps
func pgxRowsToMaps(rows pgx.Rows) ([]map[string]interface{}, error) {
	var result []map[string]interface{}

	fieldDescriptions := rows.FieldDescriptions()

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}

		row := make(map[string]interface{})
		for i, fd := range fieldDescriptions {
			row[string(fd.Name)] = values[i]
		}
		result = append(result, row)
	}

	return result, rows.Err()
}
