package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/my-s3-bucket/storage-engine/internal/metadata"
)

type contextKey string

const (
	AuthUserKey contextKey = "auth_user"
)

type Principal struct {
	AccessKeyID string
	UserID      string
	IsAdmin     bool
}

type AuthMiddleware struct {
	verifier       *SigV4Verifier
	meta           metadata.Store
	authEnabled    bool
	adminAccessKey string
	adminSecretKey string
}

func NewAuthMiddleware(meta metadata.Store, authEnabled bool, adminKey, adminSecret string) *AuthMiddleware {
	return &AuthMiddleware{
		verifier:       NewSigV4Verifier(meta),
		meta:           meta,
		authEnabled:    authEnabled,
		adminAccessKey: adminKey,
		adminSecretKey: adminSecret,
	}
}

// CORSMiddleware handles S3 cross-origin requests
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Expose-Headers", "ETag, x-amz-request-id, x-amz-version-id, Content-Length, Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Authenticate wraps an HTTP handler and validates SigV4 credentials.
func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.authEnabled {
			// Anonymous dev mode: inject admin principal
			ctx := context.WithValue(r.Context(), AuthUserKey, &Principal{
				AccessKeyID: m.adminAccessKey,
				UserID:      "admin",
				IsAdmin:     true,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		details, err := ParseAuthorization(r)
		if err != nil {
			writeS3Error(w, http.StatusForbidden, "AccessDenied", "Access Denied: "+err.Error())
			return
		}

		var secretKey string
		var principal Principal

		if details.AccessKeyID == m.adminAccessKey {
			secretKey = m.adminSecretKey
			principal = Principal{
				AccessKeyID: m.adminAccessKey,
				UserID:      "admin",
				IsAdmin:     true,
			}
		} else {
			ak, err := m.meta.GetAccessKey(r.Context(), details.AccessKeyID)
			if err != nil {
				writeS3Error(w, http.StatusForbidden, "InvalidAccessKeyId", "The AWS Access Key Id you provided does not exist in our records.")
				return
			}
			if ak.Status != "ACTIVE" {
				writeS3Error(w, http.StatusForbidden, "AccessDenied", "Access key is inactive or revoked.")
				return
			}
			secretKey = ak.SecretKeyHash
			principal = Principal{
				AccessKeyID: ak.AccessKeyID,
				UserID:      ak.UserID,
				IsAdmin:     false,
			}
		}

		if _, err := m.verifier.VerifyRequest(r, secretKey); err != nil {
			writeS3Error(w, http.StatusForbidden, "SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided.")
			return
		}

		ctx := context.WithValue(r.Context(), AuthUserKey, &principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeS3Error(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(statusCode)
	xmlResp := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>%s</Code>
  <Message>%s</Message>
</Error>`, code, strings.ReplaceAll(message, "&", "&amp;"))
	_, _ = w.Write([]byte(xmlResp))
}

func GetPrincipal(ctx context.Context) *Principal {
	if p, ok := ctx.Value(AuthUserKey).(*Principal); ok {
		return p
	}
	return nil
}

func ContextWithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, AuthUserKey, p)
}
