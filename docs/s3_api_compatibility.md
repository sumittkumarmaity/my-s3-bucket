# Amazon S3 API Compatibility Matrix

## 1. Overview

This document provides a comprehensive reference of all supported Amazon S3 REST API operations, request parameters, response structures, supported headers, and error codes implemented in the engine.

---

## 2. API Support Matrix

### 2.1 Service & Bucket Operations

| Operation | HTTP Method | Endpoint / Query | Supported Query Params | Supported Request Headers | Response Status | Compatibility Notes |
|---|---|---|---|---|---|---|
| **ListBuckets** | `GET` | `/` | — | `Authorization`, `Date`, `X-Amz-Date` | `200 OK` | Returns XML listing all buckets owned by caller. |
| **CreateBucket** | `PUT` | `/{bucket}` | — | `Authorization`, `Date`, `X-Amz-Date` | `200 OK` | Creates bucket. Rejects duplicate names with `409 BucketAlreadyExists`. |
| **HeadBucket** | `HEAD` | `/{bucket}` | — | `Authorization`, `Date`, `X-Amz-Date` | `200 OK`, `404 Not Found` | Checks bucket existence and access rights. |
| **DeleteBucket** | `DELETE` | `/{bucket}` | — | `Authorization`, `Date`, `X-Amz-Date` | `204 No Content` | Deletes empty bucket. Returns `409 BucketNotEmpty` if objects exist. |
| **GetBucketVersioning** | `GET` | `/{bucket}?versioning` | `versioning` | `Authorization`, `Date`, `X-Amz-Date` | `200 OK` | Returns `VersioningConfiguration` XML (`Status`: `ENABLED` or `SUSPENDED`). |
| **PutBucketVersioning** | `PUT` | `/{bucket}?versioning` | `versioning` | `Authorization`, `Date`, `X-Amz-Date` | `200 OK` | Accepts XML setting versioning status to `Enabled` or `Suspended`. |
| **ListObjectsV2** | `GET` | `/{bucket}` | `prefix`, `delimiter`, `max-keys`, `continuation-token` | `Authorization`, `Date`, `X-Amz-Date` | `200 OK` | Supports folder delimiters, common prefixes, pagination continuation markers. |
| **ListObjectVersions**| `GET` | `/{bucket}?versions` | `prefix`, `delimiter`, `max-keys`, `key-marker`, `version-id-marker` | `Authorization`, `Date`, `X-Amz-Date` | `200 OK` | Lists object version history and deletion markers. |

### 2.2 Object Operations

| Operation | HTTP Method | Endpoint / Query | Supported Query Params | Supported Request Headers | Response Headers | Response Status | Compatibility Notes |
|---|---|---|---|---|---|---|---|
| **PutObject** | `PUT` | `/{bucket}/{key}` | — | `Content-Type`, `Content-Length`, `X-Amz-Content-Sha256`, `x-amz-object-lock-retain-until-date` | `ETag`, `x-amz-checksum-crc32c`, `x-amz-version-id` | `200 OK` | Supports streaming AES-256-GCM encryption at rest and content sha256 validation. |
| **GetObject** | `GET` | `/{bucket}/{key}` | `versionId` | `Range`, `Authorization`, `Date` | `Content-Type`, `Content-Length`, `ETag`, `x-amz-version-id`, `Accept-Ranges` | `200 OK`, `206 Partial Content` | Transparently decrypts on read. Returns 206 for `bytes=start-end` requests. |
| **HeadObject** | `HEAD` | `/{bucket}/{key}` | `versionId` | `Range`, `Authorization`, `Date` | `Content-Type`, `Content-Length`, `ETag`, `x-amz-version-id`, `x-amz-delete-marker` | `200 OK`, `404 Not Found` | Returns metadata headers without downloading body payload. |
| **DeleteObject** | `DELETE` | `/{bucket}/{key}` | `versionId` | `Authorization`, `Date` | `x-amz-version-id`, `x-amz-delete-marker` | `204 No Content` | In versioned buckets, creates a delete marker if `versionId` is omitted. Respects object retention locks. |

### 2.3 Multipart Upload Operations

| Operation | HTTP Method | Endpoint / Query | Supported Query Params | Request Body | Response Status | Compatibility Notes |
|---|---|---|---|---|---|---|
| **CreateMultipartUpload** | `POST` | `/{bucket}/{key}?uploads` | `uploads` | None | `200 OK` | Allocates new UUID uploadId. Stores metadata in DB. |
| **UploadPart** | `PUT` | `/{bucket}/{key}?uploadId=X&partNumber=N` | `uploadId`, `partNumber` | Raw chunk data | `200 OK` | Part numbers 1-10000. Returns part MD5 `ETag` and CRC32C header. |
| **ListParts** | `GET` | `/{bucket}/{key}?uploadId=X` | `uploadId` | None | `200 OK` | Returns XML listing all successfully uploaded parts. |
| **CompleteMultipartUpload**| `POST` | `/{bucket}/{key}?uploadId=X` | `uploadId` | `<CompleteMultipartUpload><Part>...</Part></CompleteMultipartUpload>` | `200 OK` | Validates ascending order, part existence, part ETags. Assembles via AES-GCM stream. Returns deterministic `"<md5>-N"` ETag. |
| **AbortMultipartUpload** | `DELETE`| `/{bucket}/{key}?uploadId=X` | `uploadId` | None | `204 No Content` | Removes temporary part files and multipart upload records. Strict UUID check prevents path traversal. |

---

## 3. S3 Error Code Reference

The engine emits standard XML error payloads formatted as:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>ErrorCode</Code>
  <Message>Human readable description.</Message>
</Error>
```

| Error Code | HTTP Status | Root Cause |
|---|---|---|
| `AccessDenied` | `403 Forbidden` | Expired presigned request, invalid permissions, or bucket ownership mismatch. |
| `RequestTimeTooSkewed` | `403 Forbidden` | Difference between `X-Amz-Date` / `Date` header and server clock exceeds 15 minutes. |
| `SignatureDoesNotMatch` | `403 Forbidden` | Calculated HMAC-SHA256 signature does not match client signature. |
| `InvalidAccessKeyId` | `403 Forbidden` | Provided Access Key ID does not exist in the database or is inactive. |
| `NoSuchBucket` | `404 Not Found` | Requested bucket does not exist. |
| `NoSuchKey` | `404 Not Found` | Requested object key or version ID does not exist or is deleted. |
| `NoSuchUpload` | `404 Not Found` | Multipart `uploadId` does not exist or belongs to a different bucket/key. |
| `BucketAlreadyExists` | `409 Conflict` | Target bucket name is already registered. |
| `BucketNotEmpty` | `409 Conflict` | Attempted to delete a bucket containing active objects or versions. |
| `ObjectLocked` | `403 Forbidden` | Object is currently protected under an active retention lock date. |
| `XAmzContentSHA256Mismatch` | `400 Bad Request` | Body checksum did not match `X-Amz-Content-Sha256` header. |
| `InvalidPartOrder` | `400 Bad Request` | Part numbers in completion XML are not strictly in ascending order. |
| `EntityTooSmall` | `400 Bad Request` | Part size is smaller than the minimum 5 MB threshold (for non-final parts). |
| `InsufficientStorage` | `507 Insufficient Storage` | Cluster node disk free bytes or free percent violated storage admission threshold. |
| `InternalError` | `500 Internal Server Error` | Unexpected backend disk I/O failure or database error. |
