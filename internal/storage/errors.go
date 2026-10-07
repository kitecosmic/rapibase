package storage

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is a storage failure the HTTP layer can show to the caller as is:
// it carries the status code and a stable machine-readable code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func newErr(status int, code, format string, args ...any) *Error {
	return &Error{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

var (
	ErrBucketNotFound = newErr(http.StatusNotFound, "bucket_not_found", "bucket not found")
	ErrObjectNotFound = newErr(http.StatusNotFound, "object_not_found", "object not found")
	ErrBucketExists   = newErr(http.StatusConflict, "bucket_exists", "a bucket with that name already exists")
	ErrBucketNotEmpty = newErr(http.StatusConflict, "bucket_not_empty", "the bucket is not empty; delete its files first or pass force=true")
	ErrObjectExists   = newErr(http.StatusConflict, "object_exists", "an object already exists at that path; pass upsert=true to replace it")
	ErrForbidden      = newErr(http.StatusForbidden, "forbidden", "you do not have access to this object")
)

// AsError returns the *Error inside err, if any.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
