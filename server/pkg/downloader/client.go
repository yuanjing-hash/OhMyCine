package downloader

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

const (
	SourceURL                       = "url"
	SourceTorrent                   = "torrent"
	SourcePan115Share               = "115_share"
	SourceProviderItem              = "provider_item"
	OutputConstraintNone            = "none"
	OutputConstraintLocalStaging    = "local_staging"
	OutputConstraintProviderStorage = "provider_storage"
)

type Capabilities struct {
	Pause            bool   `json:"pause"`
	Resume           bool   `json:"resume"`
	Cancel           bool   `json:"cancel"`
	DeleteData       bool   `json:"delete_data"`
	DownloadSpeed    bool   `json:"download_speed"`
	UploadSpeed      bool   `json:"upload_speed"`
	ETA              bool   `json:"eta"`
	Seeding          bool   `json:"seeding"`
	NativeOffline    bool   `json:"native_offline"`
	ShareReceive     bool   `json:"share_receive"`
	OutputConstraint string `json:"output_constraint"`
}

type Config struct {
	BaseURL               string
	Username              string
	Password              string
	CloudDriver           cloud.NativeOfflineDriver
	ProviderStorageRootID string
	ProviderDirectoryID   string
}

type Source struct {
	Kind     string
	URL      string
	Torrent  []byte
	Filename string
	// ProviderItemID is an internal-only adoption source. HTTP input must never
	// accept it and public DTOs must never expose it.
	ProviderItemID string
}

type SubmitRequest struct {
	Source       Source
	SavePath     string
	Tag          string
	MetadataOnly bool
	// ProviderDirectoryID overrides the downloader's default provider staging
	// directory with an immutable Server-validated media-library intake root.
	ProviderDirectoryID string
}

type File struct {
	RelativePath     string `json:"relative_path"`
	Size             int64  `json:"size"`
	ProviderItemID   string `json:"provider_item_id,omitempty"`
	ProviderParentID string `json:"provider_parent_id,omitempty"`
	SHA1             string `json:"sha1,omitempty"`
}

type Manifest struct {
	Name     string
	Files    []File
	Complete bool
}

type Category struct {
	Name     string
	SavePath string
}

// MetadataClient is implemented by providers which can expose a bounded file
// manifest and route a paused task before content download starts.
type MetadataClient interface {
	Pause(context.Context, string) error
	Resume(context.Context, string) error
	Manifest(context.Context, string) (Manifest, error)
	Categories(context.Context) ([]Category, error)
	EnsureCategory(context.Context, string, string) error
	// UpdateCategory changes the provider-owned save path for an existing
	// category. Callers must re-read Categories and verify the provider applied
	// the requested path before routing or resuming a task.
	UpdateCategory(context.Context, string, string) error
	// SetCategory assigns the provider category and explicitly routes the task
	// to savePath. Providers such as qBittorrent do not move a task merely
	// because its category changed when automatic torrent management is off.
	SetCategory(context.Context, string, string, string) error
}

// ManifestClient exposes the completed provider output for post-download
// classification. Unlike MetadataClient it does not promise pre-download
// metadata routing (native cloud offline providers generally cannot do that).
type ManifestClient interface {
	Manifest(context.Context, string) (Manifest, error)
}

type Task struct {
	ID             string
	Name           string
	Status         string
	Progress       *float64
	BytesCompleted *int64
	BytesTotal     *int64
	DownloadSpeed  *int64
	UploadSpeed    *int64
	ETASeconds     *int64
	Ratio          *float64
	SeededSeconds  *int64
	UploadedBytes  *int64
	Seeding        bool
	Completed      bool
	Failed         bool
	ErrorCode      string
	OutputItemID   string
}

type Health struct {
	Version string `json:"version"`
}

type Client interface {
	Test(context.Context) (Health, error)
	Submit(context.Context, SubmitRequest) (Task, error)
	Get(context.Context, string) (Task, error)
	Pause(context.Context, string) error
	Resume(context.Context, string) error
	Cancel(context.Context, string, bool) error
}

type Builder func(Config) (Client, error)

type Registry struct {
	mu      sync.RWMutex
	entries map[string]registryEntry
}

type registryEntry struct {
	capabilities Capabilities
	builder      Builder
}

func NewRegistry() *Registry { return &Registry{entries: map[string]registryEntry{}} }

func (r *Registry) Register(providerType string, capabilities Capabilities, builder Builder) error {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	if providerType == "" || builder == nil {
		return errors.New("downloader provider type and builder are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[providerType]; exists {
		return errors.New("downloader provider is already registered")
	}
	r.entries[providerType] = registryEntry{capabilities: capabilities, builder: builder}
	return nil
}

func (r *Registry) Build(providerType string, config Config) (Client, error) {
	r.mu.RLock()
	entry, ok := r.entries[strings.ToLower(strings.TrimSpace(providerType))]
	r.mu.RUnlock()
	if !ok {
		return nil, errors.New("downloader provider is unavailable")
	}
	return entry.builder(config)
}

func (r *Registry) Capabilities(providerType string) (Capabilities, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[strings.ToLower(strings.TrimSpace(providerType))]
	return entry.capabilities, ok
}

func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]string, 0, len(r.entries))
	for providerType := range r.entries {
		types = append(types, providerType)
	}
	sort.Strings(types)
	return types
}

type ProviderError struct {
	Code      string
	Retryable bool
	Cause     error
}

func (e *ProviderError) Error() string { return e.Code }
func (e *ProviderError) Unwrap() error { return e.Cause }

func Error(code string, retryable bool, cause error) error {
	return &ProviderError{Code: code, Retryable: retryable, Cause: cause}
}

func ErrorInfo(err error) (code string, retryable bool) {
	var provider *ProviderError
	if errors.As(err, &provider) {
		return provider.Code, provider.Retryable
	}
	return "downloader_unavailable", true
}
