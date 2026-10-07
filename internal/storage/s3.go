package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rapibase/rapibase/internal/config"
)

// Client is a thin wrapper over any S3-compatible backend (SeaweedFS by
// default; AWS S3, Cloudflare R2, Backblaze B2 or MinIO work the same).
//
// The backend is a private blob store: rapibase is the only thing that
// talks to it, so it never holds access rules. Who can read a file is
// decided by rapibase from the storage_buckets / storage_objects tables
// in Postgres (see Service). minio-go is used purely as a generic,
// Apache-2.0 S3 client library — it has nothing to do with the MinIO
// server.
type Client struct {
	s3       *minio.Client
	endpoint string
	useSSL   bool
}

// ObjectStat is what rapibase needs to serve an object over HTTP.
type ObjectStat struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	CacheControl string
	LastModified time.Time
}

// ListedObject is one entry of a bucket listing straight from S3.
type ListedObject struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	IsPrefix     bool // a "folder" (common prefix) in a non-recursive listing
}

func NewClient(cfg *config.Config) (*Client, error) {
	client, err := minio.New(cfg.StorageEndpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure:       cfg.StorageUseSSL,
		Region:       cfg.StorageRegion,
		BucketLookup: minio.BucketLookupAuto,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}
	return &Client{s3: client, endpoint: cfg.StorageEndpoint, useSSL: cfg.StorageUseSSL}, nil
}

// isNotFound reports whether err is an S3 "no such key / bucket" error.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchBucket", "NotFound":
		return true
	}
	return minio.ToErrorResponse(err).StatusCode == http.StatusNotFound
}

func (c *Client) ListBuckets(ctx context.Context) ([]string, error) {
	buckets, err := c.s3.ListBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list buckets: %w", err)
	}
	names := make([]string, len(buckets))
	for i, b := range buckets {
		names[i] = b.Name
	}
	return names, nil
}

// MakeBucket creates a bucket; an already existing one is not an error.
func (c *Client) MakeBucket(ctx context.Context, name string) error {
	if err := c.s3.MakeBucket(ctx, name, minio.MakeBucketOptions{}); err != nil {
		code := minio.ToErrorResponse(err).Code
		if code == "BucketAlreadyOwnedByYou" || code == "BucketAlreadyExists" {
			return nil
		}
		if exists, e := c.s3.BucketExists(ctx, name); e == nil && exists {
			return nil
		}
		return fmt.Errorf("failed to create bucket: %w", err)
	}
	return nil
}

// RemoveBucket deletes an empty bucket; a missing one is not an error.
func (c *Client) RemoveBucket(ctx context.Context, name string) error {
	if err := c.s3.RemoveBucket(ctx, name); err != nil && !isNotFound(err) {
		return fmt.Errorf("failed to remove bucket: %w", err)
	}
	return nil
}

// List walks a bucket. recursive=false returns one "directory level"
// (files under prefix plus sub-prefixes); recursive=true returns every
// object. fn returning an error stops the walk.
func (c *Client) List(ctx context.Context, bucket, prefix string, recursive bool, fn func(ListedObject) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for obj := range c.s3.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: recursive}) {
		if obj.Err != nil {
			return fmt.Errorf("failed to list objects: %w", obj.Err)
		}
		lo := ListedObject{Key: obj.Key, Size: obj.Size, ETag: strings.Trim(obj.ETag, `"`), LastModified: obj.LastModified}
		// minio-go reports common prefixes as zero-size entries whose key
		// ends in "/" and that carry no modification time.
		if !recursive && strings.HasSuffix(obj.Key, "/") && obj.LastModified.IsZero() {
			lo.IsPrefix = true
		}
		if err := fn(lo); err != nil {
			if errors.Is(err, errStop) {
				return nil
			}
			return err
		}
	}
	return nil
}

func (c *Client) Put(ctx context.Context, bucket, key string, r io.Reader, size int64, contentType, cacheControl string) (string, error) {
	opts := minio.PutObjectOptions{ContentType: contentType, CacheControl: cacheControl}
	if size < 0 {
		// With no length minio-go would size parts for a 5 TiB object
		// (~550 MB buffered per part). 16 MiB parts keep memory flat and
		// still allow objects up to 160 GB.
		opts.PartSize = 16 << 20
	}
	info, err := c.s3.PutObject(ctx, bucket, key, r, size, opts)
	if err != nil {
		return "", fmt.Errorf("failed to upload object: %w", err)
	}
	return strings.Trim(info.ETag, `"`), nil
}

// Stat returns the object's metadata, or ErrObjectNotFound.
func (c *Client) Stat(ctx context.Context, bucket, key string) (*ObjectStat, error) {
	info, err := c.s3.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to stat object: %w", err)
	}
	return &ObjectStat{
		Key:          key,
		Size:         info.Size,
		ContentType:  info.ContentType,
		ETag:         strings.Trim(info.ETag, `"`),
		CacheControl: info.Metadata.Get("Cache-Control"),
		LastModified: info.LastModified,
	}, nil
}

// Open streams an object: the whole of it when rng is nil, otherwise the
// inclusive byte range rng (already resolved against the object size).
func (c *Client) Open(ctx context.Context, bucket, key string, rng *ByteRange) (io.ReadCloser, error) {
	opts := minio.GetObjectOptions{}
	if rng != nil {
		if err := opts.SetRange(rng.Start, rng.End); err != nil {
			return nil, err
		}
	}
	obj, err := c.s3.GetObject(ctx, bucket, key, opts)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("failed to get object: %w", err)
	}
	return obj, nil
}

// Remove deletes one object; a missing one is not an error.
func (c *Client) Remove(ctx context.Context, bucket, key string) error {
	if err := c.s3.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil && !isNotFound(err) {
		return fmt.Errorf("failed to delete object: %w", err)
	}
	return nil
}

// RemoveMany deletes several objects with batched DeleteObjects calls.
func (c *Client) RemoveMany(ctx context.Context, bucket string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	ch := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		ch <- minio.ObjectInfo{Key: k}
	}
	close(ch)
	for e := range c.s3.RemoveObjects(ctx, bucket, ch, minio.RemoveObjectsOptions{}) {
		if e.Err != nil && !isNotFound(e.Err) {
			return fmt.Errorf("failed to delete %q: %w", e.ObjectName, e.Err)
		}
	}
	return nil
}

func (c *Client) Copy(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	_, err := c.s3.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: dstBucket, Object: dstKey},
		minio.CopySrcOptions{Bucket: srcBucket, Object: srcKey},
	)
	if err != nil {
		if isNotFound(err) {
			return ErrObjectNotFound
		}
		return fmt.Errorf("failed to copy object: %w", err)
	}
	return nil
}

// IsMinIO reports whether the backend is a MinIO server. Only used once,
// to import the bucket policies that older rapibase versions stored in
// MinIO itself (see Service.importLegacyPolicies).
func (c *Client) IsMinIO(ctx context.Context) bool {
	scheme := "http"
	if c.useSSL {
		scheme = "https"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+c.endpoint+"/minio/health/live", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK && strings.Contains(strings.ToLower(resp.Header.Get("Server")), "minio")
}

// LegacyBucketIsPublic reads a MinIO-era anonymous-read policy.
func (c *Client) LegacyBucketIsPublic(ctx context.Context, bucket string) (bool, error) {
	policy, err := c.s3.GetBucketPolicy(ctx, bucket)
	if err != nil {
		return false, err
	}
	return strings.Contains(policy, "s3:GetObject"), nil
}

// errStop ends a List walk early without reporting an error.
var errStop = errors.New("stop")
