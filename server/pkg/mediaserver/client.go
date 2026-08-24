package mediaserver

import (
	"context"
	"errors"
)

const (
	ErrorUnavailable     = "media_server_unavailable"
	ErrorUnauthorized    = "media_server_unauthorized"
	ErrorRateLimited     = "media_server_rate_limited"
	ErrorInvalidResponse = "media_server_invalid_response"
	ErrorLibraryMissing  = "media_server_library_missing"
)

type Error struct {
	Code string
	err  error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.err }

func NewError(code string, err error) error { return &Error{Code: code, err: err} }

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return ErrorUnavailable
}

type ServerInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Library struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
}

type Client interface {
	Probe(context.Context) (ServerInfo, error)
	ListLibraries(context.Context) ([]Library, error)
	RefreshLibrary(context.Context, string) error
}
