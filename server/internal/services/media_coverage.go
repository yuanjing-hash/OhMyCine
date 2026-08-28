package services

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

const maxCoverageSeasons = 200

type MediaCoverageService struct {
	db       *gorm.DB
	metadata *MetadataSettingsService
	now      func() time.Time
}

type MediaCoverageLibrary struct {
	ID               uint       `json:"id"`
	Name             string     `json:"name"`
	ScanState        string     `json:"scan_state"`
	LastSuccessfulAt *time.Time `json:"last_successful_at,omitempty"`
	ContentRevision  uint64     `json:"content_revision"`
}

type MediaCoverageFreshness struct {
	CheckedAt        time.Time `json:"checked_at"`
	LibraryScanState string    `json:"library_scan_state"`
	TMDBState        string    `json:"tmdb_state"`
}

type MediaCoverageEpisode struct {
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name,omitempty"`
	AirDate       string `json:"air_date,omitempty"`
	Status        string `json:"status"`
	LibraryIDs    []uint `json:"library_ids"`
}

type MediaCoverageCounts struct {
	Total   int `json:"total"`
	Present int `json:"present"`
	Missing int `json:"missing"`
	Future  int `json:"future"`
	Unknown int `json:"unknown"`
}

type MediaCoverageSeason struct {
	SeasonNumber int                    `json:"season_number"`
	Name         string                 `json:"name"`
	PosterURL    string                 `json:"poster_url,omitempty"`
	Special      bool                   `json:"special"`
	Status       string                 `json:"status"`
	Counts       MediaCoverageCounts    `json:"counts"`
	Episodes     []MediaCoverageEpisode `json:"episodes"`
}

type MediaCoverage struct {
	MediaType string                 `json:"media_type"`
	TMDBID    int64                  `json:"tmdb_id"`
	Title     string                 `json:"title"`
	Status    string                 `json:"status"`
	Libraries []MediaCoverageLibrary `json:"libraries"`
	Freshness MediaCoverageFreshness `json:"freshness"`
	Movie     *struct {
		Present    bool   `json:"present"`
		LibraryIDs []uint `json:"library_ids"`
	} `json:"movie,omitempty"`
	TV *struct {
		Counts  MediaCoverageCounts   `json:"counts"`
		Seasons []MediaCoverageSeason `json:"seasons"`
	} `json:"tv,omitempty"`
}

func NewMediaCoverageService(db *gorm.DB, metadata *MetadataSettingsService) *MediaCoverageService {
	return &MediaCoverageService{db: db, metadata: metadata, now: func() time.Time { return time.Now().UTC() }}
}

func (s *MediaCoverageService) Coverage(ctx context.Context, actor Actor, mediaType string, tmdbID int64) (MediaCoverage, error) {
	if !actor.Can(authz.PermissionDiscoveryRead) || !actor.Can(authz.PermissionMediaLibrariesRead) {
		return MediaCoverage{}, appError(CodePermissionDenied, "无权查看媒体库覆盖率", nil)
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if tmdbID <= 0 || (mediaType != "movie" && mediaType != "tv") {
		return MediaCoverage{}, appError(CodeInvalidRequest, "媒体覆盖率身份无效", nil)
	}
	if s.metadata == nil {
		return MediaCoverage{}, appError(CodeTMDBUnavailable, "TMDB 详情服务暂时不可用", nil)
	}
	client, err := s.metadata.Client()
	if err != nil {
		return MediaCoverage{}, appError(CodeTMDBUnavailable, "TMDB 详情服务暂时不可用", nil)
	}
	verified, err := client.GetByID(ctx, mediaType, tmdbID, "zh-CN")
	if err != nil {
		return MediaCoverage{}, appError(tmdb.ErrorCode(err), "TMDB 作品身份无法解析", nil)
	}
	libraries, reliable, scanState, err := s.coverageLibraries()
	if err != nil {
		return MediaCoverage{}, err
	}
	result := MediaCoverage{MediaType: mediaType, TMDBID: tmdbID, Title: verified.Title, Status: "unknown", Libraries: libraries, Freshness: MediaCoverageFreshness{CheckedAt: s.now(), LibraryScanState: scanState, TMDBState: "complete"}}
	entries, err := s.coverageEntries(mediaType, tmdbID, libraries)
	if err != nil {
		return MediaCoverage{}, err
	}
	if mediaType == "movie" {
		ids := uniqueEntryLibraries(entries, nil, nil)
		result.Movie = &struct {
			Present    bool   `json:"present"`
			LibraryIDs []uint `json:"library_ids"`
		}{Present: len(ids) > 0, LibraryIDs: ids}
		switch {
		case len(ids) > 0:
			result.Status = "present"
		case reliable:
			result.Status = "missing"
		default:
			result.Status = "unknown"
		}
		normalizeMediaCoverageCollections(&result)
		return result, nil
	}

	presence := make(map[[2]int][]uint)
	for _, entry := range entries {
		if entry.Season == nil || entry.Episode == nil || *entry.Season < 0 || *entry.Episode <= 0 {
			continue
		}
		key := [2]int{*entry.Season, *entry.Episode}
		presence[key] = appendUniqueUint(presence[key], entry.LibraryID)
	}
	seasons := append([]tmdb.SeasonSnapshot(nil), verified.Snapshot.Seasons...)
	if len(seasons) > maxCoverageSeasons {
		seasons = seasons[:maxCoverageSeasons]
		result.Freshness.TMDBState = "partial"
	}
	sort.SliceStable(seasons, func(left, right int) bool { return seasons[left].SeasonNumber < seasons[right].SeasonNumber })
	seasonEpisodes := make([][]tmdb.EpisodeSnapshot, len(seasons))
	seasonErrors := make([]bool, len(seasons))
	semaphore := make(chan struct{}, 4)
	var wait sync.WaitGroup
	for index, season := range seasons {
		index, season := index, season
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				seasonErrors[index] = true
				return
			}
			episodes, fetchErr := client.GetTVSeasonEpisodes(ctx, tmdbID, season.SeasonNumber, "zh-CN")
			if fetchErr != nil {
				seasonErrors[index] = true
				return
			}
			seasonEpisodes[index] = episodes
		}()
	}
	wait.Wait()
	if ctx.Err() != nil {
		return MediaCoverage{}, ctx.Err()
	}
	tv := &struct {
		Counts  MediaCoverageCounts   `json:"counts"`
		Seasons []MediaCoverageSeason `json:"seasons"`
	}{Seasons: make([]MediaCoverageSeason, 0, len(seasons))}
	for index, season := range seasons {
		projection := MediaCoverageSeason{SeasonNumber: season.SeasonNumber, Name: season.Name, Special: season.SeasonNumber == 0, Status: "unknown", Episodes: []MediaCoverageEpisode{}}
		if upstream, imageErr := client.ImageURL(season.PosterPath, "w300"); imageErr == nil {
			projection.PosterURL = proxyDiscoveryImage("tmdb", upstream)
		}
		if seasonErrors[index] || len(seasonEpisodes[index]) == 0 {
			projection.Counts.Unknown = max(0, season.EpisodeCount)
			projection.Counts.Total = projection.Counts.Unknown
			result.Freshness.TMDBState = "partial"
			tv.Seasons = append(tv.Seasons, projection)
			if !projection.Special {
				tv.Counts.Total += projection.Counts.Total
				tv.Counts.Unknown += projection.Counts.Unknown
			}
			continue
		}
		episodes := append([]tmdb.EpisodeSnapshot(nil), seasonEpisodes[index]...)
		sort.SliceStable(episodes, func(left, right int) bool { return episodes[left].EpisodeNumber < episodes[right].EpisodeNumber })
		seenEpisodes := make(map[int]struct{}, len(episodes))
		for _, episode := range episodes {
			if _, duplicate := seenEpisodes[episode.EpisodeNumber]; duplicate {
				result.Freshness.TMDBState = "partial"
				continue
			}
			seenEpisodes[episode.EpisodeNumber] = struct{}{}
			ids := append([]uint{}, presence[[2]int{season.SeasonNumber, episode.EpisodeNumber}]...)
			sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
			item := MediaCoverageEpisode{EpisodeNumber: episode.EpisodeNumber, Name: episode.Name, AirDate: episode.AirDate, LibraryIDs: ids}
			switch {
			case len(ids) > 0:
				item.Status = "present"
			case coverageAirDateState(episode.AirDate, s.now()) == "future":
				item.Status = "future"
			case coverageAirDateState(episode.AirDate, s.now()) == "aired" && reliable:
				item.Status = "missing"
			default:
				item.Status = "unknown"
			}
			projection.Episodes = append(projection.Episodes, item)
			incrementCoverageCount(&projection.Counts, item.Status)
		}
		// TMDB occasionally returns a syntactically valid but truncated season
		// payload. Never silently shrink the declared season: retain the known
		// episode rows and account for the unavailable remainder as unknown.
		if missingFacts := max(0, season.EpisodeCount-projection.Counts.Total); missingFacts > 0 {
			projection.Counts.Total += missingFacts
			projection.Counts.Unknown += missingFacts
			result.Freshness.TMDBState = "partial"
		}
		projection.Status = coverageSeasonStatus(projection.Counts)
		tv.Seasons = append(tv.Seasons, projection)
		if !projection.Special {
			addCoverageCounts(&tv.Counts, projection.Counts)
		}
	}
	result.TV = tv
	result.Status = coverageSeasonStatus(tv.Counts)
	normalizeMediaCoverageCollections(&result)
	return result, nil
}

func normalizeMediaCoverageCollections(result *MediaCoverage) {
	if result.Libraries == nil {
		result.Libraries = []MediaCoverageLibrary{}
	}
	if result.Movie != nil && result.Movie.LibraryIDs == nil {
		result.Movie.LibraryIDs = []uint{}
	}
	if result.TV == nil {
		return
	}
	if result.TV.Seasons == nil {
		result.TV.Seasons = []MediaCoverageSeason{}
	}
	for seasonIndex := range result.TV.Seasons {
		season := &result.TV.Seasons[seasonIndex]
		if season.Episodes == nil {
			season.Episodes = []MediaCoverageEpisode{}
		}
		for episodeIndex := range season.Episodes {
			episode := &season.Episodes[episodeIndex]
			if episode.LibraryIDs == nil {
				episode.LibraryIDs = []uint{}
			}
		}
	}
}

func (s *MediaCoverageService) coverageLibraries() ([]MediaCoverageLibrary, bool, string, error) {
	var records []models.MediaLibrary
	if err := s.db.Where("enabled = ?", true).Order("sort_order ASC,id ASC").Find(&records).Error; err != nil {
		return nil, false, "unknown", err
	}
	result := make([]MediaCoverageLibrary, 0, len(records))
	reliable := len(records) > 0
	anyScanned := false
	anyPartial := false
	for _, record := range records {
		state := "unscanned"
		var run models.MediaLibraryScanRun
		runErr := s.db.Where("library_id = ?", record.ID).Order("id DESC").First(&run).Error
		if record.BaselineGeneration > 0 && record.LastSuccessfulScanAt != nil {
			anyScanned = true
			if runErr == nil && (run.Status != "success" || run.Partial) {
				state, anyPartial, reliable = "partial", true, false
			} else {
				state = "complete"
			}
		} else {
			reliable = false
		}
		result = append(result, MediaCoverageLibrary{ID: record.ID, Name: record.Name, ScanState: state, LastSuccessfulAt: record.LastSuccessfulScanAt, ContentRevision: record.ContentRevision})
	}
	aggregate := "unscanned"
	if reliable {
		aggregate = "complete"
	} else if anyPartial || anyScanned {
		aggregate = "partial"
	}
	return result, reliable, aggregate, nil
}

func (s *MediaCoverageService) coverageEntries(mediaType string, tmdbID int64, libraries []MediaCoverageLibrary) ([]models.MediaLibraryEntry, error) {
	ids := make([]uint, 0, len(libraries))
	for _, library := range libraries {
		ids = append(ids, library.ID)
	}
	if len(ids) == 0 {
		return []models.MediaLibraryEntry{}, nil
	}
	var entries []models.MediaLibraryEntry
	err := s.db.Where("library_id IN ? AND media_type = ? AND tmdb_id = ? AND match_status = ?", ids, mediaType, tmdbID, mediaRecognitionStatusMatched).Find(&entries).Error
	return entries, err
}

func coverageAirDateState(value string, now time.Time) string {
	date, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return "unknown"
	}
	today := now.UTC().Truncate(24 * time.Hour)
	if date.After(today) {
		return "future"
	}
	return "aired"
}

func coverageSeasonStatus(counts MediaCoverageCounts) string {
	switch {
	case counts.Total == 0 || counts.Unknown == counts.Total:
		return "unknown"
	case counts.Present == counts.Total:
		return "present"
	case counts.Present > 0 && counts.Missing > 0:
		return "partial"
	case counts.Missing > 0 && counts.Present == 0 && counts.Future == 0 && counts.Unknown == 0:
		return "missing"
	case counts.Future > 0 || counts.Unknown > 0:
		return "future_or_incomplete"
	default:
		return "partial"
	}
}

func incrementCoverageCount(counts *MediaCoverageCounts, status string) {
	counts.Total++
	switch status {
	case "present":
		counts.Present++
	case "missing":
		counts.Missing++
	case "future":
		counts.Future++
	default:
		counts.Unknown++
	}
}

func addCoverageCounts(target *MediaCoverageCounts, source MediaCoverageCounts) {
	target.Total += source.Total
	target.Present += source.Present
	target.Missing += source.Missing
	target.Future += source.Future
	target.Unknown += source.Unknown
}

func appendUniqueUint(values []uint, value uint) []uint {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func uniqueEntryLibraries(entries []models.MediaLibraryEntry, season, episode *int) []uint {
	result := []uint{}
	for _, entry := range entries {
		if season != nil && (entry.Season == nil || *entry.Season != *season) {
			continue
		}
		if episode != nil && (entry.Episode == nil || *entry.Episode != *episode) {
			continue
		}
		result = appendUniqueUint(result, entry.LibraryID)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}
