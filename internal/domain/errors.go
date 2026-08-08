package domain

import (
	"errors"
	"net/http"
)

// Error is a coded domain error that maps directly to an RFC 9457 problem+json
// response (§03.9). Adapters/handlers translate Code → HTTP status.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Status returns the HTTP status for this error's code (§03.9 table).
func (e *Error) Status() int {
	switch e.Code {
	case CodeInvalidArgument:
		return http.StatusBadRequest
	case CodeNotFound:
		return http.StatusNotFound
	case CodeAlreadyExists, CodeFailedPrecondition:
		return http.StatusConflict
	case CodePayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeUnprocessable:
		return http.StatusUnprocessableEntity
	case CodeRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

const (
	CodeInvalidArgument    = "invalid_argument"
	CodeNotFound           = "not_found"
	CodeAlreadyExists      = "already_exists"
	CodeFailedPrecondition = "failed_precondition"
	CodePayloadTooLarge    = "payload_too_large"
	CodeUnprocessable      = "unprocessable"
	CodeRateLimited        = "rate_limited"
	CodeInternal           = "internal"
)

func Invalid(msg string) *Error  { return &Error{Code: CodeInvalidArgument, Message: msg} }
func NotFound(msg string) *Error { return &Error{Code: CodeNotFound, Message: msg} }
func Exists(msg string) *Error   { return &Error{Code: CodeAlreadyExists, Message: msg} }
func Precondition(msg string, details map[string]any) *Error {
	return &Error{Code: CodeFailedPrecondition, Message: msg, Details: details}
}
func Unprocessable(msg string) *Error { return &Error{Code: CodeUnprocessable, Message: msg} }

// IsNotFound reports whether err is a not_found from a port. Callers that treat absence as a
// state rather than a failure — an unclassified model (§16.4), an unsealed chain (§19.6.1) —
// need to say so without repeating the type assertion at every site.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeNotFound
}
func Internal(msg string) *Error { return &Error{Code: CodeInternal, Message: msg} }
