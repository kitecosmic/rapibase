package storage

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rapibase/rapibase/internal/database"
)

// Service is rapibase storage: buckets and access rules live in Postgres
// (storage_buckets / storage_objects), bytes live in a private S3
// backend, and every file reaches the outside world through rapibase —
// public buckets at /api/v1/storage/public/…, everything else with the
// API keys or a signed URL. The handlers, the MCP tools and the
// dashboard all go through here, so the rules are the same everywhere.
type Service struct {
	s3      *Client
	db      *database.DB
	signer  *Signer
	baseURL string
}

func NewService(s3 *Client, db *database.DB, jwtSecret, appURL string) *Service {
	return &Service{
		s3:      s3,
		db:      db,
		signer:  NewSigner(jwtSecret),
		baseURL: strings.TrimSuffix(appURL, "/"),
	}
}

// Actor is who is asking: the service key (full access) or an app user.
type Actor struct {
	Service bool
	UserID  string
}

func (a Actor) owns(o *database.StorageObject) bool {
	return a.UserID != "" && o != nil && o.Owner != nil && *o.Owner == a.UserID
}

// Object is the public view of a stored file.
type Object struct {
	ID          string                 `json:"id,omitempty"`
	Bucket      string                 `json:"bucket"`
	Key         string                 `json:"key"`
	Size        int64                  `json:"size"`
	ContentType string                 `json:"content_type"`
	ETag        string                 `json:"etag"`
	Owner       *string                `json:"owner"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	// URL is the authenticated API path (needs apikey / user JWT);
	// PublicURL is set only when the bucket is public.
	URL       string `json:"url"`
	PublicURL string `json:"public_url,omitempty"`

	// Same values under the field names the list/search/metadata
	// endpoints returned before (they exposed the raw table row), so
	// apps written against them keep working.
	Name     string `json:"name"`
	BucketID string `json:"bucket_id"`
	MimeType string `json:"mime_type"`
}

func (s *Service) view(o *database.StorageObject, public bool) *Object {
	out := &Object{
		ID: o.ID, Bucket: o.BucketID, Key: o.Name, Size: o.Size, ContentType: o.MimeType,
		ETag: o.ETag, Owner: o.Owner, Metadata: o.Metadata, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
		URL:  s.APIURL(o.BucketID, o.Name),
		Name: o.Name, BucketID: o.BucketID, MimeType: o.MimeType,
	}
	if public {
		out.PublicURL = s.PublicURL(o.BucketID, o.Name)
	}
	return out
}

// ---- URLs -----------------------------------------------------------------

func (s *Service) APIURL(bucket, key string) string {
	return s.baseURL + "/api/v1/storage/" + bucket + "/" + EscapeKey(key)
}

func (s *Service) PublicURL(bucket, key string) string {
	return s.baseURL + "/api/v1/storage/public/" + bucket + "/" + EscapeKey(key)
}

// ClampTTL bounds a requested signed-URL lifetime.
func ClampTTL(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Hour
	}
	if d > MaxSignedURLTTL {
		return MaxSignedURLTTL
	}
	return d
}

// SignedURL returns a link that reads one object without any key.
// download != "" makes the browser save it (as that name, or the
// object's own name when download is "1"/"true").
func (s *Service) SignedURL(bucket, key string, ttl time.Duration, download string) (string, time.Time) {
	exp := time.Now().Add(ClampTTL(ttl))
	tok := s.signer.Sign(Grant{Op: OpRead, Bucket: bucket, Key: key, Exp: exp.Unix()})
	u := s.baseURL + "/api/v1/storage/sign/" + bucket + "/" + EscapeKey(key) + "?token=" + tok
	if download != "" {
		u += "&download=" + url.QueryEscape(download)
	}
	return u, exp
}

// SignedUploadURL returns a link that uploads exactly one object (PUT),
// owned by owner, without any key.
func (s *Service) SignedUploadURL(bucket, key, owner string, upsert bool) (string, string, time.Time) {
	exp := time.Now().Add(SignedUploadTTL)
	tok := s.signer.Sign(Grant{Op: OpUpload, Bucket: bucket, Key: key, Exp: exp.Unix(), Owner: owner, Upsert: upsert})
	return s.baseURL + "/api/v1/storage/upload/sign/" + bucket + "/" + EscapeKey(key) + "?token=" + tok, tok, exp
}

// VerifyGrant checks a signed-URL token for op on bucket/key.
func (s *Service) VerifyGrant(token, op, bucket, key string) (Grant, error) {
	g, err := s.signer.Verify(token)
	if err != nil {
		return Grant{}, err
	}
	if g.Op != op || g.Bucket != bucket || g.Key != key {
		return Grant{}, ErrTokenInvalid
	}
	return g, nil
}

// ---- buckets ----------------------------------------------------------------

// BucketSettings are the editable bucket fields. Nil means "leave as
// is". FileSizeLimit <= 0 removes the limit; an empty AllowedMimeTypes
// list allows every type.
type BucketSettings struct {
	Public           *bool     `json:"public"`
	FileSizeLimit    *int64    `json:"file_size_limit"`
	AllowedMimeTypes *[]string `json:"allowed_mime_types"`
}

func (s *Service) ListBuckets(ctx context.Context) ([]database.StorageBucket, error) {
	return s.db.ListStorageBuckets(ctx)
}

func (s *Service) GetBucket(ctx context.Context, name string) (*database.StorageBucket, error) {
	b, err := s.db.GetStorageBucketWithStats(ctx, name)
	if errors.Is(err, database.ErrNotFound) {
		return nil, ErrBucketNotFound
	}
	return b, err
}

func (s *Service) bucket(ctx context.Context, name string) (*database.StorageBucket, error) {
	b, err := s.db.GetStorageBucket(ctx, name)
	if errors.Is(err, database.ErrNotFound) {
		return nil, ErrBucketNotFound
	}
	return b, err
}

func resolveSettings(cur *database.StorageBucket, set BucketSettings) (public bool, limit *int64, mimes []string, err error) {
	if cur != nil {
		public, limit, mimes = cur.Public, cur.FileSizeLimit, cur.AllowedMimeTypes
	}
	if set.Public != nil {
		public = *set.Public
	}
	if set.FileSizeLimit != nil {
		if *set.FileSizeLimit > 0 {
			v := *set.FileSizeLimit
			limit = &v
		} else {
			limit = nil
		}
	}
	if set.AllowedMimeTypes != nil {
		if mimes, err = NormalizeMimeList(*set.AllowedMimeTypes); err != nil {
			return false, nil, nil, err
		}
	}
	return public, limit, mimes, nil
}

func (s *Service) CreateBucket(ctx context.Context, name string, set BucketSettings) (*database.StorageBucket, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if err := ValidateBucketName(name); err != nil {
		return nil, err
	}
	if _, err := s.db.GetStorageBucket(ctx, name); err == nil {
		return nil, ErrBucketExists
	}
	public, limit, mimes, err := resolveSettings(nil, set)
	if err != nil {
		return nil, err
	}
	if err := s.s3.MakeBucket(ctx, name); err != nil {
		return nil, err
	}
	b, err := s.db.CreateStorageBucket(ctx, name, public, limit, mimes)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) UpdateBucket(ctx context.Context, name string, set BucketSettings) (*database.StorageBucket, error) {
	cur, err := s.bucket(ctx, name)
	if err != nil {
		return nil, err
	}
	public, limit, mimes, err := resolveSettings(cur, set)
	if err != nil {
		return nil, err
	}
	if err := s.db.UpdateStorageBucket(ctx, name, public, limit, mimes); err != nil {
		return nil, err
	}
	return s.GetBucket(ctx, name)
}

// DeleteBucket removes a bucket. Without force it must hold no files
// (empty folders don't count).
func (s *Service) DeleteBucket(ctx context.Context, name string, force bool) error {
	if _, err := s.bucket(ctx, name); err != nil {
		return err
	}
	var keys []string
	err := s.s3.List(ctx, name, "", true, func(o ListedObject) error {
		if !force && !hiddenKey(o.Key) {
			return ErrBucketNotEmpty
		}
		keys = append(keys, o.Key)
		return nil
	})
	if err != nil && !isNotFound(err) {
		return err
	}
	if err := s.removeKeys(ctx, name, keys); err != nil {
		return err
	}
	if err := s.s3.RemoveBucket(ctx, name); err != nil {
		return err
	}
	return s.db.DeleteStorageBucket(ctx, name)
}

func (s *Service) removeKeys(ctx context.Context, bucket string, keys []string) error {
	for len(keys) > 0 {
		n := min(len(keys), 1000)
		if err := s.s3.RemoveMany(ctx, bucket, keys[:n]); err != nil {
			return err
		}
		if err := s.db.DeleteStorageObjects(ctx, bucket, keys[:n]); err != nil {
			return err
		}
		keys = keys[n:]
	}
	return nil
}

// ---- objects ----------------------------------------------------------------

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type UploadInput struct {
	Bucket       string
	Key          string
	Body         io.Reader
	Size         int64 // -1 when unknown (streamed body)
	ContentType  string
	CacheControl string
	Owner        string                 // "" = no owner (or keep the current one)
	Metadata     map[string]interface{} // nil = keep the current metadata
	Upsert       bool
	Actor        Actor
}

// limitReader fails with errTooLarge once more than max bytes are read,
// so a lying or missing Content-Length can't bypass a bucket's limit.
type limitReader struct {
	r   io.Reader
	n   int64
	max int64
}

var errTooLarge = errors.New("too large")

func (l *limitReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.max > 0 && l.n > l.max {
		return n, errTooLarge
	}
	return n, err
}

func tooLarge(limit int64) *Error {
	return newErr(http.StatusRequestEntityTooLarge, "file_too_large",
		"the file exceeds this bucket's size limit (%d bytes)", limit)
}

// checkWrite applies the replace rules for key: without upsert an
// existing object is a conflict, and an app user can only replace a
// file they own (files with no owner belong to the service key).
func (s *Service) checkWrite(ctx context.Context, a Actor, bucket, key string, upsert bool) (*database.StorageObject, error) {
	row, err := s.db.GetStorageObject(ctx, bucket, key)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		return nil, err
	}
	exists := row != nil
	if !exists {
		if _, err := s.s3.Stat(ctx, bucket, key); err == nil {
			exists = true
		} else if !errors.Is(err, ErrObjectNotFound) {
			return nil, err
		}
	}
	if !exists {
		return nil, nil
	}
	if !a.Service && !a.owns(row) {
		return nil, ErrForbidden
	}
	if !upsert {
		return nil, ErrObjectExists
	}
	return row, nil
}

func (s *Service) Upload(ctx context.Context, in UploadInput) (*Object, error) {
	if err := ValidateKey(in.Key); err != nil {
		return nil, err
	}
	b, err := s.bucket(ctx, in.Bucket)
	if err != nil {
		return nil, err
	}
	if in.Owner != "" && !uuidRe.MatchString(in.Owner) {
		return nil, newErr(http.StatusBadRequest, "invalid_owner", "owner_id must be a user UUID")
	}
	contentType := DetectContentType(in.ContentType, in.Key)
	if !MimeAllowed(contentType, b.AllowedMimeTypes) {
		return nil, newErr(http.StatusUnsupportedMediaType, "mime_type_not_allowed",
			"files of type %s are not allowed in this bucket (allowed: %s)", contentType, strings.Join(b.AllowedMimeTypes, ", "))
	}
	var limit int64
	if b.FileSizeLimit != nil {
		limit = *b.FileSizeLimit
	}
	if limit > 0 && in.Size > limit {
		return nil, tooLarge(limit)
	}
	existing, err := s.checkWrite(ctx, in.Actor, in.Bucket, in.Key, in.Upsert)
	if err != nil {
		return nil, err
	}

	body := &limitReader{r: in.Body, max: limit}
	etag, err := s.s3.Put(ctx, in.Bucket, in.Key, body, in.Size, contentType, in.CacheControl)
	if err != nil {
		if errors.Is(err, errTooLarge) {
			return nil, tooLarge(limit)
		}
		return nil, err
	}

	var owner *string
	if in.Owner != "" {
		owner = &in.Owner
	}
	row, err := s.db.UpsertStorageObject(ctx, in.Bucket, in.Key, owner, body.n, contentType, etag, in.Metadata)
	if err != nil {
		// Don't leave an untracked file behind for a brand-new upload.
		if existing == nil {
			_ = s.s3.Remove(context.Background(), in.Bucket, in.Key)
		}
		return nil, err
	}
	return s.view(row, b.Public), nil
}

// CanRead reports whether a may read bucket/key: the service key always,
// anyone when the bucket is public, otherwise only the file's owner.
func (s *Service) CanRead(ctx context.Context, a Actor, bucket, key string) (bool, error) {
	if a.Service {
		return true, nil
	}
	b, err := s.bucket(ctx, bucket)
	if err != nil {
		return false, err
	}
	if b.Public {
		return true, nil
	}
	return s.CanWrite(ctx, a, bucket, key)
}

// CanWrite reports whether a may change or delete bucket/key.
func (s *Service) CanWrite(ctx context.Context, a Actor, bucket, key string) (bool, error) {
	if a.Service {
		return true, nil
	}
	row, err := s.db.GetStorageObject(ctx, bucket, key)
	if errors.Is(err, database.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.owns(row), nil
}

// IsPublic reports whether bucket exists and is public.
func (s *Service) IsPublic(ctx context.Context, bucket string) (bool, error) {
	b, err := s.bucket(ctx, bucket)
	if err != nil {
		return false, err
	}
	return b.Public, nil
}

// Stat and Open read straight from the backend; callers check access first.
func (s *Service) Stat(ctx context.Context, bucket, key string) (*ObjectStat, error) {
	if ValidateKey(key) != nil {
		return nil, ErrObjectNotFound
	}
	return s.s3.Stat(ctx, bucket, key)
}

func (s *Service) Open(ctx context.Context, bucket, key string, rng *ByteRange) (io.ReadCloser, error) {
	return s.s3.Open(ctx, bucket, key, rng)
}

// ObjectInfo returns the recorded row of an object.
func (s *Service) ObjectInfo(ctx context.Context, bucket, key string) (*Object, error) {
	b, err := s.bucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	row, err := s.db.GetStorageObject(ctx, bucket, key)
	if errors.Is(err, database.ErrNotFound) {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.view(row, b.Public), nil
}

// ListRecorded lists recorded objects; owner != "" limits it to a user.
func (s *Service) ListRecorded(ctx context.Context, bucket, prefix, owner string, limit, offset int) ([]*Object, error) {
	b, err := s.bucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	var rows []database.StorageObject
	if owner != "" {
		rows, err = s.db.ListStorageObjectsByOwnerBucket(ctx, owner, bucket, prefix, limit, offset)
	} else {
		rows, err = s.db.ListStorageObjects(ctx, bucket, prefix, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	out := make([]*Object, len(rows))
	for i := range rows {
		out[i] = s.view(&rows[i], b.Public)
	}
	return out, nil
}

// Search finds objects whose metadata key equals value; owner != ""
// limits the result to that user's files.
func (s *Service) Search(ctx context.Context, bucket, key, value, owner string) ([]*Object, error) {
	b, err := s.bucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.SearchStorageObjectsByMetadata(ctx, bucket, key, value)
	if err != nil {
		return nil, err
	}
	out := []*Object{}
	for i := range rows {
		if owner == "" || (rows[i].Owner != nil && *rows[i].Owner == owner) {
			out = append(out, s.view(&rows[i], b.Public))
		}
	}
	return out, nil
}

// ListByOwner returns every file of a user across buckets.
func (s *Service) ListByOwner(ctx context.Context, owner string) ([]*Object, error) {
	if !uuidRe.MatchString(owner) {
		return []*Object{}, nil
	}
	rows, err := s.db.ListStorageObjectsByOwner(ctx, owner)
	if err != nil {
		return nil, err
	}
	buckets, err := s.db.ListStorageBuckets(ctx)
	if err != nil {
		return nil, err
	}
	public := map[string]bool{}
	for _, b := range buckets {
		public[b.Name] = b.Public
	}
	out := make([]*Object, len(rows))
	for i := range rows {
		out[i] = s.view(&rows[i], public[rows[i].BucketID])
	}
	return out, nil
}

// UpdateMetadata replaces an object's metadata.
func (s *Service) UpdateMetadata(ctx context.Context, bucket, key string, metadata map[string]interface{}) error {
	err := s.db.UpdateStorageObjectMetadata(ctx, bucket, key, metadata)
	if errors.Is(err, database.ErrNotFound) {
		return ErrObjectNotFound
	}
	return err
}

func (s *Service) Delete(ctx context.Context, bucket, key string) error {
	if _, err := s.bucket(ctx, bucket); err != nil {
		return err
	}
	if err := s.s3.Remove(ctx, bucket, key); err != nil {
		return err
	}
	return s.db.DeleteStorageObject(ctx, bucket, key)
}

// DeleteMany removes several objects of one bucket.
func (s *Service) DeleteMany(ctx context.Context, bucket string, keys []string) error {
	if _, err := s.bucket(ctx, bucket); err != nil {
		return err
	}
	return s.removeKeys(ctx, bucket, keys)
}

// DeleteFolder removes everything under prefix ("photos/2024/").
func (s *Service) DeleteFolder(ctx context.Context, bucket, prefix string) (int, error) {
	if _, err := s.bucket(ctx, bucket); err != nil {
		return 0, err
	}
	prefix = strings.TrimSuffix(prefix, "/") + "/"
	if err := ValidatePrefix(prefix); err != nil {
		return 0, err
	}
	var keys []string
	if err := s.s3.List(ctx, bucket, prefix, true, func(o ListedObject) error {
		keys = append(keys, o.Key)
		return nil
	}); err != nil {
		return 0, err
	}
	n := 0
	for _, k := range keys {
		if !hiddenKey(k) {
			n++
		}
	}
	return n, s.removeKeys(ctx, bucket, keys)
}

// CopyObject copies (or moves) one object. The copy is owned by the app
// user who made it; the service key keeps the original owner.
func (s *Service) CopyObject(ctx context.Context, a Actor, srcBucket, srcKey, dstBucket, dstKey string, move, upsert bool) (*Object, error) {
	if err := ValidateKey(dstKey); err != nil {
		return nil, err
	}
	if _, err := s.bucket(ctx, srcBucket); err != nil {
		return nil, err
	}
	dst, err := s.bucket(ctx, dstBucket)
	if err != nil {
		return nil, err
	}
	if srcBucket == dstBucket && srcKey == dstKey {
		return nil, newErr(http.StatusBadRequest, "same_path", "source and destination are the same")
	}
	allowed, err := s.CanRead(ctx, a, srcBucket, srcKey)
	if err != nil {
		return nil, err
	}
	if move {
		allowed, err = s.CanWrite(ctx, a, srcBucket, srcKey)
		if err != nil {
			return nil, err
		}
	}
	if !allowed {
		return nil, ErrForbidden
	}
	st, err := s.s3.Stat(ctx, srcBucket, srcKey)
	if err != nil {
		return nil, err
	}
	if !MimeAllowed(st.ContentType, dst.AllowedMimeTypes) {
		return nil, newErr(http.StatusUnsupportedMediaType, "mime_type_not_allowed", "files of type %s are not allowed in bucket %s", st.ContentType, dstBucket)
	}
	if dst.FileSizeLimit != nil && *dst.FileSizeLimit > 0 && st.Size > *dst.FileSizeLimit {
		return nil, tooLarge(*dst.FileSizeLimit)
	}
	if _, err := s.checkWrite(ctx, a, dstBucket, dstKey, upsert); err != nil {
		return nil, err
	}
	if err := s.s3.Copy(ctx, srcBucket, srcKey, dstBucket, dstKey); err != nil {
		return nil, err
	}

	var owner *string
	var meta map[string]interface{}
	if row, err := s.db.GetStorageObject(ctx, srcBucket, srcKey); err == nil {
		owner, meta = row.Owner, row.Metadata
	}
	if !a.Service {
		owner = &a.UserID
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	row, err := s.db.UpsertStorageObject(ctx, dstBucket, dstKey, owner, st.Size, st.ContentType, st.ETag, meta)
	if err != nil {
		return nil, err
	}
	if move {
		if err := s.s3.Remove(ctx, srcBucket, srcKey); err != nil {
			return nil, err
		}
		if err := s.db.DeleteStorageObject(ctx, srcBucket, srcKey); err != nil {
			return nil, err
		}
	}
	return s.view(row, dst.Public), nil
}

// CreateFolder makes an empty folder visible (S3 has no directories).
func (s *Service) CreateFolder(ctx context.Context, bucket, folder string) error {
	if _, err := s.bucket(ctx, bucket); err != nil {
		return err
	}
	folder = strings.Trim(folder, "/")
	if err := ValidatePrefix(folder); err != nil {
		return err
	}
	_, err := s.s3.Put(ctx, bucket, folder+"/"+FolderPlaceholder, strings.NewReader(""), 0, "application/octet-stream", "")
	return err
}

// Entry is one row of a folder listing (dashboard and MCP).
type Entry struct {
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	IsDir        bool      `json:"is_dir"`
	Size         int64     `json:"size"`
	ContentType  string    `json:"content_type"`
	ETag         string    `json:"etag,omitempty"`
	LastModified time.Time `json:"last_modified"`
	Owner        *string   `json:"owner,omitempty"`
	URL          string    `json:"url,omitempty"`        // signed, valid 1h
	PublicURL    string    `json:"public_url,omitempty"` // public buckets only
}

// ListFolder lists one level of a bucket straight from the backend, so
// it also shows files uploaded behind rapibase's back. withLinks adds a
// one-hour signed URL to every file (dashboard previews and downloads).
func (s *Service) ListFolder(ctx context.Context, bucket, prefix string, withLinks bool) ([]Entry, error) {
	b, err := s.bucket(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if prefix != "" {
		prefix = strings.TrimSuffix(prefix, "/") + "/"
		if err := ValidatePrefix(prefix); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.ListStorageObjectsInFolder(ctx, bucket, prefix)
	if err != nil {
		return nil, err
	}
	recorded := make(map[string]*database.StorageObject, len(rows))
	for i := range rows {
		recorded[rows[i].Name] = &rows[i]
	}

	entries := []Entry{}
	seenDirs := map[string]bool{}
	err = s.s3.List(ctx, bucket, prefix, false, func(o ListedObject) error {
		if o.IsPrefix || strings.HasSuffix(o.Key, "/") {
			if o.Key == prefix || seenDirs[o.Key] {
				return nil
			}
			seenDirs[o.Key] = true
			entries = append(entries, Entry{Key: o.Key, Name: path.Base(o.Key), IsDir: true})
			return nil
		}
		if hiddenKey(o.Key) {
			return nil
		}
		e := Entry{Key: o.Key, Name: path.Base(o.Key), Size: o.Size, ETag: o.ETag, LastModified: o.LastModified}
		if r := recorded[o.Key]; r != nil {
			e.ContentType, e.Owner = r.MimeType, r.Owner
		}
		if e.ContentType == "" {
			e.ContentType = DetectContentType("", o.Key)
		}
		if withLinks {
			e.URL, _ = s.SignedURL(bucket, o.Key, time.Hour, "")
		}
		if b.Public {
			e.PublicURL = s.PublicURL(bucket, o.Key)
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// ---- startup reconciliation -----------------------------------------------

const (
	secretPoliciesImported = "storage_legacy_policies_imported"
	secretObjectsBackfill  = "storage_objects_backfilled"
)

// Reconcile lines the backend up with Postgres at startup: buckets that
// exist only in S3 get a row, buckets that exist only in Postgres get
// created in S3 (e.g. after moving to a fresh backend), and — once — the
// anonymous-read policies that older versions kept inside MinIO become
// the public flag in Postgres. Failures are logged, never fatal: storage
// being down must not keep the API from starting.
func (s *Service) Reconcile(ctx context.Context) {
	s3Names, err := s.s3.ListBuckets(ctx)
	if err != nil {
		log.Printf("storage: backend unreachable at startup, skipping reconciliation: %v", err)
		return
	}
	dbBuckets, err := s.db.ListStorageBuckets(ctx)
	if err != nil {
		log.Printf("storage: reconcile: %v", err)
		return
	}
	inDB := map[string]bool{}
	for _, b := range dbBuckets {
		inDB[b.Name] = true
	}
	inS3 := map[string]bool{}
	for _, name := range s3Names {
		inS3[name] = true
		if !inDB[name] {
			if created, err := s.db.EnsureStorageBucket(ctx, name, false); err != nil {
				log.Printf("storage: reconcile: recording bucket %s: %v", name, err)
			} else if created {
				log.Printf("storage: recorded bucket %s found in the backend (private)", name)
			}
		}
	}
	for _, b := range dbBuckets {
		if !inS3[b.Name] {
			if err := s.s3.MakeBucket(ctx, b.Name); err != nil {
				log.Printf("storage: reconcile: creating bucket %s in the backend: %v", b.Name, err)
			} else {
				log.Printf("storage: created bucket %s in the backend", b.Name)
			}
		}
	}
	s.importLegacyPolicies(ctx, s3Names)
}

func (s *Service) importLegacyPolicies(ctx context.Context, buckets []string) {
	if _, done, err := s.db.GetSecret(ctx, secretPoliciesImported); err != nil || done {
		return
	}
	if s.s3.IsMinIO(ctx) {
		for _, name := range buckets {
			public, err := s.s3.LegacyBucketIsPublic(ctx, name)
			if err != nil {
				log.Printf("storage: reading the old policy of %s: %v", name, err)
				return // retry on the next start
			}
			if err := s.db.SetStorageBucketPublic(ctx, name, public); err != nil {
				log.Printf("storage: importing the policy of %s: %v", name, err)
				return
			}
			log.Printf("storage: bucket %s imported as %s", name, map[bool]string{true: "public", false: "private"}[public])
		}
	}
	_ = s.db.SetSecret(ctx, secretPoliciesImported, time.Now().UTC().Format(time.RFC3339))
}

// Backfill records, once, objects that exist in the backend but have no
// row (files uploaded by older versions through paths that skipped the
// table) and drops rows whose file is gone. Runs in the background.
func (s *Service) Backfill(ctx context.Context) {
	if _, done, err := s.db.GetSecret(ctx, secretObjectsBackfill); err != nil || done {
		return
	}
	buckets, err := s.db.ListStorageBuckets(ctx)
	if err != nil {
		log.Printf("storage: backfill: %v", err)
		return
	}
	added, dropped := 0, 0
	for _, b := range buckets {
		recorded, err := s.db.StorageObjectNames(ctx, b.Name)
		if err != nil {
			log.Printf("storage: backfill %s: %v", b.Name, err)
			return
		}
		present := map[string]bool{}
		err = s.s3.List(ctx, b.Name, "", true, func(o ListedObject) error {
			if hiddenKey(o.Key) || ValidateKey(o.Key) != nil {
				return nil
			}
			present[o.Key] = true
			if recorded[o.Key] {
				return nil
			}
			ct := DetectContentType("", o.Key)
			if st, err := s.s3.Stat(ctx, b.Name, o.Key); err == nil && st.ContentType != "" {
				ct = st.ContentType
			}
			if err := s.db.InsertStorageObjectIfMissing(ctx, b.Name, o.Key, o.Size, ct, o.ETag, o.LastModified); err != nil {
				return err
			}
			added++
			return nil
		})
		if err != nil {
			log.Printf("storage: backfill %s: %v", b.Name, err)
			return
		}
		var gone []string
		for name := range recorded {
			if !present[name] {
				gone = append(gone, name)
			}
		}
		if err := s.db.DeleteStorageObjects(ctx, b.Name, gone); err != nil {
			log.Printf("storage: backfill %s: %v", b.Name, err)
			return
		}
		dropped += len(gone)
	}
	if added+dropped > 0 {
		log.Printf("storage: backfill recorded %d file(s) and dropped %d stale row(s)", added, dropped)
	}
	_ = s.db.SetSecret(ctx, secretObjectsBackfill, time.Now().UTC().Format(time.RFC3339))
}
