package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/my-s3-bucket/storage-engine/internal/metadata"
)

var (
	ErrMissingAuthHeader      = errors.New("missing Authorization header or presigned query params")
	ErrInvalidAuthFormat      = errors.New("invalid Authorization format")
	ErrAccessKeyNotFound      = errors.New("invalid Access Key ID")
	ErrSignatureDoesNotMatch  = errors.New("signature does not match")
	ErrRequestExpired         = errors.New("request has expired")
	ErrUnsupportedAlgorithm   = errors.New("unsupported signature algorithm, only AWS4-HMAC-SHA256 is supported")
)

type SigV4Verifier struct {
	meta metadata.Store
}

func NewSigV4Verifier(meta metadata.Store) *SigV4Verifier {
	return &SigV4Verifier{meta: meta}
}

type SigV4Details struct {
	Algorithm     string
	AccessKeyID   string
	DateStr       string
	Region        string
	Service       string
	SignedHeaders []string
	Signature     string
	IsPresigned   bool
	Expires       int64
	Timestamp     time.Time
}

// ParseAuthorization extracts SigV4 information from headers or query parameters.
func ParseAuthorization(r *http.Request) (*SigV4Details, error) {
	q := r.URL.Query()

	// 1. Check Presigned URL query params
	if q.Get("X-Amz-Algorithm") != "" {
		if q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
			return nil, ErrUnsupportedAlgorithm
		}

		cred := q.Get("X-Amz-Credential")
		parts := strings.Split(cred, "/")
		if len(parts) < 5 {
			return nil, ErrInvalidAuthFormat
		}

		dateStr := q.Get("X-Amz-Date")
		reqTime, err := time.Parse("20060102T150405Z", dateStr)
		if err != nil {
			return nil, fmt.Errorf("invalid X-Amz-Date format: %w", err)
		}

		signedHeaders := strings.Split(strings.ToLower(q.Get("X-Amz-SignedHeaders")), ";")
		var expiresSec int64 = 900 // default 15 min
		if q.Get("X-Amz-Expires") != "" {
			fmt.Sscanf(q.Get("X-Amz-Expires"), "%d", &expiresSec)
		}

		return &SigV4Details{
			Algorithm:     q.Get("X-Amz-Algorithm"),
			AccessKeyID:   parts[0],
			DateStr:       parts[1],
			Region:        parts[2],
			Service:       parts[3],
			SignedHeaders: signedHeaders,
			Signature:     q.Get("X-Amz-Signature"),
			IsPresigned:   true,
			Expires:       expiresSec,
			Timestamp:     reqTime,
		}, nil
	}

	// 2. Check Authorization Header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, ErrMissingAuthHeader
	}

	if !strings.HasPrefix(authHeader, "AWS4-HMAC-SHA256 ") {
		return nil, ErrUnsupportedAlgorithm
	}

	authParams := strings.TrimPrefix(authHeader, "AWS4-HMAC-SHA256 ")
	parts := strings.Split(authParams, ",")
	paramMap := make(map[string]string)
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) == 2 {
			paramMap[kv[0]] = kv[1]
		}
	}

	credParts := strings.Split(paramMap["Credential"], "/")
	if len(credParts) < 5 {
		return nil, ErrInvalidAuthFormat
	}

	signedHeaders := strings.Split(strings.ToLower(paramMap["SignedHeaders"]), ";")

	dateHeader := r.Header.Get("X-Amz-Date")
	if dateHeader == "" {
		dateHeader = r.Header.Get("Date")
	}
	reqTime, err := time.Parse("20060102T150405Z", dateHeader)
	if err != nil {
		reqTime = time.Now().UTC()
	}

	return &SigV4Details{
		Algorithm:     "AWS4-HMAC-SHA256",
		AccessKeyID:   credParts[0],
		DateStr:       credParts[1],
		Region:        credParts[2],
		Service:       credParts[3],
		SignedHeaders: signedHeaders,
		Signature:     paramMap["Signature"],
		IsPresigned:   false,
		Timestamp:     reqTime,
	}, nil
}

// BuildCanonicalRequest constructs the AWS SigV4 canonical request string.
func BuildCanonicalRequest(r *http.Request, details *SigV4Details, payloadHash string) string {
	method := r.Method

	// Canonical URI
	canonicalURI := r.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalURI = uriEncode(canonicalURI, false)

	// Canonical Query String
	var queryKeys []string
	queryValues := r.URL.Query()
	for k := range queryValues {
		if details.IsPresigned && k == "X-Amz-Signature" {
			continue
		}
		queryKeys = append(queryKeys, k)
	}
	sort.Strings(queryKeys)

	var queryPairs []string
	for _, k := range queryKeys {
		vals := queryValues[k]
		sort.Strings(vals)
		for _, v := range vals {
			queryPairs = append(queryPairs, fmt.Sprintf("%s=%s", uriEncode(k, true), uriEncode(v, true)))
		}
	}
	canonicalQuery := strings.Join(queryPairs, "&")

	// Canonical Headers
	var headerLines []string
	for _, h := range details.SignedHeaders {
		var val string
		if h == "host" {
			val = r.Host
		} else {
			val = r.Header.Get(h)
		}
		trimmed := strings.TrimSpace(val)
		// replace multiple spaces with single space
		trimmed = strings.Join(strings.Fields(trimmed), " ")
		headerLines = append(headerLines, fmt.Sprintf("%s:%s\n", strings.ToLower(h), trimmed))
	}
	canonicalHeaders := strings.Join(headerLines, "")

	signedHeadersStr := strings.Join(details.SignedHeaders, ";")

	if payloadHash == "" {
		payloadHash = r.Header.Get("X-Amz-Content-Sha256")
		if payloadHash == "" {
			if details.IsPresigned {
				payloadHash = "UNSIGNED-PAYLOAD"
			} else {
				payloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // empty sha256
			}
		}
	}

	return fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeadersStr,
		payloadHash,
	)
}

// BuildStringToSign constructs the SigV4 string to sign.
func BuildStringToSign(r *http.Request, details *SigV4Details, canonicalReq string) string {
	dateStr := details.Timestamp.Format("20060102T150405Z")
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", details.DateStr, details.Region, details.Service)
	canonicalHash := sha256Hex([]byte(canonicalReq))

	return fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		dateStr,
		credentialScope,
		canonicalHash,
	)
}

// DeriveSigningKey computes the AWS SigV4 signing key.
func DeriveSigningKey(secretKey, dateStr, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStr))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

// VerifyRequest authenticates an incoming HTTP request using SigV4.
func (v *SigV4Verifier) VerifyRequest(r *http.Request, secretKey string) (*SigV4Details, error) {
	details, err := ParseAuthorization(r)
	if err != nil {
		return nil, err
	}

	// Check expiration for presigned URLs
	if details.IsPresigned {
		now := time.Now().UTC()
		expiresAt := details.Timestamp.Add(time.Duration(details.Expires) * time.Second)
		if now.After(expiresAt) {
			return nil, ErrRequestExpired
		}
	}

	// Build Canonical Request and String to Sign
	canonicalReq := BuildCanonicalRequest(r, details, "")
	stringToSign := BuildStringToSign(r, details, canonicalReq)

	// Derive key and calculate expected signature
	signingKey := DeriveSigningKey(secretKey, details.DateStr, details.Region, details.Service)
	expectedSigBytes := hmacSHA256(signingKey, []byte(stringToSign))
	expectedSig := hex.EncodeToString(expectedSigBytes)

	if subtle.ConstantTimeCompare([]byte(details.Signature), []byte(expectedSig)) != 1 {
		return nil, ErrSignatureDoesNotMatch
	}

	return details, nil
}

// Helpers
func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func uriEncode(path string, encodeSlash bool) string {
	var buf bytes.Buffer
	for i := 0; i < len(path); i++ {
		c := path[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '~' || c == '.' {
			buf.WriteByte(c)
		} else if c == '/' && !encodeSlash {
			buf.WriteByte(c)
		} else {
			buf.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return buf.String()
}
