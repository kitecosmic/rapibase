package storage

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FolderPlaceholder is the empty object that keeps an otherwise empty
// "folder" alive in S3 (which has no real directories). It never shows
// up in listings or in storage_objects.
const FolderPlaceholder = ".emptyFolderPlaceholder"

// bucketNameRe follows the S3 rules rapibase needs: lowercase letters,
// digits, dots and hyphens, starting and ending with a letter or digit.
var bucketNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// reservedBucketNames collide with fixed routes under /api/v1/storage.
var reservedBucketNames = map[string]bool{
	"buckets": true, "public": true, "sign": true, "upload": true,
	"owner": true, "object": true, "objects": true, "rules": true,
}

func ValidateBucketName(name string) error {
	if !bucketNameRe.MatchString(name) {
		return newErr(http.StatusBadRequest, "invalid_bucket_name",
			"bucket names must be 3-63 characters: lowercase letters, numbers, dots and hyphens, starting and ending with a letter or number")
	}
	if strings.Contains(name, "..") {
		return newErr(http.StatusBadRequest, "invalid_bucket_name", "bucket names cannot contain two dots in a row")
	}
	if reservedBucketNames[name] {
		return newErr(http.StatusBadRequest, "invalid_bucket_name", "%q is reserved; pick another bucket name", name)
	}
	return nil
}

// ValidateKey checks an object path ("avatars/u1/me.png").
func ValidateKey(key string) error {
	bad := func(why string) error {
		return newErr(http.StatusBadRequest, "invalid_key", "invalid object path: %s", why)
	}
	if key == "" {
		return bad("it is empty")
	}
	if len(key) > 1024 {
		return bad("it is longer than 1024 bytes")
	}
	if !utf8.ValidString(key) {
		return bad("it is not valid UTF-8")
	}
	if strings.HasPrefix(key, "/") {
		return bad("it cannot start with /")
	}
	if strings.HasSuffix(key, "/") {
		return bad("it cannot end with / (that is a folder, not a file)")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return bad("it contains control characters or backslashes")
		}
	}
	for _, seg := range strings.Split(key, "/") {
		switch seg {
		case "":
			return bad("it contains an empty segment (//)")
		case ".", "..":
			return bad("it cannot contain . or .. segments")
		case FolderPlaceholder:
			return bad(FolderPlaceholder + " is reserved")
		}
	}
	return nil
}

// ValidatePrefix checks a folder path; "" (the bucket root) is valid.
func ValidatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return ValidateKey(strings.TrimSuffix(prefix, "/") + "/x")
}

// JoinKey builds an object key from an optional folder prefix and a
// file name, tolerating a missing or doubled slash between them.
func JoinKey(prefix, name string) string {
	prefix = strings.TrimLeft(prefix, "/")
	if prefix == "" {
		return name
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimLeft(name, "/")
}

// hiddenKey reports keys that are bookkeeping, not user files.
func hiddenKey(key string) bool {
	return strings.HasSuffix(key, "/") || path.Base(key) == FolderPlaceholder
}

// EscapeKey percent-encodes each path segment but keeps the slashes.
func EscapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// DetectContentType picks the MIME type to store: the declared one,
// unless it is missing or generic, in which case the file extension
// decides.
func DetectContentType(declared, key string) string {
	ct := strings.TrimSpace(declared)
	base, _, _ := mime.ParseMediaType(ct)
	if ct == "" || base == "" || base == "application/octet-stream" {
		if byExt := mime.TypeByExtension(strings.ToLower(path.Ext(key))); byExt != "" {
			return byExt
		}
		if ct == "" {
			return "application/octet-stream"
		}
	}
	return ct
}

// MimeAllowed matches a content type against a bucket's allow-list.
// Entries may be exact ("image/png") or wildcards ("image/*"); an empty
// list allows everything.
func MimeAllowed(contentType string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	base, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		base = strings.ToLower(strings.TrimSpace(contentType))
	}
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "*" || a == "*/*" || a == base {
			return true
		}
		if strings.HasSuffix(a, "/*") && strings.HasPrefix(base, strings.TrimSuffix(a, "*")) {
			return true
		}
	}
	return false
}

// NormalizeMimeList cleans an allow-list coming from a client.
func NormalizeMimeList(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, m := range in {
		m = strings.ToLower(strings.TrimSpace(m))
		if m == "" || seen[m] {
			continue
		}
		typ, sub, ok := strings.Cut(m, "/")
		if !ok || typ == "" || sub == "" || strings.ContainsAny(m, " ;,") {
			return nil, newErr(http.StatusBadRequest, "invalid_mime_type",
				"%q is not a MIME type; use forms like image/png or image/*", m)
		}
		seen[m] = true
		out = append(out, m)
	}
	return out, nil
}

// ActiveContent reports types a browser would execute (HTML, SVG, XML).
// Those are served under a sandboxing CSP so an uploaded file can never
// run script on the instance's origin.
func ActiveContent(contentType string) bool {
	base, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		base = strings.ToLower(contentType)
	}
	switch base {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "text/xml",
		"application/xml", "text/javascript", "application/javascript",
		"text/xsl", "application/xslt+xml", "application/rdf+xml":
		return true
	}
	return strings.HasSuffix(base, "+xml") && base != "application/atom+xml" && base != "application/rss+xml"
}

// ContentDisposition builds the header value, with an ASCII fallback
// name and the exact UTF-8 name in filename* (RFC 6266 / 5987).
func ContentDisposition(kind, filename string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, filename)
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, kind, ascii, url.PathEscape(filename))
}

// ByteRange is an inclusive byte range already resolved against a size.
type ByteRange struct{ Start, End int64 }

func (r ByteRange) Length() int64 { return r.End - r.Start + 1 }

// ParseRange resolves a single-range "Range: bytes=…" header against the
// object size. ok=false with err=nil means "ignore the header and send
// the whole object" (absent, multi-range or a unit other than bytes, as
// RFC 9110 allows); err != nil means the range is unsatisfiable (416).
func ParseRange(header string, size int64) (rng ByteRange, ok bool, err error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return ByteRange{}, false, nil
	}
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return ByteRange{}, false, nil
	}
	startStr, endStr, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return ByteRange{}, false, nil
	}
	unsatisfiable := fmt.Errorf("range not satisfiable")
	if startStr == "" { // suffix: last N bytes
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n < 0 {
			return ByteRange{}, false, nil
		}
		if n == 0 || size == 0 {
			return ByteRange{}, false, unsatisfiable
		}
		if n > size {
			n = size
		}
		return ByteRange{Start: size - n, End: size - 1}, true, nil
	}
	start, err1 := strconv.ParseInt(startStr, 10, 64)
	if err1 != nil || start < 0 {
		return ByteRange{}, false, nil
	}
	if start >= size {
		return ByteRange{}, false, unsatisfiable
	}
	end := size - 1
	if endStr != "" {
		e, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || e < start {
			return ByteRange{}, false, nil
		}
		if e < end {
			end = e
		}
	}
	return ByteRange{Start: start, End: end}, true, nil
}
