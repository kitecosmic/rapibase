package database

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/rapibase/rapibase/internal/models"
)

// jsonBatchSize controls how many rows are flushed per INSERT batch when
// stream-decoding a JSON array. Larger batches improve throughput; smaller
// batches bound memory. 1000 is a healthy balance for TEXT-only rows.
const jsonBatchSize = 1000

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

// ImportJSON stream-decodes a JSON array of objects and inserts them in
// chunked batches of jsonBatchSize rows. Memory stays bounded by the chunk
// size, not the input length, so multi-GB JSON files import without OOM.
//
// When autoCreateColumns is true, the destination table is created lazily
// from the first chunk's keys and any later chunk that introduces a new key
// triggers an ALTER TABLE ADD COLUMN before that chunk is inserted.
func (db *DB) ImportJSON(ctx context.Context, tableName string, reader io.Reader, autoCreateColumns bool) (int64, error) {
	if !isValidIdentifier(tableName) {
		return 0, fmt.Errorf("invalid table name")
	}

	dec := json.NewDecoder(reader)
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return 0, fmt.Errorf("invalid JSON: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return 0, fmt.Errorf("expected JSON array at top level, got %v", tok)
	}

	knownColumns, tableExists, err := db.loadColumnSet(ctx, tableName)
	if err != nil {
		return 0, err
	}
	if !autoCreateColumns && !tableExists {
		return 0, fmt.Errorf("table %q does not exist (enable auto_create to create it)", tableName)
	}

	var (
		totalAffected int64
		buf           = make([]map[string]interface{}, 0, jsonBatchSize)
	)

	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		n, err := db.insertJSONBatch(ctx, tableName, buf, knownColumns, &tableExists, autoCreateColumns)
		totalAffected += n
		buf = buf[:0]
		return err
	}

	for dec.More() {
		var row map[string]interface{}
		if err := dec.Decode(&row); err != nil {
			_ = flush()
			return totalAffected, fmt.Errorf("invalid JSON row at offset %d: %w", dec.InputOffset(), err)
		}
		buf = append(buf, row)
		if len(buf) >= jsonBatchSize {
			if err := flush(); err != nil {
				return totalAffected, err
			}
		}
	}
	if err := flush(); err != nil {
		return totalAffected, err
	}

	if _, err := dec.Token(); err != nil && !errors.Is(err, io.EOF) {
		return totalAffected, fmt.Errorf("malformed JSON: %w", err)
	}
	return totalAffected, nil
}

// loadColumnSet returns the lowercased column names of a table and whether
// the table currently exists. Used by the JSON import to bootstrap its
// known-columns set.
func (db *DB) loadColumnSet(ctx context.Context, tableName string) (map[string]bool, bool, error) {
	existing := make(map[string]bool)
	schema, err := db.GetTableSchema(ctx, tableName)
	if err != nil {
		if strings.Contains(err.Error(), "table not found") {
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("failed to get table schema: %w", err)
	}
	for _, col := range schema.Columns {
		existing[strings.ToLower(col.Name)] = true
	}
	return existing, true, nil
}

// insertJSONBatch creates/extends the table for any new columns observed in
// the chunk (when autoCreate is on) and inserts the chunk via pgx.Batch.
// knownColumns and tableExists are mutated in place so subsequent chunks
// reuse the schema discovery from earlier ones.
func (db *DB) insertJSONBatch(
	ctx context.Context,
	tableName string,
	rows []map[string]interface{},
	knownColumns map[string]bool,
	tableExists *bool,
	autoCreate bool,
) (int64, error) {
	chunkCols := make(map[string]bool)
	for _, row := range rows {
		for col := range row {
			normalized := normalizeColumnName(col)
			if !isValidIdentifier(normalized) {
				return 0, fmt.Errorf("invalid column name: %s", col)
			}
			chunkCols[normalized] = true
		}
	}

	if autoCreate {
		if !*tableExists {
			columns := []models.CreateColumnSpec{
				{Name: "id", Type: "SERIAL", IsPrimaryKey: true},
			}
			for col := range chunkCols {
				if strings.ToLower(col) != "id" {
					columns = append(columns, models.CreateColumnSpec{
						Name:     col,
						Type:     "TEXT",
						Nullable: true,
					})
				}
			}
			if err := db.CreateTable(ctx, models.CreateTableRequest{Name: tableName, Columns: columns}); err != nil {
				return 0, fmt.Errorf("failed to create table: %w", err)
			}
			for col := range chunkCols {
				knownColumns[strings.ToLower(col)] = true
			}
			knownColumns["id"] = true
			*tableExists = true
		} else {
			for col := range chunkCols {
				if knownColumns[strings.ToLower(col)] {
					continue
				}
				query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT",
					quoteIdentifier(tableName), quoteIdentifier(col))
				if _, err := db.Pool.Exec(ctx, query); err != nil {
					return 0, fmt.Errorf("failed to add column %s: %w", col, err)
				}
				knownColumns[strings.ToLower(col)] = true
			}
		}
	}

	batch := &pgx.Batch{}
	for _, row := range rows {
		var columns []string
		var placeholders []string
		var values []interface{}
		i := 1
		for col, val := range row {
			normalizedCol := normalizeColumnName(col)
			if strings.ToLower(normalizedCol) == "id" && (val == nil || val == "") {
				continue
			}
			columns = append(columns, quoteIdentifier(normalizedCol))
			placeholders = append(placeholders, fmt.Sprintf("$%d", i))
			values = append(values, convertToString(val))
			i++
		}
		if len(columns) == 0 {
			continue
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
			quoteIdentifier(tableName),
			strings.Join(columns, ", "),
			strings.Join(placeholders, ", "),
		)
		batch.Queue(query, values...)
	}
	if batch.Len() == 0 {
		return 0, nil
	}

	results := db.Pool.SendBatch(ctx, batch)
	defer results.Close()

	var affected int64
	for i := 0; i < batch.Len(); i++ {
		tag, err := results.Exec()
		if err != nil {
			return affected, fmt.Errorf("batch insert error at row %d: %w", i, err)
		}
		affected += tag.RowsAffected()
	}
	return affected, nil
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

// ImportCSV imports a CSV file into a table using PostgreSQL's COPY FROM STDIN
// with the request body streamed directly into the protocol. Memory use stays
// constant regardless of input size, so multi-GB CDR-style files import in a
// single pass.
//
// id-column handling: when the CSV declares an `id` column, we sniff the
// first data row to pick a mode:
//   - id cell empty  → "auto-gen mode": drop the id column from COPY's column
//     list and re-stream the file through csv.Reader/Writer in a goroutine,
//     filtering the id cell out of every row. Postgres then assigns ids from
//     the SERIAL sequence. This preserves the old convenience for legacy
//     exports that left the id column blank.
//   - id cell filled → "explicit mode": include id in COPY's column list and
//     stream the body verbatim. Faster, no re-stream cost.
//   - no id column   → fast path: stream verbatim.
func (db *DB) ImportCSV(ctx context.Context, tableName string, reader io.Reader, autoCreateColumns bool) (int64, error) {
	if !isValidIdentifier(tableName) {
		return 0, fmt.Errorf("invalid table name")
	}

	br := bufio.NewReaderSize(reader, 64*1024)

	// Strip optional UTF-8 BOM so Excel-exported CSVs work transparently.
	if b, _ := br.Peek(3); bytes.Equal(b, []byte{0xEF, 0xBB, 0xBF}) {
		_, _ = br.Discard(3)
	}

	headerLine, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("failed to read CSV header: %w", err)
	}
	headerLine = strings.TrimRight(headerLine, "\r\n")
	if headerLine == "" {
		return 0, fmt.Errorf("CSV file is empty")
	}

	hdrReader := csv.NewReader(strings.NewReader(headerLine))
	hdrReader.TrimLeadingSpace = true
	headers, err := hdrReader.Read()
	if err != nil {
		return 0, fmt.Errorf("failed to parse CSV header: %w", err)
	}
	if len(headers) == 0 {
		return 0, fmt.Errorf("CSV file has no columns")
	}

	normalizedHeaders := make([]string, len(headers))
	for i, h := range headers {
		normalized := normalizeColumnName(h)
		if !isValidIdentifier(normalized) {
			return 0, fmt.Errorf("invalid column name: %s", h)
		}
		normalizedHeaders[i] = normalized
	}

	// Create the table (and any missing columns) up-front using ALL headers
	// — including id, if present — so the destination matches the source.
	if err := db.ensureTableForHeaders(ctx, tableName, normalizedHeaders, autoCreateColumns); err != nil {
		return 0, err
	}

	// Decide id mode by sniffing the first data row.
	idIdx := indexOfFold(normalizedHeaders, "id")
	copyHeaders := normalizedHeaders
	var copyReader io.Reader = br
	cleanup := func() {}

	if idIdx >= 0 {
		firstDataLine, err := br.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("failed to read first data row: %w", err)
		}
		firstDataLine = strings.TrimRight(firstDataLine, "\r\n")
		if firstDataLine == "" {
			// Header-only file: nothing to copy, schema already ensured.
			return 0, nil
		}

		firstRecord, err := csv.NewReader(strings.NewReader(firstDataLine)).Read()
		if err != nil {
			return 0, fmt.Errorf("failed to parse first data row: %w", err)
		}

		idEmpty := idIdx < len(firstRecord) && strings.TrimSpace(firstRecord[idIdx]) == ""
		if idEmpty {
			copyHeaders = sliceDropAt(normalizedHeaders, idIdx)
			pr, pw := io.Pipe()
			go restreamWithoutColumn(pw, firstRecord, br, idIdx)
			copyReader = pr
			cleanup = func() { _ = pr.Close() }
		} else {
			// id is populated — put the sniffed line back in front of the
			// remaining stream and let COPY ingest it verbatim.
			copyReader = io.MultiReader(strings.NewReader(firstDataLine+"\n"), br)
		}
	}
	defer cleanup()

	quoted := make([]string, len(copyHeaders))
	for i, h := range copyHeaders {
		quoted[i] = quoteIdentifier(h)
	}

	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	copySQL := fmt.Sprintf(
		`COPY %s (%s) FROM STDIN WITH (FORMAT csv, HEADER false, NULL '')`,
		quoteIdentifier(tableName),
		strings.Join(quoted, ", "),
	)

	tag, err := conn.Conn().PgConn().CopyFrom(ctx, copyReader, copySQL)
	if err != nil {
		return tag.RowsAffected(), fmt.Errorf("COPY failed: %w", err)
	}
	return tag.RowsAffected(), nil
}

// restreamWithoutColumn parses a CSV stream and re-emits each record with
// the cell at dropIdx removed. firstRecord is the already-parsed first row
// (consumed during sniff); src holds the remaining unread bytes.
//
// Runs in its own goroutine; closes the pipe writer on completion so the
// COPY reader sees EOF. On parse error it propagates via CloseWithError so
// the COPY call fails fast instead of hanging on a half-written stream.
func restreamWithoutColumn(pw *io.PipeWriter, firstRecord []string, src io.Reader, dropIdx int) {
	cw := csv.NewWriter(pw)
	closeErr := func() error {
		cw.Flush()
		return cw.Error()
	}

	if err := cw.Write(sliceDropAt(firstRecord, dropIdx)); err != nil {
		_ = pw.CloseWithError(err)
		return
	}

	cr := csv.NewReader(src)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1 // tolerate ragged rows; COPY will surface column-count errors
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = pw.CloseWithError(fmt.Errorf("CSV parse error during re-stream: %w", err))
			return
		}
		if err := cw.Write(sliceDropAt(rec, dropIdx)); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}
	if err := closeErr(); err != nil {
		_ = pw.CloseWithError(err)
		return
	}
	_ = pw.Close()
}

func indexOfFold(slice []string, target string) int {
	for i, s := range slice {
		if strings.EqualFold(s, target) {
			return i
		}
	}
	return -1
}

func sliceDropAt(s []string, idx int) []string {
	if idx < 0 || idx >= len(s) {
		return s
	}
	out := make([]string, 0, len(s)-1)
	out = append(out, s[:idx]...)
	out = append(out, s[idx+1:]...)
	return out
}

// ensureTableForHeaders creates the destination table (with a SERIAL id and
// the given headers as TEXT) when missing, or adds any header that is not yet
// a column. With autoCreateColumns=false it just verifies the table exists
// and returns the schema check error otherwise.
func (db *DB) ensureTableForHeaders(ctx context.Context, tableName string, headers []string, autoCreate bool) error {
	existingColumns := make(map[string]bool)
	tableExists := true
	schema, err := db.GetTableSchema(ctx, tableName)
	if err != nil {
		if strings.Contains(err.Error(), "table not found") {
			tableExists = false
		} else {
			return fmt.Errorf("failed to get table schema: %w", err)
		}
	} else {
		for _, col := range schema.Columns {
			existingColumns[strings.ToLower(col.Name)] = true
		}
	}

	if !autoCreate {
		if !tableExists {
			return fmt.Errorf("table %q does not exist (enable auto_create to create it)", tableName)
		}
		return nil
	}

	if !tableExists {
		columns := []models.CreateColumnSpec{
			{Name: "id", Type: "SERIAL", IsPrimaryKey: true},
		}
		for _, h := range headers {
			if strings.ToLower(h) != "id" {
				columns = append(columns, models.CreateColumnSpec{
					Name:     h,
					Type:     "TEXT",
					Nullable: true,
				})
			}
		}
		if err := db.CreateTable(ctx, models.CreateTableRequest{Name: tableName, Columns: columns}); err != nil {
			return fmt.Errorf("failed to create table: %w", err)
		}
		return nil
	}

	for _, h := range headers {
		if existingColumns[strings.ToLower(h)] {
			continue
		}
		query := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s TEXT",
			quoteIdentifier(tableName), quoteIdentifier(h))
		if _, err := db.Pool.Exec(ctx, query); err != nil {
			return fmt.Errorf("failed to add column %s: %w", h, err)
		}
	}
	return nil
}

// convertToString converts any value to a string for TEXT columns
func convertToString(val interface{}) interface{} {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
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
