package database

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rapibase/rapibase/internal/models"
)

// RoleReader es el usuario de Postgres con el que corre el SQL de solo
// lectura (tool query_sql). Tiene que ser un usuario con login propio y no
// un SET ROLE desde la conexión normal: esa conexión es superusuario, y
// mientras el usuario de la sesión lo sea, una consulta puede volver a él
// (set_config('role', …) y SQL dinámico con query_to_xml) y desde ahí leer
// archivos del servidor, ejecutar programas (COPY … TO PROGRAM) o cortar
// otras sesiones. Con su propia sesión no hay a dónde volver.
//
// Puede leer todas las tablas (pg_read_all_data) y no lo frena RLS
// (BYPASSRLS), igual que query_rows; no puede escribir ni usar funciones
// de superusuario, y sus sesiones arrancan en solo lectura.
const RoleReader = "rapibase_reader"

// ErrReaderUnavailable: no se pudo preparar el usuario de lectura (Postgres
// administrado sin CREATEROLE, versión anterior a la 14, pg_hba que no deja
// entrar con contraseña…). query_sql falla cerrado: nunca corre la consulta
// con la conexión normal.
var ErrReaderUnavailable = errors.New("read-only SQL is not available on this database")

type readerState struct {
	mu   sync.Mutex
	pool *pgxpool.Pool
	cfg  *pgxpool.Config // copia de la configuración de la conexión normal
}

// ExecuteReadOnly corre UNA sentencia como rapibase_reader, dentro de una
// transacción READ ONLY que siempre se descarta, con statement_timeout.
// Usa el protocolo extendido de Postgres, que acepta una sola sentencia:
// un "SELECT 1; DELETE …" se rechaza entero en vez de partirse.
func (db *DB) ExecuteReadOnly(ctx context.Context, sql string, params []interface{}, timeout time.Duration) (*models.QueryResult, error) {
	pool, err := db.readerPool(ctx)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	// margen sobre el tope del servidor, como ExecuteQueryWithTimeout
	qctx, cancel := context.WithTimeout(ctx, timeout+5*time.Second)
	defer cancel()

	tx, err := pool.BeginTx(qctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rcancel()
		_ = tx.Rollback(rctx)
	}()
	if _, err := tx.Exec(qctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
		return nil, err
	}

	start := time.Now()
	// DescribeExec: protocolo extendido sin guardar en la caché de la
	// conexión cada consulta distinta que llegue.
	args := append([]interface{}{pgx.QueryExecModeDescribeExec}, params...)
	rows, err := tx.Query(qctx, sql, args...)
	if err != nil {
		return nil, explainReadOnly(err, timeout)
	}
	defer rows.Close()
	data, err := pgxRowsToMaps(rows)
	if err != nil {
		return nil, explainReadOnly(err, timeout)
	}
	columns := []string{}
	for _, fd := range rows.FieldDescriptions() {
		columns = append(columns, string(fd.Name))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, explainReadOnly(err, timeout)
	}
	return &models.QueryResult{
		Columns:  columns,
		Rows:     data,
		Duration: time.Since(start).String(),
	}, nil
}

// explainReadOnly traduce los errores típicos del modo lectura a algo que
// diga qué hacer.
func explainReadOnly(err error, timeout time.Duration) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "25006": // read_only_sql_transaction
			return fmt.Errorf("query_sql is read-only: this statement writes. Use execute_sql (needs write access) to change data or schema (%w)", err)
		case "42501": // insufficient_privilege
			return fmt.Errorf("query_sql runs as a read-only user: no superuser functions, no writes (%w)", err)
		case "57014":
			return fmt.Errorf("la consulta superó el límite de %s y Postgres la canceló (no quedó corriendo): %w", timeout, err)
		}
	}
	if strings.Contains(err.Error(), "cannot insert multiple commands into a prepared statement") {
		return fmt.Errorf("query_sql runs one statement per call: send each SELECT in its own call (%w)", err)
	}
	return err
}

// readerPool prepara (la primera vez) el usuario rapibase_reader y abre un
// pool chico con él. Si algo falla devuelve ErrReaderUnavailable con la
// causa y lo vuelve a intentar en la próxima llamada.
func (db *DB) readerPool(ctx context.Context) (*pgxpool.Pool, error) {
	db.reader.mu.Lock()
	defer db.reader.mu.Unlock()
	if db.reader.pool != nil {
		return db.reader.pool, nil
	}
	if db.reader.cfg == nil {
		return nil, fmt.Errorf("%w: no connection config", ErrReaderUnavailable)
	}
	pw := readerPassword(db.reader.cfg.ConnConfig.Password)
	if err := db.ensureReaderRole(ctx, pw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReaderUnavailable, err)
	}
	cfg := db.reader.cfg.Copy()
	cfg.ConnConfig.User = RoleReader
	cfg.ConnConfig.Password = pw
	cfg.MaxConns = 3
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.AfterConnect = nil
	cfg.BeforeAcquire = nil
	cfg.AfterRelease = nil
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReaderUnavailable, err)
	}
	// comprobar que de verdad entra como un usuario sin privilegios
	var super bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super); err != nil || super {
		pool.Close()
		if err == nil {
			err = errors.New("the reader role is a superuser")
		}
		return nil, fmt.Errorf("%w: %v", ErrReaderUnavailable, err)
	}
	db.reader.pool = pool
	return pool, nil
}

// readerPassword deriva la contraseña de rapibase_reader de la de la
// conexión normal: estable entre reinicios y entre réplicas que comparten
// DATABASE_URL, sin guardarla en ningún lado. Quien la puede calcular ya
// tiene la del superusuario. Sin contraseña en la URL (trust, .pgpass…),
// una al azar por proceso.
func readerPassword(base string) string {
	if base == "" {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	}
	m := hmac.New(sha256.New, []byte(base))
	m.Write([]byte("rapibase_reader/v1"))
	return hex.EncodeToString(m.Sum(nil))
}

// ensureReaderRole crea o ajusta rapibase_reader con la conexión normal.
// Idempotente: corre en cada arranque, en la primera consulta de lectura.
func (db *DB) ensureReaderRole(ctx context.Context, password string) error {
	stmts := []string{
		`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rapibase_reader') THEN
				CREATE ROLE rapibase_reader;
			END IF;
		EXCEPTION WHEN duplicate_object THEN NULL;
		END $$`,
		// la contraseña es hex: se puede poner literal (ALTER ROLE no
		// acepta parámetros)
		`ALTER ROLE rapibase_reader WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION
			INHERIT BYPASSRLS CONNECTION LIMIT 5 PASSWORD '` + password + `'`,
		`ALTER ROLE rapibase_reader SET default_transaction_read_only = on`,
		`ALTER ROLE rapibase_reader SET idle_in_transaction_session_timeout = '30s'`,
		`GRANT pg_read_all_data TO rapibase_reader`,
	}
	for _, s := range stmts {
		if _, err := db.Pool.Exec(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// closeReader cierra el pool de lectura si se abrió.
func (db *DB) closeReader() {
	db.reader.mu.Lock()
	defer db.reader.mu.Unlock()
	if db.reader.pool != nil {
		db.reader.pool.Close()
		db.reader.pool = nil
	}
}
