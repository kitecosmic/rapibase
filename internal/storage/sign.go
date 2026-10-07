package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Signed URLs are issued and checked by rapibase itself, not by the S3
// backend: the token is an HMAC over a small JSON grant, keyed with a
// value derived from JWT_SECRET. That keeps the backend private (no S3
// signature ever leaves the server), works with any S3 implementation,
// and makes every URL scoped to exactly one bucket + key + operation.
//
// Rotating JWT_SECRET invalidates every outstanding signed URL.

const (
	OpRead   = "r"
	OpUpload = "w"

	// MaxSignedURLTTL caps download links. Same ceiling as AWS SigV4.
	MaxSignedURLTTL = 7 * 24 * time.Hour
	// SignedUploadTTL is how long a signed upload URL stays valid.
	SignedUploadTTL = 2 * time.Hour
)

var (
	ErrTokenInvalid = errors.New("invalid signed URL token")
	ErrTokenExpired = errors.New("signed URL has expired")
)

// Grant is what a signed URL allows.
type Grant struct {
	Op     string `json:"op"`
	Bucket string `json:"b"`
	Key    string `json:"k"`
	Exp    int64  `json:"e"`
	// Upload grants only: who will own the object, and whether an
	// existing object may be replaced.
	Owner  string `json:"o,omitempty"`
	Upsert bool   `json:"u,omitempty"`
}

type Signer struct{ key []byte }

func NewSigner(jwtSecret string) *Signer {
	m := hmac.New(sha256.New, []byte(jwtSecret))
	m.Write([]byte("rapibase-storage-signed-url-v1"))
	return &Signer{key: m.Sum(nil)}
}

func (s *Signer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

// Sign encodes g as "<payload>.<signature>", both base64url.
func (s *Signer) Sign(g Grant) string {
	raw, _ := json.Marshal(g)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + base64.RawURLEncoding.EncodeToString(s.mac(payload))
}

// Verify checks the signature and expiry and returns the grant.
func (s *Signer) Verify(token string) (Grant, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" {
		return Grant{}, ErrTokenInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(payload)) {
		return Grant{}, ErrTokenInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Grant{}, ErrTokenInvalid
	}
	var g Grant
	if err := json.Unmarshal(raw, &g); err != nil || g.Bucket == "" || g.Key == "" {
		return Grant{}, ErrTokenInvalid
	}
	if time.Now().Unix() >= g.Exp {
		return Grant{}, ErrTokenExpired
	}
	return g, nil
}
