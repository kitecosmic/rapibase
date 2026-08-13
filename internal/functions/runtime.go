// Package functions: serverless functions TypeScript/JavaScript dentro de
// la instancia. Los archivos de FUNCTIONS_DIR se transpilan con esbuild y
// corren en un pool de intérpretes goja embebidos — el código del usuario
// solo ve las APIs que este archivo le expone (db, http, env, jobs, …):
// sin filesystem, sin syscalls, sin proceso aparte.
package functions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/robfig/cron/v3"
)

// nameRe limita los nombres de functions/jobs a algo sano para URLs.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type httpEntry struct {
	fn     goja.Callable
	public bool
}

// engine es un intérprete goja con todas las functions cargadas. NO es
// seguro para uso concurrente: el pool garantiza un goroutine a la vez.
type engine struct {
	vm   *goja.Runtime
	http map[string]httpEntry
	cron map[string]goja.Callable
	jobs map[string]goja.Callable

	svc *Service
	// ctx de la invocación en curso — las APIs del host (db, fetch) lo usan
	// para respetar el timeout/interrupt de la llamada.
	ctx context.Context
	// currentFile: archivo cuyo programa se está ejecutando durante la
	// carga; sirve para atribuir registros en los metadatos.
	currentFile string
	// regErrs acumula errores de registro (nombre inválido, cron malo…)
	// detectados mientras corren los programas.
	regErrs []string
}

func (e *engine) throw(err error) {
	panic(e.vm.NewGoError(err))
}

func (e *engine) callCtx() context.Context {
	if e.ctx != nil {
		return e.ctx
	}
	return context.Background()
}

// newEngine construye un intérprete con las APIs del host instaladas y
// ejecuta todos los programas (cada archivo registra sus functions). Si
// recordsMeta es true, este engine alimenta los metadatos del Service
// (svc.loadMu debe estar tomado por el llamador).
func newEngine(svc *Service, programs []compiled, recordsMeta bool) (*engine, error) {
	e := &engine{
		vm:   goja.New(),
		http: map[string]httpEntry{},
		cron: map[string]goja.Callable{},
		jobs: map[string]goja.Callable{},
		svc:  svc,
	}
	if recordsMeta {
		svc.metaEngine = e
	}
	e.installAPIs()

	for _, p := range programs {
		e.currentFile = p.file
		if _, err := e.vm.RunProgram(p.prog); err != nil {
			return nil, fmt.Errorf("%s: %s", p.file, jsErrString(err))
		}
	}
	e.currentFile = ""
	return e, nil
}

func (e *engine) installAPIs() {
	vm := e.vm

	// --- registro: fn.http / fn.cron / fn.job -------------------------
	fnObj := vm.NewObject()
	_ = fnObj.Set("http", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		handler, ok := goja.AssertFunction(call.Argument(1))
		if !nameRe.MatchString(name) || !ok {
			e.regErrs = append(e.regErrs, fmt.Sprintf("%s: fn.http(%q): nombre inválido o handler ausente", e.currentFile, name))
			return goja.Undefined()
		}
		public := false
		if opts := call.Argument(2); opts != nil && !goja.IsUndefined(opts) && !goja.IsNull(opts) {
			if o := opts.ToObject(vm); o != nil {
				public = o.Get("public") != nil && o.Get("public").ToBoolean()
			}
		}
		e.http[name] = httpEntry{fn: handler, public: public}
		e.svc.recordMeta(e, Meta{Name: name, Kind: "http", File: e.currentFile, Public: public})
		return goja.Undefined()
	})
	_ = fnObj.Set("cron", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		spec := call.Argument(1).String()
		handler, ok := goja.AssertFunction(call.Argument(2))
		if !nameRe.MatchString(name) || !ok {
			e.regErrs = append(e.regErrs, fmt.Sprintf("%s: fn.cron(%q): nombre inválido o handler ausente", e.currentFile, name))
			return goja.Undefined()
		}
		if _, err := cron.ParseStandard(spec); err != nil {
			e.regErrs = append(e.regErrs, fmt.Sprintf("%s: fn.cron(%q): spec %q inválida: %v", e.currentFile, name, spec, err))
			return goja.Undefined()
		}
		e.cron[name] = handler
		e.svc.recordMeta(e, Meta{Name: name, Kind: "cron", File: e.currentFile, Spec: spec})
		return goja.Undefined()
	})
	_ = fnObj.Set("job", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		handler, ok := goja.AssertFunction(call.Argument(1))
		if !nameRe.MatchString(name) || !ok {
			e.regErrs = append(e.regErrs, fmt.Sprintf("%s: fn.job(%q): nombre inválido o handler ausente", e.currentFile, name))
			return goja.Undefined()
		}
		e.jobs[name] = handler
		e.svc.recordMeta(e, Meta{Name: name, Kind: "job", File: e.currentFile})
		return goja.Undefined()
	})
	vm.Set("fn", fnObj)

	// --- db.query / db.exec -------------------------------------------
	dbObj := vm.NewObject()
	_ = dbObj.Set("query", func(call goja.FunctionCall) goja.Value {
		rows, _, err := e.runSQL(call)
		if err != nil {
			e.throw(err)
		}
		if rows == nil {
			rows = []map[string]interface{}{}
		}
		return vm.ToValue(rows)
	})
	_ = dbObj.Set("exec", func(call goja.FunctionCall) goja.Value {
		_, affected, err := e.runSQL(call)
		if err != nil {
			e.throw(err)
		}
		return vm.ToValue(affected)
	})
	vm.Set("db", dbObj)

	// --- http.fetch -----------------------------------------------------
	httpObj := vm.NewObject()
	_ = httpObj.Set("fetch", func(call goja.FunctionCall) goja.Value {
		res, err := e.fetch(call)
		if err != nil {
			e.throw(err)
		}
		return res
	})
	vm.Set("http", httpObj)
	// alias global fetch(): los LLM lo escriben por instinto. Es síncrono,
	// pero `await` sobre un valor no-promesa es válido, así que el estilo
	// `const r = await fetch(...)` también funciona.
	vm.Set("fetch", httpObj.Get("fetch"))

	// --- env.get (solo FN_*) --------------------------------------------
	envObj := vm.NewObject()
	_ = envObj.Set("get", func(name string) string {
		if strings.HasPrefix(name, "FN_") {
			return os.Getenv(name)
		}
		return ""
	})
	vm.Set("env", envObj)

	// --- jobs.enqueue ----------------------------------------------------
	jobsObj := vm.NewObject()
	_ = jobsObj.Set("enqueue", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		payload := call.Argument(1).Export()
		delay := 0
		if opts := call.Argument(2); opts != nil && !goja.IsUndefined(opts) && !goja.IsNull(opts) {
			if o := opts.ToObject(vm); o != nil && o.Get("delaySeconds") != nil {
				delay = int(o.Get("delaySeconds").ToInteger())
			}
		}
		id, err := e.svc.Enqueue(e.callCtx(), name, payload, delay)
		if err != nil {
			e.throw(err)
		}
		return vm.ToValue(id)
	})
	vm.Set("jobs", jobsObj)

	// --- console / log / sleep -------------------------------------------
	logFn := func(level string) func(call goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				parts = append(parts, stringify(a))
			}
			e.svc.logf("[fn]%s %s", level, strings.Join(parts, " "))
			return goja.Undefined()
		}
	}
	consoleObj := vm.NewObject()
	_ = consoleObj.Set("log", logFn(""))
	_ = consoleObj.Set("warn", logFn(" WARN"))
	_ = consoleObj.Set("error", logFn(" ERROR"))
	vm.Set("console", consoleObj)
	vm.Set("log", consoleObj.Get("log"))

	vm.Set("sleep", func(ms int) {
		if ms <= 0 {
			return
		}
		if ms > 10000 {
			ms = 10000
		}
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
		case <-e.callCtx().Done():
		}
	})
}

// runSQL ejecuta SQL parametrizado ($1, $2, …) con los privilegios del
// servidor — las functions son código del dueño del proyecto.
func (e *engine) runSQL(call goja.FunctionCall) ([]map[string]interface{}, int64, error) {
	if e.svc.db == nil {
		return nil, 0, fmt.Errorf("db no disponible")
	}
	sql := call.Argument(0).String()
	params := make([]interface{}, 0, len(call.Arguments)-1)
	for _, a := range call.Arguments[1:] {
		params = append(params, a.Export())
	}
	res, err := e.svc.db.ExecuteQuery(e.callCtx(), sql, params)
	if err != nil {
		return nil, 0, err
	}
	return res.Rows, res.RowsAffected, nil
}

// fetch: cliente HTTP síncrono. Devuelve {status, ok, headers, body, json()}.
func (e *engine) fetch(call goja.FunctionCall) (goja.Value, error) {
	rawURL := call.Argument(0).String()
	method := "GET"
	var body io.Reader
	headers := map[string]string{}
	timeout := 15 * time.Second

	if opts := call.Argument(1); opts != nil && !goja.IsUndefined(opts) && !goja.IsNull(opts) {
		o := opts.ToObject(e.vm)
		if v := o.Get("method"); v != nil && !goja.IsUndefined(v) {
			method = strings.ToUpper(v.String())
		}
		if v := o.Get("body"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
			switch exported := v.Export().(type) {
			case string:
				body = strings.NewReader(exported)
			default:
				b, err := json.Marshal(exported)
				if err != nil {
					return nil, err
				}
				body = strings.NewReader(string(b))
				headers["Content-Type"] = "application/json"
			}
		}
		if v := o.Get("headers"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
			if m, ok := v.Export().(map[string]interface{}); ok {
				for k, hv := range m {
					headers[k] = fmt.Sprint(hv)
				}
			}
		}
		if v := o.Get("timeoutMs"); v != nil && !goja.IsUndefined(v) {
			if ms := v.ToInteger(); ms > 0 && ms <= 60000 {
				timeout = time.Duration(ms) * time.Millisecond
			}
		}
	}

	ctx, cancel := context.WithTimeout(e.callCtx(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}

	obj := e.vm.NewObject()
	_ = obj.Set("status", resp.StatusCode)
	_ = obj.Set("ok", resp.StatusCode >= 200 && resp.StatusCode < 300)
	respHeaders := map[string]string{}
	for k := range resp.Header {
		respHeaders[strings.ToLower(k)] = resp.Header.Get(k)
	}
	_ = obj.Set("headers", respHeaders)
	_ = obj.Set("body", string(respBody))
	_ = obj.Set("json", func() goja.Value {
		var v interface{}
		if err := json.Unmarshal(respBody, &v); err != nil {
			e.throw(fmt.Errorf("la respuesta no es JSON: %w", err))
		}
		return e.vm.ToValue(v)
	})
	return obj, nil
}

func stringify(v goja.Value) string {
	exported := v.Export()
	switch exported.(type) {
	case map[string]interface{}, []interface{}:
		if b, err := json.Marshal(exported); err == nil {
			return string(b)
		}
	}
	return fmt.Sprint(exported)
}

// invoke llama a un handler JS con timeout duro (vm.Interrupt) y resuelve
// promesas ya asentadas — un handler `async` cuyas awaits son sobre las
// APIs síncronas del host queda cumplido al vaciarse las microtareas.
func (e *engine) invoke(ctx context.Context, fn goja.Callable, timeout time.Duration, args ...goja.Value) (interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	e.ctx = ctx
	defer func() { e.ctx = nil }()

	timer := time.AfterFunc(timeout, func() { e.vm.Interrupt("timeout: la function superó el límite de ejecución") })
	defer timer.Stop()
	defer e.vm.ClearInterrupt()

	v, err := fn(goja.Undefined(), args...)
	if err != nil {
		return nil, fmt.Errorf("%s", jsErrString(err))
	}
	if p, ok := v.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStateFulfilled:
			return p.Result().Export(), nil
		case goja.PromiseStateRejected:
			return nil, fmt.Errorf("%s", stringify(p.Result()))
		default:
			return nil, fmt.Errorf("la function devolvió una promesa pendiente — las APIs (db, fetch, jobs) son síncronas; no hay I/O asíncrono que esperar")
		}
	}
	return v.Export(), nil
}

// jsErrString limpia el error de goja para el cliente.
func jsErrString(err error) string {
	var ex *goja.Exception
	if ok := asGojaException(err, &ex); ok {
		return ex.Value().String()
	}
	return err.Error()
}

func asGojaException(err error, target **goja.Exception) bool {
	ex, ok := err.(*goja.Exception)
	if ok {
		*target = ex
	}
	return ok
}
