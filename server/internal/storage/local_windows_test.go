//go:build windows

package storage

import (
	"errors"
	"testing"
)

func TestWindowsAbsoluteDriveAndUNCPathsReachFilesystemValidation(t *testing.T) {
	driver := LocalDriver{}
	for _, input := range []string{`Z:\ohmycine-path-that-does-not-exist`, `\\ohmycine-invalid-host\media`} {
		_, err := driver.CanonicalizeRoot(input)
		var pathErr *PathError
		if !errors.As(err, &pathErr) {
			t.Fatalf("input=%q error=%v, want PathError", input, err)
		}
		if pathErr.Code == CodePathNotAbsolute {
			t.Fatalf("Windows absolute path %q was rejected as relative", input)
		}
	}
}

func TestWindowsPathComparisonIsCaseInsensitive(t *testing.T) {
	left := NormalizeForComparison(`C:\Media\Movies`)
	right := NormalizeForComparison(`c:\media\movies`)
	if left != right {
		t.Fatalf("comparison paths differ: %q != %q", left, right)
	}
}
