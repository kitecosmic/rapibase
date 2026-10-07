package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/storage"
)

// StorageHandler serves /api/v1/storage (app API, signed and public
// links) and /api/dashboard/storage (admin panel). All rules live in
// storage.Service; this file only translates HTTP.
type StorageHandler struct {
	svc *storage.Service
}

func NewStorageHandler(svc *storage.Service) *StorageHandler {
	return &StorageHandler{svc: svc}
}

// ---- helpers ------------------------------------------------------------

func storageIsService(c *fiber.Ctx) bool { return c.Locals("apiKeyType") == "service" }

func storageCallerID(c *fiber.Ctx) string {
	s, _ := c.Locals("userID").(string)
	return s
}

func storageActor(c *fiber.Ctx) storage.Actor {
	return storage.Actor{Service: storageIsService(c), UserID: storageCallerID(c)}
}

// adminActor: the dashboard admin has full access, like the service key.
var adminActor = storage.Actor{Service: true}

func opCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// keyParam returns the object path from the route wildcard, decoded.
func keyParam(c *fiber.Ctx) string {
	raw := c.Params("*")
	if k, err := url.PathUnescape(raw); err == nil {
		return k
	}
	return raw
}

func bucketParam(c *fiber.Ctx, name string) string {
	b, err := url.PathUnescape(c.Params(name))
	if err != nil {
		return c.Params(name)
	}
	return b
}

// storageFail turns a storage error into a JSON response.
func storageFail(c *fiber.Ctx, err error) error {
	if e, ok := storage.AsError(err); ok {
		return c.Status(e.Status).JSON(fiber.Map{"error": e.Message, "code": e.Code})
	}
	switch {
	case errors.Is(err, storage.ErrTokenExpired):
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "this link has expired", "code": "token_expired"})
	case errors.Is(err, storage.ErrTokenInvalid):
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "invalid or tampered link", "code": "invalid_token"})
	}
	log.Printf("storage: %s %s: %v", c.Method(), c.Path(), err)
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "storage error, please retry", "code": "internal"})
}

func forbidden(c *fiber.Ctx) error {
	return storageFail(c, storage.ErrForbidden)
}

func serviceOnly(c *fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error": "managing buckets requires the service key", "code": "service_key_required",
	})
}

func formBool(c *fiber.Ctx, name string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(c.FormValue(name)))
	if v == "" {
		return def
	}
	return v == "true" || v == "1" || v == "yes"
}

// cacheControlValue accepts "3600" (seconds, like Supabase) or a full
// Cache-Control header value.
func cacheControlValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return fmt.Sprintf("max-age=%d", n)
	}
	return v
}

func parseMetadata(c *fiber.Ctx, raw string) (map[string]interface{}, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var m map[string]interface{}
	if err := c.App().Config().JSONDecoder([]byte(raw), &m); err != nil {
		return nil, &storage.Error{Status: fiber.StatusBadRequest, Code: "invalid_metadata", Message: "metadata must be a JSON object"}
	}
	return m, nil
}

// ---- serving files --------------------------------------------------------

type serveMode int

const (
	servePublic serveMode = iota // anyone, cacheable by browsers and CDNs
	serveSigned                  // anyone with the link, private cache
	servePrivate                 // API keys / dashboard, revalidate each time
)

// serveObject streams an object with the headers a browser, a <video>
// player or a CDN expects: ETag/Last-Modified with 304s, single byte
// ranges (206/416), HEAD, inline or attachment disposition, and a
// sandboxing CSP for types that could run script on this origin.
func (h *StorageHandler) serveObject(c *fiber.Ctx, bucket, key string, mode serveMode, maxAge int) error {
	ctx := context.Background()
	st, err := h.svc.Stat(ctx, bucket, key)
	if err != nil {
		return storageFail(c, err)
	}
	contentType := st.ContentType
	if contentType == "" {
		contentType = storage.DetectContentType("", key)
	}
	etag := `"` + st.ETag + `"`

	switch mode {
	case servePublic:
		if st.CacheControl != "" {
			c.Set(fiber.HeaderCacheControl, "public, "+strings.TrimPrefix(st.CacheControl, "public, "))
		} else {
			c.Set(fiber.HeaderCacheControl, "public, max-age=3600")
		}
	case serveSigned:
		c.Set(fiber.HeaderCacheControl, fmt.Sprintf("private, max-age=%d", maxAge))
	default:
		c.Set(fiber.HeaderCacheControl, "private, no-cache")
	}
	c.Set(fiber.HeaderETag, etag)
	if !st.LastModified.IsZero() {
		c.Set(fiber.HeaderLastModified, st.LastModified.UTC().Format(http.TimeFormat))
	}
	c.Set(fiber.HeaderAcceptRanges, "bytes")
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Set("Cross-Origin-Resource-Policy", "cross-origin")
	if storage.ActiveContent(contentType) {
		c.Set(fiber.HeaderContentSecurityPolicy, "default-src 'none'; img-src data:; style-src 'unsafe-inline'; sandbox")
	}

	filename := path.Base(key)
	if c.Context().QueryArgs().Has("download") {
		if d := strings.TrimSpace(c.Query("download")); d != "" && d != "1" && d != "true" {
			filename = d
		}
		c.Set(fiber.HeaderContentDisposition, storage.ContentDisposition("attachment", filename))
	} else {
		c.Set(fiber.HeaderContentDisposition, storage.ContentDisposition("inline", filename))
	}

	// Conditional requests.
	if inm := c.Get(fiber.HeaderIfNoneMatch); inm != "" {
		if etagMatches(inm, etag) {
			return c.SendStatus(fiber.StatusNotModified)
		}
	} else if ims := c.Get(fiber.HeaderIfModifiedSince); ims != "" && !st.LastModified.IsZero() {
		if t, err := http.ParseTime(ims); err == nil && !st.LastModified.Truncate(time.Second).After(t) {
			return c.SendStatus(fiber.StatusNotModified)
		}
	}

	// Byte ranges (ignored when If-Range no longer matches).
	rangeHeader := c.Get(fiber.HeaderRange)
	if ir := c.Get(fiber.HeaderIfRange); ir != "" && ir != etag {
		rangeHeader = ""
	}
	rng, partial, err := storage.ParseRange(rangeHeader, st.Size)
	if err != nil {
		c.Set(fiber.HeaderContentRange, fmt.Sprintf("bytes */%d", st.Size))
		return c.Status(fiber.StatusRequestedRangeNotSatisfiable).JSON(fiber.Map{"error": "requested range not satisfiable"})
	}
	length := st.Size
	var openRange *storage.ByteRange
	if partial {
		length = rng.Length()
		openRange = &rng
		c.Status(fiber.StatusPartialContent)
		c.Set(fiber.HeaderContentRange, fmt.Sprintf("bytes %d-%d/%d", rng.Start, rng.End, st.Size))
	}
	c.Set(fiber.HeaderContentType, contentType)

	if c.Method() == fiber.MethodHead {
		c.Response().SkipBody = true
		c.Response().Header.SetContentLength(int(length))
		return nil
	}
	if length == 0 {
		return c.Send(nil)
	}
	body, err := h.svc.Open(ctx, bucket, key, openRange)
	if err != nil {
		return storageFail(c, err)
	}
	return c.SendStream(body, int(length))
}

func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimSpace(part)
		p = strings.TrimPrefix(p, "W/")
		if p == "*" || p == etag {
			return true
		}
	}
	return false
}

// ServePublic: GET/HEAD /api/v1/storage/public/:bucket/* — no key.
func (h *StorageHandler) ServePublic(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "bucket"), keyParam(c)
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	public, err := h.svc.IsPublic(ctx, bucket)
	if err != nil {
		return storageFail(c, err)
	}
	if !public {
		// Same answer as a missing bucket: don't reveal private buckets.
		return storageFail(c, storage.ErrBucketNotFound)
	}
	return h.serveObject(c, bucket, key, servePublic, 0)
}

// ServeSigned: GET/HEAD /api/v1/storage/sign/:bucket/*?token= — no key.
func (h *StorageHandler) ServeSigned(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "bucket"), keyParam(c)
	g, err := h.svc.VerifyGrant(c.Query("token"), storage.OpRead, bucket, key)
	if err != nil {
		return storageFail(c, err)
	}
	maxAge := int(time.Until(time.Unix(g.Exp, 0)).Seconds())
	if maxAge > 3600 {
		maxAge = 3600
	}
	return h.serveObject(c, bucket, key, serveSigned, max(maxAge, 0))
}

// ---- uploads --------------------------------------------------------------

type uploadForm struct {
	file        *multipart.FileHeader
	key         string
	owner       string
	metadata    map[string]interface{}
	upsert      bool
	cache       string
	contentType string
}

// readUploadForm parses the multipart upload used by the app API and
// the dashboard. The object path is `key` when given, else the folder
// field (`path` or `prefix`) joined with the file's own name.
func readUploadForm(c *fiber.Ctx, folderField string) (*uploadForm, error) {
	file, err := c.FormFile("file")
	if err != nil {
		return nil, &storage.Error{Status: fiber.StatusBadRequest, Code: "no_file", Message: "no file provided (multipart field \"file\")"}
	}
	f := &uploadForm{
		file:        file,
		owner:       strings.TrimSpace(c.FormValue("owner_id")),
		upsert:      formBool(c, "upsert", true),
		cache:       cacheControlValue(c.FormValue("cache_control")),
		contentType: strings.TrimSpace(c.FormValue("content_type")),
	}
	if f.contentType == "" {
		f.contentType = file.Header.Get(fiber.HeaderContentType)
	}
	if k := strings.TrimSpace(c.FormValue("key")); k != "" {
		f.key = strings.TrimLeft(k, "/")
	} else {
		f.key = storage.JoinKey(c.FormValue(folderField), file.Filename)
	}
	if f.metadata, err = parseMetadata(c, c.FormValue("metadata")); err != nil {
		return nil, err
	}
	return f, nil
}

func (h *StorageHandler) doUpload(c *fiber.Ctx, bucket string, f *uploadForm, actor storage.Actor) error {
	src, err := f.file.Open()
	if err != nil {
		return storageFail(c, err)
	}
	defer src.Close()
	ctx, cancel := opCtx(30 * time.Minute)
	defer cancel()
	obj, err := h.svc.Upload(ctx, storage.UploadInput{
		Bucket: bucket, Key: f.key, Body: src, Size: f.file.Size,
		ContentType: f.contentType, CacheControl: f.cache,
		Owner: f.owner, Metadata: f.metadata, Upsert: f.upsert, Actor: actor,
	})
	if err != nil {
		return storageFail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(obj)
}

// UploadObjectAPI: POST /api/v1/storage/:bucket/upload (multipart).
// App users always own what they upload; the service key may set any
// owner_id (or none).
func (h *StorageHandler) UploadObjectAPI(c *fiber.Ctx) error {
	f, err := readUploadForm(c, "path")
	if err != nil {
		return storageFail(c, err)
	}
	actor := storageActor(c)
	if !actor.Service {
		if actor.UserID == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Authentication required to upload"})
		}
		f.owner = actor.UserID
	}
	return h.doUpload(c, bucketParam(c, "bucket"), f, actor)
}

// UploadSigned: PUT /api/v1/storage/upload/sign/:bucket/*?token= — no
// key. The body is the raw file (Content-Type = its type) or a multipart
// form with a "file" field.
func (h *StorageHandler) UploadSigned(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "bucket"), keyParam(c)
	g, err := h.svc.VerifyGrant(c.Query("token"), storage.OpUpload, bucket, key)
	if err != nil {
		return storageFail(c, err)
	}
	actor := storage.Actor{Service: g.Owner == "", UserID: g.Owner}
	in := storage.UploadInput{Bucket: bucket, Key: key, Owner: g.Owner, Upsert: g.Upsert, Actor: actor,
		CacheControl: cacheControlValue(c.Get("x-cache-control"))}

	ctx, cancel := opCtx(30 * time.Minute)
	defer cancel()
	ct := c.Get(fiber.HeaderContentType)
	if strings.HasPrefix(strings.ToLower(ct), "multipart/form-data") {
		f, err := readUploadForm(c, "path")
		if err != nil {
			return storageFail(c, err)
		}
		src, err := f.file.Open()
		if err != nil {
			return storageFail(c, err)
		}
		defer src.Close()
		in.Body, in.Size, in.ContentType = src, f.file.Size, f.contentType
		if f.cache != "" {
			in.CacheControl = f.cache
		}
		in.Metadata = f.metadata
	} else {
		in.ContentType = ct
		in.Size = int64(c.Request().Header.ContentLength())
		if in.Size < 0 {
			in.Size = -1
		}
		if stream := c.Context().RequestBodyStream(); stream != nil {
			in.Body = stream
		} else {
			in.Body = strings.NewReader(string(c.Body()))
			in.Size = int64(len(c.Body()))
		}
	}
	obj, err := h.svc.Upload(ctx, in)
	if err != nil {
		return storageFail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(obj)
}

// ---- signed URLs ------------------------------------------------------------

type signRequest struct {
	ExpiresIn int      `json:"expires_in"` // seconds; default 3600, max 7 days
	Download  string   `json:"download"`   // "true" or a file name
	Paths     []string `json:"paths"`      // batch only
}

func (h *StorageHandler) signOne(ctx context.Context, actor storage.Actor, bucket, key string, req signRequest) (fiber.Map, error) {
	if err := storage.ValidateKey(key); err != nil {
		return nil, err
	}
	ok, err := h.svc.CanRead(ctx, actor, bucket, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, storage.ErrForbidden
	}
	if _, err := h.svc.Stat(ctx, bucket, key); err != nil {
		return nil, err
	}
	u, exp := h.svc.SignedURL(bucket, key, time.Duration(req.ExpiresIn)*time.Second, req.Download)
	return fiber.Map{"path": key, "signed_url": u, "expires_at": exp.UTC()}, nil
}

// CreateSignedURL: POST /api/v1/storage/sign/:bucket/* — a temporary
// link to one file, for anyone who can read it.
func (h *StorageHandler) CreateSignedURL(c *fiber.Ctx) error {
	var req signRequest
	_ = c.BodyParser(&req)
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	res, err := h.signOne(ctx, storageActor(c), bucketParam(c, "bucket"), keyParam(c), req)
	if err != nil {
		return storageFail(c, err)
	}
	return c.JSON(res)
}

// CreateSignedURLs: POST /api/v1/storage/sign/:bucket {paths: [...]}.
func (h *StorageHandler) CreateSignedURLs(c *fiber.Ctx) error {
	var req signRequest
	if err := c.BodyParser(&req); err != nil || len(req.Paths) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": `body must be {"paths": ["a.png", ...], "expires_in": 3600}`})
	}
	if len(req.Paths) > 1000 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "at most 1000 paths per request"})
	}
	ctx, cancel := opCtx(60 * time.Second)
	defer cancel()
	bucket, actor := bucketParam(c, "bucket"), storageActor(c)
	out := make([]fiber.Map, 0, len(req.Paths))
	for _, p := range req.Paths {
		res, err := h.signOne(ctx, actor, bucket, p, req)
		if err != nil {
			msg := "storage error"
			if e, ok := storage.AsError(err); ok {
				msg = e.Message
			}
			out = append(out, fiber.Map{"path": p, "signed_url": nil, "error": msg})
			continue
		}
		out = append(out, res)
	}
	return c.JSON(fiber.Map{"urls": out})
}

// CreateSignedUploadURL: POST /api/v1/storage/upload/sign/:bucket/* —
// lets a client upload one file straight to that path without keys
// (valid 2 hours). The file will belong to the user who asked for it.
func (h *StorageHandler) CreateSignedUploadURL(c *fiber.Ctx) error {
	var req struct {
		Upsert  bool   `json:"upsert"`
		OwnerID string `json:"owner_id"`
	}
	_ = c.BodyParser(&req)
	bucket, key, actor := bucketParam(c, "bucket"), keyParam(c), storageActor(c)
	if err := storage.ValidateKey(key); err != nil {
		return storageFail(c, err)
	}
	owner := req.OwnerID
	if !actor.Service {
		if actor.UserID == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Authentication required to upload"})
		}
		owner = actor.UserID
	}
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	if _, err := h.svc.GetBucket(ctx, bucket); err != nil {
		return storageFail(c, err)
	}
	// Fail now rather than when the client uploads: an app user can't
	// get a link that would replace someone else's file.
	if !actor.Service {
		if ok, err := h.svc.CanWrite(ctx, actor, bucket, key); err != nil {
			return storageFail(c, err)
		} else if !ok {
			if _, err := h.svc.Stat(ctx, bucket, key); err == nil {
				return forbidden(c)
			}
		}
	}
	u, tok, exp := h.svc.SignedUploadURL(bucket, key, owner, req.Upsert)
	return c.JSON(fiber.Map{"path": key, "signed_url": u, "token": tok, "expires_at": exp.UTC()})
}

// ---- app API: objects ---------------------------------------------------

// GetObjectAPI: GET/HEAD /api/v1/storage/:bucket/*.
func (h *StorageHandler) GetObjectAPI(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "bucket"), keyParam(c)
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	ok, err := h.svc.CanRead(ctx, storageActor(c), bucket, key)
	if err != nil {
		return storageFail(c, err)
	}
	if !ok {
		return forbidden(c)
	}
	return h.serveObject(c, bucket, key, servePrivate, 0)
}

// DeleteObjectAPI: DELETE /api/v1/storage/:bucket/*.
func (h *StorageHandler) DeleteObjectAPI(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "bucket"), keyParam(c)
	ctx, cancel := opCtx(30 * time.Second)
	defer cancel()
	ok, err := h.svc.CanWrite(ctx, storageActor(c), bucket, key)
	if err != nil {
		return storageFail(c, err)
	}
	if !ok {
		return forbidden(c)
	}
	if err := h.svc.Delete(ctx, bucket, key); err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"message": "Object deleted successfully"})
}

// RemoveObjectsAPI: POST /api/v1/storage/:bucket/remove {paths: [...]}.
// Paths the caller can't delete are reported, the rest are deleted.
func (h *StorageHandler) RemoveObjectsAPI(c *fiber.Ctx) error {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := c.BodyParser(&req); err != nil || len(req.Paths) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": `body must be {"paths": ["a.png", ...]}`})
	}
	if len(req.Paths) > 1000 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "at most 1000 paths per request"})
	}
	bucket, actor := bucketParam(c, "bucket"), storageActor(c)
	ctx, cancel := opCtx(2 * time.Minute)
	defer cancel()
	allowed := []string{}
	failed := []fiber.Map{}
	for _, p := range req.Paths {
		ok, err := h.svc.CanWrite(ctx, actor, bucket, p)
		if err != nil {
			return storageFail(c, err)
		}
		if ok {
			allowed = append(allowed, p)
		} else {
			failed = append(failed, fiber.Map{"path": p, "error": storage.ErrForbidden.Message})
		}
	}
	if err := h.svc.DeleteMany(ctx, bucket, allowed); err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"deleted": allowed, "errors": failed})
}

type copyRequest struct {
	From              string `json:"from"`
	To                string `json:"to"`
	SourceKey         string `json:"source_key"`
	DestinationKey    string `json:"destination_key"`
	DestinationBucket string `json:"destination_bucket"`
	Upsert            bool   `json:"upsert"`
}

func (h *StorageHandler) copyOrMove(c *fiber.Ctx, actor storage.Actor, move bool) error {
	var req copyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	src := strings.TrimLeft(firstNonEmpty(req.From, req.SourceKey), "/")
	dst := strings.TrimLeft(firstNonEmpty(req.To, req.DestinationKey), "/")
	if src == "" || dst == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": `body must be {"from": "a.png", "to": "b.png"}`})
	}
	bucket := bucketParam(c, "bucket")
	if bucket == "" {
		bucket = bucketParam(c, "name")
	}
	dstBucket := firstNonEmpty(req.DestinationBucket, bucket)
	ctx, cancel := opCtx(5 * time.Minute)
	defer cancel()
	obj, err := h.svc.CopyObject(ctx, actor, bucket, src, dstBucket, dst, move, req.Upsert)
	if err != nil {
		return storageFail(c, err)
	}
	msg := "Object copied successfully"
	if move {
		msg = "Object moved successfully"
	}
	return c.JSON(fiber.Map{"message": msg, "object": obj})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// MoveObjectAPI / CopyObjectAPI: POST /api/v1/storage/:bucket/{move,copy}.
func (h *StorageHandler) MoveObjectAPI(c *fiber.Ctx) error {
	return h.copyOrMove(c, storageActor(c), true)
}

func (h *StorageHandler) CopyObjectAPI(c *fiber.Ctx) error {
	return h.copyOrMove(c, storageActor(c), false)
}

// ListObjectsAPI: GET /api/v1/storage/:bucket/list?prefix=&limit=&offset=.
// Service key: the real folder listing. App users: every file of a
// public bucket, otherwise only their own files.
func (h *StorageHandler) ListObjectsAPI(c *fiber.Ctx) error {
	bucket, prefix := bucketParam(c, "bucket"), c.Query("prefix", "")
	ctx, cancel := opCtx(30 * time.Second)
	defer cancel()
	if storageIsService(c) {
		entries, err := h.svc.ListFolder(ctx, bucket, prefix, false)
		if err != nil {
			return storageFail(c, err)
		}
		return c.JSON(fiber.Map{"objects": entries, "prefix": prefix})
	}
	public, err := h.svc.IsPublic(ctx, bucket)
	if err != nil {
		return storageFail(c, err)
	}
	owner := storageCallerID(c)
	if public {
		owner = ""
	}
	objs, err := h.svc.ListRecorded(ctx, bucket, prefix, owner, c.QueryInt("limit", 1000), c.QueryInt("offset", 0))
	if err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"objects": objs, "prefix": prefix})
}

// ListObjectsByOwner: GET /api/v1/storage/owner/:owner_id. App users
// always get their own files, whatever id they pass.
func (h *StorageHandler) ListObjectsByOwner(c *fiber.Ctx) error {
	owner := c.Params("owner_id")
	if !storageIsService(c) {
		owner = storageCallerID(c)
	}
	ctx, cancel := opCtx(30 * time.Second)
	defer cancel()
	objs, err := h.svc.ListByOwner(ctx, owner)
	if err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"objects": objs, "owner": owner})
}

// SearchObjectsByMetadata: GET /api/v1/storage/:bucket/search?key=&value=.
func (h *StorageHandler) SearchObjectsByMetadata(c *fiber.Ctx) error {
	bucket, key, value := bucketParam(c, "bucket"), c.Query("key"), c.Query("value")
	if key == "" || value == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Both 'key' and 'value' query parameters are required"})
	}
	ctx, cancel := opCtx(30 * time.Second)
	defer cancel()
	owner := ""
	if !storageIsService(c) {
		public, err := h.svc.IsPublic(ctx, bucket)
		if err != nil {
			return storageFail(c, err)
		}
		if !public {
			if owner = storageCallerID(c); owner == "" {
				return c.JSON(fiber.Map{"objects": []any{}, "filter": fiber.Map{"key": key, "value": value}})
			}
		}
	}
	objs, err := h.svc.Search(ctx, bucket, key, value, owner)
	if err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"objects": objs, "filter": fiber.Map{"key": key, "value": value}})
}

// GetObjectMetadata: GET /api/v1/storage/:bucket/metadata/*.
func (h *StorageHandler) GetObjectMetadata(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "bucket"), keyParam(c)
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	ok, err := h.svc.CanRead(ctx, storageActor(c), bucket, key)
	if err != nil {
		return storageFail(c, err)
	}
	if !ok {
		return forbidden(c)
	}
	obj, err := h.svc.ObjectInfo(ctx, bucket, key)
	if err != nil {
		return storageFail(c, err)
	}
	return c.JSON(obj)
}

// UpdateObjectMetadata: PATCH /api/v1/storage/:bucket/metadata/*.
func (h *StorageHandler) UpdateObjectMetadata(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "bucket"), keyParam(c)
	var body struct {
		Metadata map[string]interface{} `json:"metadata"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	ok, err := h.svc.CanWrite(ctx, storageActor(c), bucket, key)
	if err != nil {
		return storageFail(c, err)
	}
	if !ok {
		return forbidden(c)
	}
	if err := h.svc.UpdateMetadata(ctx, bucket, key, body.Metadata); err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"message": "Metadata updated successfully", "metadata": body.Metadata})
}

// ---- buckets (app API with the service key, and dashboard) ---------------

type bucketBody struct {
	Name string `json:"name"`
	storage.BucketSettings
}

// bucketMap adds the fields older clients read (policy, creation_date)
// to the bucket row.
func bucketMap(b *database.StorageBucket) fiber.Map {
	policy := "private"
	if b.Public {
		policy = "public"
	}
	return fiber.Map{
		"id": b.ID, "name": b.Name, "public": b.Public, "policy": policy,
		"file_size_limit": b.FileSizeLimit, "allowed_mime_types": b.AllowedMimeTypes,
		"object_count": b.ObjectCount, "total_size": b.TotalSize,
		"created_at": b.CreatedAt, "creation_date": b.CreatedAt, "updated_at": b.UpdatedAt,
	}
}

func (h *StorageHandler) listBuckets(c *fiber.Ctx) error {
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	buckets, err := h.svc.ListBuckets(ctx)
	if err != nil {
		return storageFail(c, err)
	}
	out := make([]fiber.Map, len(buckets))
	for i := range buckets {
		out[i] = bucketMap(&buckets[i])
	}
	return c.JSON(fiber.Map{"buckets": out})
}

func (h *StorageHandler) createBucket(c *fiber.Ctx) error {
	var body bucketBody
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	ctx, cancel := opCtx(30 * time.Second)
	defer cancel()
	b, err := h.svc.CreateBucket(ctx, body.Name, body.BucketSettings)
	if err != nil {
		return storageFail(c, err)
	}
	res := bucketMap(b)
	res["message"] = "Bucket created successfully"
	return c.Status(fiber.StatusCreated).JSON(res)
}

func (h *StorageHandler) getBucket(c *fiber.Ctx, param string) error {
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	b, err := h.svc.GetBucket(ctx, bucketParam(c, param))
	if err != nil {
		return storageFail(c, err)
	}
	return c.JSON(bucketMap(b))
}

func (h *StorageHandler) updateBucket(c *fiber.Ctx, param string) error {
	var set storage.BucketSettings
	if err := c.BodyParser(&set); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	b, err := h.svc.UpdateBucket(ctx, bucketParam(c, param), set)
	if err != nil {
		return storageFail(c, err)
	}
	return c.JSON(bucketMap(b))
}

func (h *StorageHandler) deleteBucket(c *fiber.Ctx, param string) error {
	ctx, cancel := opCtx(10 * time.Minute)
	defer cancel()
	if err := h.svc.DeleteBucket(ctx, bucketParam(c, param), c.Query("force") == "true"); err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"message": "Bucket deleted successfully"})
}

// App API (service key only): /api/v1/storage/buckets[/:name].
func (h *StorageHandler) ListBucketsAPI(c *fiber.Ctx) error {
	if !storageIsService(c) {
		return serviceOnly(c)
	}
	return h.listBuckets(c)
}

func (h *StorageHandler) CreateBucketAPI(c *fiber.Ctx) error {
	if !storageIsService(c) {
		return serviceOnly(c)
	}
	return h.createBucket(c)
}

func (h *StorageHandler) GetBucketAPI(c *fiber.Ctx) error {
	if !storageIsService(c) {
		return serviceOnly(c)
	}
	return h.getBucket(c, "name")
}

func (h *StorageHandler) UpdateBucketAPI(c *fiber.Ctx) error {
	if !storageIsService(c) {
		return serviceOnly(c)
	}
	return h.updateBucket(c, "name")
}

func (h *StorageHandler) DeleteBucketAPI(c *fiber.Ctx) error {
	if !storageIsService(c) {
		return serviceOnly(c)
	}
	return h.deleteBucket(c, "name")
}

// ---- dashboard (admin JWT): /api/dashboard/storage/… -----------------------

func (h *StorageHandler) ListBuckets(c *fiber.Ctx) error   { return h.listBuckets(c) }
func (h *StorageHandler) CreateBucket(c *fiber.Ctx) error  { return h.createBucket(c) }
func (h *StorageHandler) GetBucketInfo(c *fiber.Ctx) error { return h.getBucket(c, "name") }
func (h *StorageHandler) UpdateBucket(c *fiber.Ctx) error  { return h.updateBucket(c, "name") }
func (h *StorageHandler) DeleteBucket(c *fiber.Ctx) error  { return h.deleteBucket(c, "name") }

// SetBucketPolicy: PATCH …/buckets/:name/policy {public} (older clients).
func (h *StorageHandler) SetBucketPolicy(c *fiber.Ctx) error {
	var body struct {
		Public bool `json:"public"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	b, err := h.svc.UpdateBucket(ctx, bucketParam(c, "name"), storage.BucketSettings{Public: &body.Public})
	if err != nil {
		return storageFail(c, err)
	}
	res := bucketMap(b)
	res["message"] = "Bucket policy updated"
	return c.JSON(res)
}

// ListObjects: one folder level, each file with a 1-hour signed URL so
// the dashboard can show thumbnails and download without extra calls.
func (h *StorageHandler) ListObjects(c *fiber.Ctx) error {
	bucket, prefix := bucketParam(c, "name"), c.Query("prefix", "")
	ctx, cancel := opCtx(30 * time.Second)
	defer cancel()
	entries, err := h.svc.ListFolder(ctx, bucket, prefix, true)
	if err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"objects": entries, "prefix": prefix})
}

func (h *StorageHandler) UploadObject(c *fiber.Ctx) error {
	f, err := readUploadForm(c, "prefix")
	if err != nil {
		return storageFail(c, err)
	}
	return h.doUpload(c, bucketParam(c, "name"), f, adminActor)
}

func (h *StorageHandler) GetObject(c *fiber.Ctx) error {
	return h.serveObject(c, bucketParam(c, "name"), keyParam(c), servePrivate, 0)
}

// DeleteObject deletes a file, or a whole folder when the path ends in "/".
func (h *StorageHandler) DeleteObject(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "name"), keyParam(c)
	ctx, cancel := opCtx(5 * time.Minute)
	defer cancel()
	if strings.HasSuffix(key, "/") {
		n, err := h.svc.DeleteFolder(ctx, bucket, key)
		if err != nil {
			return storageFail(c, err)
		}
		return c.JSON(fiber.Map{"message": "Folder deleted successfully", "deleted": n})
	}
	if err := h.svc.Delete(ctx, bucket, key); err != nil {
		return storageFail(c, err)
	}
	return c.JSON(fiber.Map{"message": "Object deleted successfully"})
}

// GetPresignedURL: GET …/buckets/:name/presign/*?expiry=<minutes>.
func (h *StorageHandler) GetPresignedURL(c *fiber.Ctx) error {
	bucket, key := bucketParam(c, "name"), keyParam(c)
	ctx, cancel := opCtx(15 * time.Second)
	defer cancel()
	if _, err := h.svc.Stat(ctx, bucket, key); err != nil {
		return storageFail(c, err)
	}
	ttl := storage.ClampTTL(time.Duration(c.QueryInt("expiry", 60)) * time.Minute)
	u, exp := h.svc.SignedURL(bucket, key, ttl, c.Query("download"))
	return c.JSON(fiber.Map{"url": u, "expires_in": int(ttl.Seconds()), "expires_at": exp.UTC()})
}

func (h *StorageHandler) CreateFolder(c *fiber.Ctx) error {
	var body struct {
		Path string `json:"path"`
	}
	if err := c.BodyParser(&body); err != nil || strings.Trim(body.Path, "/") == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Path is required"})
	}
	ctx, cancel := opCtx(30 * time.Second)
	defer cancel()
	if err := h.svc.CreateFolder(ctx, bucketParam(c, "name"), body.Path); err != nil {
		return storageFail(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"message": "Folder created successfully", "path": body.Path})
}

func (h *StorageHandler) MoveObject(c *fiber.Ctx) error { return h.copyOrMove(c, adminActor, true) }
func (h *StorageHandler) CopyObject(c *fiber.Ctx) error { return h.copyOrMove(c, adminActor, false) }
