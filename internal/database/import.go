package database

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
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

// ImportJSON imports data from JSON into a table
func (db *DB) ImportJSON(ctx context.Context, tableName string, reader io.Reader) (int64, error) {
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

	// Use batch insert for performance
	batch := &pgx.Batch{}
	
	for _, row := range data {
		var columns []string
		var placeholders []string
		var values []interface{}
		i := 1

		for col, val := range row {
			if !isValidIdentifier(col) {
				return 0, fmt.Errorf("invalid column name: %s", col)
			}
			columns = append(columns, quoteIdentifier(col))
			placeholders = append(placeholders, fmt.Sprintf("$%d", i))
			values = append(values, val)
			i++
		}

		query := fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s)",
			quoteIdentifier(tableName),
			strings.Join(columns, ", "),
			strings.Join(placeholders, ", "),
		)

		batch.Queue(query, values...)
	}

	// Execute batch
	results := db.Pool.SendBatch(ctx, batch)
	defer results.Close()

	var totalAffected int64
	for i := 0; i < len(data); i++ {
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
