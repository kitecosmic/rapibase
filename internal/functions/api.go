package functions

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/rapibase/rapibase/internal/auth"
	"github.com/rapibase/rapibase/internal/config"
)

//go:embed rapibase.d.ts
var typeDefs string

var fileNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+\.(ts|js)$`)

const maxFunctionFileSize = 512 * 1024

// Setup monta todo el subsistema de functions en la app:
//
//	ALL  /api/fn/:name              — invocar (auth según la function)
//	GET  /api/v1/functions          — listar (service key)
//	GET  /api/v1/functions/:file    — código subido + X-Content-SHA256 (service key)
//	POST /api/v1/functions          — subir .ts/.js y recargar (service key)
//	DELETE /api/v1/functions/:file  — borrar archivo y recargar (service key)
//	GET  /api/v1/functions/types    — rapibase.d.ts (público)
//	POST /api/v1/jobs               — encolar un job (service key)
//	GET  /api/v1/jobs               — inspeccionar la cola (service key)
func Setup(app *fiber.App, svc *Service, cfg *config.Config, jwtManager *auth.JWTManager, requireServiceKey fiber.Handler) {
	svc.SetHostEnv(map[string]string{
		"SERVICE_KEY": cfg.ServiceKey,
		"ANON_KEY":    cfg.AnonKey,
		"APP_URL":     cfg.AppURL,
	})
	h := &httpHandler{svc: svc, cfg: cfg, jwt: jwtManager}

	app.All("/api/fn/:name", h.invoke)

	app.Get("/api/v1/functions/types", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/plain; charset=utf-8")
		return c.SendString(typeDefs)
	})
	app.Get("/api/v1/functions", requireServiceKey, h.list)
	app.Get("/api/v1/functions/:file", requireServiceKey, h.source)
	app.Post("/api/v1/functions", requireServiceKey, h.upload)
	app.Delete("/api/v1/functions/:file", requireServiceKey, h.remove)

	app.Post("/api/v1/jobs", requireServiceKey, h.enqueue)
	app.Get("/api/v1/jobs", requireServiceKey, h.listJobs)
}

type httpHandler struct {
	svc *Service
	cfg *config.Config
	jwt *auth.JWTManager
}

func bearer(header string) string {
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

// invoke despacha una request a la function. Auth (salvo public:true):
// SERVICE_KEY directa, o ANON_KEY + JWT del usuario de la app — el mismo
// contrato que el resto de /api/v1.
func (h *httpHandler) invoke(c *fiber.Ctx) error {
	name := c.Params("name")
	exists, public := h.svc.LookupHTTP(name)
	if !exists {
		return c.Status(404).JSON(fiber.Map{"error": "function no encontrada"})
	}

	var user map[string]interface{}
	role := "anon"
	if !public {
		key := c.Get("apikey")
		bearerTok := bearer(c.Get("Authorization"))
		if key == "" && h.cfg.KeyKind(bearerTok) != "" {
			key = bearerTok
			bearerTok = ""
		}
		switch h.cfg.KeyKind(key) {
		case "service":
			role = "service"
		case "anon":
			claims, err := h.jwt.ValidateToken(bearerTok, auth.AudienceApp)
			if err != nil {
				return c.Status(401).JSON(fiber.Map{"error": "con ANON_KEY hace falta el JWT del usuario (Authorization: Bearer)"})
			}
			role = "authenticated"
			user = map[string]interface{}{"id": claims.UserID, "email": claims.Email, "role": claims.Role}
		default:
			return c.Status(401).JSON(fiber.Map{"error": "apikey requerida"})
		}
	}

	headers := map[string]string{}
	for k, vals := range c.GetReqHeaders() {
		if len(vals) > 0 {
			headers[strings.ToLower(k)] = vals[0]
		}
	}
	delete(headers, "apikey")
	delete(headers, "authorization")

	body := string(c.Body())
	var parsed interface{}
	if strings.Contains(headers["content-type"], "application/json") && body != "" {
		_ = json.Unmarshal([]byte(body), &parsed)
	}

	req := map[string]interface{}{
		"method":  c.Method(),
		"path":    c.Path(),
		"query":   c.Queries(),
		"headers": headers,
		"body":    body,
		"json":    parsed,
		"role":    role,
		"user":    user,
	}

	result, err := h.svc.InvokeHTTP(c.Context(), name, req)
	if err != nil {
		if err == errNotFound {
			return c.Status(404).JSON(fiber.Map{"error": "function no encontrada"})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return writeResult(c, result)
}

// writeResult mapea el valor devuelto por el handler a la respuesta HTTP:
// nil → 204; string → texto; objeto {status?, headers?, json?|body?} →
// respuesta explícita; cualquier otra cosa → JSON 200.
//
// Un objeto solo cuenta como respuesta explícita si TODAS sus claves son
// status/headers/json/body y status (si está) es un número 100–599. Antes
// bastaba con que tuviera "status": un dato normal como
// {status: "activo", nombre: "x"} se tomaba por respuesta y al cliente le
// llegaba "OK", perdiendo el cuerpo sin aviso.
func writeResult(c *fiber.Ctx, result interface{}) error {
	if result == nil {
		return c.SendStatus(204)
	}
	if s, ok := result.(string); ok {
		c.Set("Content-Type", "text/plain; charset=utf-8")
		return c.SendString(s)
	}
	if m, ok := result.(map[string]interface{}); ok {
		if status, ok := explicitResponse(m); ok {
			if hdrs, ok := m["headers"].(map[string]interface{}); ok {
				for k, v := range hdrs {
					c.Set(k, fmt.Sprint(v))
				}
			}
			if v, ok := m["json"]; ok {
				return c.Status(status).JSON(v)
			}
			if v, ok := m["body"]; ok {
				c.Status(status)
				if s, ok := v.(string); ok {
					return c.SendString(s)
				}
				return c.JSON(v)
			}
			return c.SendStatus(status)
		}
	}
	return c.JSON(result)
}

// explicitResponse decide si el objeto devuelto describe una respuesta
// HTTP ({status?, headers?, json?|body?}) y con qué status. Ver writeResult.
func explicitResponse(m map[string]interface{}) (int, bool) {
	if len(m) == 0 {
		return 0, false
	}
	for k := range m {
		switch k {
		case "status", "headers", "json", "body":
		default:
			return 0, false
		}
	}
	_, hasStatus := m["status"]
	_, hasBody := m["body"]
	_, hasJSON := m["json"]
	if !hasStatus && !hasBody && !hasJSON {
		return 0, false
	}
	if h, ok := m["headers"]; ok && h != nil {
		if _, ok := h.(map[string]interface{}); !ok {
			return 0, false
		}
	}
	status := 200
	if hasStatus {
		f, ok := toInt(m["status"])
		if !ok || f < 100 || f > 599 {
			return 0, false
		}
		status = f
	}
	return status, true
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func (h *httpHandler) list(c *fiber.Ctx) error {
	meta, errs, files := h.svc.Snapshot()
	return c.JSON(fiber.Map{"functions": meta, "errors": errs, "files": files})
}

// upload guarda archivos .ts/.js (multipart, campo "file", repetible) y
// recarga. Los errores de compilación de TS vuelven en la respuesta — el
// agente ve al instante si su function no carga.
func (h *httpHandler) upload(c *fiber.Ctx) error {
	form, err := c.MultipartForm()
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "multipart requerido: -F \"file=@checkout.ts\" (repetible)"})
	}
	uploads := form.File["file"]
	if len(uploads) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "sin archivos: usa el campo \"file\""})
	}
	if len(uploads) > 50 {
		return c.Status(400).JSON(fiber.Map{"error": "máximo 50 archivos por subida"})
	}
	var saved []string
	for _, f := range uploads {
		name := filepath.Base(f.Filename)
		if !fileNameRe.MatchString(name) {
			return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("nombre inválido %q: solo [a-zA-Z0-9._-] y extensión .ts o .js", f.Filename)})
		}
		if f.Size > maxFunctionFileSize {
			return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("%s supera el máximo de %d KB", name, maxFunctionFileSize/1024)})
		}
		src, err := f.Open()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		data, err := io.ReadAll(io.LimitReader(src, maxFunctionFileSize+1))
		src.Close()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		if err := os.WriteFile(filepath.Join(h.svc.Dir(), name), data, 0o644); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		saved = append(saved, name)
	}
	h.svc.Reload()
	meta, errs, files := h.svc.Snapshot()
	return c.Status(201).JSON(fiber.Map{"saved": saved, "functions": meta, "errors": errs, "files": files})
}

// source devuelve el archivo tal como quedó subido, para comparar contra el
// repo: el cuerpo es el código exacto y X-Content-SHA256 su hash (el mismo
// que da `sha256sum archivo.ts`).
func (h *httpHandler) source(c *fiber.Ctx) error {
	name := filepath.Base(c.Params("file"))
	if !fileNameRe.MatchString(name) {
		return c.Status(400).JSON(fiber.Map{"error": "nombre inválido: solo [a-zA-Z0-9._-] y extensión .ts o .js"})
	}
	data, err := os.ReadFile(filepath.Join(h.svc.Dir(), name))
	if err != nil {
		if os.IsNotExist(err) {
			return c.Status(404).JSON(fiber.Map{"error": "archivo no encontrado: " + name})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	sum := sha256.Sum256(data)
	c.Set("X-Content-SHA256", hex.EncodeToString(sum[:]))
	c.Set("Cache-Control", "no-store")
	c.Set("Content-Type", "text/plain; charset=utf-8")
	return c.Send(data)
}

func (h *httpHandler) remove(c *fiber.Ctx) error {
	name := filepath.Base(c.Params("file"))
	if !fileNameRe.MatchString(name) {
		return c.Status(400).JSON(fiber.Map{"error": "nombre inválido"})
	}
	if err := os.Remove(filepath.Join(h.svc.Dir(), name)); err != nil {
		if os.IsNotExist(err) {
			return c.Status(404).JSON(fiber.Map{"error": "archivo no encontrado"})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.svc.Reload()
	meta, errs, files := h.svc.Snapshot()
	return c.JSON(fiber.Map{"deleted": name, "functions": meta, "errors": errs, "files": files})
}

func (h *httpHandler) enqueue(c *fiber.Ctx) error {
	var body struct {
		Name         string      `json:"name"`
		Payload      interface{} `json:"payload"`
		DelaySeconds int         `json:"delay_seconds"`
	}
	if err := c.BodyParser(&body); err != nil || body.Name == "" {
		return c.Status(400).JSON(fiber.Map{"error": `body JSON: {"name":"...", "payload":{...}, "delay_seconds":0}`})
	}
	id, err := h.svc.Enqueue(context.Background(), body.Name, body.Payload, body.DelaySeconds)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"id": id, "status": "pending"})
}

func (h *httpHandler) listJobs(c *fiber.Ctx) error {
	jobs, err := h.svc.ListJobs(c.Context(), c.QueryInt("limit", 50))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if jobs == nil {
		jobs = []map[string]interface{}{}
	}
	return c.JSON(fiber.Map{"jobs": jobs})
}
