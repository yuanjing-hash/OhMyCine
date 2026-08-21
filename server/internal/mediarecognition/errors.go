package mediarecognition

import (
	"errors"
	"fmt"
)

// Sentinel configuration errors are exposed for callers that validate profile
// data before creating a processor.
var (
	ErrInvalidPackCodes = errors.New("invalid built-in word pack codes")
	ErrInvalidLimits    = errors.New("invalid word processor limits")
)

// ErrorCode is a stable, non-sensitive reason suitable for recognition logs
// and unrecognized-state persistence.
type ErrorCode string

const (
	ErrorInvalidRule       ErrorCode = "invalid_rule"
	ErrorRegexCompile      ErrorCode = "regex_compile"
	ErrorInputTooLong      ErrorCode = "input_too_long"
	ErrorMatchTimeout      ErrorCode = "match_timeout"
	ErrorApplyLimit        ErrorCode = "apply_limit"
	ErrorInvalidDirectHint ErrorCode = "invalid_direct_hint"
	ErrorContextCanceled   ErrorCode = "context_canceled"
)

// ProcessingError intentionally contains rule location but never the media
// title. This keeps operational errors useful without leaking source paths.
type ProcessingError struct {
	Code     ErrorCode
	PackCode string
	Line     int
	Err      error
}

func (e *ProcessingError) Error() string {
	location := e.PackCode
	if e.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, e.Line)
	}
	if e.Err == nil {
		return fmt.Sprintf("media recognition %s at %s", e.Code, location)
	}
	return fmt.Sprintf("media recognition %s at %s: %v", e.Code, location, e.Err)
}

func (e *ProcessingError) Unwrap() error { return e.Err }
