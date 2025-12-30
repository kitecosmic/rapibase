package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rapibase/rapibase/internal/config"
)

type Client struct {
	minio       *minio.Client
	publicMinio *minio.Client // Client configured with public URL for presigned URLs
	publicURL   string
}

type BucketInfo struct {
	Name         string    `json:"name"`
	CreationDate time.Time `json:"creation_date"`
	Policy       string    `json:"policy"`
}

type ObjectInfo struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
	ContentType  string    `json:"content_type"`
	ETag         string    `json:"etag"`
	IsDir        bool      `json:"is_dir"`
}

type UploadResult struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ETag        string `json:"etag"`
	URL         string `json:"url"`
	PublicURL   string `json:"public_url,omitempty"`
}

func NewClient(cfg *config.Config) (*Client, error) {
	client, err := minio.New(cfg.StorageEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
		Secure: cfg.StorageUseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	publicURL := strings.TrimSuffix(cfg.StoragePublicURL, "/")

	// Create a second client for presigned URLs using public endpoint
	var publicClient *minio.Client
	if publicURL != "" && publicURL != "http://"+cfg.StorageEndpoint && publicURL != "https://"+cfg.StorageEndpoint {
		// Parse public URL to get host and scheme
		publicEndpoint := strings.TrimPrefix(strings.TrimPrefix(publicURL, "https://"), "http://")
		useSSL := strings.HasPrefix(publicURL, "https://")

		publicClient, err = minio.New(publicEndpoint, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.StorageAccessKey, cfg.StorageSecretKey, ""),
			Secure: useSSL,
		})
		if err != nil {
			// Fall back to internal client if public client fails
			publicClient = client
		}
	} else {
		publicClient = client
	}

	return &Client{
		minio:       client,
		publicMinio: publicClient,
		publicURL:   publicURL,
	}, nil
}

func (c *Client) ListBuckets(ctx context.Context) ([]BucketInfo, error) {
	buckets, err := c.minio.ListBuckets(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list buckets: %w", err)
	}

	result := make([]BucketInfo, len(buckets))
	for i, b := range buckets {
		policy := "private"
		policyStr, err := c.minio.GetBucketPolicy(ctx, b.Name)
		if err == nil && strings.Contains(policyStr, "s3:GetObject") {
			policy = "public"
		}
		result[i] = BucketInfo{
			Name:         b.Name,
			CreationDate: b.CreationDate,
			Policy:       policy,
		}
	}
	return result, nil
}

func (c *Client) CreateBucket(ctx context.Context, name string) error {
	exists, err := c.minio.BucketExists(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to check bucket existence: %w", err)
	}
	if exists {
		return fmt.Errorf("bucket '%s' already exists", name)
	}

	if err := c.minio.MakeBucket(ctx, name, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("failed to create bucket: %w", err)
	}
	return nil
}

func (c *Client) DeleteBucket(ctx context.Context, name string, force bool) error {
	if force {
		objectsCh := c.minio.ListObjects(ctx, name, minio.ListObjectsOptions{Recursive: true})
		for obj := range objectsCh {
			if obj.Err != nil {
				return fmt.Errorf("failed to list objects: %w", obj.Err)
			}
			if err := c.minio.RemoveObject(ctx, name, obj.Key, minio.RemoveObjectOptions{}); err != nil {
				return fmt.Errorf("failed to remove object '%s': %w", obj.Key, err)
			}
		}
	}

	if err := c.minio.RemoveBucket(ctx, name); err != nil {
		return fmt.Errorf("failed to remove bucket: %w", err)
	}
	return nil
}

func (c *Client) BucketExists(ctx context.Context, name string) (bool, error) {
	return c.minio.BucketExists(ctx, name)
}

func (c *Client) SetBucketPublic(ctx context.Context, bucket string, public bool) error {
	var policy string
	if public {
		policy = fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Principal": {"AWS": ["*"]},
				"Action": ["s3:GetObject"],
				"Resource": ["arn:aws:s3:::%s/*"]
			}]
		}`, bucket)
	} else {
		policy = ""
	}

	if err := c.minio.SetBucketPolicy(ctx, bucket, policy); err != nil {
		return fmt.Errorf("failed to set bucket policy: %w", err)
	}
	return nil
}

func (c *Client) GetBucketPolicy(ctx context.Context, bucket string) (string, error) {
	policy, err := c.minio.GetBucketPolicy(ctx, bucket)
	if err != nil {
		return "private", nil
	}
	if strings.Contains(policy, "s3:GetObject") {
		return "public", nil
	}
	return "private", nil
}

func (c *Client) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	var objects []ObjectInfo
	dirs := make(map[string]bool)

	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	}

	for obj := range c.minio.ListObjects(ctx, bucket, opts) {
		if obj.Err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", obj.Err)
		}

		key := obj.Key
		isDir := strings.HasSuffix(key, "/")

		if isDir {
			if dirs[key] {
				continue
			}
			dirs[key] = true
		}

		objects = append(objects, ObjectInfo{
			Key:          key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
			ContentType:  obj.ContentType,
			ETag:         obj.ETag,
			IsDir:        isDir,
		})
	}

	return objects, nil
}

func (c *Client) UploadObject(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) (*UploadResult, error) {
	info, err := c.minio.PutObject(ctx, bucket, key, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upload object: %w", err)
	}

	policy, _ := c.GetBucketPolicy(ctx, bucket)
	var publicURL string
	if policy == "public" {
		publicURL = fmt.Sprintf("%s/%s/%s", c.publicURL, bucket, key)
	}

	return &UploadResult{
		Key:         key,
		Size:        info.Size,
		ContentType: contentType,
		ETag:        info.ETag,
		URL:         fmt.Sprintf("/%s/%s", bucket, key),
		PublicURL:   publicURL,
	}, nil
}

func (c *Client) GetObject(ctx context.Context, bucket, key string) (*minio.Object, *minio.ObjectInfo, error) {
	obj, err := c.minio.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get object: %w", err)
	}

	stat, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, nil, fmt.Errorf("failed to stat object: %w", err)
	}

	return obj, &stat, nil
}

func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	if err := c.minio.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}
	return nil
}

func (c *Client) GetPresignedURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	// Use public client to generate presigned URL with correct signature
	presignedURL, err := c.publicMinio.PresignedGetObject(ctx, bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return presignedURL.String(), nil
}

func (c *Client) CreateFolder(ctx context.Context, bucket, path string) error {
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}

	_, err := c.minio.PutObject(ctx, bucket, path, strings.NewReader(""), 0, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to create folder: %w", err)
	}
	return nil
}

func (c *Client) CopyObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	src := minio.CopySrcOptions{
		Bucket: srcBucket,
		Object: srcKey,
	}
	dst := minio.CopyDestOptions{
		Bucket: dstBucket,
		Object: dstKey,
	}

	_, err := c.minio.CopyObject(ctx, dst, src)
	if err != nil {
		return fmt.Errorf("failed to copy object: %w", err)
	}
	return nil
}

func (c *Client) MoveObject(ctx context.Context, srcBucket, srcKey, dstBucket, dstKey string) error {
	if err := c.CopyObject(ctx, srcBucket, srcKey, dstBucket, dstKey); err != nil {
		return err
	}
	return c.DeleteObject(ctx, srcBucket, srcKey)
}

func (c *Client) GetPublicURL(bucket, key string) string {
	return fmt.Sprintf("%s/%s/%s", c.publicURL, bucket, key)
}
