package functions

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	esbuild "github.com/evanw/esbuild/pkg/api"
	"github.com/robfig/cron/v3"

	"github.com/rapibase/rapibase/internal/database"
)

// Meta describe una function cargada, para GET /api/v1/functions y el panel.
type Meta struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"` // http | cron | job
	File   string `json:"file"`
	Public bool   `json:"public,omitempty"`
	Spec   string `json:"spec,omitempty"` // solo cron
}

type compiled struct {
	file string
	prog *goja.Program
}

type enginePool struct {
	ch chan *engine
}

// Service gestiona el ciclo de vida completo: carga de archivos, pool de
// intérpretes, cron y cola de jobs.
type Service struct {
	db  *database.DB
	dir string

	mu    sync.RWMutex
	pool  *enginePool
	meta  []Meta
	errs  []string
	files []string

	// carga en curso: solo el primer engine del pool alimenta los
	// metadatos (todos ejecutan los mismos programas). Protegido por loadMu
	// porque Reload puede llamarse desde varios handlers a la vez.
	loadMu      sync.Mutex
	metaEngine  *engine
	pendingMeta []Meta

	cronMu     sync.Mutex
	cronRunner *cron.Cron

	stopJobs chan struct{}
}

const (
	poolSize    = 4
	httpTimeout = 30 * time.Second
	jobTimeout  = 5 * time.Minute
)

// New construye el servicio y hace la carga inicial. Nunca falla por
// errores en las functions del usuario: se reportan en errs y el resto de
// rapibase arranca normal.
func New(db *database.DB, dir string) *Service {
	s := &Service{db: db, dir: dir, stopJobs: make(chan struct{})}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.errs = []string{fmt.Sprintf("no se pudo crear %s: %v", dir, err)}
		return s
	}
	s.Reload()
	return s
}

func (s *Service) logf(format string, args ...interface{}) {
	log.Printf(format, args...)
}

// recordMeta: los engines llaman aquí al registrar functions; solo el
// engine designado durante la carga alimenta los metadatos.
func (s *Service) recordMeta(e *engine, m Meta) {
	if s.metaEngine != e {
		return
	}
	for i, existing := range s.pendingMeta {
		if existing.Name == m.Name && existing.Kind == m.Kind {
			s.pendingMeta[i] = m
			return
		}
	}
	s.pendingMeta = append(s.pendingMeta, m)
}

// Reload recompila todo y reconstruye el pool de forma atómica. Los
// archivos con errores se omiten (y se reportan); las demás functions
// siguen funcionando.
func (s *Service) Reload() {
	s.loadMu.Lock()
	programs, files, errs := s.compileAll()

	s.pendingMeta = nil
	pool := &enginePool{ch: make(chan *engine, poolSize)}
	built := 0
	for i := 0; i < poolSize; i++ {
		e, err := newEngine(s, programs, i == 0)
		if err != nil {
			if i == 0 {
				errs = append(errs, err.Error())
			}
			continue
		}
		if i == 0 {
			errs = append(errs, e.regErrs...)
		}
		pool.ch <- e
		built++
	}
	s.metaEngine = nil
	newMeta := append([]Meta(nil), s.pendingMeta...)
	s.pendingMeta = nil
	s.loadMu.Unlock()

	if built == 0 && len(programs) > 0 {
		errs = append(errs, "ninguna function quedó cargada")
	}

	s.mu.Lock()
	s.pool = pool
	s.meta = newMeta
	s.errs = errs
	s.files = files
	s.mu.Unlock()

	s.rebuildCron()
	if len(files) > 0 || len(errs) > 0 {
		log.Printf("functions: %d archivo(s), %d function(es), %d error(es)", len(files), len(newMeta), len(errs))
	}
}

// compileAll: lee *.ts|*.js del dir, transpila TS con esbuild (formato IIFE
// para tolerar `export`) y compila a programas goja reutilizables.
func (s *Service) compileAll() ([]compiled, []string, []string) {
	var programs []compiled
	var files, errs []string

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("leyendo %s: %v", s.dir, err)}
	}
	var names []string
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		n := en.Name()
		if strings.HasSuffix(n, ".ts") || strings.HasSuffix(n, ".js") {
			names = append(names, n)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		src, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		loader := esbuild.LoaderJS
		if strings.HasSuffix(name, ".ts") {
			loader = esbuild.LoaderTS
		}
		result := esbuild.Transform(string(src), esbuild.TransformOptions{
			Loader:     loader,
			Format:     esbuild.FormatIIFE,
			Target:     esbuild.ES2017,
			Sourcefile: name,
		})
		if len(result.Errors) > 0 {
			for _, e := range result.Errors {
				loc := ""
				if e.Location != nil {
					loc = fmt.Sprintf(":%d:%d", e.Location.Line, e.Location.Column)
				}
				errs = append(errs, fmt.Sprintf("%s%s: %s", name, loc, e.Text))
			}
			continue
		}
		prog, err := goja.Compile(name, string(result.Code), false)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		programs = append(programs, compiled{file: name, prog: prog})
		files = append(files, name)
	}
	return programs, files, errs
}

// --- acceso al estado ------------------------------------------------------

func (s *Service) Snapshot() (meta []Meta, errs []string, files []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Meta(nil), s.meta...), append([]string(nil), s.errs...), append([]string(nil), s.files...)
}

func (s *Service) Dir() string { return s.dir }

func (s *Service) currentPool() *enginePool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pool
}

var errNotFound = fmt.Errorf("function no encontrada")

// call toma un engine del pool, ejecuta y lo devuelve.
func (s *Service) call(ctx context.Context, kind, name string, timeout time.Duration, arg interface{}) (interface{}, error) {
	p := s.currentPool()
	if p == nil || cap(p.ch) == 0 {
		return nil, errNotFound
	}
	var e *engine
	select {
	case e = <-p.ch:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { p.ch <- e }()

	var fn goja.Callable
	var ok bool
	switch kind {
	case "http":
		var entry httpEntry
		entry, ok = e.http[name]
		fn = entry.fn
	case "cron":
		fn, ok = e.cron[name]
	case "job":
		fn, ok = e.jobs[name]
	}
	if !ok {
		return nil, errNotFound
	}
	return e.invoke(ctx, fn, timeout, e.vm.ToValue(arg))
}

// LookupHTTP: existe + es pública, según los metadatos publicados.
func (s *Service) LookupHTTP(name string) (exists, public bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.meta {
		if m.Kind == "http" && m.Name == name {
			return true, m.Public
		}
	}
	return false, false
}

func (s *Service) InvokeHTTP(ctx context.Context, name string, req interface{}) (interface{}, error) {
	return s.call(ctx, "http", name, httpTimeout, req)
}

// --- cron -------------------------------------------------------------------

func (s *Service) rebuildCron() {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if s.cronRunner != nil {
		s.cronRunner.Stop()
	}
	s.mu.RLock()
	metas := append([]Meta(nil), s.meta...)
	s.mu.RUnlock()

	c := cron.New()
	for _, m := range metas {
		if m.Kind != "cron" {
			continue
		}
		name := m.Name
		if _, err := c.AddFunc(m.Spec, func() {
			if _, err := s.call(context.Background(), "cron", name, jobTimeout, nil); err != nil {
				log.Printf("[fn] cron %s: %v", name, err)
			}
		}); err != nil {
			log.Printf("[fn] cron %s: spec inválida: %v", name, err)
		}
	}
	c.Start()
	s.cronRunner = c
}

// --- cola de jobs ------------------------------------------------------------

// BootstrapJobs crea la tabla de la cola (idempotente) y arranca el poller.
func (s *Service) BootstrapJobs(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS _rapibase_jobs (
			id bigserial PRIMARY KEY,
			name text NOT NULL,
			payload jsonb NOT NULL DEFAULT '{}'::jsonb,
			status text NOT NULL DEFAULT 'pending',
			run_at timestamptz NOT NULL DEFAULT now(),
			attempts int NOT NULL DEFAULT 0,
			max_attempts int NOT NULL DEFAULT 3,
			last_error text,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("functions: creando _rapibase_jobs: %w", err)
	}
	_, err = s.db.Pool.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS idx_rapibase_jobs_pending ON _rapibase_jobs (status, run_at)`)
	if err != nil {
		return fmt.Errorf("functions: índice de _rapibase_jobs: %w", err)
	}
	go s.pollJobs()
	return nil
}

func (s *Service) StopJobs() { close(s.stopJobs) }

// Enqueue inserta un trabajo en la cola. delaySeconds pospone la ejecución.
func (s *Service) Enqueue(ctx context.Context, name string, payload interface{}, delaySeconds int) (int64, error) {
	if !nameRe.MatchString(name) {
		return 0, fmt.Errorf("nombre de job inválido: %q", name)
	}
	if s.db == nil {
		return 0, fmt.Errorf("db no disponible")
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	if delaySeconds < 0 {
		delaySeconds = 0
	}
	var id int64
	err = s.db.Pool.QueryRow(ctx,
		`INSERT INTO _rapibase_jobs (name, payload, run_at) VALUES ($1, $2, now() + make_interval(secs => $3)) RETURNING id`,
		name, string(b), delaySeconds,
	).Scan(&id)
	return id, err
}

// pollJobs: reclama lotes con SKIP LOCKED y ejecuta cada job con
// reintentos y backoff.
func (s *Service) pollJobs() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopJobs:
			return
		case <-ticker.C:
		}
		s.runPendingJobs()
	}
}

type claimedJob struct {
	id          int64
	name        string
	payload     []byte
	attempts    int
	maxAttempts int
}

func (s *Service) runPendingJobs() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	rows, err := s.db.Pool.Query(ctx, `
		UPDATE _rapibase_jobs SET status='running', attempts=attempts+1, updated_at=now()
		WHERE id IN (
			SELECT id FROM _rapibase_jobs
			WHERE status='pending' AND run_at <= now()
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT 5
		)
		RETURNING id, name, payload, attempts, max_attempts`)
	if err != nil {
		cancel()
		return
	}
	var claimed []claimedJob
	for rows.Next() {
		var j claimedJob
		if err := rows.Scan(&j.id, &j.name, &j.payload, &j.attempts, &j.maxAttempts); err == nil {
			claimed = append(claimed, j)
		}
	}
	rows.Close()
	cancel()

	for _, j := range claimed {
		s.runJob(j)
	}
}

func (s *Service) runJob(j claimedJob) {
	var payload interface{}
	_ = json.Unmarshal(j.payload, &payload)

	_, err := s.call(context.Background(), "job", j.name, jobTimeout, payload)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err == nil {
		_, _ = s.db.Pool.Exec(ctx, `UPDATE _rapibase_jobs SET status='done', last_error=NULL, updated_at=now() WHERE id=$1`, j.id)
		return
	}
	if err == errNotFound {
		err = fmt.Errorf("no hay fn.job(%q) registrado", j.name)
	}
	if j.attempts >= j.maxAttempts {
		_, _ = s.db.Pool.Exec(ctx, `UPDATE _rapibase_jobs SET status='failed', last_error=$2, updated_at=now() WHERE id=$1`, j.id, err.Error())
		log.Printf("[fn] job %s (#%d) agotó reintentos: %v", j.name, j.id, err)
		return
	}
	_, _ = s.db.Pool.Exec(ctx,
		`UPDATE _rapibase_jobs SET status='pending', run_at=now() + make_interval(secs => $2), last_error=$3, updated_at=now() WHERE id=$1`,
		j.id, 30*j.attempts, err.Error())
}

// ListJobs devuelve los últimos trabajos para inspección (service key).
func (s *Service) ListJobs(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	res, err := s.db.ExecuteQuery(ctx,
		`SELECT id, name, status, attempts, max_attempts, last_error, run_at, created_at, updated_at
		 FROM _rapibase_jobs ORDER BY id DESC LIMIT $1`, []interface{}{limit})
	if err != nil {
		return nil, err
	}
	return res.Rows, nil
}
