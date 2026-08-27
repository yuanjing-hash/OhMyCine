package directory

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	AdapterVersion     = "native-v1"
	DefaultResultLimit = 500
	maxScannedEntries  = 5000
)

type Root struct {
	Path       string
	Name       string
	Kind       string
	Selectable bool
	Enterable  bool
	Reason     string
}

type Entry struct {
	Path       string
	Name       string
	Selectable bool
	Enterable  bool
	Reason     string
}

type Adapter interface {
	Platform() string
	Version() string
	Roots(context.Context) ([]Root, error)
	Directories(context.Context, string, int) ([]Entry, bool, error)
	Validate(context.Context, string) error
}

type NativeAdapter struct{}

func (NativeAdapter) Platform() string { return runtime.GOOS }
func (NativeAdapter) Version() string  { return AdapterVersion }
func (NativeAdapter) Validate(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return validateDirectoryPath(path)
}
func (NativeAdapter) Roots(ctx context.Context) ([]Root, error) {
	return nativeRoots(ctx)
}

func (NativeAdapter) Directories(ctx context.Context, current string, limit int) ([]Entry, bool, error) {
	if limit <= 0 || limit > DefaultResultLimit {
		limit = DefaultResultLimit
	}
	if err := validateDirectoryPath(current); err != nil {
		return nil, false, err
	}
	directory, err := os.Open(current)
	if err != nil {
		return nil, false, classify(err)
	}
	defer func() { _ = directory.Close() }()

	items := make([]Entry, 0, limit)
	truncated := false
	scanned := 0
	for scanned < maxScannedEntries && len(items) <= limit {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		batch, readErr := directory.ReadDir(64)
		for _, candidate := range batch {
			scanned++
			if scanned > maxScannedEntries {
				truncated = true
				break
			}
			path := filepath.Join(current, candidate.Name())
			info, statErr := os.Lstat(path)
			if statErr != nil {
				continue
			}
			if isUnsafeLink(path, info) {
				items = append(items, Entry{Path: path, Name: candidate.Name(), Reason: "link_not_allowed"})
				continue
			}
			if !info.IsDir() {
				continue
			}
			item := Entry{Path: path, Name: candidate.Name(), Selectable: true, Enterable: true}
			items = append(items, item)
			if len(items) > limit {
				truncated = true
				break
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return nil, false, classify(readErr)
			}
			break
		}
	}
	if scanned >= maxScannedEntries {
		truncated = true
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i].Name, items[j].Name
		if runtime.GOOS == "windows" {
			left, right = strings.ToLower(left), strings.ToLower(right)
		}
		if left == right {
			return items[i].Name < items[j].Name
		}
		return left < right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, truncated, nil
}

// validateDirectoryPath rejects a path when any component has become a
// symlink, junction, mount-point Reparse Point, or other unsafe link since its
// navigation token was issued. This check is intentionally repeated at use
// time because directory trees can change between requests.
func validateDirectoryPath(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return &AdapterError{Kind: ErrorUnavailable}
	}
	volume := filepath.VolumeName(clean)
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	if err := validateDirectoryComponent(current); err != nil {
		return err
	}
	rest := strings.TrimLeft(strings.TrimPrefix(clean, volume), `/\`)
	for _, part := range strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' }) {
		current = filepath.Join(current, part)
		if err := validateDirectoryComponent(current); err != nil {
			return err
		}
	}
	return nil
}

func validateDirectoryComponent(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return classify(err)
	}
	if !info.IsDir() || isUnsafeLink(path, info) {
		return &AdapterError{Kind: ErrorUnavailable}
	}
	return nil
}

type ErrorKind string

const (
	ErrorNotFound    ErrorKind = "not_found"
	ErrorUnreadable  ErrorKind = "unreadable"
	ErrorUnavailable ErrorKind = "unavailable"
)

type AdapterError struct{ Kind ErrorKind }

func (e *AdapterError) Error() string { return string(e.Kind) }

func classify(err error) error {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return &AdapterError{Kind: ErrorNotFound}
	case errors.Is(err, os.ErrPermission):
		return &AdapterError{Kind: ErrorUnreadable}
	default:
		return &AdapterError{Kind: ErrorUnavailable}
	}
}

func decodeMountPath(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}
