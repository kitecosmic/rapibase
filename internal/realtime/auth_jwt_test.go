package realtime

import (
	"strconv"
	"testing"
	"time"

	"github.com/rapibase/rapibase/internal/auth"
)

func newJWT(t *testing.T) *auth.JWTManager {
	t.Helper()
	return auth.NewJWTManagerWithExpiry("test-secret-xx", time.Hour)
}

func issueToken(t *testing.T, jwt *auth.JWTManager, userID, role string) string {
	t.Helper()
	tok, err := jwt.GenerateToken(userID, userID+"@example.com", role, auth.AudienceApp)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestJWTAuth_ServiceKey_NoToken(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	role, uid, err := v.Validate("service", "")
	if err != nil {
		t.Fatal(err)
	}
	if role != RoleService || uid != "" {
		t.Fatalf("got role=%q uid=%q", role, uid)
	}
}

func TestJWTAuth_ServiceKey_WithToken_CapturesUser(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	tok := issueToken(t, jwt, "42", "authenticated")
	role, uid, err := v.Validate("service", tok)
	if err != nil {
		t.Fatal(err)
	}
	if role != RoleService {
		t.Fatalf("role should remain service_role, got %q", role)
	}
	if uid != "42" {
		t.Fatalf("expected uid=42, got %q", uid)
	}
}

func TestJWTAuth_ServiceKey_BadToken_StillService(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	role, uid, err := v.Validate("service", "garbage")
	if err != nil {
		t.Fatalf("invalid token alongside service key should not fail: %v", err)
	}
	if role != RoleService || uid != "" {
		t.Fatalf("expected service_role + empty uid; got %q / %q", role, uid)
	}
}

func TestJWTAuth_AnonKey_NoToken_IsAnonymous(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	role, uid, err := v.Validate("anon", "")
	if err != nil {
		t.Fatal(err)
	}
	if role != RoleAnon || uid != "" {
		t.Fatalf("got role=%q uid=%q", role, uid)
	}
}

func TestJWTAuth_AnonKey_ValidToken_UsesClaims(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	tok := issueToken(t, jwt, "7", "user")
	role, uid, err := v.Validate("anon", tok)
	if err != nil {
		t.Fatal(err)
	}
	if role != "user" || uid != "7" {
		t.Fatalf("got role=%q uid=%q", role, uid)
	}
}

func TestJWTAuth_AnonKey_EmptyRoleFallsBackToAuthenticated(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	tok := issueToken(t, jwt, "7", "")
	role, _, err := v.Validate("anon", tok)
	if err != nil {
		t.Fatal(err)
	}
	if role != RoleAuthenticated {
		t.Fatalf("expected fallback %q, got %q", RoleAuthenticated, role)
	}
}

func TestJWTAuth_AnonKey_InvalidToken_Errors(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	if _, _, err := v.Validate("anon", "garbage"); err == nil {
		t.Fatal("invalid token under anon key should error")
	}
}

func TestJWTAuth_UnknownApiKey_Errors(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	if _, _, err := v.Validate("not-an-api-key", ""); err == nil {
		t.Fatal("expected error for unknown api key")
	}
}

func TestJWTAuth_EmptyApiKey_Errors(t *testing.T) {
	v := NewJWTAuthValidator(newJWT(t), "anon", "service")
	if _, _, err := v.Validate("", ""); err == nil {
		t.Fatal("expected error for empty api key")
	}
}

// Sanity: issuing a token, validating it manually and through the
// adapter both yield the same user id. Catches misuse if the
// JWTManager signature changes.
func TestJWTAuth_RoundTripsMultipleTokens(t *testing.T) {
	jwt := newJWT(t)
	v := NewJWTAuthValidator(jwt, "anon", "service")
	for i := 1; i <= 3; i++ {
		uid := strconv.Itoa(i)
		tok := issueToken(t, jwt, uid, "user")
		_, gotUID, err := v.Validate("anon", tok)
		if err != nil {
			t.Fatalf("validate %d: %v", i, err)
		}
		if gotUID != uid {
			t.Fatalf("uid mismatch %s != %s", gotUID, uid)
		}
	}
}

func TestPermissiveChecker(t *testing.T) {
	p := PermissiveChecker{}
	if !p.CanRead("anon", "public", "users", "secret") {
		t.Fatal("permissive should allow")
	}
	in := []string{"a", "b"}
	out := p.ReadableColumns("anon", "public", "users", in)
	if len(out) != 2 || out[0] != "a" || out[1] != "b" {
		t.Fatalf("got %v", out)
	}
	// Mutating output should not affect input.
	out[0] = "z"
	if in[0] != "a" {
		t.Fatal("PermissiveChecker returned shared slice")
	}
}
