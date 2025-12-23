package database

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rapibase/rapibase/internal/models"
)

// ImportSQL imports data from SQL statements
func (db *DB) ImportSQL(ctx context.Context, reader io.Reader) (int64, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB max line

	var totalAffected int64
	var currentStatement strings.Builder

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "--") || strings.HasPrefix(line, "/*") {
			continue
		}

		currentStatement.WriteString(line)
		currentStatement.WriteString(" ")

		// Check if statement is complete
		if strings.HasSuffix(line, ";") {
			sql := strings.TrimSpace(currentStatement.String())
			currentStatement.Reset()

			if sql == "" || sql == ";" {
				continue
			}

			// Block dangerous operations on internal tables
			upperSQL := strings.ToUpper(sql)
			if strings.Contains(upperSQL, "_RAPIBASE_") {
				continue
			}

			result, err := db.Pool.Exec(ctx, sql)
			if err != nil {
				return totalAffected, fmt.Errorf("SQL error: %w\nStatement: %s", err, truncate(sql, 200))
			}
			totalAffected += result.RowsAffected()
		}
	}

	if err := scanner.Err(); err != nil {
		return totalAffected, fmt.Errorf("scanner error: %w", err)
	}

	return totalAffected, nil
}

// ImportJSON imports data from JSON into a table with auto-column creation
func (db *DB) ImportJSON(ctx context.Context, tableName string, reader io.Reader, autoCreateColumns bool) (int64, error) {
	if !isValidIdentifier(tableName) {
		return 0, fmt.Errorf("invalid table name")
	}

	// Decode JSON
	var data []map[string]interface{}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&data); err != nil {
		return 0, fmt.Errorf("invalid JSON: %w", err)
	}

	if len(data) == 0 {
		return 0, nil
	}

	// Collect all unique column names from all rows
	allColumns := make(map[string]bool)
	for _, row := range data {
		for col := range row {
			normalized := normalizeColumnName(col)
			if !isValidIdentifier(normalized) {
				return 0, fmt.Errorf("invalid column name: %s", col)
			}
			allColumns[normalized] = true
		}
	}

	// Check if table exists and get existing columns
	existingColumns := make(map[string]bool)
	tableExists := true
	schema, err := db.GetTableSchema(ctx, tableName)
	if err != nil {
		if strings.Contains(err.Error(), "table not found") {
			tableExists = false
		} else {
			return 0, fmt.Errorf("failed to get table schema: %w", err)
		}
	} else {
		for _, col := range schema.Columns {
			existingColumns[strings.ToLower(col.Name)] = true
		}
	}

	// Auto-create columns if enabled
	if autoCreateColumns {
		if !tableExists {
			// Create table with id column and all JSON columns
			columns := []models.CreateColumnSpec{
				{
					Name:         "id",
					Type:         "SERIAL",
					IsPrimaryKey: true,
				},
			}
			for col := range allColumns {
				if strings.ToLower(col) != "id" {
					columns = append(columns, models.CreateColumnSpec{
						Name:     col,
						Type:     "TEXT",
						Nullable: true,
					})
				}
			}
			err = db.CreateTable(ctx, models.CreateTableRequest{
				Name:    tableName,
				Columns: columns,
			})
			if err != nil {
				return 0, fmt.Errorf("failed to create table: %w", err)
			}
		} else {
			// Add missing columns to existing table
			for col := range allColumns {
				if !existingColumns[strings.ToLower(col)] {
					query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT",
						quoteIdentifier(tableName),
						quoteIdentifier(col))
					_, err := db.Pool.Exec(ctx, query)
					if err != nil {
						return 0, fmt.Errorf("failed to add column %s: %w", col, err)
					}
				}
			}
		}
	}

	// Use batch insert for performance
	batch := &pgx.Batch{}

	for _, row := range data {
		var columns []string
		var placeholders []string
		var values []interface{}
		i := 1

		for col, val := range row {
			normalizedCol := normalizeColumnName(col)
			// Skip id column if it's nil or empty (let DB auto-generate)
			if strings.ToLower(normalizedCol) == "id" && (val == nil || val == "") {
				continue
			}
			columns = append(columns, quoteIdentifier(normalizedCol))
			placeholders = append(placeholders, fmt.Sprintf("$%d", i))
			// Convert value to string for TEXT columns
			values = append(values, convertToString(val))
			i++
		}

		if len(columns) == 0 {
			continue
		}

		query := fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s)",
			quoteIdentifier(tableName),
			strings.Join(columns, ", "),
			strings.Join(placeholders, ", "),
		)

		batch.Queue(query, values...)
	}

	if batch.Len() == 0 {
		return 0, nil
	}

	// Execute batch
	results := db.Pool.SendBatch(ctx, batch)
	defer results.Close()

	var totalAffected int64
	for i := 0; i < batch.Len(); i++ {
		result, err := results.Exec()
		if err != nil {
			return totalAffected, fmt.Errorf("batch insert error at row %d: %w", i, err)
		}
		totalAffected += result.RowsAffected()
	}

	return totalAffected, nil
}

// ExportTableJSON exports a table to JSON
func (db *DB) ExportTableJSON(ctx context.Context, tableName string, writer io.Writer) error {
	if !isValidIdentifier(tableName) {
		return fmt.Errorf("invalid table name")
	}

	query := fmt.Sprintf("SELECT * FROM %s", quoteIdentifier(tableName))
	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	data, err := pgxRowsToMaps(rows)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// ExportTableSQL exports a table to SQL INSERT statements
func (db *DB) ExportTableSQL(ctx context.Context, tableName string, writer io.Writer) error {
	if !isValidIdentifier(tableName) {
		return fmt.Errorf("invalid table name")
	}

	// Get schema first
	schema, err := db.GetTableSchema(ctx, tableName)
	if err != nil {
		return err
	}

	// Write CREATE TABLE statement
	var columnDefs []string
	for _, col := range schema.Columns {
		def := fmt.Sprintf("  %s %s", quoteIdentifier(col.Name), col.Type)
		if col.IsPrimaryKey {
			def += " PRIMARY KEY"
		}
		if !col.Nullable {
			def += " NOT NULL"
		}
		if col.DefaultValue != nil {
			def += fmt.Sprintf(" DEFAULT %s", *col.DefaultValue)
		}
		columnDefs = append(columnDefs, def)
	}

	fmt.Fprintf(writer, "CREATE TABLE IF NOT EXISTS %s (\n%s\n);\n\n",
		quoteIdentifier(tableName),
		strings.Join(columnDefs, ",\n"),
	)

	// Get data
	query := fmt.Sprintf("SELECT * FROM %s", quoteIdentifier(tableName))
	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()

	data, err := pgxRowsToMaps(rows)
	if err != nil {
		return err
	}

	// Write INSERT statements
	for _, row := range data {
		var columns []string
		var values []string

		for _, col := range schema.Columns {
			columns = append(columns, quoteIdentifier(col.Name))
			values = append(values, formatSQLValue(row[col.Name]))
		}

		fmt.Fprintf(writer, "INSERT INTO %s (%s) VALUES (%s);\n",
			quoteIdentifier(tableName),
			strings.Join(columns, ", "),
			strings.Join(values, ", "),
		)
	}

	return nil
}

func formatSQLValue(v interface{}) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	case bool:
		if val {
			return "TRUE"
		}
		return "FALSE"
	default:
		return fmt.Sprintf("%v", val)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ImportCSV imports data from CSV into a table with auto-column creation
func (db *DB) ImportCSV(ctx context.Context, tableName string, reader io.Reader, autoCreateColumns bool) (int64, error) {
	if !isValidIdentifier(tableName) {
		return 0, fmt.Errorf("invalid table name")
	}

	csvReader := csv.NewReader(reader)
	csvReader.TrimLeadingSpace = true

	// Read header row
	headers, err := csvReader.Read()
	if err != nil {
		return 0, fmt.Errorf("failed to read CSV headers: %w", err)
	}

	if len(headers) == 0 {
		return 0, fmt.Errorf("CSV file has no columns")
	}

	// Normalize and validate headers
	normalizedHeaders := make([]string, len(headers))
	for i, h := range headers {
		normalized := normalizeColumnName(h)
		if !isValidIdentifier(normalized) {
			return 0, fmt.Errorf("invalid column name: %s", h)
		}
		normalizedHeaders[i] = normalized
	}

	// Check if table exists and get existing columns
	existingColumns := make(map[string]bool)
	tableExists := true
	schema, err := db.GetTableSchema(ctx, tableName)
	if err != nil {
		if strings.Contains(err.Error(), "table not found") {
			tableExists = false
		} else {
			return 0, fmt.Errorf("failed to get table schema: %w", err)
		}
	} else {
		for _, col := range schema.Columns {
			existingColumns[strings.ToLower(col.Name)] = true
		}
	}

	// Auto-create columns if enabled
	if autoCreateColumns {
		if !tableExists {
			// Create table with id column and all CSV columns
			columns := []models.CreateColumnSpec{
				{
					Name:         "id",
					Type:         "SERIAL",
					IsPrimaryKey: true,
				},
			}
			for _, h := range normalizedHeaders {
				if strings.ToLower(h) != "id" {
					columns = append(columns, models.CreateColumnSpec{
						Name:     h,
						Type:     "TEXT",
						Nullable: true,
					})
				}
			}
			err = db.CreateTable(ctx, models.CreateTableRequest{
				Name:    tableName,
				Columns: columns,
			})
			if err != nil {
				return 0, fmt.Errorf("failed to create table: %w", err)
			}
		} else {
			// Add missing columns to existing table
			for _, h := range normalizedHeaders {
				if !existingColumns[strings.ToLower(h)] {
					query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT",
						quoteIdentifier(tableName),
						quoteIdentifier(h))
					_, err := db.Pool.Exec(ctx, query)
					if err != nil {
						return 0, fmt.Errorf("failed to add column %s: %w", h, err)
					}
				}
			}
		}
	}

	// Read all rows
	var rows [][]string
	for {
		record, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("failed to read CSV row: %w", err)
		}
		rows = append(rows, record)
	}

	if len(rows) == 0 {
		return 0, nil
	}

	// Use batch insert for performance
	batch := &pgx.Batch{}

	for _, row := range rows {
		var columns []string
		var placeholders []string
		var values []interface{}
		idx := 1

		for i, val := range row {
			if i >= len(normalizedHeaders) {
				break
			}
			colName := normalizedHeaders[i]
			// Skip id column if it's empty (let DB auto-generate)
			if strings.ToLower(colName) == "id" && val == "" {
				continue
			}
			columns = append(columns, quoteIdentifier(colName))
			placeholders = append(placeholders, fmt.Sprintf("$%d", idx))
			// Handle empty strings as NULL for non-text columns
			if val == "" {
				values = append(values, nil)
			} else {
				values = append(values, val)
			}
			idx++
		}

		if len(columns) == 0 {
			continue
		}

		query := fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s)",
			quoteIdentifier(tableName),
			strings.Join(columns, ", "),
			strings.Join(placeholders, ", "),
		)

		batch.Queue(query, values...)
	}

	if batch.Len() == 0 {
		return 0, nil
	}

	// Execute batch
	results := db.Pool.SendBatch(ctx, batch)
	defer results.Close()

	var totalAffected int64
	for i := 0; i < batch.Len(); i++ {
		result, err := results.Exec()
		if err != nil {
			return totalAffected, fmt.Errorf("batch insert error at row %d: %w", i, err)
		}
		totalAffected += result.RowsAffected()
	}

	return totalAffected, nil
}

// convertToString converts any value to a string for TEXT columns
func convertToString(val interface{}) interface{} {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case string:
		return v
	case float64:
		// Check if it's a whole number
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// normalizeColumnName converts a header to a valid column name
func normalizeColumnName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	// Replace spaces and special chars with underscore
	var result strings.Builder
	for i, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			result.WriteRune(c)
		} else if c == ' ' || c == '-' || c == '.' {
			result.WriteRune('_')
		} else if i == 0 && c >= 'A' && c <= 'Z' {
			result.WriteRune(c + 32) // lowercase
		}
	}
	if result.Len() == 0 {
		return "column"
	}
	// Ensure doesn't start with number
	resultStr := result.String()
	if resultStr[0] >= '0' && resultStr[0] <= '9' {
		return "col_" + resultStr
	}
	return resultStr
}
