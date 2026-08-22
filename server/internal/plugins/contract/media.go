package contract

type LifecycleState string

const (
	LifecycleDiscovered      LifecycleState = "discovered"
	LifecycleValidating      LifecycleState = "validating"
	LifecycleInstalled       LifecycleState = "installed"
	LifecycleDisabled        LifecycleState = "disabled"
	LifecycleStarting        LifecycleState = "starting"
	LifecycleEnabled         LifecycleState = "enabled"
	LifecycleUnhealthy       LifecycleState = "unhealthy"
	LifecycleUpgrading       LifecycleState = "upgrading"
	LifecycleRollbackPending LifecycleState = "rollback-pending"
	LifecycleFailed          LifecycleState = "failed"
)

type StreamVariant struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Available         bool   `json:"available"`
	Width             int    `json:"width,omitempty"`
	Height            int    `json:"height,omitempty"`
	Bitrate           int64  `json:"bitrate,omitempty"`
	VideoCodec        string `json:"videoCodec,omitempty"`
	AudioCodec        string `json:"audioCodec,omitempty"`
	DynamicRange      string `json:"dynamicRange,omitempty"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
}

type MediaVersion struct {
	ID           string          `json:"id"`
	Label        string          `json:"label"`
	SourceLabel  string          `json:"sourceLabel,omitempty"`
	Edition      string          `json:"edition,omitempty"`
	ReleaseGroup string          `json:"releaseGroup,omitempty"`
	Variants     []StreamVariant `json:"variants"`
}

type MediaSegment struct {
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Index         int            `json:"index"`
	SeasonNumber  *int           `json:"seasonNumber,omitempty"`
	EpisodeNumber *int           `json:"episodeNumber,omitempty"`
	Versions      []MediaVersion `json:"versions"`
}

type PlaybackAsset struct {
	Kind       string `json:"kind"`
	URLRef     string `json:"urlRef"`
	HeadersRef string `json:"headersRef,omitempty"`
}

type PlaybackPlan struct {
	WorkID       string            `json:"workId"`
	SegmentID    string            `json:"segmentId"`
	VersionID    string            `json:"versionId"`
	VariantID    string            `json:"variantId"`
	Variants     []StreamVariant   `json:"variants"`
	Assets       []PlaybackAsset   `json:"assets"`
	Delivery     string            `json:"delivery"`
	ExpiresAt    string            `json:"expiresAt,omitempty"`
	RefreshToken string            `json:"refreshToken,omitempty"`
	Subtitles    []TrackDescriptor `json:"subtitles,omitempty"`
	Danmaku      []TrackDescriptor `json:"danmaku,omitempty"`
}

type TrackDescriptor struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Language string `json:"language,omitempty"`
	Format   string `json:"format,omitempty"`
	URLRef   string `json:"urlRef"`
}

type DownloadAsset struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	URLRef              string `json:"urlRef"`
	HeadersRef          string `json:"headersRef,omitempty"`
	ExpectedContentType string `json:"expectedContentType,omitempty"`
	ExpectedBytes       int64  `json:"expectedBytes,omitempty"`
}

type DownloadMerge struct {
	Kind         string `json:"kind"`
	VideoAssetID string `json:"videoAssetId"`
	AudioAssetID string `json:"audioAssetId"`
}

type DownloadPlan struct {
	WorkID            string          `json:"workId"`
	SegmentID         string          `json:"segmentId"`
	VersionID         string          `json:"versionId"`
	VariantID         string          `json:"variantId"`
	SuggestedFileName string          `json:"suggestedFileName"`
	Assets            []DownloadAsset `json:"assets"`
	Merge             *DownloadMerge  `json:"merge,omitempty"`
}
