package functions

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFn(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPFunctionTS(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "echo.ts", `
		interface Saludo { hola: string }
		fn.http("echo", (req: FnRequest): FnResponse => {
			const s: Saludo = { hola: req.query["nombre"] ?? "mundo" }
			return { metodo: req.method, saludo: s.hola }
		})
	`)
	s := New(nil, dir)
	meta, errs, _ := s.Snapshot()
	if len(errs) != 0 {
		t.Fatalf("errores inesperados: %v", errs)
	}
	if len(meta) != 1 || meta[0].Name != "echo" || meta[0].Kind != "http" {
		t.Fatalf("meta inesperada: %+v", meta)
	}
	res, err := s.InvokeHTTP(context.Background(), "echo", map[string]interface{}{
		"method": "GET",
		"query":  map[string]string{"nombre": "ada"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]interface{})
	if m["saludo"] != "ada" || m["metodo"] != "GET" {
		t.Fatalf("respuesta inesperada: %v", m)
	}
}

func TestEnvGetHostKeysAndSecrets(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "vars.ts", `
		fn.http("vars", () => ({
			svc: env.get("SERVICE_KEY"),
			anon: env.get("ANON_KEY"),
			url: env.get("APP_URL"),
			secreto: env.get("FN_TEST_SECRET"),
			vetada: env.get("PATH"),
		}))
	`)
	t.Setenv("FN_TEST_SECRET", "abc")
	s := New(nil, dir)
	s.SetHostEnv(map[string]string{
		"SERVICE_KEY": "sk-123",
		"ANON_KEY":    "ak-456",
		"APP_URL":     "http://localhost:8080",
	})
	res, err := s.InvokeHTTP(context.Background(), "vars", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]interface{})
	if m["svc"] != "sk-123" || m["anon"] != "ak-456" || m["url"] != "http://localhost:8080" {
		t.Fatalf("claves de instancia no expuestas: %v", m)
	}
	if m["secreto"] != "abc" {
		t.Fatalf("secret FN_ no legible: %v", m)
	}
	if m["vetada"] != "" {
		t.Fatalf("una variable fuera de la whitelist se filtró: %v", m)
	}
}

func TestAsyncHandlerResolves(t *testing.T) {
	dir := t.TempDir()
	// los LLM escriben async por instinto: await sobre valores síncronos
	// debe funcionar y la promesa quedar cumplida al salir.
	writeFn(t, dir, "a.ts", `
		fn.http("asincrona", async () => {
			const x = await 40
			return { total: x + 2 }
		})
	`)
	s := New(nil, dir)
	res, err := s.InvokeHTTP(context.Background(), "asincrona", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]interface{})["total"] != int64(42) {
		t.Fatalf("respuesta: %v", res)
	}
}

func TestTSCompileErrorReported(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "rota.ts", `fn.http("rota", (req => { return 1 })`) // paréntesis sin cerrar
	writeFn(t, dir, "sana.ts", `fn.http("sana", () => "ok")`)
	s := New(nil, dir)
	meta, errs, _ := s.Snapshot()
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "rota.ts") {
		t.Fatalf("esperaba error de rota.ts, errs=%v", errs)
	}
	// la function sana sigue cargando
	if len(meta) != 1 || meta[0].Name != "sana" {
		t.Fatalf("meta: %+v", meta)
	}
	if res, err := s.InvokeHTTP(context.Background(), "sana", map[string]interface{}{}); err != nil || res != "ok" {
		t.Fatalf("sana: res=%v err=%v", res, err)
	}
}

func TestPublicFlagAndCronRegistration(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "todo.ts", `
		fn.http("webhook", () => null, { public: true })
		fn.cron("limpieza", "0 3 * * *", () => { log("limpieza") })
		fn.cron("mala", "99 99 * *", () => {})
	`)
	s := New(nil, dir)
	meta, errs, _ := s.Snapshot()
	if exists, public := s.LookupHTTP("webhook"); !exists || !public {
		t.Fatalf("webhook debería ser público: exists=%v public=%v", exists, public)
	}
	foundCron := false
	for _, m := range meta {
		if m.Kind == "cron" && m.Name == "limpieza" && m.Spec == "0 3 * * *" {
			foundCron = true
		}
		if m.Name == "mala" {
			t.Fatalf("la spec inválida no debería registrarse: %+v", m)
		}
	}
	if !foundCron {
		t.Fatalf("cron limpieza no registrado: %+v", meta)
	}
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, " "), "mala") {
		t.Fatalf("esperaba error por la spec de 'mala': %v", errs)
	}
}

func TestThrowBecomesError(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "boom.ts", `
		fn.http("boom", () => { throw new Error("me rompí a propósito") })
	`)
	s := New(nil, dir)
	_, err := s.InvokeHTTP(context.Background(), "boom", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "me rompí a propósito") {
		t.Fatalf("err: %v", err)
	}
}

func TestConcurrentInvocations(t *testing.T) {
	dir := t.TempDir()
	writeFn(t, dir, "c.ts", `fn.http("suma", (req) => ({ r: req.json.a + req.json.b }))`)
	s := New(nil, dir)
	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(n int) {
			res, err := s.InvokeHTTP(context.Background(), "suma", map[string]interface{}{
				"json": map[string]interface{}{"a": n, "b": 1},
			})
			if err == nil {
				if res.(map[string]interface{})["r"] != int64(n+1) {
					err = os.ErrInvalid
				}
			}
			done <- err
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
