package services

import (
	"context"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

func TestMediaLibraryBackendRegistryRejectsUnknownStorageType(t *testing.T) {
	registry := NewMediaLibraryBackendRegistry(localMediaLibraryBackend{})
	if _, err := registry.Get(models.StorageTypePan115); err == nil {
		t.Fatal("expected an unsupported backend error")
	}
}

func TestPan115MediaLibraryBackendUsesLibraryProviderRoot(t *testing.T) {
	driver := &fakeCloudDriver{
		items: map[string]cloudpkg.Item{
			"library-root": {ID: "library-root", ParentID: "storage-root", Name: "电视剧", IsDir: true},
			"episode":      {ID: "episode", ParentID: "library-root", Name: "Example.S01E01.mkv", Size: 1024},
		},
		children: map[string][]cloudpkg.Item{
			"library-root": {{ID: "episode", ParentID: "library-root", Name: "Example.S01E01.mkv", Size: 1024}},
		},
	}
	backend := pan115MediaLibraryBackend{driver: func(connectionID uint) (cloudpkg.Driver, error) {
		if connectionID != 7 {
			t.Fatalf("unexpected connection id %d", connectionID)
		}
		return driver, nil
	}}
	connectionID := uint(7)
	result, err := backend.Scan(context.Background(), MediaLibraryScanRequest{
		Library:         models.MediaLibrary{ProviderRootID: "library-root", Recursive: true},
		Storage:         models.Storage{Type: models.StorageTypePan115, RootPath: "storage-root", ConnectionID: &connectionID},
		VideoExtensions: []string{".mkv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].ProviderID != "episode" {
		t.Fatalf("unexpected provider scan result: %+v", result.Files)
	}
}
