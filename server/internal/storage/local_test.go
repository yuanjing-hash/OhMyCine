package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalizeRootAndProbeAreReadOnly(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "marker.mp4")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	driver := LocalDriver{}
	canonical, err := driver.CanonicalizeRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	probe := driver.ProbeRoot(canonical)
	if !probe.Exists || !probe.Readable || !probe.Available || probe.FreeBytes == nil || probe.TotalBytes == nil || probe.ErrorCode != "" {
		t.Fatalf("unexpected probe: %+v", probe)
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "unchanged" {
		t.Fatalf("probe changed marker: %q %v", content, err)
	}
}

func TestCanonicalizeRootRejectsUnsafeInputs(t *testing.T) {
	driver := LocalDriver{}
	assertCode := func(input, code string) {
		t.Helper()
		_, err := driver.CanonicalizeRoot(input)
		var pathErr *PathError
		if !errors.As(err, &pathErr) || pathErr.Code != code {
			t.Fatalf("input=%q error=%v, want %s", input, err, code)
		}
	}
	assertCode("relative/path", CodePathNotAbsolute)
	assertCode(filepath.Join(t.TempDir(), "missing"), CodePathNotFound)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	assertCode(file, CodePathNotDirectory)
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic-link creation unavailable: %v", err)
	}
	assertCode(link, CodePathReparsePoint)
}

func TestConstrainRejectsBoundaryEscape(t *testing.T) {
	root := t.TempDir()
	if resolved, err := Constrain(root, "."); err != nil || resolved != filepath.Clean(root) {
		t.Fatalf("root resolution=%q error=%v", resolved, err)
	}
	if _, err := Constrain(root, filepath.Join("nested", "file.mkv")); err != nil {
		t.Fatal(err)
	}
	_, err := Constrain(root, filepath.Join("..", "outside.mkv"))
	var pathErr *PathError
	if !errors.As(err, &pathErr) || pathErr.Code != CodePathOutsideRoot {
		t.Fatalf("unexpected error: %v", err)
	}
	sibling := filepath.Clean(root) + "-sibling"
	_, err = Constrain(root, sibling)
	if !errors.As(err, &pathErr) || pathErr.Code != CodePathOutsideRoot {
		t.Fatalf("sibling boundary error: %v", err)
	}
}

func TestLocalCapabilitiesAdvertiseDirectoryAndWatchOnly(t *testing.T) {
	capabilities := (LocalDriver{}).Capabilities()
	if !capabilities.DirectoryList || !capabilities.Watch || capabilities.NetworkDrive || capabilities.NativeOfflineDownload || capabilities.TemporaryDirectURL || capabilities.SignedProxy || capabilities.ChangeCursor {
		t.Fatalf("unexpected capabilities: %+v", capabilities)
	}
}
