package protocol

import "fmt"

// ErrorCode is the closed enumeration of application errors carried in
// FrameError frames. The set is intentionally small and stable so SDKs can
// switch on it without parsing free-form text.
type ErrorCode string

const (
	ErrUnauthorized    ErrorCode = "unauthorized"
	ErrForbiddenFilter ErrorCode = "forbidden_filter"
	ErrUnknownChannel  ErrorCode = "unknown_channel"
	ErrUnknownFunction ErrorCode = "unknown_function"
	ErrInvalidFilter   ErrorCode = "invalid_filter"
	ErrInvalidPayload  ErrorCode = "invalid_payload"
	ErrSlotTruncated   ErrorCode = "slot_truncated"
	ErrRateLimited     ErrorCode = "rate_limited"
	ErrQuotaExceeded   ErrorCode = "quota_exceeded"
	ErrInternal        ErrorCode = "internal"
)

// SystemCode is the closed enumeration of system messages carried in
// FrameSystem frames. Unlike ErrorCode these do not necessarily indicate
// failure — they are informational signals about connection state.
type SystemCode string

const (
	SysBehind         SystemCode = "behind"
	SysLSNAdvance     SystemCode = "lsn_advance"
	SysQuota          SystemCode = "quota"
	SysAuthExpired    SystemCode = "auth_expired"
	SysServerShutdown SystemCode = "server_shutdown"
)

// WebSocket close codes specific to rapibase realtime. Values are in the
// private range 4000-4999.
const (
	CloseUnsupportedVersion = 4400
	CloseUnauthorized       = 4401
	CloseHeartbeatTimeout   = 4408
	ClosePayloadTooLarge    = 4413
	CloseSlowConsumer       = 4429
	CloseInternalError      = 4500
	CloseServerShutdown     = 4503
)

// Error is the structured server-side error type returned through the wire.
// It satisfies the standard error interface so it can flow through normal
// Go error handling, while preserving the closed code for clients.
type Error struct {
	Code      ErrorCode
	Message   string
	Retryable bool
	RetryMs   int
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("realtime: %s: %s", e.Code, e.Message)
}

// NewError constructs an Error with the given code and message. Retryable
// defaults to false; use NewRetryableError for retryable variants.
func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// NewRetryableError constructs a retryable Error, optionally with a
// retry-after hint in milliseconds.
func NewRetryableError(code ErrorCode, message string, retryAfterMs int) *Error {
	return &Error{Code: code, Message: message, Retryable: true, RetryMs: retryAfterMs}
}
