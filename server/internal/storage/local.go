package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	CodePathNotAbsolute  = "storage_path_not_absolute"
	CodePathNotFound     = "storage_path_not_found"
	CodePathNotDirectory = "storage_path_not_directory"
	CodePathReparsePoint = "storage_path_reparse_point"
	CodePathOutsideRoot  = "storage_path_outside_root"
	CodeUnreadable       = "storage_unreadable"
	CodeCapacityUnknown  = "storage_capacity_unknown"
)

type PathError struct{ Code string }

func (e *PathError) Error() string { return e.Code }

type Capabilities struct {
	NetworkDrive          bool `json:"network_drive"`
	DirectoryList         bool `json:"directory_list"`
	Watch                 bool `json:"watch"`
	NativeOfflineDownload bool `json:"native_offline_download"`
	TemporaryDirectURL    bool `json:"temporary_direct_url"`
	SignedProxy           bool `json:"signed_proxy"`
	ChangeCursor          bool `json:"change_cursor"`
}

type Probe struct {
	Exists        bool      `json:"exists"`
	Readable      bool      `json:"readable"`
	Available     bool      `json:"available"`
	FreeBytes     *uint64   `json:"free_bytes"`
	TotalBytes    *uint64   `json:"total_bytes"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	ErrorCode     string    `json:"error_code"`
}

type LocalDriver struct{}

func (LocalDriver) Capabilities() Capabilities {
	return Capabilities{DirectoryList: true, Watch: false}
}

// CanonicalizeRoot validates only the configured root. It does not scan child media.
func (LocalDriver) CanonicalizeRoot(input string) (string, error) {
	value := strings.TrimSpace(input)
	if value == "" || !filepath.IsAbs(value) {
		return "", &PathError{Code: CodePathNotAbsolute}
	}
	canonical := filepath.Clean(value)
	info, err := os.Lstat(canonical)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &PathError{Code: CodePathNotFound}
		}
		return "", &PathError{Code: CodeUnreadable}
	}
	if isReparsePoint(canonical, info) {
		return "", &PathError{Code: CodePathReparsePoint}
	}
	if !info.IsDir() {
		return "", &PathError{Code: CodePathNotDirectory}
	}
	return canonical, nil
}

// Constrain resolves a future child operation beneath a canonical root. Every
// later traversal must additionally re-check each directory before file writes.
func Constrain(root, candidate string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", &PathError{Code: CodePathNotAbsolute}
	}
	resolved := candidate
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, candidate)
	}
	resolved = filepath.Clean(resolved)
	relative, err := filepath.Rel(filepath.Clean(root), resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", &PathError{Code: CodePathOutsideRoot}
	}
	return resolved, nil
}

// ProbeRoot performs a bounded, read-only directory-open/read and filesystem
// capacity query. It never creates a sentinel file and never recursively scans.
func (driver LocalDriver) ProbeRoot(root string) Probe {
	checked := time.Now().UTC()
	canonical, err := driver.CanonicalizeRoot(root)
	if err != nil {
		return Probe{LastCheckedAt: checked, ErrorCode: pathErrorCode(err)}
	}
	result := Probe{Exists: true, LastCheckedAt: checked}
	directory, err := os.Open(canonical)
	if err != nil {
		result.ErrorCode = CodeUnreadable
		return result
	}
	_, readErr := directory.Readdirnames(1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
		result.ErrorCode = CodeUnreadable
		return result
	}
	result.Readable = true
	free, total, capacityErr := diskCapacity(canonical)
	if capacityErr != nil {
		result.Available = true
		result.ErrorCode = CodeCapacityUnknown
		return result
	}
	result.Available = true
	result.FreeBytes, result.TotalBytes = &free, &total
	return result
}

func pathErrorCode(err error) string {
	var pathErr *PathError
	if errors.As(err, &pathErr) {
		return pathErr.Code
	}
	return CodeUnreadable
}

func NormalizeForComparison(path string) string {
	cleaned := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}
