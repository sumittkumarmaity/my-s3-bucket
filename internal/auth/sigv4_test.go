package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSigV4Verification(t *testing.T) {
	accessKey := "TESTACCESSKEY"
	secretKey := "TESTSECRETKEY123456789"
	region := "us-east-1"
	service := "s3"

	now := time.Now().UTC()
	dateStr := now.Format("20060102")
	dateTimeStr := now.Format("20060102T150405Z")

	// Construct request
	req := httptest.NewRequest(http.MethodGet, "http://localhost:9000/my-bucket/photo.jpg", nil)
	req.Header.Set("Host", "localhost:9000")
	req.Header.Set("X-Amz-Date", dateTimeStr)
	req.Header.Set("X-Amz-Content-Sha256", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"

	details := &SigV4Details{
		Algorithm:     "AWS4-HMAC-SHA256",
		AccessKeyID:   accessKey,
		DateStr:       dateStr,
		Region:        region,
		Service:       service,
		SignedHeaders: strings.Split(signedHeaders, ";"),
		Timestamp:     now,
	}

	canonicalReq := BuildCanonicalRequest(req, details, req.Header.Get("X-Amz-Content-Sha256"))
	stringToSign := BuildStringToSign(req, details, canonicalReq)
	signingKey := DeriveSigningKey(secretKey, dateStr, region, service)

	h := hmac.New(sha256.New, signingKey)
	h.Write([]byte(stringToSign))
	signature := hex.EncodeToString(h.Sum(nil))

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s/%s/%s/aws4_request, SignedHeaders=%s, Signature=%s",
		accessKey, dateStr, region, service, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)

	verifier := NewSigV4Verifier(nil)
	verified, err := verifier.VerifyRequest(req, secretKey)
	if err != nil {
		t.Fatalf("verification failed: %v", err)
	}
	if verified.AccessKeyID != accessKey {
		t.Fatalf("expected access key %s, got %s", accessKey, verified.AccessKeyID)
	}

	// Test with wrong secret key
	_, err = verifier.VerifyRequest(req, "WRONGSECRETKEY")
	if err != ErrSignatureDoesNotMatch {
		t.Fatalf("expected ErrSignatureDoesNotMatch, got %v", err)
	}
}
