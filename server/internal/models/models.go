package models

import "time"

const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"
	RoleKindSystem     = "system"
	RoleKindCustom     = "custom"
)

type User struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	Username           string     `gorm:"size:64;not null" json:"username"`
	UsernameNormalized string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	DisplayName        string     `gorm:"size:128;not null" json:"display_name"`
	PasswordHash       string     `gorm:"not null" json:"-"`
	Status             string     `gorm:"size:16;not null;index" json:"status"`
	IsOwner            bool       `gorm:"not null;default:false" json:"is_owner"`
	AuthzVersion       uint64     `gorm:"not null;default:1" json:"authz_version"`
	LastLoginAt        *time.Time `json:"last_login_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type Role struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Code        string    `gorm:"size:64;not null;uniqueIndex" json:"code"`
	Name        string    `gorm:"size:128;not null" json:"name"`
	Description string    `gorm:"size:512;not null" json:"description"`
	Kind        string    `gorm:"size:16;not null" json:"kind"`
	Protected   bool      `gorm:"not null;default:false" json:"protected"`
	Active      bool      `gorm:"not null;default:true" json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Permission struct {
	Code         string     `gorm:"primaryKey;size:96" json:"code"`
	Module       string     `gorm:"size:64;not null;index" json:"module"`
	Name         string     `gorm:"size:128;not null" json:"name"`
	Description  string     `gorm:"size:512;not null" json:"description"`
	Risk         string     `gorm:"size:16;not null" json:"risk"`
	DeprecatedAt *time.Time `json:"deprecated_at"`
}

type UserRole struct {
	UserID     uint      `gorm:"primaryKey" json:"user_id"`
	RoleID     uint      `gorm:"primaryKey" json:"role_id"`
	AssignedBy *uint     `json:"assigned_by"`
	CreatedAt  time.Time `json:"created_at"`
}

type RolePermission struct {
	RoleID         uint      `gorm:"primaryKey" json:"role_id"`
	PermissionCode string    `gorm:"primaryKey;size:96" json:"permission_code"`
	CreatedAt      time.Time `json:"created_at"`
}

type Session struct {
	ID                string     `gorm:"primaryKey;size:64" json:"id"`
	TokenHash         string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	UserID            uint       `gorm:"not null;index" json:"user_id"`
	CreatedAt         time.Time  `json:"created_at"`
	LastSeenAt        time.Time  `json:"last_seen_at"`
	IdleExpiresAt     time.Time  `gorm:"index" json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time  `gorm:"index" json:"absolute_expires_at"`
	RevokedAt         *time.Time `gorm:"index" json:"revoked_at"`
	UserAgentHash     string     `gorm:"size:64" json:"-"`
	IPHint            string     `gorm:"size:96" json:"-"`
}

// DeviceToken is a revocable Player credential. Only the one-way hashes are
// persisted; the raw device identifier and bearer token never reach SQLite.
type DeviceToken struct {
	ID                string     `gorm:"primaryKey;size:64" json:"id"`
	TokenHash         string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	UserID            uint       `gorm:"not null;index" json:"user_id"`
	DeviceIDHash      string     `gorm:"size:64;not null;index" json:"-"`
	DeviceName        string     `gorm:"size:128;not null" json:"device_name"`
	ClientKind        string     `gorm:"size:32;not null;index" json:"client_kind"`
	CreatedAt         time.Time  `json:"created_at"`
	LastSeenAt        time.Time  `json:"last_seen_at"`
	IdleExpiresAt     time.Time  `gorm:"index" json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time  `gorm:"index" json:"absolute_expires_at"`
	RevokedAt         *time.Time `gorm:"index" json:"revoked_at,omitempty"`
}

type AuditLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ActorID    *uint     `gorm:"index" json:"actor_id"`
	Action     string    `gorm:"size:96;not null;index" json:"action"`
	TargetType string    `gorm:"size:64;not null;index" json:"target_type"`
	TargetID   string    `gorm:"size:96;not null" json:"target_id"`
	Outcome    string    `gorm:"size:16;not null;index" json:"outcome"`
	Metadata   string    `gorm:"type:text;not null" json:"-"`
	RequestID  string    `gorm:"size:64" json:"request_id"`
	IPHint     string    `gorm:"size:96" json:"ip_hint"`
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

// RuntimeLogPolicy is the singleton, administrator-managed file retention policy.
// The physical log directory is deployment configuration and is deliberately not persisted.
type RuntimeLogPolicy struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Level         string    `gorm:"size:16;not null" json:"level"`
	MaxFileMiB    int       `gorm:"not null" json:"max_file_mib"`
	MaxBackups    int       `gorm:"not null" json:"max_backups"`
	RetentionDays int       `gorm:"not null" json:"retention_days"`
	MaxTotalMiB   int       `gorm:"not null" json:"max_total_mib"`
	Revision      uint64    `gorm:"not null;default:1" json:"revision"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PluginRepository is an administrator-configured GitHub plugin registry.
// CachedRegistryJSON is written only after the pinned registry has passed the
// shared contract validation. GitHub credentials are deliberately not stored
// here; future authenticated GitHub access must use the credential boundary.
type PluginRepository struct {
	ID                 uint       `gorm:"primaryKey" json:"id"`
	Name               string     `gorm:"size:128;not null" json:"name"`
	GitHubURL          string     `gorm:"column:github_url;size:512;not null;uniqueIndex" json:"github_url"`
	GitHubOwner        string     `gorm:"column:github_owner;size:128;not null" json:"-"`
	GitHubRepo         string     `gorm:"column:github_repo;size:128;not null" json:"-"`
	Enabled            bool       `gorm:"not null;default:true;index" json:"enabled"`
	Priority           int64      `gorm:"not null;index" json:"priority"`
	Revision           uint64     `gorm:"not null;default:1" json:"revision"`
	LastCommitSHA      string     `gorm:"column:last_commit_sha;size:40;not null;default:''" json:"last_commit_sha"`
	LastRefreshedAt    *time.Time `json:"last_refreshed_at"`
	LastErrorCode      string     `gorm:"size:96;not null;default:''" json:"last_error_code"`
	CachedRegistryJSON string     `gorm:"type:text;not null;default:''" json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

const (
	PluginInstallationDisabled = "disabled"
	PluginInstallationEnabled  = "enabled"
	PluginInstallationFailed   = "failed"

	PluginRuntimeStarting = "starting"
	PluginRuntimeRunning  = "running"
	PluginRuntimeStopped  = "stopped"
	PluginRuntimeFailed   = "failed"
)

// PluginPackage is an immutable, verified release artifact. PackagePath is a
// Server-owned content-addressed directory and must never be exposed by APIs.
// Repository identity is copied into the record so deleting a discovery
// repository never makes an already installed package lose provenance.
type PluginPackage struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	PluginID            string    `gorm:"size:128;not null;index:idx_plugin_packages_identity,priority:1" json:"plugin_id"`
	Version             string    `gorm:"size:128;not null;index:idx_plugin_packages_identity,priority:2" json:"version"`
	RepositoryID        *uint     `gorm:"index" json:"repository_id"`
	RepositoryOwner     string    `gorm:"size:128;not null;index:idx_plugin_packages_identity,priority:3" json:"-"`
	RepositoryRepo      string    `gorm:"size:128;not null;index:idx_plugin_packages_identity,priority:4" json:"-"`
	RegistryCommit      string    `gorm:"size:40;not null" json:"-"`
	RegistryEntryJSON   string    `gorm:"type:text;not null" json:"-"`
	ManifestURL         string    `gorm:"type:text;not null" json:"-"`
	PackageURL          string    `gorm:"type:text;not null" json:"-"`
	PackageSHA256       string    `gorm:"size:64;not null;uniqueIndex" json:"package_sha256"`
	ExtractedTreeSHA256 string    `gorm:"size:64;not null" json:"-"`
	ManifestJSON        string    `gorm:"type:text;not null" json:"-"`
	PackagePath         string    `gorm:"type:text;not null" json:"-"`
	VerifiedAt          time.Time `gorm:"not null" json:"verified_at"`
	CreatedAt           time.Time `json:"created_at"`
}

// PluginInstallation is the mutable selection for one stable plugin ID.
// Revision protects all user-visible lifecycle changes with compare-and-swap.
type PluginInstallation struct {
	PluginID             string     `gorm:"primaryKey;size:128" json:"plugin_id"`
	ActivePackageID      uint       `gorm:"not null;index" json:"active_package_id"`
	PreviousPackageID    *uint      `gorm:"index" json:"previous_package_id"`
	Status               string     `gorm:"size:16;not null;index" json:"status"`
	Revision             uint64     `gorm:"not null;default:1" json:"revision"`
	RuntimeGeneration    uint64     `gorm:"not null;default:0" json:"runtime_generation"`
	LastRuntimeErrorCode string     `gorm:"size:96;not null;default:''" json:"last_runtime_error_code"`
	InstalledAt          time.Time  `gorm:"not null" json:"installed_at"`
	UpdatedAt            time.Time  `gorm:"not null" json:"updated_at"`
	EnabledAt            *time.Time `json:"enabled_at"`
}

// PluginPermissionGrant snapshots the exact canonical permission granted for
// a package. Grants are package-specific; an update with additions therefore
// cannot inherit a broader grant implicitly.
type PluginPermissionGrant struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	PluginID        string    `gorm:"size:128;not null;index:idx_plugin_permission_grants_identity,priority:1" json:"plugin_id"`
	PluginPackageID uint      `gorm:"not null;index:idx_plugin_permission_grants_identity,priority:2" json:"plugin_package_id"`
	PermissionKey   string    `gorm:"size:64;not null;index:idx_plugin_permission_grants_identity,priority:3" json:"permission_key"`
	PermissionJSON  string    `gorm:"type:text;not null" json:"-"`
	GrantedBy       *uint     `gorm:"index" json:"granted_by"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
}

// PluginRuntimeGeneration is an append-only terminal history of runtime
// starts. At most one generation per plugin is active in the in-memory host.
type PluginRuntimeGeneration struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	PluginID        string     `gorm:"size:128;not null;uniqueIndex:idx_plugin_runtime_generation" json:"plugin_id"`
	PluginPackageID uint       `gorm:"not null;index" json:"plugin_package_id"`
	Generation      uint64     `gorm:"not null;uniqueIndex:idx_plugin_runtime_generation" json:"generation"`
	Status          string     `gorm:"size:16;not null;index" json:"status"`
	SafeErrorCode   string     `gorm:"size:96;not null;default:''" json:"error_code"`
	StartedAt       time.Time  `gorm:"not null" json:"started_at"`
	StoppedAt       *time.Time `json:"stopped_at"`
}

// PluginInstallPreview binds a verified immutable package and its exact
// permission set to an explicit, expiring user confirmation.
type PluginInstallPreview struct {
	ID                    string     `gorm:"primaryKey;size:36" json:"id"`
	PluginID              string     `gorm:"size:128;not null;index" json:"plugin_id"`
	PluginPackageID       uint       `gorm:"not null;index" json:"plugin_package_id"`
	Operation             string     `gorm:"size:16;not null" json:"operation"`
	PermissionFingerprint string     `gorm:"size:64;not null" json:"permission_fingerprint"`
	InstallationRevision  uint64     `gorm:"not null" json:"installation_revision"`
	CreatedBy             uint       `gorm:"not null;index" json:"created_by"`
	ExpiresAt             time.Time  `gorm:"not null;index" json:"expires_at"`
	ConsumedAt            *time.Time `json:"consumed_at"`
	CreatedAt             time.Time  `gorm:"not null" json:"created_at"`
}

const (
	PluginCredentialModeNone   = "none"
	PluginCredentialModeCookie = "cookie"
	PluginCredentialModeBearer = "bearer"
)

// PluginConnection is an administrator-created instance of an installed
// plugin. Secrets remain encrypted and are only consumed by the controlled
// Host HTTP capability; they are never returned to guest memory or Player.
type PluginConnection struct {
	ID                   string     `gorm:"primaryKey;size:36" json:"id"`
	PluginID             string     `gorm:"size:128;not null;index" json:"plugin_id"`
	Name                 string     `gorm:"size:128;not null" json:"name"`
	ConfigJSON           string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	CredentialScope      string     `gorm:"size:128;not null;default:''" json:"credential_scope"`
	CredentialMode       string     `gorm:"size:16;not null;default:'none'" json:"credential_mode"`
	CredentialCiphertext string     `gorm:"type:text;not null;default:''" json:"-"`
	Enabled              bool       `gorm:"not null;default:true;index" json:"enabled"`
	LastHealthStatus     string     `gorm:"size:16;not null;default:'unknown';index" json:"last_health_status"`
	LastHealthErrorCode  string     `gorm:"size:96;not null;default:''" json:"last_health_error_code"`
	LastHealthCheckedAt  *time.Time `json:"last_health_checked_at"`
	Revision             uint64     `gorm:"not null;default:1" json:"revision"`
	CreatedAt            time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt            time.Time  `gorm:"not null" json:"updated_at"`
}

// PluginPrivateKV is encrypted per connection because plugins may keep remote
// cursors or session-derived state in it. Quota accounting uses PlaintextBytes
// and never relies on ciphertext expansion.
type PluginPrivateKV struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	PluginID        string    `gorm:"size:128;not null;uniqueIndex:idx_plugin_private_kv_identity,priority:1" json:"plugin_id"`
	ConnectionID    string    `gorm:"size:36;not null;uniqueIndex:idx_plugin_private_kv_identity,priority:2" json:"connection_id"`
	Key             string    `gorm:"size:128;not null;uniqueIndex:idx_plugin_private_kv_identity,priority:3" json:"key"`
	ValueCiphertext string    `gorm:"type:text;not null" json:"-"`
	PlaintextBytes  int64     `gorm:"not null" json:"-"`
	CreatedAt       time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt       time.Time `gorm:"not null" json:"updated_at"`
}

func (PluginPrivateKV) TableName() string { return "plugin_private_kv" }

// PluginOnlineLibrary is a logical online catalog published by one plugin
// connection. It is deliberately separate from physical media_libraries: it
// cannot be scanned, transferred, or used as a STRM projection root.
type PluginOnlineLibrary struct {
	ID                    string    `gorm:"primaryKey;size:36" json:"id"`
	PluginID              string    `gorm:"size:128;not null;index" json:"plugin_id"`
	ConnectionID          string    `gorm:"size:36;not null;uniqueIndex:idx_plugin_online_library_identity,priority:1" json:"connection_id"`
	ExternalKey           string    `gorm:"size:128;not null;uniqueIndex:idx_plugin_online_library_identity,priority:2" json:"external_key"`
	Name                  string    `gorm:"size:128;not null" json:"name"`
	HomeContributionsJSON string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	Enabled               bool      `gorm:"not null;default:true;index" json:"enabled"`
	SortOrder             int64     `gorm:"not null;default:0;index" json:"sort_order"`
	Revision              uint64    `gorm:"not null;default:1" json:"revision"`
	CreatedAt             time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt             time.Time `gorm:"not null" json:"updated_at"`
}

// PluginFeedCache stores only host-validated, credential-free DTOs. Provider
// cursors remain opaque and are bound to a refresh session so a new refresh
// never invalidates an older page that the Player is still browsing.
type PluginFeedCache struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	LibraryID      string    `gorm:"size:36;not null;uniqueIndex:idx_plugin_feed_cache_identity,priority:1" json:"library_id"`
	RouteKey       string    `gorm:"size:256;not null;uniqueIndex:idx_plugin_feed_cache_identity,priority:2" json:"route_key"`
	CursorKey      string    `gorm:"size:64;not null;uniqueIndex:idx_plugin_feed_cache_identity,priority:3" json:"-"`
	RefreshSession string    `gorm:"size:36;not null;uniqueIndex:idx_plugin_feed_cache_identity,priority:4" json:"refresh_session"`
	ResponseJSON   string    `gorm:"type:text;not null" json:"-"`
	ExpiresAt      time.Time `gorm:"not null;index" json:"expires_at"`
	CreatedAt      time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt      time.Time `gorm:"not null" json:"updated_at"`
}

// PluginActionReceipt makes a remote-mutating site action idempotent without
// storing the raw user-provided idempotency key.
type PluginActionReceipt struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	LibraryID       string    `gorm:"size:36;not null;uniqueIndex:idx_plugin_action_receipt_identity,priority:1" json:"library_id"`
	Action          string    `gorm:"size:64;not null;uniqueIndex:idx_plugin_action_receipt_identity,priority:2" json:"action"`
	IdempotencyHash string    `gorm:"size:64;not null;uniqueIndex:idx_plugin_action_receipt_identity,priority:3" json:"-"`
	ResponseJSON    string    `gorm:"type:text;not null" json:"-"`
	CreatedAt       time.Time `gorm:"not null;index" json:"created_at"`
}

const (
	StorageTypeLocal           = "local"
	StorageTypePan115          = "pan115"
	ConnectionProviderPan115   = "pan115"
	ConnectionProviderEmby     = "emby"
	ConnectionProviderJellyfin = "jellyfin"
)

// Connection owns one external-provider credential and its redacted health
// summary. The encrypted credential model must never be serialized directly.
type Connection struct {
	ID                          uint       `gorm:"primaryKey" json:"id"`
	Name                        string     `gorm:"size:128;not null" json:"name"`
	NameNormalized              string     `gorm:"size:128;not null;uniqueIndex" json:"-"`
	Provider                    string     `gorm:"size:32;not null;index" json:"provider"`
	Endpoint                    string     `gorm:"size:2048;not null;default:''" json:"-"`
	CredentialCiphertext        string     `gorm:"type:text;not null" json:"-"`
	RecycleCredentialCiphertext string     `gorm:"type:text;not null;default:''" json:"-"`
	Enabled                     bool       `gorm:"not null" json:"enabled"`
	AccountID                   string     `gorm:"size:128;not null;default:''" json:"-"`
	AccountName                 string     `gorm:"size:256;not null;default:''" json:"-"`
	AccountVIP                  bool       `gorm:"column:account_vip;not null;default:false" json:"-"`
	QuotaUsedBytes              *uint64    `json:"-"`
	QuotaTotalBytes             *uint64    `json:"-"`
	LastHealthStatus            string     `gorm:"size:16;not null;default:'unknown'" json:"-"`
	LastHealthErrorCode         string     `gorm:"size:96;not null;default:''" json:"-"`
	LastHealthCheckedAt         *time.Time `json:"-"`
	Revision                    uint64     `gorm:"not null;default:1" json:"revision"`
	CreatedAt                   time.Time  `json:"created_at"`
	UpdatedAt                   time.Time  `json:"updated_at"`
}

const (
	Pan115PlaybackRolePrimary   = "primary"
	Pan115PlaybackRoleSecondary = "secondary"

	Pan115PlaybackLeaseActive         = "active"
	Pan115PlaybackLeaseCopyPending    = "copy_pending"
	Pan115PlaybackLeaseCleanupPending = "cleanup_pending"
	Pan115PlaybackLeaseCleanupFailed  = "cleanup_failed"
	Pan115PlaybackLeaseCompleted      = "completed"
)

// Pan115PlaybackLease is a private ownership and recovery fact for bounded
// two-device playback. ClientFingerprint is irreversible and is routing-only;
// raw IP/User-Agent, pickcodes and resolved CDN URLs are never persisted.
type Pan115PlaybackLease struct {
	ID                   string     `gorm:"primaryKey;size:36" json:"-"`
	ConnectionID         uint       `gorm:"not null;index" json:"-"`
	ArtifactOpaqueID     string     `gorm:"size:64;not null;uniqueIndex:idx_pan115_playback_client" json:"-"`
	ClientFingerprint    string     `gorm:"size:64;not null;uniqueIndex:idx_pan115_playback_client" json:"-"`
	Role                 string     `gorm:"size:16;not null" json:"-"`
	SourceProviderItemID string     `gorm:"size:128;not null" json:"-"`
	CopyDirectoryID      string     `gorm:"size:128;not null;default:''" json:"-"`
	CopyItemID           string     `gorm:"size:128;not null;default:''" json:"-"`
	Status               string     `gorm:"size:32;not null;index" json:"-"`
	LeaseExpiresAt       time.Time  `gorm:"not null;index" json:"-"`
	CleanupAfter         *time.Time `gorm:"index" json:"-"`
	RetryCount           int        `gorm:"not null;default:0" json:"-"`
	NextRetryAt          *time.Time `gorm:"index" json:"-"`
	LastErrorCode        string     `gorm:"size:96;not null;default:''" json:"-"`
	CleanedAt            *time.Time `json:"-"`
	CreatedAt            time.Time  `json:"-"`
	UpdatedAt            time.Time  `json:"-"`
}

// Storage is a registered provider root. It does not classify media or choose a
// final placement; those responsibilities belong to later library/destination domains.
type Storage struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	Name                string     `gorm:"size:128;not null" json:"name"`
	NameNormalized      string     `gorm:"size:128;not null;uniqueIndex" json:"-"`
	Type                string     `gorm:"size:32;not null" json:"type"`
	RootPath            string     `gorm:"type:text;not null" json:"root_path"`
	RootDisplayPath     string     `gorm:"type:text;not null;default:''" json:"root_display_path"`
	RootPathNormalized  string     `gorm:"type:text;not null;uniqueIndex" json:"-"`
	ConnectionID        *uint      `gorm:"index" json:"connection_id"`
	Enabled             bool       `gorm:"not null;default:true" json:"enabled"`
	Capabilities        string     `gorm:"type:text;not null" json:"-"`
	LastProbeExists     bool       `gorm:"not null;default:false" json:"-"`
	LastProbeReadable   bool       `gorm:"not null;default:false" json:"-"`
	LastProbeAvailable  bool       `gorm:"not null;default:false" json:"-"`
	LastProbeFreeBytes  *uint64    `json:"-"`
	LastProbeTotalBytes *uint64    `json:"-"`
	LastProbeErrorCode  string     `gorm:"size:64;not null;default:''" json:"-"`
	LastProbeCheckedAt  *time.Time `json:"-"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// ProviderEvent is a normalized, credential-free inbox record. The composite
// provider identity is unique so retries and process restarts are idempotent.
type ProviderEvent struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	ConnectionID     uint       `gorm:"not null;uniqueIndex:idx_provider_event_identity;index" json:"connection_id"`
	Stream           string     `gorm:"size:32;not null;uniqueIndex:idx_provider_event_identity" json:"stream"`
	ProviderEventID  string     `gorm:"size:128;not null;uniqueIndex:idx_provider_event_identity" json:"provider_event_id"`
	EventTime        time.Time  `gorm:"not null;index" json:"event_time"`
	Kind             string     `gorm:"size:16;not null" json:"kind"`
	ItemID           string     `gorm:"size:128;not null;index" json:"item_id"`
	ParentID         string     `gorm:"size:128;not null;default:''" json:"parent_id"`
	PreviousParentID string     `gorm:"size:128;not null;default:''" json:"previous_parent_id"`
	Name             string     `gorm:"size:512;not null;default:''" json:"name"`
	PayloadJSON      string     `gorm:"type:text;not null" json:"-"`
	ProcessedAt      *time.Time `gorm:"index" json:"processed_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

type ProviderCursor struct {
	ConnectionID uint      `gorm:"primaryKey" json:"connection_id"`
	Stream       string    `gorm:"primaryKey;size:32" json:"stream"`
	CursorTime   time.Time `gorm:"not null" json:"cursor_time"`
	CursorID     string    `gorm:"size:128;not null;default:''" json:"cursor_id"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const (
	MediaClassificationProfileKindSystem = "system"
	MediaClassificationProfileKindCustom = "custom"
)

// MediaClassificationProfile stores logical post-identification grouping
// rules. It is intentionally separate from download/import CategoryRule.
type MediaClassificationProfile struct {
	ID                          uint      `gorm:"primaryKey" json:"id"`
	Code                        *string   `gorm:"size:64;uniqueIndex" json:"code"`
	Name                        string    `gorm:"size:128;not null" json:"name"`
	NameNormalized              string    `gorm:"size:128;not null;uniqueIndex" json:"-"`
	Kind                        string    `gorm:"size:16;not null" json:"kind"`
	Protected                   bool      `gorm:"not null;default:false" json:"protected"`
	SchemaVersion               int       `gorm:"not null" json:"schema_version"`
	RulesJSON                   string    `gorm:"type:text;not null" json:"-"`
	BuiltinRecognitionPacksJSON string    `gorm:"type:text;not null;default:'[\"tv-v1\",\"anime-v1\"]'" json:"-"`
	RecognitionRulesJSON        string    `gorm:"type:text;not null;default:'[]'" json:"-"`
	MovieDirectoryTemplate      string    `gorm:"size:512;not null;default:'{category}/{title} ({year})'" json:"-"`
	MovieFilenameTemplate       string    `gorm:"size:512;not null;default:'{title} ({year})'" json:"-"`
	TVDirectoryTemplate         string    `gorm:"size:512;not null;default:'{category}/{title} ({year})/Season {season:02}'" json:"-"`
	TVFilenameTemplate          string    `gorm:"size:512;not null;default:'{title} - S{season:02}E{episode:02}'" json:"-"`
	Revision                    uint64    `gorm:"not null;default:1" json:"revision"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

const (
	MediaLibraryStatusDisabled             = "disabled"
	MediaLibraryStatusInitializing         = "initializing"
	MediaLibraryStatusInitializationFailed = "initialization_failed"
	MediaLibraryStatusAttachingListener    = "attaching_listener"
	MediaLibraryStatusReconciling          = "catch_up_reconciliation"
	MediaLibraryStatusListening            = "listening"
	MediaLibraryTransferMove               = "move"
	MediaLibraryTransferCopy               = "copy"
	MediaLibraryTransferSymlink            = "symlink"
	MediaLibraryConflictAsk                = "ask"
	MediaLibraryConflictOverwrite          = "overwrite"
	MediaLibraryConflictSkip               = "skip"
	MediaLibraryConflictRename             = "rename"
)

type MediaLibrary struct {
	ID                           uint       `gorm:"primaryKey" json:"id"`
	Name                         string     `gorm:"size:128;not null" json:"name"`
	NameNormalized               string     `gorm:"size:128;not null;uniqueIndex" json:"-"`
	StorageID                    uint       `gorm:"not null;index" json:"storage_id"`
	ProfileID                    uint       `gorm:"not null;index" json:"profile_id"`
	ProfileRevision              uint64     `gorm:"not null" json:"profile_revision"`
	RelativeRoot                 string     `gorm:"size:1024;not null" json:"relative_root"`
	ProviderRootID               string     `gorm:"size:128;not null;default:'';index:idx_media_libraries_provider_root,priority:2" json:"-"`
	SortOrder                    int        `gorm:"not null;default:0;index" json:"sort_order"`
	TransferMode                 string     `gorm:"size:16;not null;default:'move'" json:"transfer_mode"`
	ConflictPolicy               string     `gorm:"size:16;not null;default:'ask'" json:"conflict_policy"`
	MovieDirectoryTemplate       string     `gorm:"size:512;not null;default:'{category}/{title} ({year})'" json:"movie_directory_template"`
	MovieFilenameTemplate        string     `gorm:"size:512;not null;default:'{title} ({year})'" json:"movie_filename_template"`
	TVDirectoryTemplate          string     `gorm:"size:512;not null;default:'{category}/{title} ({year})/Season {season:02}'" json:"tv_directory_template"`
	TVFilenameTemplate           string     `gorm:"size:512;not null;default:'{title} - S{season:02}E{episode:02}'" json:"tv_filename_template"`
	Enabled                      bool       `gorm:"not null" json:"enabled"`
	Recursive                    bool       `gorm:"not null" json:"recursive"`
	FullScanIntervalHours        int        `gorm:"not null;default:24" json:"full_scan_interval_hours"`
	IncrementalMinutes           int        `gorm:"not null;default:15" json:"incremental_minutes"`
	VideoExtensionsJSON          string     `gorm:"type:text;not null" json:"-"`
	STRMAssetExtraExtensionsJSON string     `gorm:"column:strm_asset_extra_extensions;type:text;not null;default:'[]'" json:"-"`
	IgnorePatternsJSON           string     `gorm:"type:text;not null" json:"-"`
	MetadataLanguage             string     `gorm:"size:16;not null;default:'zh-CN'" json:"metadata_language"`
	MetadataRegion               string     `gorm:"size:8;not null;default:'CN'" json:"metadata_region"`
	MatchStrategy                string     `gorm:"size:32;not null;default:'balanced'" json:"match_strategy"`
	ProviderRatePerSecond        int        `gorm:"not null;default:100" json:"provider_rate_per_second"`
	ProviderConcurrency          int        `gorm:"not null;default:2" json:"provider_concurrency"`
	MetadataRatePerSecond        int        `gorm:"not null;default:5" json:"metadata_rate_per_second"`
	MetadataConcurrency          int        `gorm:"not null;default:1" json:"metadata_concurrency"`
	STRMEnabled                  bool       `gorm:"not null;default:false" json:"strm_enabled"`
	STRMLocalRoot                string     `gorm:"type:text;not null;default:''" json:"-"`
	SignedProxyEnabled           bool       `gorm:"not null;default:false" json:"signed_proxy_enabled"`
	MetadataArtifactsEnabled     bool       `gorm:"not null;default:false" json:"metadata_artifacts_enabled"`
	UploadSidecars               bool       `gorm:"not null;default:false" json:"upload_sidecars"`
	ArtifactGeneration           uint64     `gorm:"not null;default:0" json:"artifact_generation"`
	ArtifactAppliedGeneration    uint64     `gorm:"not null;default:0" json:"artifact_applied_generation"`
	ArtifactStatus               string     `gorm:"size:32;not null;default:'idle'" json:"artifact_status"`
	ArtifactError                string     `gorm:"size:96;not null;default:''" json:"artifact_error"`
	ArtifactUpdatedAt            *time.Time `json:"artifact_updated_at"`
	ArtifactCleanupRemoved       int        `gorm:"not null;default:0" json:"artifact_cleanup_removed"`
	ArtifactCleanupError         string     `gorm:"size:96;not null;default:''" json:"artifact_cleanup_error"`
	ArtifactCleanupAt            *time.Time `json:"artifact_cleanup_at"`
	IngestEnabled                bool       `gorm:"not null;default:false" json:"ingest_enabled"`
	IngestDownloaderID           *string    `gorm:"size:36;index" json:"ingest_downloader_id,omitempty"`
	IngestOwnerID                *uint      `gorm:"index" json:"-"`
	IngestProviderRootID         string     `gorm:"size:128;not null;default:''" json:"-"`
	IngestRelativeRoot           string     `gorm:"size:2048;not null;default:''" json:"ingest_relative_root"`
	Status                       string     `gorm:"size:32;not null;index" json:"status"`
	StatusErrorCode              string     `gorm:"size:64;not null;default:''" json:"status_error_code"`
	NextRetryAt                  *time.Time `gorm:"index" json:"next_retry_at"`
	LastScanAt                   *time.Time `json:"last_scan_at"`
	LastSuccessfulScanAt         *time.Time `json:"last_successful_scan_at"`
	BaselineGeneration           uint64     `gorm:"not null;default:0" json:"baseline_generation"`
	DirtyGeneration              uint64     `gorm:"not null;default:0" json:"dirty_generation"`
	ReclassificationDue          bool       `gorm:"not null;default:false" json:"reclassification_due"`
	ContentRevision              uint64     `gorm:"not null;default:0" json:"content_revision"`
	CreatedAt                    time.Time  `json:"created_at"`
	UpdatedAt                    time.Time  `json:"updated_at"`
}

const (
	MediaLibraryChangePending = "pending"
	MediaLibraryChangeReady   = "ready"

	MediaLibraryChangeCatalog  = "catalog"
	MediaLibraryChangeMetadata = "metadata"
	MediaLibraryChangeRemoval  = "removal"
)

// MediaLibraryChange is the durable, credential-free outbox shared by media
// server refresh workers and Player long polling. Revision is monotonic within
// a library; Sequence is the global opaque cursor.
type MediaLibraryChange struct {
	Sequence   uint64     `gorm:"primaryKey;autoIncrement" json:"sequence"`
	LibraryID  uint       `gorm:"not null;uniqueIndex:idx_media_library_change_revision;index:idx_media_library_changes_ready,priority:2" json:"library_id"`
	Revision   uint64     `gorm:"not null;uniqueIndex:idx_media_library_change_revision" json:"revision"`
	Kind       string     `gorm:"size:16;not null" json:"kind"`
	State      string     `gorm:"size:16;not null;index:idx_media_library_changes_ready,priority:1" json:"state"`
	Generation uint64     `gorm:"not null;default:0" json:"-"`
	ReadyAt    *time.Time `gorm:"index" json:"ready_at,omitempty"`
	CreatedAt  time.Time  `gorm:"not null;index" json:"created_at"`
}

type MediaServerRefreshTarget struct {
	ID                         uint       `gorm:"primaryKey" json:"id"`
	LibraryID                  uint       `gorm:"not null;uniqueIndex:idx_media_server_refresh_target_identity;index" json:"library_id"`
	ConnectionID               uint       `gorm:"not null;uniqueIndex:idx_media_server_refresh_target_identity;index" json:"connection_id"`
	UpstreamLibraryID          string     `gorm:"size:256;not null;uniqueIndex:idx_media_server_refresh_target_identity" json:"-"`
	UpstreamLibraryName        string     `gorm:"size:256;not null" json:"upstream_library_name"`
	Enabled                    bool       `gorm:"not null;default:true;index" json:"enabled"`
	DesiredRevision            uint64     `gorm:"not null;default:0;index" json:"desired_revision"`
	SuccessfulRevision         uint64     `gorm:"not null;default:0" json:"successful_revision"`
	ManualGeneration           uint64     `gorm:"not null;default:0" json:"-"`
	SuccessfulManualGeneration uint64     `gorm:"not null;default:0" json:"-"`
	LastJobID                  *string    `gorm:"size:36;index" json:"last_job_id,omitempty"`
	LastStatus                 string     `gorm:"size:32;not null;default:'idle';index" json:"last_status"`
	LastErrorCode              string     `gorm:"size:96;not null;default:''" json:"last_error_code"`
	LastAttemptAt              *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessfulAt           *time.Time `json:"last_successful_at,omitempty"`
	Revision                   uint64     `gorm:"not null;default:1" json:"revision"`
	CreatedAt                  time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt                  time.Time  `gorm:"not null" json:"updated_at"`
}

type MediaServerRefreshRun struct {
	ID              string     `gorm:"primaryKey;size:36" json:"id"`
	TargetID        uint       `gorm:"not null;index" json:"target_id"`
	JobID           string     `gorm:"size:36;not null;index" json:"job_id"`
	DesiredRevision uint64     `gorm:"not null" json:"desired_revision"`
	Status          string     `gorm:"size:32;not null;index" json:"status"`
	ErrorCode       string     `gorm:"size:96;not null;default:''" json:"error_code"`
	StartedAt       time.Time  `gorm:"not null" json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

type MediaLibraryScanRun struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	LibraryID         uint       `gorm:"not null;index" json:"library_id"`
	Kind              string     `gorm:"size:24;not null;index" json:"kind"`
	Status            string     `gorm:"size:24;not null;index" json:"status"`
	Generation        uint64     `gorm:"not null" json:"generation"`
	Discovered        int        `gorm:"not null;default:0" json:"discovered"`
	Added             int        `gorm:"not null;default:0" json:"added"`
	Updated           int        `gorm:"not null;default:0" json:"updated"`
	Removed           int        `gorm:"not null;default:0" json:"removed"`
	Matched           int        `gorm:"not null;default:0" json:"matched"`
	Unrecognized      int        `gorm:"not null;default:0" json:"unrecognized"`
	CacheHits         int        `gorm:"not null;default:0" json:"cache_hits"`
	RecognitionFailed int        `gorm:"not null;default:0" json:"recognition_failed"`
	ErrorCode         string     `gorm:"size:64;not null;default:''" json:"error_code"`
	Partial           bool       `gorm:"not null;default:false" json:"partial"`
	StartedAt         time.Time  `gorm:"index" json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
}

type MediaLibraryEntry struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	LibraryID            uint      `gorm:"not null;uniqueIndex:idx_library_path;index:idx_media_library_entries_work,priority:1;index:idx_media_library_entries_search,priority:1;index:idx_media_library_entries_tmdb,priority:1" json:"library_id"`
	RelativePath         string    `gorm:"size:2048;not null;uniqueIndex:idx_library_path" json:"relative_path"`
	ProviderID           string    `gorm:"size:128;not null" json:"-"`
	RecognitionID        *uint     `gorm:"index" json:"recognition_id,omitempty"`
	Size                 int64     `gorm:"not null" json:"size"`
	ModifiedAt           time.Time `json:"modified_at"`
	MediaType            string    `gorm:"size:16;not null;index:idx_media_library_entries_search,priority:2" json:"media_type"`
	Title                string    `gorm:"size:512;not null;index:idx_media_library_entries_search,priority:3" json:"title"`
	WorkKey              string    `gorm:"size:80;not null;default:'';index:idx_media_library_entries_work,priority:2" json:"-"`
	SeriesTitle          string    `gorm:"size:512;not null;default:''" json:"series_title"`
	Season               *int      `json:"season"`
	Episode              *int      `json:"episode"`
	MatchStatus          string    `gorm:"size:24;not null" json:"match_status"`
	TMDBID               *int64    `gorm:"index:idx_media_library_entries_tmdb,priority:2" json:"tmdb_id,omitempty"`
	ReleaseYear          *int      `json:"release_year,omitempty"`
	MatchConfidence      *float64  `json:"match_confidence,omitempty"`
	RecognitionErrorCode string    `gorm:"size:96;not null;default:''" json:"recognition_error_code"`
	CategoryName         string    `gorm:"size:128;not null" json:"category_name"`
	MatchedRuleID        *string   `gorm:"size:128" json:"matched_rule_id"`
	LastGeneration       uint64    `gorm:"not null;index" json:"last_generation"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// MediaLibraryRecognition stores one library-scoped work/package recognition.
// SourceKey, fingerprints and metadata JSON are private projections and must
// never be serialized directly by handlers.
type MediaLibraryRecognition struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	LibraryID        uint      `gorm:"not null;uniqueIndex:idx_library_recognition_source;index" json:"library_id"`
	SourceKey        string    `gorm:"size:80;not null;uniqueIndex:idx_library_recognition_source" json:"-"`
	InputFingerprint string    `gorm:"size:64;not null;index" json:"-"`
	ProfileID        uint      `gorm:"not null;index" json:"profile_id"`
	ProfileRevision  uint64    `gorm:"not null" json:"profile_revision"`
	Status           string    `gorm:"size:24;not null;index" json:"status"`
	ErrorCode        string    `gorm:"size:96;not null;default:''" json:"error_code"`
	MediaType        string    `gorm:"size:16;not null;default:'';index" json:"media_type"`
	Title            string    `gorm:"size:512;not null;default:''" json:"title"`
	ReleaseYear      *int      `json:"release_year,omitempty"`
	TMDBID           *int64    `gorm:"index" json:"tmdb_id,omitempty"`
	Confidence       *float64  `json:"confidence,omitempty"`
	CategoryName     string    `gorm:"size:128;not null;default:''" json:"category_name"`
	MatchedRuleID    *string   `gorm:"size:128" json:"matched_rule_id,omitempty"`
	MetadataJSON     string    `gorm:"type:text;not null;default:'{}'" json:"-"`
	ManualOverride   bool      `gorm:"not null;default:false" json:"manual_override"`
	LastGeneration   uint64    `gorm:"not null;index" json:"last_generation"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// MediaRecognitionCache contains only canonical, credential-free TMDB match
// projections. It never stores upstream responses, URLs, paths or provider IDs.
type MediaRecognitionCache struct {
	LookupKey  string    `gorm:"primaryKey;size:64" json:"-"`
	Status     string    `gorm:"size:24;not null" json:"-"`
	ErrorCode  string    `gorm:"size:96;not null;default:''" json:"-"`
	ResultJSON string    `gorm:"type:text;not null;default:'{}'" json:"-"`
	ExpiresAt  time.Time `gorm:"not null;index" json:"-"`
	CreatedAt  time.Time `json:"-"`
	UpdatedAt  time.Time `json:"-"`
}

const (
	MediaArtifactStatusIdle       = "idle"
	MediaArtifactStatusQueued     = "queued"
	MediaArtifactStatusRunning    = "running"
	MediaArtifactStatusCompleted  = "completed"
	MediaArtifactStatusFailed     = "failed"
	MediaArtifactStatusSuperseded = "superseded"
	MediaArtifactStatusCleanup    = "cleanup"

	MediaArtifactCleanupPending   = "pending"
	MediaArtifactCleanupRunning   = "running"
	MediaArtifactCleanupCompleted = "completed"
	MediaArtifactCleanupFailed    = "failed"
	MediaArtifactCleanupSkipped   = "skipped"

	MediaArtifactKindSTRM        = "strm"
	MediaArtifactKindNFO         = "nfo"
	MediaArtifactKindPoster      = "poster"
	MediaArtifactKindFanart      = "fanart"
	MediaArtifactKindThumb       = "thumb"
	MediaArtifactKindSubtitle    = "subtitle"
	MediaArtifactKindImage       = "image"
	MediaArtifactKindSourceAsset = "source_asset"

	MediaArtifactTargetLocalAdjacent   = "local_adjacent"
	MediaArtifactTargetLocalProjection = "local_projection"
	MediaArtifactTargetProviderSidecar = "provider_sidecar"

	ProxySigningKeyStatusActive   = "active"
	ProxySigningKeyStatusPrevious = "previous"
	ProxySigningKeyStatusRetired  = "retired"
)

// MediaLibrarySourceAsset stores allowlisted source-side companion facts.
// Provider identities and hashes stay private and are never serialized by API
// handlers. Video catalog entries continue to live in MediaLibraryEntry.
type MediaLibrarySourceAsset struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	LibraryID        uint      `gorm:"not null;uniqueIndex:idx_media_library_source_asset_path;index" json:"library_id"`
	Generation       uint64    `gorm:"not null;index" json:"generation"`
	ProviderID       string    `gorm:"size:128;not null;default:''" json:"-"`
	ParentProviderID string    `gorm:"size:128;not null;default:''" json:"-"`
	RelativePath     string    `gorm:"size:2048;not null;uniqueIndex:idx_media_library_source_asset_path" json:"relative_path"`
	Name             string    `gorm:"size:512;not null" json:"name"`
	Extension        string    `gorm:"size:16;not null;index" json:"extension"`
	Size             int64     `gorm:"not null" json:"size"`
	ModifiedAt       time.Time `json:"modified_at"`
	HashHint         string    `gorm:"size:128;not null;default:''" json:"-"`
	Active           bool      `gorm:"not null;default:true;index" json:"active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// MediaArtifactRun is the durable generation-level execution fact. PolicyJSON
// is an immutable private snapshot and the queue payload contains only ID.
type MediaArtifactRun struct {
	ID               string     `gorm:"primaryKey;size:36" json:"id"`
	LibraryID        uint       `gorm:"not null;uniqueIndex:idx_media_artifact_run_generation;index" json:"library_id"`
	Generation       uint64     `gorm:"not null;uniqueIndex:idx_media_artifact_run_generation" json:"generation"`
	JobID            *string    `gorm:"size:36;uniqueIndex" json:"job_id,omitempty"`
	PolicyJSON       string     `gorm:"type:text;not null" json:"-"`
	Status           string     `gorm:"size:32;not null;index" json:"status"`
	ExpectedCount    int        `gorm:"not null;default:0" json:"expected_count"`
	WrittenCount     int        `gorm:"not null;default:0" json:"written_count"`
	UpdatedCount     int        `gorm:"not null;default:0" json:"updated_count"`
	RemovedCount     int        `gorm:"not null;default:0" json:"removed_count"`
	SkippedCount     int        `gorm:"not null;default:0" json:"skipped_count"`
	FailedCount      int        `gorm:"not null;default:0" json:"failed_count"`
	RetryCount       int        `gorm:"not null;default:0" json:"retry_count"`
	ErrorCode        string     `gorm:"size:96;not null;default:''" json:"error_code"`
	CleanupStatus    string     `gorm:"size:32;not null;default:'pending'" json:"cleanup_status"`
	CleanupErrorCode string     `gorm:"size:96;not null;default:''" json:"cleanup_error_code"`
	CleanupAt        *time.Time `json:"cleanup_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// MediaArtifact is the ownership manifest. Only rows with Managed=true may be
// changed or removed by reconciliation; an unmanaged on-disk name collision is
// never adopted implicitly.
type MediaArtifact struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	OpaqueID           string    `gorm:"size:64;not null;uniqueIndex" json:"-"`
	RunID              string    `gorm:"size:36;not null;index" json:"run_id"`
	LibraryID          uint      `gorm:"not null;uniqueIndex:idx_media_artifact_target;index" json:"library_id"`
	SourceIdentity     string    `gorm:"size:96;not null;default:'';index" json:"-"`
	ProviderItemID     string    `gorm:"size:128;not null;default:''" json:"-"`
	ProviderParentID   string    `gorm:"size:128;not null;default:''" json:"-"`
	Kind               string    `gorm:"size:32;not null;index" json:"kind"`
	TargetKind         string    `gorm:"size:32;not null;uniqueIndex:idx_media_artifact_target" json:"target_kind"`
	RelativePath       string    `gorm:"size:2048;not null;uniqueIndex:idx_media_artifact_target" json:"relative_path"`
	ContentFingerprint string    `gorm:"size:64;not null;default:''" json:"-"`
	TargetProviderID   string    `gorm:"size:128;not null;default:''" json:"-"`
	Managed            bool      `gorm:"not null" json:"managed"`
	Active             bool      `gorm:"not null;default:true;index" json:"active"`
	Status             string    `gorm:"size:32;not null;index" json:"status"`
	ErrorCode          string    `gorm:"size:96;not null;default:''" json:"error_code"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ProxySigningKey stores only an encrypted HMAC secret. ID is the public kid;
// the plaintext secret exists only in memory during signing/verification.
type ProxySigningKey struct {
	ID               string     `gorm:"primaryKey;size:32" json:"id"`
	SecretCiphertext string     `gorm:"type:text;not null" json:"-"`
	Status           string     `gorm:"size:16;not null;index" json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
	DeactivatedAt    *time.Time `json:"deactivated_at"`
}

// EmbyProxyGateway binds one fixed Emby upstream to one stable public gateway
// identifier. Client Emby credentials are never persisted here.
type EmbyProxyGateway struct {
	ID                    uint       `gorm:"primaryKey" json:"id"`
	ConnectionID          uint       `gorm:"not null;uniqueIndex" json:"connection_id"`
	PublicID              string     `gorm:"size:64;not null;uniqueIndex" json:"public_id"`
	Enabled               bool       `gorm:"not null;default:false" json:"enabled"`
	ExternalPlayerEnabled bool       `gorm:"not null;default:true" json:"external_player_enabled"`
	FanartEnabled         bool       `gorm:"not null;default:true" json:"fanart_enabled"`
	PolicyRevision        uint64     `gorm:"not null;default:1" json:"policy_revision"`
	LastHealthStatus      string     `gorm:"size:16;not null;default:'unknown'" json:"last_health_status"`
	LastHealthErrorCode   string     `gorm:"size:96;not null;default:''" json:"last_health_error_code"`
	LastHealthCheckedAt   *time.Time `json:"last_health_checked_at"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

func (MediaRecognitionCache) TableName() string { return "media_recognition_cache" }

const (
	JobStatusQueued            = "queued"
	JobStatusRunning           = "running"
	JobStatusWaitingUserAction = "waiting_user_action"
	JobStatusRetryWait         = "retry_wait"
	JobStatusPaused            = "paused"
	JobStatusCompleted         = "completed"
	JobStatusFailed            = "failed"
	JobStatusCancelled         = "cancelled"
)

// Job is the durable scheduling fact. PayloadJSON and CheckpointJSON are private
// worker state and must never be serialized directly by an HTTP handler or event.
type Job struct {
	ID                string     `gorm:"primaryKey;size:36" json:"id"`
	OwnerID           *uint      `gorm:"index" json:"owner_id"`
	CreatedByKind     string     `gorm:"size:16;not null;default:'user'" json:"created_by_kind"`
	JobType           string     `gorm:"size:32;not null;index:idx_jobs_lane,priority:1;index:idx_jobs_claim,priority:1" json:"job_type"`
	Priority          int        `gorm:"not null;index:idx_jobs_lane,priority:2;index:idx_jobs_claim,priority:2" json:"priority"`
	LanePosition      int64      `gorm:"not null;index:idx_jobs_lane,priority:3;index:idx_jobs_claim,priority:5" json:"lane_position"`
	Revision          uint64     `gorm:"not null;default:1" json:"revision"`
	Status            string     `gorm:"size:32;not null;index;index:idx_jobs_claim,priority:3" json:"status"`
	DisplayName       string     `gorm:"size:256;not null" json:"display_name"`
	Provider          string     `gorm:"size:64;not null;default:'';index" json:"provider"`
	ResourceKey       string     `gorm:"size:256;not null;default:'';index" json:"resource_key"`
	CoalescingKey     string     `gorm:"size:256;not null;default:''" json:"-"`
	Generation        uint64     `gorm:"not null;default:1" json:"generation"`
	StartedGeneration uint64     `gorm:"not null;default:0" json:"-"`
	PayloadJSON       string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	CheckpointJSON    string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	Progress          *float64   `json:"progress"`
	ProcessedItems    *int64     `json:"processed_items"`
	TotalItems        *int64     `json:"total_items"`
	Speed             *float64   `json:"speed"`
	ETASeconds        *int64     `json:"eta_seconds"`
	LastErrorCode     string     `gorm:"size:96;not null;default:''" json:"last_error_code"`
	LastErrorMessage  string     `gorm:"size:512;not null;default:''" json:"last_error_message"`
	NextAttemptAt     *time.Time `gorm:"index;index:idx_jobs_claim,priority:4" json:"next_attempt_at"`
	LeaseTokenHash    string     `gorm:"size:64;not null;default:''" json:"-"`
	LeaseExpiresAt    *time.Time `gorm:"index" json:"-"`
	HeartbeatAt       *time.Time `json:"-"`
	CancellationAsked bool       `gorm:"not null;default:false" json:"cancellation_requested"`
	InterruptStatus   string     `gorm:"size:16;not null;default:''" json:"-"`
	AttemptCount      int        `gorm:"not null;default:0" json:"attempt_count"`
	CreatedAt         time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt         time.Time  `gorm:"index" json:"updated_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
}

type JobAttempt struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	JobID            string     `gorm:"size:36;not null;index" json:"job_id"`
	AttemptNumber    int        `gorm:"not null" json:"attempt_number"`
	LeaseTokenHash   string     `gorm:"size:64;not null" json:"-"`
	Status           string     `gorm:"size:32;not null" json:"status"`
	SafeErrorCode    string     `gorm:"size:96;not null;default:''" json:"error_code"`
	SafeErrorMessage string     `gorm:"size:512;not null;default:''" json:"error_message"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

type JobStatusEvent struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	JobID      string    `gorm:"size:36;not null;index" json:"job_id"`
	EventType  string    `gorm:"size:48;not null" json:"event_type"`
	FromStatus string    `gorm:"size:32;not null;default:''" json:"from_status"`
	ToStatus   string    `gorm:"size:32;not null;default:''" json:"to_status"`
	ActorID    *uint     `json:"actor_id"`
	SafeCode   string    `gorm:"size:96;not null;default:''" json:"code"`
	CreatedAt  time.Time `json:"created_at"`
}

type JobActionRequest struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	JobID       string     `gorm:"size:36;not null;index" json:"job_id"`
	Version     uint64     `gorm:"not null" json:"version"`
	ActionType  string     `gorm:"size:64;not null" json:"action_type"`
	Prompt      string     `gorm:"size:512;not null" json:"prompt"`
	OptionsJSON string     `gorm:"type:text;not null" json:"-"`
	PreviewJSON string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Response    string     `gorm:"size:128;not null;default:''" json:"response"`
	RespondedBy *uint      `json:"responded_by"`
	RespondedAt *time.Time `json:"responded_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type QueuePolicy struct {
	JobType             string    `gorm:"primaryKey;size:32" json:"job_type"`
	Concurrency         int       `gorm:"not null" json:"concurrency"`
	ResourceConcurrency int       `gorm:"not null;default:0" json:"resource_concurrency"`
	MaxAttempts         int       `gorm:"not null;default:3" json:"max_attempts"`
	LeaseSeconds        int       `gorm:"not null;default:30" json:"lease_seconds"`
	Revision            uint64    `gorm:"not null;default:1" json:"revision"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

const (
	DownloaderTypeFake                 = "fake"
	DownloaderTypeQBittorrent          = "qbittorrent"
	DownloaderTypePan115Offline        = "pan115_offline"
	DownloaderTypePluginHTTP           = "plugin_http"
	DownloadTaskStatusQueued           = "queued"
	DownloadTaskStatusSubmitting       = "submitting"
	DownloadTaskStatusResolving        = "resolving"
	DownloadTaskStatusMetadata         = "metadata"
	DownloadTaskStatusClassifying      = "classifying"
	DownloadTaskStatusWaiting          = "waiting_user_action"
	DownloadTaskStatusCategorized      = "categorized"
	DownloadTaskStatusDownloading      = "downloading"
	DownloadTaskStatusVerifying        = "verifying"
	DownloadTaskStatusMerging          = "merging"
	DownloadTaskStatusPaused           = "paused"
	DownloadTaskStatusCompleted        = "completed"
	DownloadTaskStatusFailed           = "failed"
	DownloadTaskStatusCancelled        = "cancelled"
	DownloadSourceOriginUser           = "user"
	DownloadSourceOriginShare          = "share"
	DownloadSourceOriginProviderIngest = "provider_ingest"
	DownloadSourceOriginPlugin         = "plugin"
)

// Downloader stores only encrypted credentials. Public APIs must use an
// allowlisted DTO and never serialize this model directly.
type Downloader struct {
	ID                    string     `gorm:"primaryKey;size:36" json:"id"`
	OwnerID               uint       `gorm:"not null;default:0;index" json:"-"`
	Name                  string     `gorm:"size:128;not null" json:"name"`
	NameNormalized        string     `gorm:"size:128;not null;uniqueIndex" json:"-"`
	Type                  string     `gorm:"size:32;not null;index" json:"type"`
	BaseURL               string     `gorm:"size:2048;not null;default:''" json:"base_url"`
	UsernameCiphertext    string     `gorm:"type:text;not null;default:''" json:"-"`
	PasswordCiphertext    string     `gorm:"type:text;not null;default:''" json:"-"`
	StorageID             *uint      `gorm:"index" json:"storage_id"`
	ProviderDirectoryID   string     `gorm:"size:128;not null;default:''" json:"-"`
	ProviderDirectoryPath string     `gorm:"size:2048;not null;default:''" json:"-"`
	AutoListenLifeEvents  bool       `gorm:"not null;default:false" json:"auto_listen_life_events"`
	Enabled               bool       `gorm:"not null;default:true" json:"enabled"`
	CapabilitiesJSON      string     `gorm:"type:text;not null" json:"-"`
	LastHealthStatus      string     `gorm:"size:24;not null;default:'unknown'" json:"-"`
	LastHealthVersion     string     `gorm:"size:64;not null;default:''" json:"-"`
	LastHealthErrorCode   string     `gorm:"size:96;not null;default:''" json:"-"`
	LastHealthCheckedAt   *time.Time `json:"-"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// DownloadSettings is the singleton Server-wide local staging boundary. It is
// intentionally separate from downloader connections and final MediaLibraries.
type DownloadSettings struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	StorageID    *uint     `gorm:"index" json:"storage_id"`
	RelativePath string    `gorm:"size:1024;not null;default:'/'" json:"relative_path"`
	AbsolutePath string    `gorm:"type:text;not null;default:''" json:"-"`
	Revision     uint64    `gorm:"not null;default:1" json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const (
	SeedingCompletionAll = "all"
	SeedingCompletionAny = "any"
)

// SeedingSettings is the singleton default cleanup policy. Enabled controls
// automatic provider cleanup only; copy/symlink imports are still represented
// in seeding management when cleanup is disabled.
type SeedingSettings struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	Enabled            bool      `gorm:"not null;default:false" json:"enabled"`
	MinimumSeedMinutes int       `gorm:"not null;default:1440" json:"minimum_seed_minutes"`
	MinimumRatio       float64   `gorm:"not null;default:1" json:"minimum_ratio"`
	CompletionMode     string    `gorm:"size:8;not null;default:'all'" json:"completion_mode"`
	Revision           uint64    `gorm:"not null;default:1" json:"revision"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// MetadataSettings stores an encrypted TMDB credential with an explicit kind.
// Public settings DTOs expose configured state, never the ciphertext.
type MetadataSettings struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	TMDBTokenCiphertext string    `gorm:"type:text;not null;default:''" json:"-"`
	TMDBCredentialKind  string    `gorm:"size:32;not null;default:'read_access_token'" json:"-"`
	APIBaseURL          string    `gorm:"size:2048;not null" json:"api_base_url"`
	ImageBaseURL        string    `gorm:"size:2048;not null" json:"image_base_url"`
	Revision            uint64    `gorm:"not null;default:1" json:"revision"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// AIRecognitionSettings is the singleton, opt-in Server media-recognition AI
// configuration. APIKeyCiphertext is never serialized; runtime callers must go
// through the settings service so disabled means no decryption or network use.
type AIRecognitionSettings struct {
	ID                    uint      `gorm:"primaryKey" json:"id"`
	Enabled               bool      `gorm:"not null;default:false" json:"enabled"`
	ProviderType          string    `gorm:"size:32;not null;default:'openai_compatible'" json:"provider_type"`
	BaseURL               string    `gorm:"size:2048;not null;default:''" json:"base_url"`
	APIKeyCiphertext      string    `gorm:"type:text;not null;default:''" json:"-"`
	Model                 string    `gorm:"size:256;not null;default:''" json:"model"`
	SendRelativeBasenames bool      `gorm:"not null;default:false" json:"send_relative_basenames"`
	Revision              uint64    `gorm:"not null;default:1" json:"revision"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// DiscoveryCache stores only credential-free provider projections. Cached
// upstream payloads, request URLs and headers are never persisted.
type DiscoveryCache struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Provider    string    `gorm:"size:32;not null;uniqueIndex:idx_discovery_cache_identity" json:"provider"`
	Section     string    `gorm:"size:64;not null;uniqueIndex:idx_discovery_cache_identity" json:"section"`
	Locale      string    `gorm:"size:32;not null;uniqueIndex:idx_discovery_cache_identity" json:"locale"`
	Page        int       `gorm:"not null;uniqueIndex:idx_discovery_cache_identity" json:"page"`
	PayloadJSON string    `gorm:"type:text;not null" json:"-"`
	FreshUntil  time.Time `gorm:"not null;index" json:"fresh_until"`
	StaleUntil  time.Time `gorm:"not null;index" json:"stale_until"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Site is an administrator-managed PT connection. CredentialCiphertext holds
// the cookie/passkey envelope and is never serialized or copied to jobs.
type Site struct {
	ID                   uint       `gorm:"primaryKey" json:"id"`
	Name                 string     `gorm:"size:128;not null" json:"name"`
	NameNormalized       string     `gorm:"size:128;not null;uniqueIndex" json:"-"`
	Kind                 string     `gorm:"size:32;not null;index" json:"kind"`
	BaseURL              string     `gorm:"size:2048;not null" json:"base_url"`
	CredentialCiphertext string     `gorm:"type:text;not null" json:"-"`
	UserAgent            string     `gorm:"size:256;not null;default:''" json:"user_agent"`
	BrowserEmulation     bool       `gorm:"not null;default:false" json:"browser_emulation"`
	BrowserServiceURL    string     `gorm:"size:2048;not null;default:''" json:"-"`
	Enabled              bool       `gorm:"not null;default:true;index" json:"enabled"`
	Priority             int        `gorm:"not null;default:100;index" json:"priority"`
	TimeoutSeconds       int        `gorm:"not null;default:12" json:"timeout_seconds"`
	RateLimitPerMinute   int        `gorm:"not null;default:12" json:"rate_limit_per_minute"`
	LastHealthStatus     string     `gorm:"size:16;not null;default:'unknown'" json:"last_health_status"`
	LastHealthErrorCode  string     `gorm:"size:96;not null;default:''" json:"last_health_error_code"`
	LastHealthUsername   string     `gorm:"size:128;not null;default:''" json:"last_health_username"`
	LastHealthCheckedAt  *time.Time `json:"last_health_checked_at"`
	Revision             uint64     `gorm:"not null;default:1" json:"revision"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// CookieCloudSettings is the singleton site-credential synchronization policy.
// UUID, password and local upload authentication are stored only in the
// encrypted credential envelope.
type CookieCloudSettings struct {
	ID                   uint       `gorm:"primaryKey" json:"id"`
	Mode                 string     `gorm:"size:16;not null;default:'disabled'" json:"mode"`
	BaseURL              string     `gorm:"size:2048;not null;default:''" json:"base_url"`
	CredentialCiphertext string     `gorm:"type:text;not null;default:''" json:"-"`
	AutoSyncMinutes      int        `gorm:"not null;default:0" json:"auto_sync_minutes"`
	LastSyncStatus       string     `gorm:"size:24;not null;default:'never'" json:"last_sync_status"`
	LastSyncErrorCode    string     `gorm:"size:96;not null;default:''" json:"last_sync_error_code"`
	LastSyncAt           *time.Time `json:"last_sync_at"`
	Revision             uint64     `gorm:"not null;default:1" json:"revision"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// CookieCloudPayload stores the already end-to-end encrypted extension blob.
// The UUID itself is represented only by a hash so local uploads cannot be
// enumerated through the database.
type CookieCloudPayload struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	UUIDHash         string    `gorm:"size:64;not null;default:''" json:"-"`
	EncryptedPayload string    `gorm:"type:text;not null;default:''" json:"-"`
	CryptoType       string    `gorm:"size:32;not null;default:'legacy'" json:"-"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// DownloadTask is the durable provider fact linked one-to-one to a queue Job.
// SourceCiphertext may contain PT passkeys and is never exposed or logged.
type DownloadTask struct {
	ID                                 string     `gorm:"primaryKey;size:36" json:"id"`
	OwnerID                            uint       `gorm:"not null;index" json:"owner_id"`
	JobID                              string     `gorm:"size:36;not null;uniqueIndex" json:"job_id"`
	DownloaderID                       *string    `gorm:"size:36;index" json:"downloader_id"`
	DownloaderName                     string     `gorm:"size:128;not null" json:"downloader_name"`
	ProviderType                       string     `gorm:"size:32;not null;index" json:"provider_type"`
	ProviderTaskID                     string     `gorm:"size:256;not null;default:'';index" json:"-"`
	ProviderOutputID                   string     `gorm:"size:128;not null;default:''" json:"-"`
	ProviderTag                        string     `gorm:"size:96;not null;default:''" json:"-"`
	SourceCiphertext                   string     `gorm:"type:text;not null" json:"-"`
	StagingStorageID                   *uint      `gorm:"index" json:"-"`
	StagingRelativePath                string     `gorm:"size:1024;not null;default:''" json:"-"`
	StagingAbsolutePath                string     `gorm:"type:text;not null;default:''" json:"-"`
	StagingProviderDirectoryID         string     `gorm:"size:128;not null;default:''" json:"-"`
	IngestSourceKey                    string     `gorm:"size:64;not null;default:''" json:"-"`
	SourceOrigin                       string     `gorm:"size:24;not null;default:'user'" json:"-"`
	FollowSubscriptionID               string     `gorm:"size:36;not null;default:'';index" json:"-"`
	FollowResourceFingerprint          string     `gorm:"size:64;not null;default:''" json:"-"`
	PluginID                           string     `gorm:"size:128;not null;default:'';index" json:"-"`
	PluginVersion                      string     `gorm:"size:128;not null;default:''" json:"-"`
	PluginConnectionID                 string     `gorm:"size:36;not null;default:'';index" json:"-"`
	ProviderMetadataJSON               string     `gorm:"type:text;not null;default:''" json:"-"`
	ProfileID                          uint       `gorm:"not null;default:0" json:"-"`
	ProfileRevision                    uint64     `gorm:"not null;default:0" json:"-"`
	ProfileRulesJSON                   string     `gorm:"type:text;not null;default:''" json:"-"`
	ProfileBuiltinRecognitionPacksJSON string     `gorm:"type:text;not null;default:'[\"tv-v1\",\"anime-v1\"]'" json:"-"`
	ProfileRecognitionRulesJSON        string     `gorm:"type:text;not null;default:'[]'" json:"-"`
	TargetLibraryID                    *uint      `gorm:"index" json:"-"`
	TargetLibraryName                  string     `gorm:"size:128;not null;default:''" json:"-"`
	TargetStorageID                    *uint      `gorm:"index" json:"-"`
	TargetStorageType                  string     `gorm:"size:32;not null;default:''" json:"-"`
	TargetConnectionID                 *uint      `gorm:"index" json:"-"`
	TargetProviderRootID               string     `gorm:"size:128;not null;default:''" json:"-"`
	TargetStorageRoot                  string     `gorm:"type:text;not null;default:''" json:"-"`
	TargetRelativeRoot                 string     `gorm:"size:1024;not null;default:''" json:"-"`
	TransferMode                       string     `gorm:"size:16;not null;default:''" json:"-"`
	ConflictPolicy                     string     `gorm:"size:16;not null;default:''" json:"-"`
	MovieDirectoryTemplate             string     `gorm:"size:512;not null;default:''" json:"-"`
	MovieFilenameTemplate              string     `gorm:"size:512;not null;default:''" json:"-"`
	TVDirectoryTemplate                string     `gorm:"size:512;not null;default:''" json:"-"`
	TVFilenameTemplate                 string     `gorm:"size:512;not null;default:''" json:"-"`
	SeedingCleanupEnabled              bool       `gorm:"not null;default:false" json:"-"`
	SeedingMinimumMinutes              int        `gorm:"not null;default:1440" json:"-"`
	SeedingMinimumRatio                float64    `gorm:"not null;default:1" json:"-"`
	SeedingCompletionMode              string     `gorm:"size:8;not null;default:'all'" json:"-"`
	DisplayName                        string     `gorm:"size:256;not null" json:"display_name"`
	ProviderStatus                     string     `gorm:"size:64;not null;default:''" json:"provider_status"`
	Phase                              string     `gorm:"size:32;not null" json:"phase"`
	Progress                           *float64   `json:"progress"`
	BytesCompleted                     *int64     `json:"bytes_completed"`
	BytesTotal                         *int64     `json:"bytes_total"`
	DownloadSpeed                      *int64     `json:"download_speed"`
	UploadSpeed                        *int64     `json:"upload_speed"`
	ETASeconds                         *int64     `json:"eta_seconds"`
	LastSampledAt                      *time.Time `json:"last_sampled_at"`
	LastErrorCode                      string     `gorm:"size:96;not null;default:''" json:"last_error_code"`
	LastErrorMessage                   string     `gorm:"size:512;not null;default:''" json:"last_error_message"`
	ScrapeStatus                       string     `gorm:"size:32;not null;default:''" json:"-"`
	ScrapeTitle                        string     `gorm:"size:256;not null;default:''" json:"-"`
	ScrapeMediaType                    string     `gorm:"size:16;not null;default:''" json:"-"`
	ScrapeCategory                     string     `gorm:"size:128;not null;default:''" json:"-"`
	ScrapeTMDBID                       *int64     `json:"-"`
	ScrapeYear                         *int       `json:"-"`
	ScrapeSeason                       *int       `json:"-"`
	ScrapeEpisode                      *int       `json:"-"`
	ScrapeConfidence                   *float64   `json:"-"`
	RecognitionOverrideTMDBID          *int64     `json:"-"`
	RecognitionOverrideMediaType       string     `gorm:"size:16;not null;default:''" json:"-"`
	RecognitionOverrideSeason          *int       `json:"-"`
	RecognitionOverrideEpisode         *int       `json:"-"`
	IdentitySource                     string     `gorm:"size:32;not null;default:''" json:"-"`
	IdentityStatus                     string     `gorm:"size:32;not null;default:''" json:"-"`
	IdentityLocked                     bool       `gorm:"not null;default:false" json:"-"`
	IdentityRevision                   uint64     `gorm:"not null;default:0" json:"-"`
	IdentitySnapshotJSON               string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	// CompletedManifestJSON is a private provider-relative snapshot captured
	// after authoritative completion. It lets recognition recovery continue
	// without resubmitting or re-polling an already completed download.
	CompletedManifestJSON string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	StagingCategory       string     `gorm:"size:128;not null;default:''" json:"-"`
	ManifestFileCount     int        `gorm:"not null;default:0" json:"-"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	FinishedAt            *time.Time `json:"finished_at"`
}

const (
	FollowStatusActive    = "active"
	FollowStatusPaused    = "paused"
	FollowStatusCompleted = "completed"
	FollowStatusBlocked   = "blocked"

	FollowRunQueued    = "queued"
	FollowRunRunning   = "running"
	FollowRunNoMatch   = "no_match"
	FollowRunSubmitted = "submitted"
	FollowRunCompleted = "completed"
	FollowRunFailed    = "failed"
	FollowRunCancelled = "cancelled"
	FollowRunStale     = "stale"
)

// FollowSubscription stores only stable references and a credential-free,
// versioned execution snapshot. Runtime jobs copy that snapshot into FollowRun
// so ordinary edits cannot change work already in progress.
type FollowSubscription struct {
	ID                    string     `gorm:"primaryKey;size:36" json:"id"`
	OwnerID               uint       `gorm:"not null;index" json:"owner_id"`
	MediaType             string     `gorm:"size:8;not null" json:"media_type"`
	TMDBID                int64      `gorm:"not null;index" json:"tmdb_id"`
	Title                 string     `gorm:"size:256;not null" json:"title"`
	Year                  *int       `json:"year,omitempty"`
	PosterRef             string     `gorm:"size:1024;not null;default:''" json:"poster_ref,omitempty"`
	Status                string     `gorm:"size:16;not null;index" json:"status"`
	Revision              uint64     `gorm:"not null;default:1" json:"revision"`
	LifecycleRevision     uint64     `gorm:"not null;default:1" json:"-"`
	ExecutionSnapshotJSON string     `gorm:"type:text;not null" json:"-"`
	ProgressTarget        int        `gorm:"not null;default:0" json:"progress_target"`
	ProgressPresent       int        `gorm:"not null;default:0" json:"progress_present"`
	ProgressMissing       int        `gorm:"not null;default:0" json:"progress_missing"`
	LastRunID             *string    `gorm:"size:36" json:"last_run_id,omitempty"`
	LastRunAt             *time.Time `json:"last_run_at,omitempty"`
	NextRunAt             *time.Time `gorm:"index" json:"next_run_at,omitempty"`
	LastErrorCode         string     `gorm:"size:96;not null;default:''" json:"last_error_code"`
	LastErrorMessage      string     `gorm:"size:256;not null;default:''" json:"last_error_message"`
	CreatedAt             time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt             time.Time  `gorm:"not null" json:"updated_at"`
}

type FollowSubscriptionSeason struct {
	SubscriptionID string `gorm:"primaryKey;size:36" json:"subscription_id"`
	OwnerID        uint   `gorm:"not null;uniqueIndex:idx_follow_owner_season,priority:1" json:"owner_id"`
	TMDBID         int64  `gorm:"not null;uniqueIndex:idx_follow_owner_season,priority:2" json:"tmdb_id"`
	SeasonNumber   int    `gorm:"primaryKey;uniqueIndex:idx_follow_owner_season,priority:3" json:"season_number"`
	Special        bool   `gorm:"not null;default:false" json:"special"`
}

type FollowRun struct {
	ID                    string     `gorm:"primaryKey;size:36" json:"id"`
	SubscriptionID        string     `gorm:"size:36;not null;index" json:"subscription_id"`
	OwnerID               uint       `gorm:"not null;index" json:"owner_id"`
	SubscriptionRevision  uint64     `gorm:"not null" json:"subscription_revision"`
	LifecycleRevision     uint64     `gorm:"not null" json:"-"`
	ExecutionSnapshotJSON string     `gorm:"type:text;not null" json:"-"`
	JobID                 string     `gorm:"size:36;not null;uniqueIndex" json:"job_id"`
	Trigger               string     `gorm:"size:16;not null" json:"trigger"`
	Status                string     `gorm:"size:16;not null;index" json:"status"`
	MissingSnapshotJSON   string     `gorm:"type:text;not null;default:'[]'" json:"-"`
	SearchedNamesCount    int        `gorm:"not null;default:0" json:"searched_names_count"`
	Candidates            int        `gorm:"not null;default:0" json:"candidates"`
	Selected              int        `gorm:"not null;default:0" json:"selected"`
	FilterSummaryJSON     string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	ErrorCode             string     `gorm:"size:96;not null;default:''" json:"error_code"`
	ErrorMessage          string     `gorm:"size:256;not null;default:''" json:"error_message"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
	CreatedAt             time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt             time.Time  `gorm:"not null" json:"updated_at"`
}

type FollowEpisodeClaim struct {
	SubscriptionID      string    `gorm:"primaryKey;size:36" json:"subscription_id"`
	SeasonNumber        int       `gorm:"primaryKey" json:"season_number"`
	EpisodeNumber       int       `gorm:"primaryKey" json:"episode_number"`
	State               string    `gorm:"size:16;not null" json:"state"`
	RunID               *string   `gorm:"size:36;index" json:"run_id,omitempty"`
	DownloadTaskID      *string   `gorm:"size:36;index" json:"download_task_id,omitempty"`
	ResourceFingerprint string    `gorm:"size:64;not null;default:''" json:"resource_fingerprint"`
	UpdatedAt           time.Time `gorm:"not null" json:"updated_at"`
}

const (
	TransferTaskStatusQueued              = "queued"
	TransferTaskStatusPlanning            = "planning"
	TransferTaskStatusCheckingDirectories = "checking_directories"
	TransferTaskStatusCreatingDirectories = "creating_directories"
	TransferTaskStatusCheckingConflicts   = "checking_conflicts"
	TransferTaskStatusMoving              = "moving"
	TransferTaskStatusRenaming            = "renaming"
	TransferTaskStatusRiskBackoff         = "risk_backoff"
	TransferTaskStatusTransferring        = "transferring"
	TransferTaskStatusReconciling         = "reconciling"
	TransferTaskStatusCompleted           = "completed"
	TransferTaskStatusFailed              = "failed"

	TransferCleanupPending   = "pending"
	TransferCleanupDeferred  = "deferred"
	TransferCleanupRunning   = "running"
	TransferCleanupCompleted = "completed"
	TransferCleanupFailed    = "failed"
	TransferCleanupSkipped   = "skipped"
)

// TransferTask is the private durable import fact. ManifestJSON contains
// provider-relative media names and must never be serialized by an API.
type TransferTask struct {
	ID                 string     `gorm:"primaryKey;size:36" json:"id"`
	OwnerID            uint       `gorm:"not null;index" json:"owner_id"`
	JobID              string     `gorm:"size:36;not null;uniqueIndex" json:"job_id"`
	DownloadTaskID     string     `gorm:"size:36;not null;uniqueIndex" json:"download_task_id"`
	LibraryID          uint       `gorm:"not null;index" json:"library_id"`
	LibraryName        string     `gorm:"size:128;not null" json:"library_name"`
	ManifestJSON       string     `gorm:"type:text;not null" json:"-"`
	SourceManifestJSON string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	PlanSummaryJSON    string     `gorm:"type:text;not null;default:''" json:"-"`
	CloudStateJSON     string     `gorm:"type:text;not null;default:''" json:"-"`
	Phase              string     `gorm:"size:32;not null;index" json:"phase"`
	ProcessedFiles     int        `gorm:"not null;default:0" json:"processed_files"`
	TotalFiles         int        `gorm:"not null;default:0" json:"total_files"`
	LastErrorCode      string     `gorm:"size:96;not null;default:''" json:"last_error_code"`
	CleanupStatus      string     `gorm:"size:32;not null;default:'pending';index" json:"cleanup_status"`
	CleanupRemoved     int        `gorm:"not null;default:0" json:"cleanup_removed"`
	CleanupErrorCode   string     `gorm:"size:96;not null;default:''" json:"cleanup_error_code"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}

const (
	MediaManagedItemKindVideo   = "video"
	MediaManagedItemKindSidecar = "sidecar"

	MediaReorganizationPhaseQueued      = "queued"
	MediaReorganizationPhaseExecuting   = "executing"
	MediaReorganizationPhaseReconciling = "reconciling"
	MediaReorganizationPhaseCompleted   = "completed"
	MediaReorganizationPhaseFailed      = "failed"
)

// MediaManagedItem is the durable ownership boundary for imported media.
// Reorganization may only mutate active rows with Managed=true. Provider IDs
// and transfer provenance are private and must never be returned by handlers.
type MediaManagedItem struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	OpaqueID         string    `gorm:"size:64;not null;uniqueIndex" json:"-"`
	LibraryID        uint      `gorm:"not null;uniqueIndex:idx_media_managed_item_target;index" json:"library_id"`
	TransferTaskID   string    `gorm:"size:36;not null;index" json:"-"`
	DownloadTaskID   string    `gorm:"size:36;not null;index" json:"-"`
	IdentityRevision uint64    `gorm:"not null" json:"identity_revision"`
	Kind             string    `gorm:"size:16;not null" json:"kind"`
	RelativePath     string    `gorm:"size:2048;not null;uniqueIndex:idx_media_managed_item_target" json:"relative_path"`
	ProviderItemID   string    `gorm:"size:128;not null;default:''" json:"-"`
	ProviderParentID string    `gorm:"size:128;not null;default:''" json:"-"`
	Size             int64     `gorm:"not null;default:0" json:"size"`
	Managed          bool      `gorm:"not null;default:true" json:"managed"`
	Active           bool      `gorm:"not null;default:true;index" json:"active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// MediaReorganizationPreview stores the private, immutable plan behind an
// opaque one-time token. PlanJSON may contain provider identities and paths and
// therefore must never be serialized directly.
type MediaReorganizationPreview struct {
	ID                     string     `gorm:"primaryKey;size:36" json:"-"`
	TokenHash              string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ActorID                uint       `gorm:"not null;index" json:"-"`
	LibraryID              uint       `gorm:"not null;index" json:"library_id"`
	TransferTaskID         string     `gorm:"size:36;not null;index" json:"-"`
	SourceIdentityRevision uint64     `gorm:"not null" json:"source_identity_revision"`
	TargetIdentityJSON     string     `gorm:"type:text;not null" json:"-"`
	ManagedManifestDigest  string     `gorm:"size:64;not null" json:"-"`
	RuleRevision           uint64     `gorm:"not null" json:"rule_revision"`
	ConflictPolicy         string     `gorm:"size:16;not null" json:"conflict_policy"`
	PlanJSON               string     `gorm:"type:text;not null" json:"-"`
	ExpiresAt              time.Time  `gorm:"not null;index" json:"expires_at"`
	ConsumedAt             *time.Time `json:"-"`
	CreatedAt              time.Time  `json:"-"`
}

// MediaReorganizationTask is restart-safe private worker state. The public API
// exposes only relative plan projections and stable error codes.
type MediaReorganizationTask struct {
	ID                     string     `gorm:"primaryKey;size:36" json:"id"`
	OwnerID                uint       `gorm:"not null;index" json:"owner_id"`
	JobID                  string     `gorm:"size:36;not null;uniqueIndex" json:"job_id"`
	LibraryID              uint       `gorm:"not null;index" json:"library_id"`
	TransferTaskID         string     `gorm:"size:36;not null;index" json:"-"`
	SourceIdentityRevision uint64     `gorm:"not null" json:"source_identity_revision"`
	TargetIdentityRevision uint64     `gorm:"not null" json:"target_identity_revision"`
	TargetIdentityJSON     string     `gorm:"type:text;not null" json:"-"`
	ManagedManifestDigest  string     `gorm:"size:64;not null" json:"-"`
	RuleRevision           uint64     `gorm:"not null" json:"rule_revision"`
	ConflictPolicy         string     `gorm:"size:16;not null" json:"conflict_policy"`
	PlanJSON               string     `gorm:"type:text;not null" json:"-"`
	StateJSON              string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	Phase                  string     `gorm:"size:24;not null;index" json:"phase"`
	TotalItems             int        `gorm:"not null;default:0" json:"total_items"`
	ProcessedItems         int        `gorm:"not null;default:0" json:"processed_items"`
	LastErrorCode          string     `gorm:"size:96;not null;default:''" json:"last_error_code"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	FinishedAt             *time.Time `json:"finished_at"`
}

const (
	TransferDeletionScopeRecordOnly             = "record_only"
	TransferDeletionScopeRecordAndSource        = "record_and_source"
	TransferDeletionScopeRecordAndLibrary       = "record_and_library"
	TransferDeletionScopeRecordSourceAndLibrary = "record_source_and_library"
)

// TransferDeletionPreview binds one explicit destructive choice to the exact
// pipeline and ownership facts observed during preview. Only TokenHash is
// persisted; StateJSON is private recovery state for partial executions.
type TransferDeletionPreview struct {
	ID                    string     `gorm:"primaryKey;size:36" json:"-"`
	TokenHash             string     `gorm:"size:64;not null;uniqueIndex" json:"-"`
	ActorID               uint       `gorm:"not null;index" json:"-"`
	TransferTaskID        string     `gorm:"size:36;not null;index" json:"-"`
	DownloadTaskID        string     `gorm:"size:36;not null;index" json:"-"`
	LibraryID             uint       `gorm:"not null;index" json:"-"`
	Scope                 string     `gorm:"size:40;not null" json:"-"`
	IdentityRevision      uint64     `gorm:"not null" json:"-"`
	SourceManifestDigest  string     `gorm:"size:64;not null" json:"-"`
	ManagedManifestDigest string     `gorm:"size:64;not null" json:"-"`
	TransferJobRevision   uint64     `gorm:"not null" json:"-"`
	DownloadJobRevision   uint64     `gorm:"not null" json:"-"`
	SeedingJobRevision    uint64     `gorm:"not null;default:0" json:"-"`
	StateJSON             string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	LastErrorCode         string     `gorm:"size:96;not null;default:''" json:"-"`
	ExpiresAt             time.Time  `gorm:"not null;index" json:"-"`
	ConsumedAt            *time.Time `json:"-"`
	CompletedAt           *time.Time `json:"-"`
	CreatedAt             time.Time  `gorm:"not null" json:"-"`
	UpdatedAt             time.Time  `gorm:"not null" json:"-"`
}

const (
	SeedingTaskStatusQueued    = "queued"
	SeedingTaskStatusSeeding   = "seeding"
	SeedingTaskStatusCleanup   = "cleanup"
	SeedingTaskStatusRetained  = "retained"
	SeedingTaskStatusCompleted = "completed"
	SeedingTaskStatusFailed    = "failed"
)

// SeedingTask is the durable, provider-neutral cleanup fact. ProviderTaskID is
// private and no source path is persisted here; DeleteData is allowed for copy
// only when the transfer manifests contain no protected unselected media.
type SeedingTask struct {
	ID                 string     `gorm:"primaryKey;size:36" json:"id"`
	OwnerID            uint       `gorm:"not null;index" json:"owner_id"`
	JobID              string     `gorm:"size:36;not null;uniqueIndex" json:"job_id"`
	DownloadTaskID     string     `gorm:"size:36;not null;uniqueIndex" json:"download_task_id"`
	DownloaderID       *string    `gorm:"size:36;index" json:"-"`
	DownloaderName     string     `gorm:"size:128;not null" json:"downloader_name"`
	ProviderType       string     `gorm:"size:32;not null" json:"provider_type"`
	ProviderTaskID     string     `gorm:"size:256;not null" json:"-"`
	TransferMode       string     `gorm:"size:16;not null" json:"transfer_mode"`
	DeleteData         bool       `gorm:"not null" json:"delete_data"`
	CleanupEnabled     bool       `gorm:"not null" json:"cleanup_enabled"`
	MinimumSeedMinutes int        `gorm:"not null" json:"minimum_seed_minutes"`
	MinimumRatio       float64    `gorm:"not null" json:"minimum_ratio"`
	CompletionMode     string     `gorm:"size:8;not null" json:"completion_mode"`
	Phase              string     `gorm:"size:24;not null;index" json:"phase"`
	Ratio              *float64   `json:"ratio"`
	SeededSeconds      *int64     `json:"seeded_seconds"`
	UploadedBytes      *int64     `json:"uploaded_bytes"`
	LastSampledAt      *time.Time `json:"last_sampled_at"`
	LastErrorCode      string     `gorm:"size:96;not null;default:''" json:"last_error_code"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}
