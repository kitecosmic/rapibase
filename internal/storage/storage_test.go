package storage

import (
	"strings"
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	cases := []struct {
		header  string
		size    int64
		want    ByteRange
		partial bool
		err     bool
	}{
		{"", 100, ByteRange{}, false, false},
		{"bytes=0-9", 100, ByteRange{0, 9}, true, false},
		{"bytes=0-0", 100, ByteRange{0, 0}, true, false},
		{"bytes=90-", 100, ByteRange{90, 99}, true, false},
		{"bytes=90-500", 100, ByteRange{90, 99}, true, false},
		{"bytes=-10", 100, ByteRange{90, 99}, true, false},
		{"bytes=-500", 100, ByteRange{0, 99}, true, false},
		{"bytes=100-", 100, ByteRange{}, false, true},
		{"bytes=-0", 100, ByteRange{}, false, true},
		{"bytes=0-1,5-6", 100, ByteRange{}, false, false}, // multi-range: send all
		{"items=0-1", 100, ByteRange{}, false, false},
		{"bytes=9-3", 100, ByteRange{}, false, false},
		{"bytes=abc", 100, ByteRange{}, false, false},
	}
	for _, c := range cases {
		got, partial, err := ParseRange(c.header, c.size)
		if (err != nil) != c.err || partial != c.partial || got != c.want {
			t.Errorf("ParseRange(%q, %d) = %v, %v, %v; want %v, %v, err=%v", c.header, c.size, got, partial, err, c.want, c.partial, c.err)
		}
	}
}

func TestValidateKey(t *testing.T) {
	ok := []string{"a.png", "avatars/u1/me.png", "docs/año 2024/informe final.pdf", "a/b/c/d.txt"}
	for _, k := range ok {
		if err := ValidateKey(k); err != nil {
			t.Errorf("ValidateKey(%q) = %v, want ok", k, err)
		}
	}
	bad := []string{"", "/a.png", "a/", "a//b", "../etc/passwd", "a/../b", "./a", "a\\b", "a\x00b", "x/" + FolderPlaceholder, strings.Repeat("a", 1025)}
	for _, k := range bad {
		if err := ValidateKey(k); err == nil {
			t.Errorf("ValidateKey(%q) = nil, want error", k)
		}
	}
}

func TestValidateBucketName(t *testing.T) {
	for _, n := range []string{"avatars", "my-bucket", "a.b.c", "abc"} {
		if err := ValidateBucketName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"ab", "Avatars", "-abc", "abc-", "a..b", "buckets", "public", "sign", "owner", "my_bucket"} {
		if err := ValidateBucketName(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestMimeAllowed(t *testing.T) {
	allowed := []string{"image/*", "application/pdf"}
	for ct, want := range map[string]bool{
		"image/png":                true,
		"image/svg+xml":            true,
		"application/pdf":          true,
		"text/html; charset=utf-8": false,
		"application/zip":          false,
	} {
		if got := MimeAllowed(ct, allowed); got != want {
			t.Errorf("MimeAllowed(%q) = %v, want %v", ct, got, want)
		}
	}
	if !MimeAllowed("anything/at-all", nil) {
		t.Error("empty allow-list must allow everything")
	}
	if _, err := NormalizeMimeList([]string{"image/png", "nope"}); err == nil {
		t.Error("NormalizeMimeList accepted an invalid type")
	}
}

func TestDetectContentType(t *testing.T) {
	if got := DetectContentType("", "a/b.png"); got != "image/png" {
		t.Errorf("by extension: %q", got)
	}
	if got := DetectContentType("application/octet-stream", "doc.pdf"); got != "application/pdf" {
		t.Errorf("generic declared type should fall back to the extension: %q", got)
	}
	if got := DetectContentType("text/plain", "doc.pdf"); got != "text/plain" {
		t.Errorf("declared type wins: %q", got)
	}
	if got := DetectContentType("", "noext"); got != "application/octet-stream" {
		t.Errorf("unknown: %q", got)
	}
}

func TestActiveContent(t *testing.T) {
	for ct, want := range map[string]bool{
		"text/html":                true,
		"text/html; charset=utf-8": true,
		"image/svg+xml":            true,
		"application/xml":          true,
		"image/png":                false,
		"application/pdf":          false,
		"video/mp4":                false,
		"application/json":         false,
	} {
		if got := ActiveContent(ct); got != want {
			t.Errorf("ActiveContent(%q) = %v, want %v", ct, got, want)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	got := ContentDisposition("attachment", `año "final".pdf`)
	want := `attachment; filename="a_o _final_.pdf"; filename*=UTF-8''a%C3%B1o%20%22final%22.pdf`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestEscapeAndJoinKey(t *testing.T) {
	if got := EscapeKey("docs/año 2024/a#b?.pdf"); got != "docs/a%C3%B1o%202024/a%23b%3F.pdf" {
		t.Errorf("EscapeKey: %s", got)
	}
	for _, c := range [][3]string{{"", "a.png", "a.png"}, {"x", "a.png", "x/a.png"}, {"x/", "a.png", "x/a.png"}, {"/x/", "/a.png", "x/a.png"}} {
		if got := JoinKey(c[0], c[1]); got != c[2] {
			t.Errorf("JoinKey(%q, %q) = %q", c[0], c[1], got)
		}
	}
}

func TestSigner(t *testing.T) {
	s := NewSigner("secret-one")
	g := Grant{Op: OpRead, Bucket: "b", Key: "k/x.png", Exp: time.Now().Add(time.Minute).Unix()}
	tok := s.Sign(g)
	got, err := s.Verify(tok)
	if err != nil || got != g {
		t.Fatalf("Verify = %+v, %v", got, err)
	}
	if _, err := NewSigner("secret-two").Verify(tok); err != ErrTokenInvalid {
		t.Errorf("other secret: %v", err)
	}
	payload, sig, _ := strings.Cut(tok, ".")
	forged := NewSigner("x").Sign(Grant{Op: OpRead, Bucket: "b", Key: "other", Exp: g.Exp})
	fp, _, _ := strings.Cut(forged, ".")
	if _, err := s.Verify(fp + "." + sig); err != ErrTokenInvalid {
		t.Errorf("swapped payload: %v", err)
	}
	if _, err := s.Verify(payload); err != ErrTokenInvalid {
		t.Errorf("no signature: %v", err)
	}
	old := s.Sign(Grant{Op: OpRead, Bucket: "b", Key: "k", Exp: time.Now().Add(-time.Second).Unix()})
	if _, err := s.Verify(old); err != ErrTokenExpired {
		t.Errorf("expired: %v", err)
	}
}

func TestClampTTL(t *testing.T) {
	if ClampTTL(0) != time.Hour || ClampTTL(-5) != time.Hour {
		t.Error("default TTL should be one hour")
	}
	if ClampTTL(30*24*time.Hour) != MaxSignedURLTTL {
		t.Error("TTL must be capped")
	}
}
