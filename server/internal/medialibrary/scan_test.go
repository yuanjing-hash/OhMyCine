package medialibrary

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanLocalFindsMixedMediaWithoutWriting(t *testing.T) {
	root := t.TempDir()
	files := []string{"Movie.2024.mp4", filepath.Join("Show", "Season 01", "Show.S01E01.mkv"), "ignore.txt"}
	for _, name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.Stat(filepath.Join(root, files[0]))
	result, err := ScanLocal(context.Background(), root, "/", true, []string{".mp4", ".mkv"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("files=%+v", result.Files)
	}
	if result.Files[0].MediaType != "movie" || result.Files[1].MediaType != "tv" || result.Files[1].Season == nil || *result.Files[1].Season != 1 {
		t.Fatalf("parse=%+v", result.Files)
	}
	after, _ := os.Stat(filepath.Join(root, files[0]))
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Fatal("scan modified source")
	}
}

func TestNormalizeRelativeRootRejectsTraversal(t *testing.T) {
	if got, err := NormalizeRelativeRoot("shows/season 1"); err != nil || got != "/shows/season 1" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := NormalizeRelativeRoot("../../outside"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}
