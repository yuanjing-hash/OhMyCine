package cloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const ProviderPan115 = "pan115"

type Capabilities struct {
	NetworkDrive          bool `json:"network_drive"`
	DirectoryList         bool `json:"directory_list"`
	Watch                 bool `json:"watch"`
	NativeOfflineDownload bool `json:"native_offline_download"`
	ShareReceive          bool `json:"share_receive"`
	TemporaryDirectURL    bool `json:"temporary_direct_url"`
	SignedProxy           bool `json:"signed_proxy"`
	SmallFileUpload       bool `json:"small_file_upload"`
	FileUpload            bool `json:"file_upload"`
	ChangeCursor          bool `json:"change_cursor"`
	CreateDirectory       bool `json:"create_directory"`
	Move                  bool `json:"move"`
	Copy                  bool `json:"copy"`
	Rename                bool `json:"rename"`
	Recycle               bool `json:"recycle"`
}

type Config struct {
	ConnectionID    uint
	Cookie          string
	RecyclePassword string
}

type Account struct {
	ID         string
	Name       string
	VIP        bool
	UsedBytes  *uint64
	TotalBytes *uint64
}

type Item struct {
	ID         string
	ParentID   string
	Name       string
	IsDir      bool
	Size       int64
	SHA1       string
	PickCode   string
	CreatedAt  time.Time
	ModifiedAt time.Time
}

type PageRequest struct {
	Offset int64
	Limit  int64
}

type Page struct {
	Items   []Item
	Offset  int64
	HasMore bool
}

// TreeEntry is a provider-relative item returned by an optional bulk tree
// enumerator. RelativePath always starts with '/' and is rooted below the
// requested provider directory.
type TreeEntry struct {
	Item
	RelativePath string
}

type TreeResult struct {
	Entries []TreeEntry
	Partial bool
}

// BulkTreeDriver is implemented by providers that expose a recursive listing
// API. It keeps full scans off the latency-sensitive interactive List lane and
// avoids one network request per small directory.
type BulkTreeDriver interface {
	ListTree(context.Context, string, int) (TreeResult, error)
}

type DirectURLRequest struct {
	FileID    string
	PickCode  string
	UserAgent string
}

type TemporaryURL struct {
	URL       string
	Headers   http.Header
	ExpiresAt time.Time
}

type OfflineTask struct {
	ID             string
	Name           string
	Status         string
	Progress       *float64
	BytesTotal     *int64
	ETASeconds     *int64
	OutputItemID   string
	Completed      bool
	Failed         bool
	ProviderStatus int
}

const (
	ChangeCreated = "created"
	ChangeMoved   = "moved"
	ChangeRenamed = "renamed"
	ChangeDeleted = "deleted"
)

// ChangeCursor is an ordered, provider-owned position. Event ID is retained
// alongside time because 115 can emit several life events in the same second.
type ChangeCursor struct {
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

// ChangeEvent is deliberately allowlisted. Provider response bodies,
// credentials, pickcodes and temporary URLs must never enter the event inbox.
type ChangeEvent struct {
	ID               string
	Time             time.Time
	Kind             string
	ItemID           string
	ParentID         string
	PreviousParentID string
	Name             string
}

type ChangePage struct {
	Events     []ChangeEvent
	NextCursor ChangeCursor
	HasMore    bool
}

type ChangeSource interface {
	Changes(context.Context, ChangeCursor, int) (ChangePage, error)
}

type NativeOfflineDriver interface {
	Driver
	SubmitOffline(context.Context, string, string) (OfflineTask, error)
	GetOffline(context.Context, string) (OfflineTask, error)
	CancelOffline(context.Context, string, bool) error
}

// ShareItem is a bounded top-level fact from a provider share. Share and
// receive codes remain private inside ShareSnapshot and must never be exposed
// through public DTOs or logs.
type ShareItem struct {
	ID    string
	Name  string
	IsDir bool
	Size  int64
}

type ShareSnapshot struct {
	ShareCode   string
	ReceiveCode string
	Title       string
	Items       []ShareItem
}

// ShareReceiveDriver is an optional cloud capability. Providers own URL
// parsing and receive API quirks; orchestration receives only bounded facts.
type ShareReceiveDriver interface {
	Driver
	InspectShare(context.Context, string) (ShareSnapshot, error)
	ReceiveShare(context.Context, ShareSnapshot, string) error
}

// MutationDriver is an optional provider capability used only by the durable
// transfer pipeline. All identities are provider-owned opaque item IDs.
type MutationDriver interface {
	Driver
	CreateDirectory(context.Context, string, string) (Item, error)
	Move(context.Context, string, string) error
	Copy(context.Context, string, string) error
	Rename(context.Context, string, string) error
	Recycle(context.Context, string) error
}

// MaxBatchMutationItems is the provider-neutral hard ceiling used by the
// transfer worker. Drivers may reject smaller provider-specific limits, but a
// task or user input can never increase this request size.
const MaxBatchMutationItems = 100

// BatchMutationDriver is an optional acceleration capability for providers
// whose mutation API accepts several opaque identities in one request. A nil
// error means only that the provider accepted the request; durable callers
// must still reconcile every item before marking it complete.
type BatchMutationDriver interface {
	MutationDriver
	MoveMany(context.Context, []string, string) error
	CopyMany(context.Context, []string, string) error
	RecycleMany(context.Context, []string) error
}

// OperationTimingSnapshot contains only task-scoped aggregate counters and
// durations. It deliberately has no labels, item identities, names, paths, or
// provider responses, so it is safe to project into structured operation logs.
type OperationTimingSnapshot struct {
	ProviderWaitCalls  int
	ProviderWait       time.Duration
	ProviderCallCalls  int
	ProviderCall       time.Duration
	TargetListCalls    int
	TargetList         time.Duration
	BatchMutationCalls int
	BatchMutation      time.Duration
	DBCheckpointCalls  int
	DBCheckpoint       time.Duration
}

type OperationTimingCollector struct {
	mu       sync.Mutex
	snapshot OperationTimingSnapshot
}

type operationTimingContextKey struct{}

func NewOperationTimingCollector() *OperationTimingCollector {
	return &OperationTimingCollector{}
}

func WithOperationTimingCollector(ctx context.Context, collector *OperationTimingCollector) context.Context {
	if ctx == nil || collector == nil {
		return ctx
	}
	return context.WithValue(ctx, operationTimingContextKey{}, collector)
}

func (collector *OperationTimingCollector) Snapshot() OperationTimingSnapshot {
	if collector == nil {
		return OperationTimingSnapshot{}
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	return collector.snapshot
}

func recordOperationTiming(ctx context.Context, update func(*OperationTimingSnapshot)) {
	if ctx == nil {
		return
	}
	collector, _ := ctx.Value(operationTimingContextKey{}).(*OperationTimingCollector)
	if collector == nil {
		return
	}
	collector.mu.Lock()
	update(&collector.snapshot)
	collector.mu.Unlock()
}

func RecordProviderWait(ctx context.Context, duration time.Duration) {
	recordOperationTiming(ctx, func(snapshot *OperationTimingSnapshot) {
		snapshot.ProviderWaitCalls++
		if duration > 0 {
			snapshot.ProviderWait += duration
		}
	})
}

func RecordProviderCall(ctx context.Context, duration time.Duration) {
	recordOperationTiming(ctx, func(snapshot *OperationTimingSnapshot) {
		snapshot.ProviderCallCalls++
		if duration > 0 {
			snapshot.ProviderCall += duration
		}
	})
}

func RecordTargetList(ctx context.Context, duration time.Duration) {
	recordOperationTiming(ctx, func(snapshot *OperationTimingSnapshot) {
		snapshot.TargetListCalls++
		if duration > 0 {
			snapshot.TargetList += duration
		}
	})
}

func RecordBatchMutation(ctx context.Context, duration time.Duration) {
	recordOperationTiming(ctx, func(snapshot *OperationTimingSnapshot) {
		snapshot.BatchMutationCalls++
		if duration > 0 {
			snapshot.BatchMutation += duration
		}
	})
}

func RecordDBCheckpoint(ctx context.Context, duration time.Duration) {
	recordOperationTiming(ctx, func(snapshot *OperationTimingSnapshot) {
		snapshot.DBCheckpointCalls++
		if duration > 0 {
			snapshot.DBCheckpoint += duration
		}
	})
}

// ExactRecyclePurger permanently removes only the explicitly owned recycle
// item. There is deliberately no empty/all-items variant in this contract.
type ExactRecyclePurger interface {
	Driver
	PurgeRecycle(context.Context, string) error
}

// SmallFileUploadDriver is deliberately narrower than a general upload API.
// Artifact orchestration must enforce extension, MIME, size and ancestry
// policy before invoking it; providers repeat their own boundary validation.
type SmallFileUploadDriver interface {
	Driver
	UploadSmallFile(context.Context, string, string, string, int64, io.ReadSeeker) (Item, error)
}

type UploadRequest struct {
	ParentID string
	Name     string
	Size     int64
	Reader   io.ReadSeeker
}

// UploadDriver is available only to the Server transfer worker. Plugins never
// receive this interface, local paths, destination identities, or credentials.
type UploadDriver interface {
	Driver
	Upload(context.Context, UploadRequest) (Item, error)
}

// ReadRequest identifies one immutable provider file and an optional restart
// offset. Provider-specific temporary URLs, acquisition headers and cookies
// must remain inside the driver implementation.
type ReadRequest struct {
	FileID string
	Offset int64
}

// ReadResult is a single streaming response. OffsetAccepted is false when the
// provider ignored a non-zero range request; callers must then discard their
// partial file and restart from byte zero.
type ReadResult struct {
	Body           io.ReadCloser
	OffsetAccepted bool
	TotalSize      *int64
}

// ReadDriver is the optional source-export capability used by cross-data-source
// transfer. It deliberately exposes a stream rather than a direct URL so
// credentials and expiring provider URLs never enter checkpoints or logs.
type ReadDriver interface {
	Driver
	OpenRead(context.Context, ReadRequest) (ReadResult, error)
}

type Driver interface {
	Provider() string
	Capabilities() Capabilities
	Probe(context.Context) (Account, error)
	List(context.Context, string, PageRequest) (Page, error)
	Stat(context.Context, string) (Item, error)
	DirectURL(context.Context, DirectURLRequest) (TemporaryURL, error)
}

type Builder func(Config) (Driver, error)

type Registry struct {
	mu      sync.RWMutex
	entries map[string]Builder
}

func NewRegistry() *Registry { return &Registry{entries: map[string]Builder{}} }

func (r *Registry) Register(provider string, builder Builder) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || builder == nil {
		return errors.New("cloud provider and builder are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[provider]; exists {
		return errors.New("cloud provider is already registered")
	}
	r.entries[provider] = builder
	return nil
}

func (r *Registry) Build(provider string, config Config) (Driver, error) {
	r.mu.RLock()
	builder, ok := r.entries[strings.ToLower(strings.TrimSpace(provider))]
	r.mu.RUnlock()
	if !ok {
		return nil, errors.New("cloud provider is unavailable")
	}
	return builder(config)
}

func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]string, 0, len(r.entries))
	for provider := range r.entries {
		items = append(items, provider)
	}
	sort.Strings(items)
	return items
}

const (
	CodeCookieInvalid     = "pan115_cookie_invalid"
	CodeAuthExpired       = "pan115_auth_expired"
	CodeRateLimited       = "pan115_rate_limited"
	CodeUnavailable       = "pan115_unavailable"
	CodeResponseInvalid   = "pan115_response_invalid"
	CodeNotFound          = "pan115_item_not_found"
	CodeOfflineNoQuota    = "pan115_offline_quota_exhausted"
	CodeOfflineBadLink    = "pan115_offline_source_invalid"
	CodeOfflineTaskExists = "pan115_offline_task_exists"
	CodeShareInvalid      = "pan115_share_invalid"
	CodeShareEmpty        = "pan115_share_empty"
	CodeShareTooLarge     = "pan115_share_too_large"
	CodeShareUnknown      = "pan115_share_result_unknown"
	CodeConflict          = "cloud_item_conflict"
	CodeMutationUnknown   = "cloud_mutation_result_unknown"
)

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

func ErrorInfo(err error) (string, bool) {
	var provider *ProviderError
	if errors.As(err, &provider) {
		return provider.Code, provider.Retryable
	}
	return CodeUnavailable, true
}
