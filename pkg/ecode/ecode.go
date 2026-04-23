// Package ecode defines machine-readable error codes returned by all service layers.
// Every API response that indicates failure must carry one of these codes.
package ecode

import "fmt"

// Code is an integer error identifier.
type Code int

const (
	OK Code = 0

	// 1xxx — client-side errors (4xx equivalent)
	ErrBadRequest   Code = 1000
	ErrUnauthorized Code = 1001
	ErrForbidden    Code = 1002
	ErrNotFound     Code = 1003
	ErrConflict     Code = 1004
	ErrValidation   Code = 1005
	ErrRateLimit    Code = 1006

	// 2xxx — judge pipeline errors
	ErrCompileFailed  Code = 2000
	ErrJudgeTimeout   Code = 2001 // judger node did not respond within SLA
	ErrSandboxFailed  Code = 2002 // sandbox backend returned ExecSE
	ErrCheckerFailed  Code = 2003 // checker/interactor binary crashed
	ErrTaskDuplicate  Code = 2004 // idempotency check: task already processed

	// 9xxx — internal infrastructure errors (5xx equivalent)
	ErrInternal     Code = 9000
	ErrDatabase     Code = 9001
	ErrMessageQueue Code = 9002
	ErrStorage      Code = 9003 // shared storage (NFS / MinIO) unreachable
)

// Error is the standard error type used across all internal service functions.
// It serialises directly into the API error response body.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	// Detail carries structured context (e.g., per-field validation failures).
	Detail any `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

func New(code Code, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Is enables errors.Is() matching by Code alone, ignoring Message and Detail.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}
