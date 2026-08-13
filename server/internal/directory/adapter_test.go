package directory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeAdapterListsOnlyDirectoriesAndTruncates(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < 4; index++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("folder-%d", index)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "movie.mkv"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	items, truncated, err := (NativeAdapter{}).Directories(context.Background(), root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !truncated {
		t.Fatalf("items=%d truncated=%v", len(items), truncated)
	}
	for _, item := range items {
		if item.Name == "movie.mkv" {
			t.Fatal("regular file leaked into directory listing")
		}
	}
}

func TestNativeAdapterMarksSymbolicLinkUnavailable(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	items, _, err := (NativeAdapter{}).Directories(context.Background(), root, DefaultResultLimit)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Name == "link" && (item.Enterable || item.Selectable || item.Reason != "link_not_allowed") {
			t.Fatalf("unsafe link was not disabled: %+v", item)
		}
		if item.Name == "link" {
			found = true
		}
	}
	if !found {
		t.Fatal("symbolic link was not returned with an unavailable reason")
	}
}

func TestNativeAdapterRejectsSymlinkInCurrentPathAncestry(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Mkdir(filepath.Join(target, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	path := filepath.Join(link, "nested")
	if err := (NativeAdapter{}).Validate(context.Background(), path); err == nil {
		t.Fatal("path beneath a symbolic-link ancestor was accepted")
	}
	if _, _, err := (NativeAdapter{}).Directories(context.Background(), path, DefaultResultLimit); err == nil {
		t.Fatal("directory beneath a symbolic-link ancestor was browsed")
	}
}
