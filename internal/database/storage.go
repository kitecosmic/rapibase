package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// storage_buckets and storage_objects are the source of truth for
// storage: which buckets exist, whether they are public, their upload
// limits, and who owns each file. The S3 backend only holds the bytes.

type StorageBucket struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Public           bool      `json:"public"`
	FileSizeLimit    *int64    `json:"file_size_limit"`
	AllowedMimeTypes []string  `json:"allowed_mime_types"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	// Filled by ListStorageBucketsWithStats / GetStorageBucketWithStats.
	ObjectCount int64 `json:"object_count"`
	TotalSize   int64 `json:"total_size"`
}

type StorageObject struct {
	ID        string                 `json:"id"`
	BucketID  string                 `json:"bucket_id"`
	Name      string                 `json:"name"`
	Owner     *string                `json:"owner,omitempty"`
	Size      int64                  `json:"size"`
	MimeType  string                 `json:"mime_type"`
	ETag      string                 `json:"etag"`
	Metadata  map[string]interface{} `json:"metadata"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// ErrNotFound is returned by the storage getters when no row matches.
var ErrNotFound = errors.New("not found")

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

const bucketCols = `b.id, b.name, b.public, b.file_size_limit, b.allowed_mime_types, b.created_at, b.updated_at`

func scanBucket(row pgx.Row, withStats bool) (*StorageBucket, error) {
	b := &StorageBucket{}
	dest := []any{&b.ID, &b.Name, &b.Public, &b.FileSizeLimit, &b.AllowedMimeTypes, &b.CreatedAt, &b.UpdatedAt}
	if withStats {
		dest = append(dest, &b.ObjectCount, &b.TotalSize)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, notFound(err)
	}
	if b.AllowedMimeTypes == nil {
		b.AllowedMimeTypes = []string{}
	}
	return b, nil
}

// CreateStorageBucket records a bucket. A row that already exists is
// reported as an error by the unique constraint.
func (db *DB) CreateStorageBucket(ctx context.Context, name string, public bool, fileSizeLimit *int64, allowedMimeTypes []string) (*StorageBucket, error) {
	row := db.Pool.QueryRow(ctx, `
		INSERT INTO storage_buckets AS b (id, name, public, file_size_limit, allowed_mime_types)
		VALUES ($1, $1, $2, $3, $4)
		RETURNING `+bucketCols,
		name, public, fileSizeLimit, nilIfEmpty(allowedMimeTypes))
	b, err := scanBucket(row, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create bucket: %w", err)
	}
	return b, nil
}

// EnsureStorageBucket inserts a private bucket row if it is missing.
func (db *DB) EnsureStorageBucket(ctx context.Context, name string, public bool) (created bool, err error) {
	tag, err := db.Pool.Exec(ctx, `
		INSERT INTO storage_buckets (id, name, public) VALUES ($1, $1, $2)
		ON CONFLICT DO NOTHING`, name, public)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (db *DB) GetStorageBucket(ctx context.Context, name string) (*StorageBucket, error) {
	return scanBucket(db.Pool.QueryRow(ctx, `SELECT `+bucketCols+` FROM storage_buckets b WHERE b.id = $1`, name), false)
}

const bucketStatsQuery = `
	SELECT ` + bucketCols + `, COALESCE(s.n, 0), COALESCE(s.bytes, 0)
	FROM storage_buckets b
	LEFT JOIN (
		SELECT bucket_id, COUNT(*) AS n, SUM(size) AS bytes
		FROM storage_objects GROUP BY bucket_id
	) s ON s.bucket_id = b.id`

func (db *DB) GetStorageBucketWithStats(ctx context.Context, name string) (*StorageBucket, error) {
	return scanBucket(db.Pool.QueryRow(ctx, bucketStatsQuery+` WHERE b.id = $1`, name), true)
}

// ListStorageBuckets returns every bucket with its file count and size.
func (db *DB) ListStorageBuckets(ctx context.Context) ([]StorageBucket, error) {
	rows, err := db.Pool.Query(ctx, bucketStatsQuery+` ORDER BY b.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := []StorageBucket{}
	for rows.Next() {
		b, err := scanBucket(rows, true)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, *b)
	}
	return buckets, rows.Err()
}

// UpdateStorageBucket overwrites a bucket's settings.
func (db *DB) UpdateStorageBucket(ctx context.Context, name string, public bool, fileSizeLimit *int64, allowedMimeTypes []string) error {
	tag, err := db.Pool.Exec(ctx, `
		UPDATE storage_buckets
		SET public = $2, file_size_limit = $3, allowed_mime_types = $4, updated_at = NOW()
		WHERE id = $1`, name, public, fileSizeLimit, nilIfEmpty(allowedMimeTypes))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStorageBucketPublic changes only the public flag.
func (db *DB) SetStorageBucketPublic(ctx context.Context, name string, public bool) error {
	_, err := db.Pool.Exec(ctx, `UPDATE storage_buckets SET public = $2, updated_at = NOW() WHERE id = $1`, name, public)
	return err
}

// DeleteStorageBucket deletes a bucket row; its object rows go with it
// (ON DELETE CASCADE).
func (db *DB) DeleteStorageBucket(ctx context.Context, name string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM storage_buckets WHERE id = $1`, name)
	return err
}

const objectCols = `id, bucket_id, name, owner, size, mime_type, etag, metadata, created_at, updated_at`

func scanObject(row pgx.Row) (*StorageObject, error) {
	o := &StorageObject{}
	var mimeType, etag *string
	if err := row.Scan(&o.ID, &o.BucketID, &o.Name, &o.Owner, &o.Size, &mimeType, &etag, &o.Metadata, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if mimeType != nil {
		o.MimeType = *mimeType
	}
	if etag != nil {
		o.ETag = *etag
	}
	if o.Metadata == nil {
		o.Metadata = map[string]interface{}{}
	}
	return o, nil
}

func scanObjects(rows pgx.Rows) ([]StorageObject, error) {
	defer rows.Close()
	out := []StorageObject{}
	for rows.Next() {
		o, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// UpsertStorageObject records an uploaded object. On replace, a nil
// owner keeps the current owner and nil metadata keeps the current
// metadata — re-uploading a file must not orphan it or wipe its tags.
func (db *DB) UpsertStorageObject(ctx context.Context, bucketID, name string, ownerID *string, size int64, mimeType, etag string, metadata map[string]interface{}) (*StorageObject, error) {
	var meta any
	if metadata != nil {
		meta = metadata
	}
	row := db.Pool.QueryRow(ctx, `
		INSERT INTO storage_objects (bucket_id, name, owner, size, mime_type, etag, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7::jsonb, '{}'::jsonb))
		ON CONFLICT (bucket_id, name) DO UPDATE SET
			owner = COALESCE(EXCLUDED.owner, storage_objects.owner),
			size = EXCLUDED.size,
			mime_type = EXCLUDED.mime_type,
			etag = EXCLUDED.etag,
			metadata = COALESCE($7::jsonb, storage_objects.metadata),
			updated_at = NOW()
		RETURNING `+objectCols,
		bucketID, name, ownerID, size, mimeType, etag, meta)
	o, err := scanObject(row)
	if err != nil {
		return nil, fmt.Errorf("failed to record object: %w", err)
	}
	return o, nil
}

func (db *DB) GetStorageObject(ctx context.Context, bucketID, name string) (*StorageObject, error) {
	return scanObject(db.Pool.QueryRow(ctx, `SELECT `+objectCols+` FROM storage_objects WHERE bucket_id = $1 AND name = $2`, bucketID, name))
}

// likePrefix escapes LIKE wildcards so a prefix matches literally.
func likePrefix(prefix string) string {
	r := make([]rune, 0, len(prefix)+1)
	for _, c := range prefix {
		if c == '%' || c == '_' || c == '\\' {
			r = append(r, '\\')
		}
		r = append(r, c)
	}
	return string(r) + "%"
}

// ListStorageObjects returns objects in a bucket under a name prefix.
func (db *DB) ListStorageObjects(ctx context.Context, bucketID, prefix string, limit, offset int) ([]StorageObject, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT `+objectCols+` FROM storage_objects
		WHERE bucket_id = $1 AND name LIKE $2
		ORDER BY name LIMIT $3 OFFSET $4`, bucketID, likePrefix(prefix), limit, offset)
	if err != nil {
		return nil, err
	}
	return scanObjects(rows)
}

// ListStorageObjectsInFolder returns only the files directly inside a
// folder (not in its sub-folders), for folder listings of big buckets.
func (db *DB) ListStorageObjectsInFolder(ctx context.Context, bucketID, folder string) ([]StorageObject, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT `+objectCols+` FROM storage_objects
		WHERE bucket_id = $1 AND name LIKE $2
		  AND strpos(substr(name, char_length($3) + 1), '/') = 0`,
		bucketID, likePrefix(folder), folder)
	if err != nil {
		return nil, err
	}
	return scanObjects(rows)
}

// ListStorageObjectsByOwner returns all objects owned by a user.
func (db *DB) ListStorageObjectsByOwner(ctx context.Context, ownerID string) ([]StorageObject, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT `+objectCols+` FROM storage_objects
		WHERE owner = $1 ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	return scanObjects(rows)
}

// ListStorageObjectsByOwnerBucket returns a user's objects within one
// bucket, optionally under a name prefix.
func (db *DB) ListStorageObjectsByOwnerBucket(ctx context.Context, ownerID, bucketID, prefix string, limit, offset int) ([]StorageObject, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT `+objectCols+` FROM storage_objects
		WHERE bucket_id = $1 AND owner = $2 AND name LIKE $3
		ORDER BY name LIMIT $4 OFFSET $5`, bucketID, ownerID, likePrefix(prefix), limit, offset)
	if err != nil {
		return nil, err
	}
	return scanObjects(rows)
}

// SearchStorageObjectsByMetadata finds objects whose metadata key equals value.
func (db *DB) SearchStorageObjectsByMetadata(ctx context.Context, bucketID, key, value string) ([]StorageObject, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT `+objectCols+` FROM storage_objects
		WHERE bucket_id = $1 AND metadata->>$2 = $3
		ORDER BY created_at DESC`, bucketID, key, value)
	if err != nil {
		return nil, err
	}
	return scanObjects(rows)
}

func (db *DB) DeleteStorageObject(ctx context.Context, bucketID, name string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM storage_objects WHERE bucket_id = $1 AND name = $2`, bucketID, name)
	return err
}

func (db *DB) DeleteStorageObjects(ctx context.Context, bucketID string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	_, err := db.Pool.Exec(ctx, `DELETE FROM storage_objects WHERE bucket_id = $1 AND name = ANY($2)`, bucketID, names)
	return err
}

// UpdateStorageObjectMetadata replaces an object's metadata.
func (db *DB) UpdateStorageObjectMetadata(ctx context.Context, bucketID, name string, metadata map[string]interface{}) error {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	tag, err := db.Pool.Exec(ctx, `
		UPDATE storage_objects SET metadata = $1, updated_at = NOW()
		WHERE bucket_id = $2 AND name = $3`, metadata, bucketID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// StorageObjectNames returns every object name recorded for a bucket.
func (db *DB) StorageObjectNames(ctx context.Context, bucketID string) (map[string]bool, error) {
	rows, err := db.Pool.Query(ctx, `SELECT name FROM storage_objects WHERE bucket_id = $1`, bucketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names[n] = true
	}
	return names, rows.Err()
}

// InsertStorageObjectIfMissing records an object found in the backend
// that has no row yet (no owner: only the service key can reach it).
func (db *DB) InsertStorageObjectIfMissing(ctx context.Context, bucketID, name string, size int64, mimeType, etag string, created time.Time) error {
	if created.IsZero() {
		created = time.Now()
	}
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO storage_objects (bucket_id, name, size, mime_type, etag, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (bucket_id, name) DO NOTHING`, bucketID, name, size, mimeType, etag, created)
	return err
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}
