package mediatool

import (
	"context"
	"errors"
)

const (
	CodeUnavailable = "media_tool_unavailable"
	CodeFailed      = "media_tool_failed"
)

// Tool performs host-owned media operations. Implementations must never accept
// provider/plugin supplied command-line fragments.
type Tool interface {
	Version(context.Context) (string, error)
	MergeDASH(context.Context, string, string, string) error
}

type Error struct {
	Code  string
	Cause error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.Cause }

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return CodeFailed
}
