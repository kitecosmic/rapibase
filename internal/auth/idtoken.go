// Verificación de ID tokens OpenID Connect de Google y Apple: lo que
// entregan los SDK nativos (google_sign_in, Sign in with Apple, Apple JS
// con popup) para que el backend abra la sesión sin redirect de navegador.
//
// Se valida la firma RS256 contra las claves públicas (JWKS) del emisor,
// el iss, el aud (uno de los client IDs configurados), exp/iat y, si la
// app lo usó, el nonce (raw o su SHA-256 en hex, como hace Apple).
package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// IDTokenIssuer describes a provider that signs OIDC ID tokens.
type IDTokenIssuer struct {
	Name    string
	JWKSURL string
	Issuers []string
}

var (
	GoogleIssuer = IDTokenIssuer{
		Name:    "google",
		JWKSURL: "https://www.googleapis.com/oauth2/v3/certs",
		Issuers: []string{"https://accounts.google.com", "accounts.google.com"},
	}
	AppleIssuer = IDTokenIssuer{
		Name:    "apple",
		JWKSURL: "https://appleid.apple.com/auth/keys",
		Issuers: []string{"https://appleid.apple.com"},
	}
)

// IDTokenIdentity is what the app needs from a verified ID token.
type IDTokenIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
}

// ErrIDToken wraps every validation failure (safe to show to the client).
var ErrIDToken = errors.New("invalid id_token")

// IDTokenVerifier caches each issuer's JWKS.
type IDTokenVerifier struct {
	client *http.Client
	mu     sync.Mutex
	keys   map[string]map[string]*rsa.PublicKey // jwks url -> kid -> key
	expiry map[string]time.Time
	tried  map[string]time.Time // last forced refresh (unknown kid)
	now    func() time.Time
}

func NewIDTokenVerifier() *IDTokenVerifier {
	return &IDTokenVerifier{
		client: &http.Client{Timeout: 10 * time.Second},
		keys:   map[string]map[string]*rsa.PublicKey{},
		expiry: map[string]time.Time{},
		tried:  map[string]time.Time{},
		now:    time.Now,
	}
}

// SetKeys preloads an issuer's keys (tests).
func (v *IDTokenVerifier) SetKeys(jwksURL string, keys map[string]*rsa.PublicKey) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.keys[jwksURL] = keys
	v.expiry[jwksURL] = v.now().Add(24 * time.Hour)
}

type idTokenClaims struct {
	jwt.RegisteredClaims
	Email         string      `json:"email"`
	EmailVerified interface{} `json:"email_verified"` // Google: bool, Apple: "true"/"false" or bool
	Name          string      `json:"name"`
	Picture       string      `json:"picture"`
	Nonce         string      `json:"nonce"`
}

// Verify checks raw against iss and the allowed audiences. nonce "" means
// the app did not use one; if the token carries one, it must be sent.
func (v *IDTokenVerifier) Verify(ctx context.Context, iss IDTokenIssuer, raw string, audiences []string, nonce string) (*IDTokenIdentity, error) {
	if len(audiences) == 0 {
		return nil, fmt.Errorf("%w: provider has no client IDs configured", ErrIDToken)
	}
	claims := &idTokenClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithLeeway(2*time.Minute),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(v.now),
	)
	_, err := parser.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		return v.key(ctx, iss.JWKSURL, kid)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIDToken, err)
	}

	okIss := false
	for _, i := range iss.Issuers {
		if claims.Issuer == i {
			okIss = true
		}
	}
	if !okIss {
		return nil, fmt.Errorf("%w: unexpected issuer %q", ErrIDToken, claims.Issuer)
	}
	okAud := false
	for _, a := range claims.Audience {
		for _, want := range audiences {
			if a == want {
				okAud = true
			}
		}
	}
	if !okAud {
		return nil, fmt.Errorf("%w: audience %v is not one of this project's client IDs", ErrIDToken, []string(claims.Audience))
	}
	if claims.Subject == "" {
		return nil, fmt.Errorf("%w: missing sub", ErrIDToken)
	}
	switch {
	case claims.Nonce == "" && nonce != "":
		return nil, fmt.Errorf("%w: nonce sent but the token has none", ErrIDToken)
	case claims.Nonce != "" && nonce == "":
		return nil, fmt.Errorf("%w: the token has a nonce: send the raw nonce too", ErrIDToken)
	case claims.Nonce != "":
		sum := sha256.Sum256([]byte(nonce))
		if claims.Nonce != nonce && claims.Nonce != hex.EncodeToString(sum[:]) {
			return nil, fmt.Errorf("%w: nonce mismatch", ErrIDToken)
		}
	}

	verified := false
	switch ev := claims.EmailVerified.(type) {
	case bool:
		verified = ev
	case string:
		verified = ev == "true"
	}
	return &IDTokenIdentity{
		Subject:       claims.Subject,
		Email:         strings.ToLower(strings.TrimSpace(claims.Email)),
		EmailVerified: verified,
		Name:          claims.Name,
		Picture:       claims.Picture,
	}, nil
}

func (v *IDTokenVerifier) key(ctx context.Context, jwksURL, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	keys, fresh := v.keys[jwksURL], v.now().Before(v.expiry[jwksURL])
	if k, ok := keys[kid]; ok && fresh {
		v.mu.Unlock()
		return k, nil
	}
	// Unknown kid on a fresh set = provider rotated keys; refetch at most
	// once a minute so a stream of bogus tokens can't hammer the JWKS.
	if fresh && v.now().Sub(v.tried[jwksURL]) < time.Minute {
		v.mu.Unlock()
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	v.tried[jwksURL] = v.now()
	v.mu.Unlock()

	fetched, ttl, err := v.fetch(ctx, jwksURL)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.keys[jwksURL] = fetched
	v.expiry[jwksURL] = v.now().Add(ttl)
	v.mu.Unlock()
	if k, ok := fetched[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func (v *IDTokenVerifier) fetch(ctx context.Context, jwksURL string) (map[string]*rsa.PublicKey, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", jwksURL, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetching provider keys: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("fetching provider keys: HTTP %d", resp.StatusCode)
	}
	keys, err := ParseJWKS(body)
	if err != nil {
		return nil, 0, err
	}
	return keys, time.Hour, nil
}

// ParseJWKS reads the RSA keys of a JWKS document.
func ParseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parsing provider keys: %w", err)
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp<<8 | int(b)
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("provider keys: no RSA keys")
	}
	return out, nil
}
