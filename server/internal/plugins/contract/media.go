package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

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
	ID                string  `json:"id"`
	Label             string  `json:"label"`
	Available         bool    `json:"available"`
	Width             int     `json:"width,omitempty"`
	Height            int     `json:"height,omitempty"`
	Bitrate           int64   `json:"bitrate,omitempty"`
	VideoCodec        string  `json:"videoCodec,omitempty"`
	AudioCodec        string  `json:"audioCodec,omitempty"`
	DynamicRange      string  `json:"dynamicRange,omitempty"`
	FrameRate         float64 `json:"frameRate,omitempty"`
	Container         string  `json:"container,omitempty"`
	HDR               bool    `json:"hdr,omitempty"`
	DolbyVision       bool    `json:"dolbyVision,omitempty"`
	DolbyAtmos        bool    `json:"dolbyAtmos,omitempty"`
	UnavailableReason string  `json:"unavailableReason,omitempty"`
}

type MediaVersion struct {
	ID             string          `json:"id"`
	Label          string          `json:"label"`
	SourceLabel    string          `json:"sourceLabel,omitempty"`
	Edition        string          `json:"edition,omitempty"`
	ReleaseGroup   string          `json:"releaseGroup,omitempty"`
	Resolution     string          `json:"resolution,omitempty"`
	SourceMedium   string          `json:"sourceMedium,omitempty"`
	ReleaseKind    string          `json:"releaseKind,omitempty"`
	DynamicRange   string          `json:"dynamicRange,omitempty"`
	VideoCodec     string          `json:"videoCodec,omitempty"`
	AudioCodec     string          `json:"audioCodec,omitempty"`
	AudioLanguages []string        `json:"audioLanguages,omitempty"`
	SizeBytes      int64           `json:"sizeBytes,omitempty"`
	Delivery       string          `json:"delivery,omitempty"`
	Variants       []StreamVariant `json:"variants"`
}

type MediaSegment struct {
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Index         int            `json:"index"`
	SeasonNumber  *int           `json:"seasonNumber,omitempty"`
	EpisodeNumber *int           `json:"episodeNumber,omitempty"`
	Versions      []MediaVersion `json:"versions"`
}

type MediaIdentity struct {
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
}

type MediaWork struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Kind            string         `json:"kind"`
	Identity        MediaIdentity  `json:"identity"`
	OriginalTitle   string         `json:"originalTitle,omitempty"`
	Overview        string         `json:"overview,omitempty"`
	PosterURL       string         `json:"posterUrl,omitempty"`
	BackdropURL     string         `json:"backdropUrl,omitempty"`
	Author          string         `json:"author,omitempty"`
	PublishedAt     string         `json:"publishedAt,omitempty"`
	DurationSeconds int64          `json:"durationSeconds,omitempty"`
	Segments        []MediaSegment `json:"segments,omitempty"`
}

type LibraryArtworkCandidate struct {
	ID       string `json:"id"`
	AssetRef string `json:"assetRef"`
}

func NormalizeLibraryArtworkCandidates(data []byte) ([]LibraryArtworkCandidate, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var candidates []LibraryArtworkCandidate
	if err := decoder.Decode(&candidates); err != nil || len(candidates) == 0 || len(candidates) > 9 {
		return nil, errors.New("library artwork candidates are invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("library artwork candidates contain trailing data")
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if !safeDTOText(candidate.ID, 512) {
			return nil, errors.New("library artwork candidate identity is invalid")
		}
		if _, err := uuid.Parse(candidate.AssetRef); err != nil {
			return nil, errors.New("library artwork candidate asset is invalid")
		}
		if _, exists := seen[candidate.ID]; exists {
			return nil, errors.New("library artwork candidate identity is duplicated")
		}
		seen[candidate.ID] = struct{}{}
	}
	return candidates, nil
}

type SiteActionDescriptor struct {
	ID                   string `json:"id"`
	Label                string `json:"label"`
	State                *bool  `json:"state,omitempty"`
	RequiresConfirmation bool   `json:"requiresConfirmation,omitempty"`
	Destructive          bool   `json:"destructive,omitempty"`
}

var knownSiteActionIDs = map[string]struct{}{
	"like.add": {}, "like.remove": {}, "favorite.add": {}, "favorite.remove": {},
	"watch-later.add": {}, "watch-later.remove": {}, "follow.add": {}, "follow.remove": {}, "history.remove": {},
}

func (action *SiteActionDescriptor) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var id string
		if err := json.Unmarshal(data, &id); err != nil || !safeDTOText(id, 64) {
			return errors.New("site action is invalid")
		}
		action.ID, action.Label = id, id
		return nil
	}
	type alias SiteActionDescriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value alias
	if err := decoder.Decode(&value); err != nil || !safeDTOText(value.ID, 64) || !safeDTOText(value.Label, 128) {
		return errors.New("site action is invalid")
	}
	*action = SiteActionDescriptor(value)
	return nil
}

type FeedItem struct {
	Work    MediaWork              `json:"work"`
	Actions []SiteActionDescriptor `json:"actions,omitempty"`
}

type FeedSection struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Layout         string     `json:"layout"`
	Items          []FeedItem `json:"items"`
	Cursor         string     `json:"cursor,omitempty"`
	RefreshSession string     `json:"refreshSession,omitempty"`
	HomeEligible   bool       `json:"homeEligible,omitempty"`
	Refreshable    bool       `json:"refreshable,omitempty"`
}

func NormalizeFeedSections(data []byte, refreshSession string) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var sections []FeedSection
	if err := decoder.Decode(&sections); err != nil || len(sections) == 0 || len(sections) > 32 {
		return nil, errors.New("feed sections are invalid")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return nil, errors.New("feed contains trailing data")
	}
	seenSections := make(map[string]struct{}, len(sections))
	for index := range sections {
		section := &sections[index]
		if !safeDTOText(section.ID, 256) || !safeDTOText(section.Title, 256) || len(section.Items) > 100 || !safeOptionalDTOText(section.Cursor, 512) {
			return nil, errors.New("feed section is invalid")
		}
		switch section.Layout {
		case "hero", "row", "poster-grid", "video-list":
		default:
			return nil, errors.New("feed layout is invalid")
		}
		if _, exists := seenSections[section.ID]; exists {
			return nil, errors.New("feed section id is duplicated")
		}
		seenSections[section.ID] = struct{}{}
		if refreshSession != "" {
			section.RefreshSession = refreshSession
		} else if !safeOptionalDTOText(section.RefreshSession, 512) {
			return nil, errors.New("feed refresh session is invalid")
		}
		for _, item := range section.Items {
			if err := validateMediaWork(item.Work); err != nil || len(item.Actions) > 32 {
				return nil, errors.New("feed item is invalid")
			}
			for _, action := range item.Actions {
				if _, known := knownSiteActionIDs[action.ID]; !known || !safeDTOText(action.Label, 128) {
					return nil, errors.New("feed action is invalid")
				}
			}
		}
	}
	return json.Marshal(sections)
}

func validateMediaWork(work MediaWork) error {
	if !safeDTOText(work.ID, 512) || !safeDTOText(work.Title, 512) || !safeDTOText(work.Identity.Scheme, 128) || !safeDTOText(work.Identity.Value, 512) || work.DurationSeconds < 0 || work.DurationSeconds > 365*24*60*60 || len(work.Segments) > 1000 {
		return errors.New("media work is invalid")
	}
	switch work.Kind {
	case "movie", "series", "episode", "video", "live", "creator", "collection":
	default:
		return errors.New("media kind is invalid")
	}
	for _, rawURL := range []string{work.PosterURL, work.BackdropURL} {
		if rawURL == "" {
			continue
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" || parsed.Fragment != "" || len(rawURL) > 2048 {
			return errors.New("media image URL is invalid")
		}
	}
	for _, segment := range work.Segments {
		if !safeDTOText(segment.ID, 512) || !safeDTOText(segment.Title, 512) || len(segment.Versions) == 0 || len(segment.Versions) > 64 {
			return errors.New("media segment is invalid")
		}
		for _, version := range segment.Versions {
			if !safeDTOText(version.ID, 512) || !safeDTOText(version.Label, 256) || len(version.Variants) == 0 || len(version.Variants) > 32 || version.SizeBytes < 0 {
				return errors.New("media version is invalid")
			}
			for _, variant := range version.Variants {
				if !safeDTOText(variant.ID, 512) || !safeDTOText(variant.Label, 256) || variant.Width < 0 || variant.Height < 0 || variant.Bitrate < 0 || variant.FrameRate < 0 {
					return errors.New("stream variant is invalid")
				}
			}
		}
	}
	return nil
}

type PlaybackAsset struct {
	Kind       string `json:"kind"`
	URLRef     string `json:"urlRef"`
	HeadersRef string `json:"headersRef,omitempty"`
}

type PlaybackPlan struct {
	WorkID         string            `json:"workId"`
	SegmentID      string            `json:"segmentId"`
	VersionID      string            `json:"versionId"`
	VariantID      string            `json:"variantId"`
	Variants       []StreamVariant   `json:"variants"`
	Assets         []PlaybackAsset   `json:"assets"`
	Delivery       string            `json:"delivery"`
	ExpiresAt      string            `json:"expiresAt,omitempty"`
	RefreshToken   string            `json:"refreshToken,omitempty"`
	SelectionToken string            `json:"selectionToken,omitempty"`
	Subtitles      []TrackDescriptor `json:"subtitles,omitempty"`
	Danmaku        []TrackDescriptor `json:"danmaku,omitempty"`
}

func ValidatePlaybackPlan(plan PlaybackPlan, now time.Time) error {
	if !safeDTOText(plan.WorkID, 512) || !safeDTOText(plan.SegmentID, 512) || !safeDTOText(plan.VersionID, 512) || !safeDTOText(plan.VariantID, 512) {
		return errors.New("playback identity is invalid")
	}
	if plan.Delivery != "direct" && plan.Delivery != "server-gateway" && plan.Delivery != "loopback-bridge" {
		return errors.New("playback delivery is invalid")
	}
	if len(plan.Assets) == 0 || len(plan.Assets) > 8 || len(plan.Variants) > 32 || len(plan.Subtitles) > 64 || len(plan.Danmaku) > 8 {
		return errors.New("playback collection size is invalid")
	}
	seenAssets := map[string]struct{}{}
	for _, asset := range plan.Assets {
		switch asset.Kind {
		case "progressive", "hls", "dash-video", "dash-audio":
		default:
			return errors.New("playback asset kind is invalid")
		}
		if _, err := uuid.Parse(asset.URLRef); err != nil || (asset.HeadersRef != "" && !safeDTOText(asset.HeadersRef, 512)) {
			return errors.New("playback asset reference is invalid")
		}
		seenAssets[asset.Kind] = struct{}{}
	}
	_, dashVideo := seenAssets["dash-video"]
	_, dashAudio := seenAssets["dash-audio"]
	if dashVideo != dashAudio || (dashVideo && len(seenAssets) != 2) {
		return errors.New("DASH playback requires one video and one audio asset")
	}
	if plan.ExpiresAt != "" {
		expiresAt, err := time.Parse(time.RFC3339Nano, plan.ExpiresAt)
		if err != nil || !expiresAt.After(now.UTC()) || expiresAt.After(now.UTC().Add(24*time.Hour)) {
			return errors.New("playback expiry is invalid")
		}
	}
	if !safeOptionalDTOText(plan.RefreshToken, 1024) || !safeOptionalDTOText(plan.SelectionToken, 1024) {
		return errors.New("playback selection token is invalid")
	}
	for _, track := range append(append([]TrackDescriptor(nil), plan.Subtitles...), plan.Danmaku...) {
		if !safeDTOText(track.ID, 256) || !safeDTOText(track.Label, 256) {
			return errors.New("playback track is invalid")
		}
		if _, err := uuid.Parse(track.URLRef); err != nil {
			return errors.New("playback track reference is invalid")
		}
	}
	return nil
}

func safeDTOText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func safeOptionalDTOText(value string, maximum int) bool {
	return value == "" || safeDTOText(value, maximum)
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

type ProviderArtwork struct {
	Kind     string `json:"kind"`
	AssetRef string `json:"assetRef"`
}

type ProviderMetadataSnapshot struct {
	Version         int               `json:"version"`
	WorkID          string            `json:"workId"`
	SegmentID       string            `json:"segmentId"`
	Kind            string            `json:"kind"`
	Title           string            `json:"title"`
	OriginalTitle   string            `json:"originalTitle,omitempty"`
	Overview        string            `json:"overview,omitempty"`
	Author          string            `json:"author,omitempty"`
	PublishedAt     string            `json:"publishedAt,omitempty"`
	DurationSeconds int64             `json:"durationSeconds,omitempty"`
	SeasonNumber    *int              `json:"seasonNumber,omitempty"`
	EpisodeNumber   *int              `json:"episodeNumber,omitempty"`
	Genres          []string          `json:"genres,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	UniqueIDs       map[string]string `json:"uniqueIds"`
	Artwork         []ProviderArtwork `json:"artwork,omitempty"`
}

func ValidateProviderMetadataSnapshot(snapshot ProviderMetadataSnapshot, workID, segmentID string) error {
	if snapshot.Version != 1 || snapshot.WorkID != workID || snapshot.SegmentID != segmentID || !safeDTOText(snapshot.Title, 512) || !safeOptionalDTOText(snapshot.OriginalTitle, 512) || !safeOptionalDTOText(snapshot.Overview, 16*1024) || !safeOptionalDTOText(snapshot.Author, 512) || snapshot.DurationSeconds < 0 || snapshot.DurationSeconds > 365*24*60*60 {
		return errors.New("provider metadata identity is invalid")
	}
	switch snapshot.Kind {
	case "movie", "series", "episode", "video":
	default:
		return errors.New("provider metadata kind is invalid")
	}
	if snapshot.PublishedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, snapshot.PublishedAt); err != nil || parsed.Year() < 1900 || parsed.After(time.Now().UTC().Add(24*time.Hour)) {
			return errors.New("provider metadata published time is invalid")
		}
	}
	if len(snapshot.UniqueIDs) == 0 || len(snapshot.UniqueIDs) > 16 || len(snapshot.Genres) > 32 || len(snapshot.Tags) > 64 || len(snapshot.Artwork) > 4 {
		return errors.New("provider metadata collection size is invalid")
	}
	for key, value := range snapshot.UniqueIDs {
		if !safeDTOText(key, 64) || !safeDTOText(value, 512) {
			return errors.New("provider metadata unique id is invalid")
		}
	}
	for _, value := range append(append([]string(nil), snapshot.Genres...), snapshot.Tags...) {
		if !safeDTOText(value, 128) {
			return errors.New("provider metadata label is invalid")
		}
	}
	seenArtwork := make(map[string]struct{}, len(snapshot.Artwork))
	for _, artwork := range snapshot.Artwork {
		if artwork.Kind != "poster" && artwork.Kind != "fanart" {
			return errors.New("provider metadata artwork kind is invalid")
		}
		if _, err := uuid.Parse(artwork.AssetRef); err != nil {
			return errors.New("provider metadata artwork reference is invalid")
		}
		if _, duplicate := seenArtwork[artwork.Kind]; duplicate {
			return errors.New("provider metadata artwork kind is duplicated")
		}
		seenArtwork[artwork.Kind] = struct{}{}
	}
	return nil
}
