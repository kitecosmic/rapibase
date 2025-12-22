package handlers

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/rapibase/rapibase/internal/config"
	"github.com/rapibase/rapibase/internal/database"
	"github.com/rapibase/rapibase/internal/storage"
)

type StorageHandler struct {
	storage *storage.Client
	db      *database.DB
	cfg     *config.Config
}

func NewStorageHandler(storageClient *storage.Client, db *database.DB, cfg *config.Config) *StorageHandler {
	return &StorageHandler{
		storage: storageClient,
		db:      db,
		cfg:     cfg,
	}
}

func (h *StorageHandler) ListBuckets(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	buckets, err := h.storage.ListBuckets(ctx)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"buckets": buckets,
	})
}

func (h *StorageHandler) CreateBucket(c *fiber.Ctx) error {
	var body struct {
		Name   string `json:"name"`
		Public bool   `json:"public"`
	}

	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if body.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Bucket name is required",
		})
	}

	body.Name = strings.ToLower(strings.TrimSpace(body.Name))
	if len(body.Name) < 3 || len(body.Name) > 63 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Bucket name must be between 3 and 63 characters",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.storage.CreateBucket(ctx, body.Name); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	if body.Public {
		if err := h.storage.SetBucketPublic(ctx, body.Name, true); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": fmt.Sprintf("Bucket created but failed to set public policy: %v", err),
			})
		}
	}

	// Save bucket to database
	bucket, err := h.db.CreateStorageBucket(ctx, body.Name, body.Public)
	if err != nil {
		// Bucket created in MinIO but failed to save to DB - log but don't fail
		fmt.Printf("Warning: Failed to save bucket to database: %v\n", err)
	}

	response := fiber.Map{
		"message": "Bucket created successfully",
		"name":    body.Name,
		"public":  body.Public,
	}
	if bucket != nil {
		response["id"] = bucket.ID
		response["created_at"] = bucket.CreatedAt
	}

	return c.Status(fiber.StatusCreated).JSON(response)
}

func (h *StorageHandler) DeleteBucket(c *fiber.Ctx) error {
	name := c.Params("name")
	force := c.Query("force") == "true"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if force {
		// Delete all object records from database first
		h.db.DeleteStorageObjectsByBucket(ctx, name)
	}

	if err := h.storage.DeleteBucket(ctx, name, force); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Delete bucket from database
	h.db.DeleteStorageBucket(ctx, name)

	return c.JSON(fiber.Map{
		"message": "Bucket deleted successfully",
	})
}

func (h *StorageHandler) GetBucketInfo(c *fiber.Ctx) error {
	name := c.Params("name")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exists, err := h.storage.BucketExists(ctx, name)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	if !exists {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Bucket not found",
		})
	}

	policy, _ := h.storage.GetBucketPolicy(ctx, name)

	return c.JSON(fiber.Map{
		"name":   name,
		"policy": policy,
	})
}

func (h *StorageHandler) SetBucketPolicy(c *fiber.Ctx) error {
	name := c.Params("name")

	var body struct {
		Public bool `json:"public"`
	}

	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.storage.SetBucketPublic(ctx, name, body.Public); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	policy := "private"
	if body.Public {
		policy = "public"
	}

	return c.JSON(fiber.Map{
		"message": "Bucket policy updated",
		"policy":  policy,
	})
}

func (h *StorageHandler) ListObjects(c *fiber.Ctx) error {
	bucket := c.Params("name")
	prefix := c.Query("prefix", "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	objects, err := h.storage.ListObjects(ctx, bucket, prefix)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"objects": objects,
		"prefix":  prefix,
	})
}

func (h *StorageHandler) UploadObject(c *fiber.Ctx) error {
	bucket := c.Params("name")
	prefix := c.FormValue("prefix", "")
	ownerID := c.FormValue("owner_id", "")
	metadataStr := c.FormValue("metadata", "{}")

	file, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "No file provided",
		})
	}

	src, err := file.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to open file",
		})
	}
	defer src.Close()

	key := prefix + file.Filename
	contentType := file.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := h.storage.UploadObject(ctx, bucket, key, src, file.Size, contentType)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Parse metadata JSON
	var metadata map[string]interface{}
	if metadataStr != "" && metadataStr != "{}" {
		if err := c.App().Config().JSONDecoder([]byte(metadataStr), &metadata); err != nil {
			metadata = make(map[string]interface{})
		}
	} else {
		metadata = make(map[string]interface{})
	}

	// Save object to database
	var ownerPtr *string
	if ownerID != "" {
		ownerPtr = &ownerID
	}
	dbObj, err := h.db.CreateStorageObject(ctx, bucket, key, ownerPtr, file.Size, contentType, result.ETag, metadata)
	if err != nil {
		fmt.Printf("Warning: Failed to save object to database: %v\n", err)
	}

	response := fiber.Map{
		"key":          result.Key,
		"size":         result.Size,
		"content_type": result.ContentType,
		"etag":         result.ETag,
		"url":          result.URL,
		"public_url":   result.PublicURL,
	}
	if dbObj != nil {
		response["id"] = dbObj.ID
		response["owner"] = dbObj.Owner
		response["metadata"] = dbObj.Metadata
		response["created_at"] = dbObj.CreatedAt
	}

	return c.Status(fiber.StatusCreated).JSON(response)
}

func (h *StorageHandler) GetObject(c *fiber.Ctx) error {
	bucket := c.Params("name")
	key := c.Params("*")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	obj, stat, err := h.storage.GetObject(ctx, bucket, key)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Object not found",
		})
	}
	defer obj.Close()

	c.Set("Content-Type", stat.ContentType)
	c.Set("Content-Length", fmt.Sprintf("%d", stat.Size))
	c.Set("ETag", stat.ETag)
	c.Set("Last-Modified", stat.LastModified.Format(time.RFC1123))

	filename := filepath.Base(key)
	if c.Query("download") == "true" {
		c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	} else {
		c.Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, filename))
	}

	return c.SendStream(obj)
}

func (h *StorageHandler) DeleteObject(c *fiber.Ctx) error {
	bucket := c.Params("name")
	key := c.Params("*")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.storage.DeleteObject(ctx, bucket, key); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Delete from database
	h.db.DeleteStorageObject(ctx, bucket, key)

	return c.JSON(fiber.Map{
		"message": "Object deleted successfully",
	})
}

func (h *StorageHandler) GetPresignedURL(c *fiber.Ctx) error {
	bucket := c.Params("name")
	key := c.Params("*")
	expiryMinutes := c.QueryInt("expiry", 60)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	expiry := time.Duration(expiryMinutes) * time.Minute
	url, err := h.storage.GetPresignedURL(ctx, bucket, key, expiry)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"url":        url,
		"expires_in": expiryMinutes * 60,
	})
}

func (h *StorageHandler) CreateFolder(c *fiber.Ctx) error {
	bucket := c.Params("name")

	var body struct {
		Path string `json:"path"`
	}

	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	if body.Path == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Path is required",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.storage.CreateFolder(ctx, bucket, body.Path); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "Folder created successfully",
		"path":    body.Path,
	})
}

func (h *StorageHandler) MoveObject(c *fiber.Ctx) error {
	bucket := c.Params("name")

	var body struct {
		SourceKey         string `json:"source_key"`
		DestinationKey    string `json:"destination_key"`
		DestinationBucket string `json:"destination_bucket"`
	}

	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	dstBucket := body.DestinationBucket
	if dstBucket == "" {
		dstBucket = bucket
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := h.storage.MoveObject(ctx, bucket, body.SourceKey, dstBucket, body.DestinationKey); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "Object moved successfully",
	})
}

func (h *StorageHandler) CopyObject(c *fiber.Ctx) error {
	bucket := c.Params("name")

	var body struct {
		SourceKey         string `json:"source_key"`
		DestinationKey    string `json:"destination_key"`
		DestinationBucket string `json:"destination_bucket"`
	}

	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	dstBucket := body.DestinationBucket
	if dstBucket == "" {
		dstBucket = bucket
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := h.storage.CopyObject(ctx, bucket, body.SourceKey, dstBucket, body.DestinationKey); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message": "Object copied successfully",
	})
}

func (h *StorageHandler) UploadObjectAPI(c *fiber.Ctx) error {
	bucket := c.Params("bucket")
	prefix := c.FormValue("path", "")
	ownerID := c.FormValue("owner_id", "")
	metadataStr := c.FormValue("metadata", "{}")

	file, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "No file provided",
		})
	}

	src, err := file.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to open file",
		})
	}
	defer src.Close()

	key := prefix + file.Filename
	contentType := file.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := h.storage.UploadObject(ctx, bucket, key, src, file.Size, contentType)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Parse metadata JSON
	var metadata map[string]interface{}
	if metadataStr != "" && metadataStr != "{}" {
		if err := c.App().Config().JSONDecoder([]byte(metadataStr), &metadata); err != nil {
			metadata = make(map[string]interface{})
		}
	} else {
		metadata = make(map[string]interface{})
	}

	// Save object to database
	var ownerPtr *string
	if ownerID != "" {
		ownerPtr = &ownerID
	}
	dbObj, err := h.db.CreateStorageObject(ctx, bucket, key, ownerPtr, file.Size, contentType, result.ETag, metadata)
	if err != nil {
		fmt.Printf("Warning: Failed to save object to database: %v\n", err)
	}

	response := fiber.Map{
		"key":          result.Key,
		"size":         result.Size,
		"content_type": result.ContentType,
		"etag":         result.ETag,
		"url":          result.URL,
		"public_url":   result.PublicURL,
	}
	if dbObj != nil {
		response["id"] = dbObj.ID
		response["owner"] = dbObj.Owner
		response["metadata"] = dbObj.Metadata
		response["created_at"] = dbObj.CreatedAt
	}

	return c.Status(fiber.StatusCreated).JSON(response)
}

func (h *StorageHandler) ListObjectsAPI(c *fiber.Ctx) error {
	bucket := c.Params("bucket")
	prefix := c.Query("prefix", "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	objects, err := h.storage.ListObjects(ctx, bucket, prefix)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"objects": objects,
		"prefix":  prefix,
	})
}

func (h *StorageHandler) GetObjectAPI(c *fiber.Ctx) error {
	bucket := c.Params("bucket")
	key := c.Params("*")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	obj, stat, err := h.storage.GetObject(ctx, bucket, key)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Object not found",
		})
	}
	defer obj.Close()

	c.Set("Content-Type", stat.ContentType)
	c.Set("Content-Length", fmt.Sprintf("%d", stat.Size))
	c.Set("ETag", stat.ETag)

	data, err := io.ReadAll(obj)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Failed to read object",
		})
	}

	return c.Send(data)
}

func (h *StorageHandler) DeleteObjectAPI(c *fiber.Ctx) error {
	bucket := c.Params("bucket")
	key := c.Params("*")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.storage.DeleteObject(ctx, bucket, key); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// Delete from database
	h.db.DeleteStorageObject(ctx, bucket, key)

	return c.JSON(fiber.Map{
		"message": "Object deleted successfully",
	})
}

// ListObjectsByOwner returns all files owned by a specific user
func (h *StorageHandler) ListObjectsByOwner(c *fiber.Ctx) error {
	ownerID := c.Params("owner_id")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	objects, err := h.db.ListStorageObjectsByOwner(ctx, ownerID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"objects": objects,
		"owner":   ownerID,
	})
}

// SearchObjectsByMetadata searches files by metadata key-value
func (h *StorageHandler) SearchObjectsByMetadata(c *fiber.Ctx) error {
	bucket := c.Params("bucket")
	key := c.Query("key")
	value := c.Query("value")

	if key == "" || value == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Both 'key' and 'value' query parameters are required",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	objects, err := h.db.SearchStorageObjectsByMetadata(ctx, bucket, key, value)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"objects": objects,
		"filter": fiber.Map{
			"key":   key,
			"value": value,
		},
	})
}

// GetObjectMetadata returns the database metadata for an object
func (h *StorageHandler) GetObjectMetadata(c *fiber.Ctx) error {
	bucket := c.Params("bucket")
	key := c.Params("*")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	obj, err := h.db.GetStorageObject(ctx, bucket, key)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "Object not found",
		})
	}

	return c.JSON(obj)
}

// UpdateObjectMetadata updates the metadata of an object
func (h *StorageHandler) UpdateObjectMetadata(c *fiber.Ctx) error {
	bucket := c.Params("bucket")
	key := c.Params("*")

	var body struct {
		Metadata map[string]interface{} `json:"metadata"`
	}

	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Invalid request body",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.db.UpdateStorageObjectMetadata(ctx, bucket, key, body.Metadata); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"message":  "Metadata updated successfully",
		"metadata": body.Metadata,
	})
}
