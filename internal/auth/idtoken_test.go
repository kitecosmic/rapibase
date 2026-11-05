package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testVerifier(t *testing.T) (*IDTokenVerifier, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	v := NewIDTokenVerifier()
	v.SetKeys(AppleIssuer.JWKSURL, map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	v.SetKeys(GoogleIssuer.JWKSURL, map[string]*rsa.PublicKey{"k1": &key.PublicKey})
	return v, key
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func appleClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss": "https://appleid.apple.com", "aud": "com.acme.app", "sub": "001234.abcd",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"email": "Ana@PrivateRelay.AppleID.com", "email_verified": "true",
	}
}

func TestVerifyAppleToken(t *testing.T) {
	v, key := testVerifier(t)
	ctx := context.Background()
	id, err := v.Verify(ctx, AppleIssuer, sign(t, key, "k1", appleClaims()), []string{"com.acme.app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "001234.abcd" || id.Email != "ana@privaterelay.appleid.com" || !id.EmailVerified {
		t.Errorf("identity: %+v", id)
	}
}

func TestVerifyRejects(t *testing.T) {
	v, key := testVerifier(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	ctx := context.Background()
	mod := func(k string, val interface{}) jwt.MapClaims {
		c := appleClaims()
		if val == nil {
			delete(c, k)
		} else {
			c[k] = val
		}
		return c
	}
	cases := map[string]string{
		"wrong audience": sign(t, key, "k1", mod("aud", "com.evil.app")),
		"wrong issuer":   sign(t, key, "k1", mod("iss", "https://accounts.google.com")),
		"expired":        sign(t, key, "k1", mod("exp", time.Now().Add(-time.Hour).Unix())),
		"no exp":         sign(t, key, "k1", mod("exp", nil)),
		"no sub":         sign(t, key, "k1", mod("sub", nil)),
		"foreign key":    sign(t, other, "k1", appleClaims()),
		"garbage":        "not.a.jwt",
	}
	for name, tok := range cases {
		if _, err := v.Verify(ctx, AppleIssuer, tok, []string{"com.acme.app"}, ""); !errors.Is(err, ErrIDToken) {
			t.Errorf("%s: want ErrIDToken, got %v", name, err)
		}
	}
	// HS256 signed with the public modulus as secret (alg confusion) must fail.
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, appleClaims())
	hs.Header["kid"] = "k1"
	hsTok, _ := hs.SignedString(key.PublicKey.N.Bytes())
	if _, err := v.Verify(ctx, AppleIssuer, hsTok, []string{"com.acme.app"}, ""); err == nil {
		t.Error("HS256 token accepted")
	}
}

func TestVerifyNonce(t *testing.T) {
	v, key := testVerifier(t)
	ctx := context.Background()
	raw := "n-0nce-raw"
	sum := sha256.Sum256([]byte(raw))
	aud := []string{"com.acme.app"}

	hashed := appleClaims()
	hashed["nonce"] = hex.EncodeToString(sum[:]) // Apple: SHA-256 of the raw nonce
	plain := appleClaims()
	plain["nonce"] = raw // Google: the raw value

	if _, err := v.Verify(ctx, AppleIssuer, sign(t, key, "k1", hashed), aud, raw); err != nil {
		t.Errorf("hashed nonce: %v", err)
	}
	if _, err := v.Verify(ctx, AppleIssuer, sign(t, key, "k1", plain), aud, raw); err != nil {
		t.Errorf("raw nonce: %v", err)
	}
	if _, err := v.Verify(ctx, AppleIssuer, sign(t, key, "k1", hashed), aud, "other"); err == nil {
		t.Error("wrong nonce accepted")
	}
	if _, err := v.Verify(ctx, AppleIssuer, sign(t, key, "k1", hashed), aud, ""); err == nil {
		t.Error("token with nonce accepted without one")
	}
	if _, err := v.Verify(ctx, AppleIssuer, sign(t, key, "k1", appleClaims()), aud, raw); err == nil {
		t.Error("nonce sent for a token without one accepted")
	}
}

func TestVerifyGoogleAudienceList(t *testing.T) {
	v, key := testVerifier(t)
	c := jwt.MapClaims{
		"iss": "accounts.google.com", "aud": "web-client.apps.googleusercontent.com", "sub": "1098",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"email": "ana@gmail.com", "email_verified": true, "name": "Ana",
	}
	id, err := v.Verify(context.Background(), GoogleIssuer, sign(t, key, "k1", c),
		[]string{"android.apps.googleusercontent.com", "web-client.apps.googleusercontent.com"}, "")
	if err != nil || !id.EmailVerified || id.Name != "Ana" {
		t.Fatalf("google: %+v %v", id, err)
	}
}

func TestParseJWKS(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
	doc := fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"a","n":%q,"e":"AQAB","alg":"RS256"},{"kty":"EC","kid":"b"}]}`, n)
	keys, err := ParseJWKS([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys["a"].E != 65537 || keys["a"].N.Cmp(key.PublicKey.N) != 0 {
		t.Errorf("parsed: %+v", keys)
	}
	if _, err := ParseJWKS([]byte(`{"keys":[]}`)); err == nil || !strings.Contains(err.Error(), "no RSA") {
		t.Errorf("empty set: %v", err)
	}
}
