package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/rapibase/rapibase/internal/models"
)

// GetTables returns all user tables (excluding internal _rapibase_ and auth_ tables)
func (db *DB) GetTables(ctx context.Context) ([]models.TableInfo, error) {
	query := `
		SELECT 
			t.table_name,
			t.table_schema,
			COALESCE(s.n_live_tup, 0) as row_count
		FROM information_schema.tables t
		LEFT JOIN pg_stat_user_tables s ON t.table_name = s.relname
		WHERE t.table_schema = 'public' 
		AND t.table_type = 'BASE TABLE'
		AND t.table_name NOT LIKE '_rapibase_%'
		AND t.table_name NOT LIKE 'auth_%'
		ORDER BY t.table_name
	`

	rows, err := db.Pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []models.TableInfo
	for rows.Next() {
		var t models.TableInfo
		if err := rows.Scan(&t.Name, &t.Schema, &t.RowCount); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}

	return tables, rows.Err()
}

// GetTableSchema returns detailed schema information for a table
func (db *DB) GetTableSchema(ctx context.Context, tableName string) (*models.TableInfo, error) {
	// Validate table name to prevent SQL injection
	if !isValidIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}

	// Get columns
	columnsQuery := `
		SELECT 
			c.column_name,
			c.data_type,
			c.is_nullable = 'YES' as nullable,
			c.column_default,
			COALESCE(pk.is_pk, false) as is_primary_key,
			COALESCE(fk.is_fk, false) as is_foreign_key,
			fk.references
		FROM information_schema.columns c
		LEFT JOIN (
			SELECT kcu.column_name, true as is_pk
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu 
				ON tc.constraint_name = kcu.constraint_name
			WHERE tc.table_name = $1 AND tc.constraint_type = 'PRIMARY KEY'
		) pk ON c.column_name = pk.column_name
		LEFT JOIN (
			SELECT 
				kcu.column_name, 
				true as is_fk,
				ccu.table_name || '.' || ccu.column_name as references
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu 
				ON tc.constraint_name = kcu.constraint_name
			JOIN information_schema.constraint_column_usage ccu 
				ON tc.constraint_name = ccu.constraint_name
			WHERE tc.table_name = $1 AND tc.constraint_type = 'FOREIGN KEY'
		) fk ON c.column_name = fk.column_name
		WHERE c.table_name = $1 AND c.table_schema = 'public'
		ORDER BY c.ordinal_position
	`

	rows, err := db.Pool.Query(ctx, columnsQuery, tableName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []models.ColumnInfo
	var primaryKey string

	for rows.Next() {
		var col models.ColumnInfo
		if err := rows.Scan(
			&col.Name, &col.Type, &col.Nullable,
			&col.DefaultValue, &col.IsPrimaryKey,
			&col.IsForeignKey, &col.References,
		); err != nil {
			return nil, err
		}
		if col.IsPrimaryKey {
			primaryKey = col.Name
		}
		columns = append(columns, col)
	}

	if len(columns) == 0 {
		return nil, fmt.Errorf("table not found: %s", tableName)
	}

	// Get row count
	var rowCount int64
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM %s", quoteIdentifier(tableName))
	db.Pool.QueryRow(ctx, countQuery).Scan(&rowCount)

	return &models.TableInfo{
		Name:       tableName,
		Schema:     "public",
		Columns:    columns,
		PrimaryKey: primaryKey,
		RowCount:   rowCount,
	}, nil
}

// CreateTable creates a new table
func (db *DB) CreateTable(ctx context.Context, req models.CreateTableRequest) error {
	if !isValidIdentifier(req.Name) {
		return fmt.Errorf("invalid table name")
	}

	lowerName := strings.ToLower(req.Name)
	if strings.HasPrefix(lowerName, "_rapibase_") || strings.HasPrefix(lowerName, "auth_") {
		return fmt.Errorf("table name cannot start with _rapibase_ or auth_")
	}

	var columnDefs []string
	for _, col := range req.Columns {
		if !isValidIdentifier(col.Name) {
			return fmt.Errorf("invalid column name: %s", col.Name)
		}

		def := fmt.Sprintf("%s %s", quoteIdentifier(col.Name), col.Type)

		if col.IsPrimaryKey {
			def += " PRIMARY KEY"
		}
		if !col.Nullable && !col.IsPrimaryKey {
			def += " NOT NULL"
		}
		if col.IsUnique {
			def += " UNIQUE"
		}
		if col.DefaultValue != nil {
			def += fmt.Sprintf(" DEFAULT %s", *col.DefaultValue)
		}

		columnDefs = append(columnDefs, def)
	}

	query := fmt.Sprintf(
		"CREATE TABLE %s (%s)",
		quoteIdentifier(req.Name),
		strings.Join(columnDefs, ", "),
	)

	_, err := db.Pool.Exec(ctx, query)
	return err
}

// DropTable drops a table
func (db *DB) DropTable(ctx context.Context, tableName string) error {
	if !isValidIdentifier(tableName) {
		return fmt.Errorf("invalid table name")
	}

	lowerName := strings.ToLower(tableName)
	if strings.HasPrefix(lowerName, "_rapibase_") || strings.HasPrefix(lowerName, "auth_") {
		return fmt.Errorf("cannot drop internal tables")
	}

	query := fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", quoteIdentifier(tableName))
	_, err := db.Pool.Exec(ctx, query)
	return err
}

// Helper functions
func isValidIdentifier(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i, c := range name {
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	return true
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
