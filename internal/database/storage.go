package database

import (
	"context"
	"fmt"
	"time"
)

type StorageBucket struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Public           bool      `json:"public"`
	FileSizeLimit    *int64    `json:"file_size_limit,omitempty"`
	AllowedMimeTypes []string  `json:"allowed_mime_types,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
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

// CreateBucket creates a new storage bucket in the database
func (db *DB) CreateStorageBucket(ctx context.Context, name string, public bool) (*StorageBucket, error) {
	bucket := &StorageBucket{
		ID:     name,
		Name:   name,
		Public: public,
	}

	err := db.Pool.QueryRow(ctx, `
		INSERT INTO storage_buckets (id, name, public)
		VALUES ($1, $2, $3)
		RETURNING created_at, updated_at
	`, bucket.ID, bucket.Name, bucket.Public).Scan(&bucket.CreatedAt, &bucket.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create bucket: %w", err)
	}

	return bucket, nil
}

// GetBucket retrieves a bucket by name
func (db *DB) GetStorageBucket(ctx context.Context, name string) (*StorageBucket, error) {
	bucket := &StorageBucket{}
	err := db.Pool.QueryRow(ctx, `
		SELECT id, name, public, file_size_limit, allowed_mime_types, created_at, updated_at
		FROM storage_buckets WHERE name = $1
	`, name).Scan(&bucket.ID, &bucket.Name, &bucket.Public, &bucket.FileSizeLimit, &bucket.AllowedMimeTypes, &bucket.CreatedAt, &bucket.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return bucket, nil
}

// ListBuckets returns all storage buckets
func (db *DB) ListStorageBuckets(ctx context.Context) ([]StorageBucket, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, name, public, file_size_limit, allowed_mime_types, created_at, updated_at
		FROM storage_buckets ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buckets []StorageBucket
	for rows.Next() {
		var b StorageBucket
		if err := rows.Scan(&b.ID, &b.Name, &b.Public, &b.FileSizeLimit, &b.AllowedMimeTypes, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		buckets = append(buckets, b)
	}

	return buckets, nil
}

// UpdateBucketPolicy updates the public/private status of a bucket
func (db *DB) UpdateStorageBucketPolicy(ctx context.Context, name string, public bool) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE storage_buckets SET public = $1, updated_at = NOW() WHERE name = $2
	`, public, name)
	return err
}

// DeleteBucket deletes a bucket (objects should be deleted first via MinIO)
func (db *DB) DeleteStorageBucket(ctx context.Context, name string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM storage_buckets WHERE name = $1`, name)
	return err
}

// CreateObject records a new object in the database
func (db *DB) CreateStorageObject(ctx context.Context, bucketID, name string, ownerID *string, size int64, mimeType, etag string, metadata map[string]interface{}) (*StorageObject, error) {
	obj := &StorageObject{
		BucketID: bucketID,
		Name:     name,
		Owner:    ownerID,
		Size:     size,
		MimeType: mimeType,
		ETag:     etag,
		Metadata: metadata,
	}

	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	err := db.Pool.QueryRow(ctx, `
		INSERT INTO storage_objects (bucket_id, name, owner, size, mime_type, etag, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bucket_id, name) DO UPDATE SET
			size = EXCLUDED.size,
			mime_type = EXCLUDED.mime_type,
			etag = EXCLUDED.etag,
			metadata = EXCLUDED.metadata,
			updated_at = NOW()
		RETURNING id, created_at, updated_at
	`, bucketID, name, ownerID, size, mimeType, etag, metadata).Scan(&obj.ID, &obj.CreatedAt, &obj.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create object record: %w", err)
	}

	return obj, nil
}

// GetObject retrieves object metadata from the database
func (db *DB) GetStorageObject(ctx context.Context, bucketID, name string) (*StorageObject, error) {
	obj := &StorageObject{}
	err := db.Pool.QueryRow(ctx, `
		SELECT id, bucket_id, name, owner, size, mime_type, etag, metadata, created_at, updated_at
		FROM storage_objects WHERE bucket_id = $1 AND name = $2
	`, bucketID, name).Scan(&obj.ID, &obj.BucketID, &obj.Name, &obj.Owner, &obj.Size, &obj.MimeType, &obj.ETag, &obj.Metadata, &obj.CreatedAt, &obj.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return obj, nil
}

// ListObjects returns objects in a bucket with optional prefix filter
func (db *DB) ListStorageObjects(ctx context.Context, bucketID, prefix string, limit int) ([]StorageObject, error) {
	query := `
		SELECT id, bucket_id, name, owner, size, mime_type, etag, metadata, created_at, updated_at
		FROM storage_objects 
		WHERE bucket_id = $1 AND name LIKE $2
		ORDER BY name
		LIMIT $3
	`

	rows, err := db.Pool.Query(ctx, query, bucketID, prefix+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []StorageObject
	for rows.Next() {
		var o StorageObject
		if err := rows.Scan(&o.ID, &o.BucketID, &o.Name, &o.Owner, &o.Size, &o.MimeType, &o.ETag, &o.Metadata, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		objects = append(objects, o)
	}

	return objects, nil
}

// ListObjectsByOwner returns all objects owned by a specific user
func (db *DB) ListStorageObjectsByOwner(ctx context.Context, ownerID string) ([]StorageObject, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, bucket_id, name, owner, size, mime_type, etag, metadata, created_at, updated_at
		FROM storage_objects WHERE owner = $1 ORDER BY created_at DESC
	`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []StorageObject
	for rows.Next() {
		var o StorageObject
		if err := rows.Scan(&o.ID, &o.BucketID, &o.Name, &o.Owner, &o.Size, &o.MimeType, &o.ETag, &o.Metadata, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		objects = append(objects, o)
	}

	return objects, nil
}

// ListObjectsByOwnerBucket returns a user's objects within one bucket,
// optionally filtered by name prefix. Used to scope the public list API
// to the authenticated owner.
func (db *DB) ListStorageObjectsByOwnerBucket(ctx context.Context, ownerID, bucketID, prefix string, limit int) ([]StorageObject, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := db.Pool.Query(ctx, `
		SELECT id, bucket_id, name, owner, size, mime_type, etag, metadata, created_at, updated_at
		FROM storage_objects
		WHERE bucket_id = $1 AND owner = $2 AND name LIKE $3
		ORDER BY name
		LIMIT $4
	`, bucketID, ownerID, prefix+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []StorageObject
	for rows.Next() {
		var o StorageObject
		if err := rows.Scan(&o.ID, &o.BucketID, &o.Name, &o.Owner, &o.Size, &o.MimeType, &o.ETag, &o.Metadata, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		objects = append(objects, o)
	}
	return objects, nil
}

// SearchObjectsByMetadata searches objects by metadata key-value
func (db *DB) SearchStorageObjectsByMetadata(ctx context.Context, bucketID, key, value string) ([]StorageObject, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT id, bucket_id, name, owner, size, mime_type, etag, metadata, created_at, updated_at
		FROM storage_objects 
		WHERE bucket_id = $1 AND metadata->>$2 = $3
		ORDER BY created_at DESC
	`, bucketID, key, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objects []StorageObject
	for rows.Next() {
		var o StorageObject
		if err := rows.Scan(&o.ID, &o.BucketID, &o.Name, &o.Owner, &o.Size, &o.MimeType, &o.ETag, &o.Metadata, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		objects = append(objects, o)
	}

	return objects, nil
}

// DeleteObject removes an object record from the database
func (db *DB) DeleteStorageObject(ctx context.Context, bucketID, name string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM storage_objects WHERE bucket_id = $1 AND name = $2`, bucketID, name)
	return err
}

// DeleteObjectsByBucket removes all object records for a bucket
func (db *DB) DeleteStorageObjectsByBucket(ctx context.Context, bucketID string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM storage_objects WHERE bucket_id = $1`, bucketID)
	return err
}

// UpdateObjectMetadata updates the metadata of an object
func (db *DB) UpdateStorageObjectMetadata(ctx context.Context, bucketID, name string, metadata map[string]interface{}) error {
	_, err := db.Pool.Exec(ctx, `
		UPDATE storage_objects SET metadata = $1, updated_at = NOW() 
		WHERE bucket_id = $2 AND name = $3
	`, metadata, bucketID, name)
	return err
}

// BucketExists checks if a bucket exists in the database
func (db *DB) StorageBucketExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM storage_buckets WHERE name = $1)`, name).Scan(&exists)
	return exists, err
}
