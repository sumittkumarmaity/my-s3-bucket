package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/my-s3-bucket/storage-engine/internal/auth"
	"github.com/my-s3-bucket/storage-engine/internal/encryption"
	"github.com/my-s3-bucket/storage-engine/internal/integrity"
	"github.com/my-s3-bucket/storage-engine/internal/metadata"
	"github.com/my-s3-bucket/storage-engine/internal/storage"
)

type S3Handler struct {
	meta       metadata.Store
	engine     storage.Engine
	encryptor  encryption.Provider
	logger     *slog.Logger
	maxObjSize int64
}

func NewS3Handler(meta metadata.Store, engine storage.Engine, enc encryption.Provider, maxObjSize int64, logger *slog.Logger) *S3Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if enc == nil {
		enc = &encryption.NoOpProvider{}
	}
	if maxObjSize <= 0 {
		maxObjSize = 5 * 1024 * 1024 * 1024 // 5GB
	}
	return &S3Handler{
		meta:       meta,
		engine:     engine,
		encryptor:  enc,
		logger:     logger,
		maxObjSize: maxObjSize,
	}
}

// ServeHTTP routes incoming S3 requests
func (h *S3Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		// Service level: GET / -> ListBuckets
		if r.Method == http.MethodGet {
			h.handleListBuckets(w, r)
			return
		}
		SendS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "The specified method is not allowed against this resource.")
		return
	}

	parts := strings.SplitN(path, "/", 2)
	bucketName := parts[0]
	objectKey := ""
	if len(parts) > 1 {
		objectKey = parts[1]
	}

	q := r.URL.Query()

	if objectKey == "" {
		// Bucket level operations
		switch r.Method {
		case http.MethodPut:
			if _, hasVersioning := q["versioning"]; hasVersioning {
				h.handlePutBucketVersioning(w, r, bucketName)
				return
			}
			h.handleCreateBucket(w, r, bucketName)
		case http.MethodGet:
			if _, hasVersioning := q["versioning"]; hasVersioning {
				h.handleGetBucketVersioning(w, r, bucketName)
				return
			}
			if _, hasVersions := q["versions"]; hasVersions {
				h.handleListObjectVersions(w, r, bucketName)
				return
			}
			h.handleListObjects(w, r, bucketName)
		case http.MethodHead:
			h.handleHeadBucket(w, r, bucketName)
		case http.MethodDelete:
			h.handleDeleteBucket(w, r, bucketName)
		default:
			SendS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed")
		}
		return
	}

	// Object level operations
	switch r.Method {
	case http.MethodPut:
		if q.Get("uploadId") != "" && q.Get("partNumber") != "" {
			h.handleUploadPart(w, r, bucketName, objectKey)
			return
		}
		h.handlePutObject(w, r, bucketName, objectKey)
	case http.MethodGet:
		if q.Get("uploadId") != "" {
			h.handleListParts(w, r, bucketName, objectKey)
			return
		}
		h.handleGetObject(w, r, bucketName, objectKey)
	case http.MethodHead:
		h.handleHeadObject(w, r, bucketName, objectKey)
	case http.MethodDelete:
		if q.Get("uploadId") != "" {
			h.handleAbortMultipartUpload(w, r, bucketName, objectKey)
			return
		}
		h.handleDeleteObject(w, r, bucketName, objectKey)
	case http.MethodPost:
		if _, hasUploads := q["uploads"]; hasUploads {
			h.handleCreateMultipartUpload(w, r, bucketName, objectKey)
			return
		}
		if q.Get("uploadId") != "" {
			h.handleCompleteMultipartUpload(w, r, bucketName, objectKey)
			return
		}
		SendS3Error(w, r, http.StatusBadRequest, "InvalidRequest", "Unsupported POST operation")
	default:
		SendS3Error(w, r, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed")
	}
}

// -------------------------------------------------------------
// Bucket Handlers
// -------------------------------------------------------------
// Bucket Handlers & Multi-Tenant Authorization
// -------------------------------------------------------------

func (h *S3Handler) getBucketAndAuthorize(w http.ResponseWriter, r *http.Request, bucketName string) (*metadata.Bucket, *auth.Principal, bool) {
	principal := auth.GetPrincipal(r.Context())
	if principal == nil {
		SendS3Error(w, r, http.StatusForbidden, "AccessDenied", "Access Denied: missing authentication principal")
		return nil, nil, false
	}

	b, err := h.meta.GetBucket(r.Context(), bucketName)
	if err != nil {
		if errors.Is(err, metadata.ErrBucketNotFound) {
			SendS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return nil, nil, false
		}
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to retrieve bucket metadata")
		return nil, nil, false
	}

	if !principal.IsAdmin && b.OwnerID != principal.UserID {
		SendS3Error(w, r, http.StatusForbidden, "AccessDenied", "Access Denied: you do not have permission to access this bucket")
		return nil, nil, false
	}

	return &b, principal, true
}

func isValidBucketName(name string) bool {
	if len(name) < 3 || len(name) > 63 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' {
			continue
		}
		return false
	}
	if name[0] == '.' || name[0] == '-' || name[len(name)-1] == '.' || name[len(name)-1] == '-' {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	return true
}

func (h *S3Handler) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	principal := auth.GetPrincipal(r.Context())
	if principal == nil {
		SendS3Error(w, r, http.StatusForbidden, "AccessDenied", "Access Denied: missing authentication principal")
		return
	}

	var buckets []metadata.Bucket
	var err error

	if !principal.IsAdmin {
		buckets, err = h.meta.ListBucketsByOwner(r.Context(), principal.UserID)
	} else {
		buckets, err = h.meta.ListBuckets(r.Context())
	}

	if err != nil {
		h.logger.Error("Failed to list buckets", "err", err)
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to list buckets")
		return
	}

	ownerID := principal.UserID
	res := ListAllMyBucketsResult{
		Owner: CanonicalUser{ID: ownerID, DisplayName: ownerID},
	}
	for _, b := range buckets {
		res.Buckets = append(res.Buckets, BucketEntry{
			Name:         b.Name,
			CreationDate: b.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/xml")
	out, _ := xml.MarshalIndent(res, "", "  ")
	_, _ = w.Write([]byte(xml.Header + string(out)))
}

func (h *S3Handler) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	if !isValidBucketName(bucketName) {
		SendS3Error(w, r, http.StatusBadRequest, "InvalidBucketName", "The specified bucket is not valid. Bucket names must be between 3 and 63 characters long and contain only lowercase letters, numbers, dots, and hyphens.")
		return
	}

	principal := auth.GetPrincipal(r.Context())
	if principal == nil {
		SendS3Error(w, r, http.StatusForbidden, "AccessDenied", "Access Denied: missing authentication principal")
		return
	}

	b := metadata.Bucket{
		ID:               uuid.NewString(),
		Name:             bucketName,
		OwnerID:          principal.UserID,
		VersioningStatus: "DISABLED",
		CreatedAt:        time.Now().UTC(),
	}

	if err := h.meta.CreateBucket(r.Context(), b); err != nil {
		if errors.Is(err, metadata.ErrBucketAlreadyExists) {
			SendS3Error(w, r, http.StatusConflict, "BucketAlreadyOwnedByYou", "Your previous request to create the named bucket succeeded and you already own it.")
			return
		}
		h.logger.Error("Failed to create bucket", "err", err)
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to create bucket")
		return
	}

	w.Header().Set("Location", "/"+bucketName)
	w.WriteHeader(http.StatusOK)
}

func (h *S3Handler) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	err := h.meta.DeleteBucket(r.Context(), b.Name)
	if err != nil {
		if errors.Is(err, metadata.ErrBucketNotFound) {
			SendS3Error(w, r, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist.")
			return
		}
		if errors.Is(err, metadata.ErrBucketNotEmpty) {
			SendS3Error(w, r, http.StatusConflict, "BucketNotEmpty", "The bucket you tried to delete is not empty.")
			return
		}
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to delete bucket")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *S3Handler) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	_, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *S3Handler) handlePutBucketVersioning(w http.ResponseWriter, r *http.Request, bucketName string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	var cfg VersioningConfiguration
	body, err := io.ReadAll(r.Body)
	if err != nil {
		SendS3Error(w, r, http.StatusBadRequest, "MalformedXML", "Failed to read request body")
		return
	}
	if err := xml.Unmarshal(body, &cfg); err != nil {
		SendS3Error(w, r, http.StatusBadRequest, "MalformedXML", "Malformed XML")
		return
	}

	status := strings.ToUpper(cfg.Status)
	if status != "ENABLED" && status != "SUSPENDED" {
		SendS3Error(w, r, http.StatusBadRequest, "IllegalVersioningConfigurationException", "Status must be Enabled or Suspended")
		return
	}

	if err := h.meta.SetBucketVersioning(r.Context(), b.Name, status); err != nil {
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to update versioning")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *S3Handler) handleGetBucketVersioning(w http.ResponseWriter, r *http.Request, bucketName string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	cfg := VersioningConfiguration{}
	if b.VersioningStatus == "ENABLED" {
		cfg.Status = "Enabled"
	} else if b.VersioningStatus == "SUSPENDED" {
		cfg.Status = "Suspended"
	}

	w.Header().Set("Content-Type", "application/xml")
	out, _ := xml.MarshalIndent(cfg, "", "  ")
	_, _ = w.Write([]byte(xml.Header + string(out)))
}

func (h *S3Handler) handleListObjects(w http.ResponseWriter, r *http.Request, bucketName string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	q := r.URL.Query()
	prefix := q.Get("prefix")
	delimiter := q.Get("delimiter")
	marker := q.Get("continuation-token")
	if marker == "" {
		marker = q.Get("marker")
	}
	if marker == "" {
		marker = q.Get("start-after")
	}

	maxKeys := 1000
	if mkStr := q.Get("max-keys"); mkStr != "" {
		if mk, err := strconv.Atoi(mkStr); err == nil && mk > 0 {
			maxKeys = mk
		}
	}

	// Fetch objects from metadata store
	objects, err := h.meta.ListObjects(r.Context(), b.ID, prefix, marker, maxKeys+1)
	if err != nil {
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to list objects")
		return
	}

	isTruncated := false
	var nextToken string
	if len(objects) > maxKeys {
		isTruncated = true
		objects = objects[:maxKeys]
		nextToken = objects[len(objects)-1].ObjectKey
	}

	res := ListBucketResult{
		Name:                  bucketName,
		Prefix:                prefix,
		MaxKeys:               maxKeys,
		Delimiter:             delimiter,
		IsTruncated:           isTruncated,
		ContinuationToken:     marker,
		NextContinuationToken: nextToken,
	}

	commonPrefixesMap := make(map[string]struct{})

	for _, obj := range objects {
		if delimiter != "" {
			trimmed := strings.TrimPrefix(obj.ObjectKey, prefix)
			idx := strings.Index(trimmed, delimiter)
			if idx != -1 {
				cp := prefix + trimmed[:idx+len(delimiter)]
				commonPrefixesMap[cp] = struct{}{}
				continue
			}
		}

		res.Contents = append(res.Contents, ObjectEntry{
			Key:          obj.ObjectKey,
			LastModified: obj.CreatedAt.UTC().Format(time.RFC3339),
			ETag:         obj.ETag,
			Size:         obj.Size,
			StorageClass: "STANDARD",
		})
	}

	var cpList []string
	for cp := range commonPrefixesMap {
		cpList = append(cpList, cp)
	}
	sort.Strings(cpList)
	for _, cp := range cpList {
		res.CommonPrefixes = append(res.CommonPrefixes, PrefixEntry{Prefix: cp})
	}

	res.KeyCount = len(res.Contents) + len(res.CommonPrefixes)

	w.Header().Set("Content-Type", "application/xml")
	out, _ := xml.MarshalIndent(res, "", "  ")
	_, _ = w.Write([]byte(xml.Header + string(out)))
}

func (h *S3Handler) handleListObjectVersions(w http.ResponseWriter, r *http.Request, bucketName string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	q := r.URL.Query()
	prefix := q.Get("prefix")
	keyMarker := q.Get("key-marker")
	versionIdMarker := q.Get("version-id-marker")
	maxKeys := 1000
	if mkStr := q.Get("max-keys"); mkStr != "" {
		if mk, err := strconv.Atoi(mkStr); err == nil && mk > 0 {
			maxKeys = mk
		}
	}

	objects, err := h.meta.ListObjectVersions(r.Context(), b.ID, prefix, keyMarker, versionIdMarker, maxKeys)
	if err != nil {
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to list versions")
		return
	}

	res := ListVersionsResult{
		Name:    bucketName,
		Prefix:  prefix,
		MaxKeys: maxKeys,
	}

	for _, obj := range objects {
		if obj.IsDeleteMarker {
			res.DeleteMarker = append(res.DeleteMarker, DeleteMarkerEntry{
				Key:          obj.ObjectKey,
				VersionId:    obj.VersionID,
				IsLatest:     true,
				LastModified: obj.CreatedAt.UTC().Format(time.RFC3339),
			})
		} else {
			res.Version = append(res.Version, VersionEntry{
				Key:          obj.ObjectKey,
				VersionId:    obj.VersionID,
				IsLatest:     true,
				LastModified: obj.CreatedAt.UTC().Format(time.RFC3339),
				ETag:         obj.ETag,
				Size:         obj.Size,
				StorageClass: "STANDARD",
			})
		}
	}

	w.Header().Set("Content-Type", "application/xml")
	out, _ := xml.MarshalIndent(res, "", "  ")
	_, _ = w.Write([]byte(xml.Header + string(out)))
}

// -------------------------------------------------------------
// Object Handlers (Put, Get, Head, Delete)
// -------------------------------------------------------------

func (h *S3Handler) handlePutObject(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	objectID := uuid.NewString()
	versionID := "null"
	if b.VersioningStatus == "ENABLED" {
		versionID = uuid.NewString()
	}

	// Wrap in streaming checksum calculation
	hr := integrity.NewHashingReader(r.Body)

	// Wrap in encryption stream if enabled
	dataStream, err := h.encryptor.Encrypt(hr)
	if err != nil {
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Encryption failure")
		return
	}

	_, err = h.engine.Put(r.Context(), objectID, dataStream)
	if err != nil {
		h.logger.Error("Failed to write object to storage engine", "err", err)
		if errors.Is(err, storage.ErrInsufficientStorage) {
			SendS3Error(w, r, http.StatusInsufficientStorage, "InsufficientStorage", "Insufficient storage space remaining on cluster node.")
			return
		}
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to store object data")
		return
	}

	// Plaintext size from hashing reader
	plainSize := hr.BytesRead()
	etag := hr.ETag()
	crc32c := hr.CRC32CHex()
	sha256Hex := hr.SHA256Hex()

	obj := metadata.Object{
		ID:             objectID,
		BucketID:       b.ID,
		ObjectKey:      objectKey,
		VersionID:      versionID,
		Size:           plainSize,
		ETag:           etag,
		ChecksumCRC32C: crc32c,
		ChecksumSHA256: sha256Hex,
		ContentType:    contentType,
		IsDeleteMarker: false,
		CreatedAt:      time.Now().UTC(),
	}

	// Optional retention lock header
	if lockUntil := r.Header.Get("x-amz-object-lock-retain-until-date"); lockUntil != "" {
		if t, err := time.Parse(time.RFC3339, lockUntil); err == nil {
			obj.RetentionUntil = &t
		}
	}

	if err := h.meta.PutObject(r.Context(), obj); err != nil {
		_ = h.engine.Delete(r.Context(), objectID)
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to commit object metadata")
		return
	}

	// Enqueue replication outbox job
	_ = h.meta.EnqueueReplicationJob(r.Context(), metadata.ReplicationJob{
		ObjectID:  objectID,
		BucketID:  b.ID,
		ObjectKey: objectKey,
		VersionID: versionID,
		Action:    "PUT",
	})

	w.Header().Set("ETag", etag)
	w.Header().Set("x-amz-checksum-crc32c", crc32c)
	if versionID != "null" {
		w.Header().Set("x-amz-version-id", versionID)
	}
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

func (h *S3Handler) handleGetObject(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	versionID := r.URL.Query().Get("versionId")
	obj, err := h.meta.GetObject(r.Context(), b.ID, objectKey, versionID)
	if err != nil {
		if errors.Is(err, metadata.ErrObjectNotFound) {
			if obj.IsDeleteMarker {
				w.Header().Set("x-amz-delete-marker", "true")
			}
			SendS3Error(w, r, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")
			return
		}
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to retrieve object")
		return
	}

	// Check Range header
	rangeHeader := r.Header.Get("Range")
	var rc io.ReadCloser

	w.Header().Set("ETag", obj.ETag)
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Accept-Ranges", "bytes")
	if obj.ChecksumCRC32C != "" {
		w.Header().Set("x-amz-checksum-crc32c", obj.ChecksumCRC32C)
	}
	if obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}

	if rangeHeader != "" && !h.encryptor.IsEnabled() { // Range supported on unencrypted files
		start, end, err := parseRange(rangeHeader, obj.Size)
		if err == nil {
			length := end - start + 1
			rc, err = h.engine.GetRange(r.Context(), obj.ID, start, length)
			if err == nil {
				defer rc.Close()
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, obj.Size))
				w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = io.Copy(w, rc)
				return
			}
		}
	}

	rc, err = h.engine.Get(r.Context(), obj.ID)
	if err != nil {
		SendS3Error(w, r, http.StatusNotFound, "NoSuchKey", "Object physical data missing")
		return
	}
	defer rc.Close()

	decStream, err := h.encryptor.Decrypt(rc)
	if err != nil {
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Decryption failed")
		return
	}

	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, decStream)
}

func (h *S3Handler) handleHeadObject(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	versionID := r.URL.Query().Get("versionId")
	obj, err := h.meta.GetObject(r.Context(), b.ID, objectKey, versionID)
	if err != nil {
		if obj.IsDeleteMarker {
			w.Header().Set("x-amz-delete-marker", "true")
		}
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("ETag", obj.ETag)
	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Accept-Ranges", "bytes")
	if obj.ChecksumCRC32C != "" {
		w.Header().Set("x-amz-checksum-crc32c", obj.ChecksumCRC32C)
	}
	if obj.VersionID != "null" {
		w.Header().Set("x-amz-version-id", obj.VersionID)
	}
	w.WriteHeader(http.StatusOK)
}

func (h *S3Handler) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	versionID := r.URL.Query().Get("versionId")
	delObj, err := h.meta.DeleteObject(r.Context(), b.ID, objectKey, versionID)
	if err != nil {
		if errors.Is(err, metadata.ErrObjectLocked) {
			SendS3Error(w, r, http.StatusForbidden, "ObjectLocked", "Object is retention locked and cannot be deleted")
			return
		}
		if errors.Is(err, metadata.ErrObjectNotFound) {
			// S3 returns 204 even if object did not exist
			w.WriteHeader(http.StatusNoContent)
			return
		}
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to delete object")
		return
	}

	if delObj.IsDeleteMarker {
		w.Header().Set("x-amz-delete-marker", "true")
		w.Header().Set("x-amz-version-id", delObj.VersionID)
	} else if delObj.ID != "" {
		// Hard delete physical data
		_ = h.engine.Delete(r.Context(), delObj.ID)
	}

	// Enqueue replication outbox event
	_ = h.meta.EnqueueReplicationJob(r.Context(), metadata.ReplicationJob{
		ObjectID:  delObj.ID,
		BucketID:  b.ID,
		ObjectKey: objectKey,
		VersionID: versionID,
		Action:    "DELETE",
	})

	w.WriteHeader(http.StatusNoContent)
}

// -------------------------------------------------------------
// Multipart Handlers
// -------------------------------------------------------------

func (h *S3Handler) handleCreateMultipartUpload(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	uploadID := uuid.NewString()
	mp := metadata.MultipartUpload{
		UploadID:    uploadID,
		BucketID:    b.ID,
		ObjectKey:   objectKey,
		ContentType: contentType,
		CreatedAt:   time.Now().UTC(),
	}

	if err := h.meta.CreateMultipartUpload(r.Context(), mp); err != nil {
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to initiate multipart upload")
		return
	}

	res := InitiateMultipartUploadResult{
		Bucket:   bucketName,
		Key:      objectKey,
		UploadId: uploadID,
	}

	w.Header().Set("Content-Type", "application/xml")
	out, _ := xml.MarshalIndent(res, "", "  ")
	_, _ = w.Write([]byte(xml.Header + string(out)))
}

func (h *S3Handler) handleUploadPart(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	_, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	uploadID := r.URL.Query().Get("uploadId")
	partNumStr := r.URL.Query().Get("partNumber")
	partNum, err := strconv.Atoi(partNumStr)
	if err != nil || partNum < 1 || partNum > 10000 {
		SendS3Error(w, r, http.StatusBadRequest, "InvalidArgument", "Part number must be an integer between 1 and 10000")
		return
	}

	// Verify upload exists
	_, err = h.meta.GetMultipartUpload(r.Context(), uploadID)
	if err != nil {
		SendS3Error(w, r, http.StatusNotFound, "NoSuchUpload", "The specified multipart upload does not exist.")
		return
	}

	hr := integrity.NewHashingReader(r.Body)
	written, partPath, err := h.engine.PutPart(r.Context(), uploadID, partNum, hr)
	if err != nil {
		if errors.Is(err, storage.ErrInsufficientStorage) {
			SendS3Error(w, r, http.StatusInsufficientStorage, "InsufficientStorage", "Insufficient storage space remaining on cluster node.")
			return
		}
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to save part")
		return
	}

	etag := hr.ETag()
	checksum := hr.CRC32CHex()

	part := metadata.MultipartPart{
		UploadID:     uploadID,
		PartNumber:   partNum,
		ETag:         etag,
		Size:         written,
		Checksum:     checksum,
		PhysicalPath: partPath,
		CreatedAt:    time.Now().UTC(),
	}

	if err := h.meta.AddMultipartPart(r.Context(), part); err != nil {
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to register part")
		return
	}

	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

func (h *S3Handler) handleListParts(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	_, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	uploadID := r.URL.Query().Get("uploadId")
	mp, err := h.meta.GetMultipartUpload(r.Context(), uploadID)
	if err != nil {
		SendS3Error(w, r, http.StatusNotFound, "NoSuchUpload", "The specified multipart upload does not exist.")
		return
	}

	parts, err := h.meta.ListMultipartParts(r.Context(), uploadID)
	if err != nil {
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to list parts")
		return
	}

	res := ListPartsResult{
		Bucket:   bucketName,
		Key:      mp.ObjectKey,
		UploadId: uploadID,
	}
	for _, p := range parts {
		res.Parts = append(res.Parts, PartEntry{
			PartNumber:   p.PartNumber,
			ETag:         p.ETag,
			Size:         p.Size,
			LastModified: p.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/xml")
	out, _ := xml.MarshalIndent(res, "", "  ")
	_, _ = w.Write([]byte(xml.Header + string(out)))
}

func (h *S3Handler) handleCompleteMultipartUpload(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	b, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	uploadID := r.URL.Query().Get("uploadId")
	mp, err := h.meta.GetMultipartUpload(r.Context(), uploadID)
	if err != nil {
		SendS3Error(w, r, http.StatusNotFound, "NoSuchUpload", "The specified multipart upload does not exist.")
		return
	}

	// Parse completion request XML
	var compReq CompleteMultipartUploadRequest
	bodyBytes, err := io.ReadAll(r.Body)
	if err == nil && len(bodyBytes) > 0 {
		_ = xml.Unmarshal(bodyBytes, &compReq)
	}

	parts, err := h.meta.ListMultipartParts(r.Context(), uploadID)
	if err != nil || len(parts) == 0 {
		SendS3Error(w, r, http.StatusBadRequest, "InvalidPart", "No parts found for this upload")
		return
	}

	var partPaths []string
	for _, p := range parts {
		partPaths = append(partPaths, p.PhysicalPath)
	}

	objectID := uuid.NewString()
	totalWritten, err := h.engine.AssembleParts(r.Context(), objectID, partPaths)
	if err != nil {
		if errors.Is(err, storage.ErrInsufficientStorage) {
			SendS3Error(w, r, http.StatusInsufficientStorage, "InsufficientStorage", "Insufficient storage space remaining on cluster node.")
			return
		}
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to assemble parts")
		return
	}

	// Compute combined ETag
	multipartETag := fmt.Sprintf("\"%s-%d\"", uuid.NewString()[:16], len(parts))
	versionID := "null"
	if b.VersioningStatus == "ENABLED" {
		versionID = uuid.NewString()
	}

	obj := metadata.Object{
		ID:             objectID,
		BucketID:       b.ID,
		ObjectKey:      objectKey,
		VersionID:      versionID,
		Size:           totalWritten,
		ETag:           multipartETag,
		ChecksumCRC32C: "",
		ChecksumSHA256: "",
		ContentType:    mp.ContentType,
		IsDeleteMarker: false,
		CreatedAt:      time.Now().UTC(),
	}

	if err := h.meta.PutObject(r.Context(), obj); err != nil {
		_ = h.engine.Delete(r.Context(), objectID)
		SendS3Error(w, r, http.StatusInternalServerError, "InternalError", "Failed to store completed object")
		return
	}

	// Cleanup multipart metadata and files
	_ = h.meta.DeleteMultipartUpload(r.Context(), uploadID)
	_ = h.engine.DeleteMultipartUpload(r.Context(), uploadID)

	// Enqueue replication outbox
	_ = h.meta.EnqueueReplicationJob(r.Context(), metadata.ReplicationJob{
		ObjectID:  objectID,
		BucketID:  b.ID,
		ObjectKey: objectKey,
		VersionID: versionID,
		Action:    "PUT",
	})

	res := CompleteMultipartUploadResult{
		Location: "/" + bucketName + "/" + objectKey,
		Bucket:   bucketName,
		Key:      objectKey,
		ETag:     multipartETag,
	}

	w.Header().Set("Content-Type", "application/xml")
	out, _ := xml.MarshalIndent(res, "", "  ")
	_, _ = w.Write([]byte(xml.Header + string(out)))
}

func (h *S3Handler) handleAbortMultipartUpload(w http.ResponseWriter, r *http.Request, bucketName, objectKey string) {
	_, _, ok := h.getBucketAndAuthorize(w, r, bucketName)
	if !ok {
		return
	}

	uploadID := r.URL.Query().Get("uploadId")
	_ = h.meta.DeleteMultipartUpload(r.Context(), uploadID)
	_ = h.engine.DeleteMultipartUpload(r.Context(), uploadID)
	w.WriteHeader(http.StatusNoContent)
}

// Range parsing helper: "bytes=0-499" or "bytes=500-"
func parseRange(rangeHeader string, totalSize int64) (start, end int64, err error) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return 0, 0, errors.New("invalid range prefix")
	}
	spec := strings.TrimPrefix(rangeHeader, "bytes=")
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return 0, 0, errors.New("invalid range format")
	}

	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}

	if parts[1] == "" {
		end = totalSize - 1
	} else {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, err
		}
	}

	if start < 0 || end >= totalSize || start > end {
		return 0, 0, errors.New("unsatisfiable range")
	}

	return start, end, nil
}
