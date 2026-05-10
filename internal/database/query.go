package database

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

	// Build SELECT clause
	selectClause := "*"
	if params.Select != "" {
		cols := strings.Split(params.Select, ",")
		var validCols []string
		for _, col := range cols {
			col = strings.TrimSpace(col)
			if isValidIdentifier(col) {
				validCols = append(validCols, quoteIdentifier(col))
			}
		}
		if len(validCols) > 0 {
			selectClause = strings.Join(validCols, ", ")
		}
	}

	// Build WHERE clause from filters
	var whereClauses []string
	var queryArgs []interface{}
	argIndex := 1

	for _, filter := range params.Filters {
		if !isValidIdentifier(filter.Column) {
			continue
		}

		clause, args, newIndex := buildFilterClause(filter, argIndex)
		if clause != "" {
			whereClauses = append(whereClauses, clause)
			queryArgs = append(queryArgs, args...)
			argIndex = newIndex
		}
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Get total count (with filters)
	var totalRows int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s%s", quoteIdentifier(tableName), whereSQL)
	if err := db.Pool.QueryRow(ctx, countQuery, queryArgs...).Scan(&totalRows); err != nil {
		return nil, err
	}

	// Build main query
	query := fmt.Sprintf("SELECT %s FROM %s%s", selectClause, quoteIdentifier(tableName), whereSQL)

	if params.OrderBy != "" && isValidIdentifier(params.OrderBy) {
		query += fmt.Sprintf(" ORDER BY %s %s", quoteIdentifier(params.OrderBy), strings.ToUpper(params.Order))
	}

	query += fmt.Sprintf(" LIMIT %d OFFSET %d", params.PageSize, offset)

	rows, err := db.Pool.Query(ctx, query, queryArgs...)
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

// buildFilterClause builds a SQL WHERE clause from a filter condition
func buildFilterClause(filter models.FilterCondition, argIndex int) (string, []interface{}, int) {
	col := quoteIdentifier(filter.Column)
	var args []interface{}

	switch strings.ToLower(filter.Operator) {
	case "eq":
		args = append(args, filter.Value)
		return fmt.Sprintf("%s = $%d", col, argIndex), args, argIndex + 1
	case "neq":
		args = append(args, filter.Value)
		return fmt.Sprintf("%s != $%d", col, argIndex), args, argIndex + 1
	case "gt":
		args = append(args, filter.Value)
		return fmt.Sprintf("%s > $%d", col, argIndex), args, argIndex + 1
	case "gte":
		args = append(args, filter.Value)
		return fmt.Sprintf("%s >= $%d", col, argIndex), args, argIndex + 1
	case "lt":
		args = append(args, filter.Value)
		return fmt.Sprintf("%s < $%d", col, argIndex), args, argIndex + 1
	case "lte":
		args = append(args, filter.Value)
		return fmt.Sprintf("%s <= $%d", col, argIndex), args, argIndex + 1
	case "like":
		args = append(args, filter.Value)
		return fmt.Sprintf("%s LIKE $%d", col, argIndex), args, argIndex + 1
	case "ilike":
		args = append(args, filter.Value)
		return fmt.Sprintf("%s ILIKE $%d", col, argIndex), args, argIndex + 1
	case "is":
		// For NULL checks: is.null or is.true or is.false
		val := strings.ToLower(fmt.Sprintf("%v", filter.Value))
		if val == "null" {
			return fmt.Sprintf("%s IS NULL", col), nil, argIndex
		} else if val == "true" {
			return fmt.Sprintf("%s IS TRUE", col), nil, argIndex
		} else if val == "false" {
			return fmt.Sprintf("%s IS FALSE", col), nil, argIndex
		}
		return "", nil, argIndex
	case "in":
		// Value should be a slice or comma-separated string
		var values []interface{}
		switch v := filter.Value.(type) {
		case []interface{}:
			values = v
		case string:
			for _, s := range strings.Split(v, ",") {
				values = append(values, strings.TrimSpace(s))
			}
		}
		if len(values) == 0 {
			return "", nil, argIndex
		}
		placeholders := make([]string, len(values))
		for i, val := range values {
			placeholders[i] = fmt.Sprintf("$%d", argIndex+i)
			args = append(args, val)
		}
		return fmt.Sprintf("%s IN (%s)", col, strings.Join(placeholders, ", ")), args, argIndex + len(values)
	default:
		return "", nil, argIndex
	}
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

		row := make(map[string]interface{}, len(fieldDescriptions))
		for i, fd := range fieldDescriptions {
			row[string(fd.Name)] = normalizeValue(fd.DataTypeOID, values[i])
		}
		result = append(result, row)
	}

	return result, rows.Err()
}

// normalizeValue converts pgx default decodings into JSON-friendly forms.
// Without this, uuid columns surface as [16]byte and JSON-encode as a numeric array.
func normalizeValue(oid uint32, v interface{}) interface{} {
	if v == nil {
		return nil
	}
	switch oid {
	case pgtype.UUIDOID:
		if b, ok := v.([16]byte); ok {
			return formatUUID(b)
		}
	}
	return v
}

func formatUUID(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
