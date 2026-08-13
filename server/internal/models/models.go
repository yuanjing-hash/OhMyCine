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

const StorageTypeLocal = "local"

// Storage is a registered provider root. It does not classify media or choose a
// final placement; those responsibilities belong to later library/destination domains.
type Storage struct {
	ID                  uint       `gorm:"primaryKey" json:"id"`
	Name                string     `gorm:"size:128;not null" json:"name"`
	NameNormalized      string     `gorm:"size:128;not null;uniqueIndex" json:"-"`
	Type                string     `gorm:"size:32;not null" json:"type"`
	RootPath            string     `gorm:"type:text;not null" json:"root_path"`
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

const (
	MediaClassificationProfileKindSystem = "system"
	MediaClassificationProfileKindCustom = "custom"
)

// MediaClassificationProfile stores logical post-identification grouping
// rules. It is intentionally separate from download/import CategoryRule.
type MediaClassificationProfile struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	Code           *string   `gorm:"size:64;uniqueIndex" json:"code"`
	Name           string    `gorm:"size:128;not null" json:"name"`
	NameNormalized string    `gorm:"size:128;not null;uniqueIndex" json:"-"`
	Kind           string    `gorm:"size:16;not null" json:"kind"`
	Protected      bool      `gorm:"not null;default:false" json:"protected"`
	SchemaVersion  int       `gorm:"not null" json:"schema_version"`
	RulesJSON      string    `gorm:"type:text;not null" json:"-"`
	Revision       uint64    `gorm:"not null;default:1" json:"revision"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const (
	MediaLibraryStatusDisabled             = "disabled"
	MediaLibraryStatusInitializing         = "initializing"
	MediaLibraryStatusInitializationFailed = "initialization_failed"
	MediaLibraryStatusAttachingListener    = "attaching_listener"
	MediaLibraryStatusReconciling          = "catch_up_reconciliation"
	MediaLibraryStatusListening            = "listening"
)

type MediaLibrary struct {
	ID                    uint       `gorm:"primaryKey" json:"id"`
	Name                  string     `gorm:"size:128;not null" json:"name"`
	NameNormalized        string     `gorm:"size:128;not null;uniqueIndex" json:"-"`
	StorageID             uint       `gorm:"not null;index" json:"storage_id"`
	ProfileID             uint       `gorm:"not null;index" json:"profile_id"`
	ProfileRevision       uint64     `gorm:"not null" json:"profile_revision"`
	RelativeRoot          string     `gorm:"size:1024;not null" json:"relative_root"`
	Enabled               bool       `gorm:"not null" json:"enabled"`
	Recursive             bool       `gorm:"not null" json:"recursive"`
	FullScanIntervalHours int        `gorm:"not null;default:24" json:"full_scan_interval_hours"`
	IncrementalMinutes    int        `gorm:"not null;default:15" json:"incremental_minutes"`
	VideoExtensionsJSON   string     `gorm:"type:text;not null" json:"-"`
	IgnorePatternsJSON    string     `gorm:"type:text;not null" json:"-"`
	MetadataLanguage      string     `gorm:"size:16;not null;default:'zh-CN'" json:"metadata_language"`
	MetadataRegion        string     `gorm:"size:8;not null;default:'CN'" json:"metadata_region"`
	MatchStrategy         string     `gorm:"size:32;not null;default:'balanced'" json:"match_strategy"`
	ProviderRatePerSecond int        `gorm:"not null;default:100" json:"provider_rate_per_second"`
	ProviderConcurrency   int        `gorm:"not null;default:2" json:"provider_concurrency"`
	MetadataRatePerSecond int        `gorm:"not null;default:5" json:"metadata_rate_per_second"`
	MetadataConcurrency   int        `gorm:"not null;default:1" json:"metadata_concurrency"`
	STRMEnabled           bool       `gorm:"not null;default:false" json:"strm_enabled"`
	STRMLocalRoot         string     `gorm:"type:text;not null;default:''" json:"-"`
	Status                string     `gorm:"size:32;not null;index" json:"status"`
	StatusErrorCode       string     `gorm:"size:64;not null;default:''" json:"status_error_code"`
	NextRetryAt           *time.Time `gorm:"index" json:"next_retry_at"`
	LastScanAt            *time.Time `json:"last_scan_at"`
	LastSuccessfulScanAt  *time.Time `json:"last_successful_scan_at"`
	BaselineGeneration    uint64     `gorm:"not null;default:0" json:"baseline_generation"`
	DirtyGeneration       uint64     `gorm:"not null;default:0" json:"dirty_generation"`
	ReclassificationDue   bool       `gorm:"not null;default:false" json:"reclassification_due"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type MediaLibraryScanRun struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	LibraryID  uint       `gorm:"not null;index" json:"library_id"`
	Kind       string     `gorm:"size:24;not null;index" json:"kind"`
	Status     string     `gorm:"size:24;not null;index" json:"status"`
	Generation uint64     `gorm:"not null" json:"generation"`
	Discovered int        `gorm:"not null;default:0" json:"discovered"`
	Added      int        `gorm:"not null;default:0" json:"added"`
	Updated    int        `gorm:"not null;default:0" json:"updated"`
	Removed    int        `gorm:"not null;default:0" json:"removed"`
	ErrorCode  string     `gorm:"size:64;not null;default:''" json:"error_code"`
	Partial    bool       `gorm:"not null;default:false" json:"partial"`
	StartedAt  time.Time  `gorm:"index" json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type MediaLibraryEntry struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	LibraryID      uint      `gorm:"not null;uniqueIndex:idx_library_path" json:"library_id"`
	RelativePath   string    `gorm:"size:2048;not null;uniqueIndex:idx_library_path" json:"relative_path"`
	ProviderID     string    `gorm:"size:128;not null" json:"provider_id"`
	Size           int64     `gorm:"not null" json:"size"`
	ModifiedAt     time.Time `json:"modified_at"`
	MediaType      string    `gorm:"size:16;not null" json:"media_type"`
	Title          string    `gorm:"size:512;not null" json:"title"`
	Season         *int      `json:"season"`
	Episode        *int      `json:"episode"`
	MatchStatus    string    `gorm:"size:24;not null" json:"match_status"`
	CategoryName   string    `gorm:"size:128;not null" json:"category_name"`
	MatchedRuleID  *string   `gorm:"size:128" json:"matched_rule_id"`
	LastGeneration uint64    `gorm:"not null;index" json:"last_generation"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
