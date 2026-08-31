package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

// MediaLibraryScanRequest contains only provider-neutral library scan facts.
// A backend owns the translation from these facts to its concrete storage API.
type MediaLibraryScanRequest struct {
	Library         models.MediaLibrary
	Storage         models.Storage
	VideoExtensions []string
	AssetExtensions []string
	IgnorePatterns  []string
}

// MediaLibraryListener reports normalized reconciliation reasons. It never
// writes catalog state itself; MediaLibraryService remains the single owner of
// recognition, generations, artifacts, and downstream notifications.
type MediaLibraryListener interface {
	Run(context.Context, func(context.Context, string) error) error
	Close() error
}

// MediaLibraryBackend is the strict storage-facing contract used by the
// provider-neutral MediaLibrary lifecycle. Provider differences belong in an
// implementation or a narrow cloud capability, not in reconciliation.
type MediaLibraryBackend interface {
	StorageType() string
	Scan(context.Context, MediaLibraryScanRequest) (medialibrary.Result, error)
	OpenListener(context.Context, models.MediaLibrary, models.Storage, <-chan struct{}) (MediaLibraryListener, error)
}

type MediaLibraryBackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]MediaLibraryBackend
}

func NewMediaLibraryBackendRegistry(backends ...MediaLibraryBackend) *MediaLibraryBackendRegistry {
	registry := &MediaLibraryBackendRegistry{backends: make(map[string]MediaLibraryBackend, len(backends))}
	for _, backend := range backends {
		registry.Register(backend)
	}
	return registry
}

func (r *MediaLibraryBackendRegistry) Register(backend MediaLibraryBackend) {
	if r == nil || backend == nil || backend.StorageType() == "" {
		return
	}
	r.mu.Lock()
	r.backends[backend.StorageType()] = backend
	r.mu.Unlock()
}

func (r *MediaLibraryBackendRegistry) Get(storageType string) (MediaLibraryBackend, error) {
	if r == nil {
		return nil, errors.New("media library backend registry is unavailable")
	}
	r.mu.RLock()
	backend := r.backends[storageType]
	r.mu.RUnlock()
	if backend == nil {
		return nil, errors.New("media library backend is unsupported")
	}
	return backend, nil
}

type localMediaLibraryBackend struct{}

func (localMediaLibraryBackend) StorageType() string { return models.StorageTypeLocal }

func (localMediaLibraryBackend) Scan(ctx context.Context, request MediaLibraryScanRequest) (medialibrary.Result, error) {
	return medialibrary.ScanLocal(ctx, request.Storage.RootPath, request.Library.RelativeRoot, request.Library.Recursive, request.VideoExtensions, request.AssetExtensions, request.IgnorePatterns)
}

func (localMediaLibraryBackend) OpenListener(_ context.Context, library models.MediaLibrary, storage models.Storage, _ <-chan struct{}) (MediaLibraryListener, error) {
	root, err := medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
	if err != nil {
		return nil, err
	}
	watcher, err := newRecursiveWatcher(root)
	if err != nil {
		return nil, err
	}
	return &localMediaLibraryListener{watcher: watcher, incremental: time.Duration(library.IncrementalMinutes) * time.Minute, full: time.Duration(library.FullScanIntervalHours) * time.Hour}, nil
}

type localMediaLibraryListener struct {
	watcher     *fsnotify.Watcher
	incremental time.Duration
	full        time.Duration
}

func (l *localMediaLibraryListener) Close() error {
	if l == nil || l.watcher == nil {
		return nil
	}
	return l.watcher.Close()
}

func (l *localMediaLibraryListener) Run(ctx context.Context, reconcile func(context.Context, string) error) error {
	incremental := time.NewTicker(l.incremental)
	full := time.NewTicker(l.full)
	defer incremental.Stop()
	defer full.Stop()
	var debounce *time.Timer
	var debounceC <-chan time.Time
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-l.watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&fsnotify.Create != 0 {
				if isDirectory, err := osStatDir(event.Name); err == nil && isDirectory {
					_ = addWatchTree(l.watcher, event.Name)
				}
			}
			if debounce == nil {
				debounce = time.NewTimer(600 * time.Millisecond)
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(600 * time.Millisecond)
			}
			debounceC = debounce.C
		case <-debounceC:
			_ = reconcile(ctx, "event")
			debounceC = nil
		case <-incremental.C:
			_ = reconcile(ctx, "incremental")
		case <-full.C:
			_ = reconcile(ctx, "full")
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return nil
			}
			return err
		}
	}
}

type pan115MediaLibraryBackend struct {
	driver func(uint) (cloudpkg.Driver, error)
}

func (pan115MediaLibraryBackend) StorageType() string { return models.StorageTypePan115 }

func (b pan115MediaLibraryBackend) Scan(ctx context.Context, request MediaLibraryScanRequest) (medialibrary.Result, error) {
	ctx = cloudpkg.WithReadClass(ctx, cloudpkg.ReadClassBackground)
	if request.Storage.ConnectionID == nil || b.driver == nil {
		return medialibrary.Result{}, errors.New("provider connection is unavailable")
	}
	driver, err := b.driver(*request.Storage.ConnectionID)
	if err != nil {
		return medialibrary.Result{}, err
	}
	providerRootID := request.Library.ProviderRootID
	if providerRootID == "" {
		providerRootID = request.Storage.RootPath
	}
	return medialibrary.ScanProvider(ctx, driver, providerRootID, request.Library.Recursive, request.VideoExtensions, request.AssetExtensions, request.IgnorePatterns)
}

func (pan115MediaLibraryBackend) OpenListener(_ context.Context, library models.MediaLibrary, _ models.Storage, wake <-chan struct{}) (MediaLibraryListener, error) {
	return &providerMediaLibraryListener{wake: wake, incremental: time.Duration(library.IncrementalMinutes) * time.Minute, full: time.Duration(library.FullScanIntervalHours) * time.Hour}, nil
}

type providerMediaLibraryListener struct {
	wake        <-chan struct{}
	incremental time.Duration
	full        time.Duration
}

func (*providerMediaLibraryListener) Close() error { return nil }

func (l *providerMediaLibraryListener) Run(ctx context.Context, reconcile func(context.Context, string) error) error {
	incremental := time.NewTicker(l.incremental)
	full := time.NewTicker(l.full)
	defer incremental.Stop()
	defer full.Stop()
	var debounce *time.Timer
	var debounceC <-chan time.Time
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-l.wake:
			if !ok {
				return nil
			}
			if debounce == nil {
				debounce = time.NewTimer(2 * time.Second)
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(2 * time.Second)
			}
			debounceC = debounce.C
		case <-debounceC:
			_ = reconcile(ctx, "event")
			debounceC = nil
		case <-incremental.C:
			_ = reconcile(ctx, "incremental")
		case <-full.C:
			_ = reconcile(ctx, "full")
		}
	}
}
