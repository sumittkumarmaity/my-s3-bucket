package s3

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"time"
)

// Standard S3 XML Error
type ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestId string   `xml:"RequestId,omitempty"`
	HostId    string   `xml:"HostId,omitempty"`
}

func SendS3Error(w http.ResponseWriter, r *http.Request, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(statusCode)
	if r != nil && r.Method == http.MethodHead {
		return
	}
	resp := ErrorResponse{
		Code:      code,
		Message:   message,
		Resource:  r.URL.Path,
		RequestId: fmt.Sprintf("%d", time.Now().UnixNano()),
		HostId:    "storage-engine-node",
	}
	out, _ := xml.MarshalIndent(resp, "", "  ")
	_, _ = w.Write([]byte(xml.Header + string(out)))
}

// ListAllMyBucketsResult
type ListAllMyBucketsResult struct {
	XMLName xml.Name      `xml:"ListAllMyBucketsResult"`
	Owner   CanonicalUser `xml:"Owner"`
	Buckets []BucketEntry `xml:"Buckets>Bucket"`
}

type CanonicalUser struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type BucketEntry struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

// ListBucketResult (ListObjectsV2)
type ListBucketResult struct {
	XMLName               xml.Name        `xml:"ListBucketResult"`
	Name                  string          `xml:"Name"`
	Prefix                string          `xml:"Prefix"`
	KeyCount              int             `xml:"KeyCount"`
	MaxKeys               int             `xml:"MaxKeys"`
	Delimiter             string          `xml:"Delimiter,omitempty"`
	IsTruncated           bool            `xml:"IsTruncated"`
	ContinuationToken     string          `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string          `xml:"NextContinuationToken,omitempty"`
	Contents              []ObjectEntry   `xml:"Contents"`
	CommonPrefixes        []PrefixEntry   `xml:"CommonPrefixes,omitempty"`
}

type ObjectEntry struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type PrefixEntry struct {
	Prefix string `xml:"Prefix"`
}

// ListVersionsResult
type ListVersionsResult struct {
	XMLName             xml.Name            `xml:"ListVersionsResult"`
	Name                string              `xml:"Name"`
	Prefix              string              `xml:"Prefix"`
	MaxKeys             int                 `xml:"MaxKeys"`
	IsTruncated         bool                `xml:"IsTruncated"`
	Version             []VersionEntry      `xml:"Version,omitempty"`
	DeleteMarker        []DeleteMarkerEntry `xml:"DeleteMarker,omitempty"`
}

type VersionEntry struct {
	Key          string `xml:"Key"`
	VersionId    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type DeleteMarkerEntry struct {
	Key          string `xml:"Key"`
	VersionId    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
}

// VersioningConfiguration
type VersioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Status  string   `xml:"Status"` // Enabled or Suspended
}

// Multipart XML
type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadId string   `xml:"UploadId"`
}

type CompleteMultipartUploadRequest struct {
	XMLName xml.Name    `xml:"CompleteMultipartUpload"`
	Parts   []PartInput `xml:"Part"`
}

type PartInput struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type ListPartsResult struct {
	XMLName  xml.Name    `xml:"ListPartsResult"`
	Bucket   string      `xml:"Bucket"`
	Key      string      `xml:"Key"`
	UploadId string      `xml:"UploadId"`
	Parts    []PartEntry `xml:"Part"`
}

type PartEntry struct {
	PartNumber   int    `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
}
