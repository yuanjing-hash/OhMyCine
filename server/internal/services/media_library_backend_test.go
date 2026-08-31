package services

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

type trackingMediaLibraryBackend struct {
	active *atomic.Int32
	max    *atomic.Int32
}

func (trackingMediaLibraryBackend) StorageType() string { return models.StorageTypeLocal }
func (trackingMediaLibraryBackend) Scan(context.Context, MediaLibraryScanRequest) (medialibrary.Result, error) {
	return medialibrary.Result{}, nil
}
func (b trackingMediaLibraryBackend) OpenListener(context.Context, models.MediaLibrary, models.Storage, <-chan struct{}) (MediaLibraryListener, error) {
	return &trackingMediaLibraryListener{active: b.active, max: b.max}, nil
}

type trackingMediaLibraryListener struct {
	active *atomic.Int32
	max    *atomic.Int32
}

func (*trackingMediaLibraryListener) Close() error { return nil }
func (l *trackingMediaLibraryListener) Run(ctx context.Context, _ func(context.Context, string) error) error {
	current := l.active.Add(1)
	defer l.active.Add(-1)
	for {
		maximum := l.max.Load()
		if current <= maximum || l.max.CompareAndSwap(maximum, current) {
			break
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

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

func TestProviderMediaLibraryListenerStopsWhenWakeChannelCloses(t *testing.T) {
	wake := make(chan struct{})
	close(wake)
	listener := &providerMediaLibraryListener{wake: wake, incremental: time.Hour, full: time.Hour}
	done := make(chan error, 1)
	var reconciled atomic.Bool
	go func() {
		done <- listener.Run(context.Background(), func(context.Context, string) error {
			reconciled.Store(true)
			return nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener busy-looped on a closed wake channel")
	}
	if reconciled.Load() {
		t.Fatal("closed wake channel triggered reconciliation")
	}
}

func TestMediaLibraryConcurrentSupervisorReplacementLeavesOneListener(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	created, err := service.Create(context.Background(), actor, testLibraryInput("Supervisor replacement", storage, profile, false), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaLibrary{}).Where("id = ?", created.ID).Updates(map[string]any{"enabled": true, "baseline_generation": 1, "dirty_generation": 1}).Error; err != nil {
		t.Fatal(err)
	}
	var active, maximum atomic.Int32
	service.backends = NewMediaLibraryBackendRegistry(trackingMediaLibraryBackend{active: &active, max: &maximum})
	var starts sync.WaitGroup
	for index := 0; index < 12; index++ {
		starts.Add(1)
		go func() {
			defer starts.Done()
			service.startSupervisor(context.Background(), created.ID)
		}()
	}
	starts.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && active.Load() != 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if active.Load() != 1 || maximum.Load() != 1 {
		t.Fatalf("active=%d maximum=%d", active.Load(), maximum.Load())
	}
	service.stopSupervisor(created.ID)
	if active.Load() != 0 {
		t.Fatalf("listener remained active after stop: %d", active.Load())
	}
}

func TestMediaLibraryConcurrentStopAndStartDoNotOverlapListeners(t *testing.T) {
	service, db, actor, storage, profile := mediaLibraryTestService(t)
	created, err := service.Create(context.Background(), actor, testLibraryInput("Supervisor stop-start", storage, profile, false), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.MediaLibrary{}).Where("id = ?", created.ID).Updates(map[string]any{"enabled": true, "baseline_generation": 1, "dirty_generation": 1}).Error; err != nil {
		t.Fatal(err)
	}
	var active, maximum atomic.Int32
	service.backends = NewMediaLibraryBackendRegistry(trackingMediaLibraryBackend{active: &active, max: &maximum})
	service.startSupervisor(context.Background(), created.ID)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && active.Load() != 1 {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 1 {
		t.Fatal("initial listener did not start")
	}
	stopped := make(chan struct{})
	go func() {
		service.stopSupervisor(created.ID)
		close(stopped)
	}()
	service.startSupervisor(context.Background(), created.ID)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not finish")
	}
	if active.Load() == 0 {
		service.startSupervisor(context.Background(), created.ID)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && active.Load() != 1 {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 1 || maximum.Load() != 1 {
		t.Fatalf("active=%d maximum=%d", active.Load(), maximum.Load())
	}
	service.stopSupervisor(created.ID)
}
