package services

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

const (
	maxPlayerEpisodeMetadata        = 1000
	maxPlayerEpisodeSeasonsPerFetch = 8
)

type playerEpisodeKey struct {
	season  int
	episode int
}

func (s *MediaLibraryService) playerEpisodeMetadata(ctx context.Context, libraryID uint, workKey string, entries []models.MediaLibraryEntry) map[playerEpisodeKey]tmdb.EpisodeSnapshot {
	wanted := make(map[playerEpisodeKey]struct{}, len(entries))
	for _, entry := range entries {
		season, episode := resolvedCatalogEpisodeFacts(entry)
		if episode == nil || *episode <= 0 {
			continue
		}
		key := playerEpisodeKey{episode: *episode}
		if season != nil {
			key.season = *season
		}
		wanted[key] = struct{}{}
	}
	if len(wanted) == 0 {
		return map[playerEpisodeKey]tmdb.EpisodeSnapshot{}
	}

	var recognition models.MediaLibraryRecognition
	err := s.db.WithContext(ctx).Table("media_library_recognitions").
		Joins("JOIN media_library_entries ON media_library_entries.recognition_id = media_library_recognitions.id").
		Where("media_library_entries.library_id = ? AND media_library_entries.work_key = ?", libraryID, workKey).
		Order("media_library_recognitions.updated_at DESC").First(&recognition).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			s.log.Warn().Uint("library_id", libraryID).Str("error_code", "player_episode_snapshot_read_failed").Msg("读取分集元数据快照失败")
		}
		return map[playerEpisodeKey]tmdb.EpisodeSnapshot{}
	}
	classificationMetadata, snapshot, err := decodeRecognitionMetadata(recognition.MetadataJSON)
	if err != nil {
		s.log.Warn().Uint("library_id", libraryID).Str("error_code", "player_episode_snapshot_invalid").Msg("分集元数据快照无效")
		return map[playerEpisodeKey]tmdb.EpisodeSnapshot{}
	}
	if snapshot.TMDBID <= 0 && recognition.TMDBID != nil && recognition.MediaType == "tv" {
		snapshot.TMDBID = *recognition.TMDBID
		snapshot.MediaType = "tv"
		snapshot.Title = recognition.Title
	}
	var library models.MediaLibrary
	if err := s.db.WithContext(ctx).Select("id", "metadata_language").First(&library, libraryID).Error; err != nil {
		return map[playerEpisodeKey]tmdb.EpisodeSnapshot{}
	}
	metadataLanguage := strings.TrimSpace(library.MetadataLanguage)
	if snapshot.EpisodeLanguage != metadataLanguage {
		snapshot.EpisodeSnapshots = nil
		snapshot.EpisodeSeasons = nil
		snapshot.EpisodeLanguage = metadataLanguage
	}

	cache := make(map[playerEpisodeKey]tmdb.EpisodeSnapshot, min(len(snapshot.EpisodeSnapshots), maxPlayerEpisodeMetadata))
	for _, episode := range snapshot.EpisodeSnapshots {
		if len(cache) == maxPlayerEpisodeMetadata {
			break
		}
		if episode.SeasonNumber < 0 || episode.SeasonNumber > 10000 || episode.EpisodeNumber <= 0 || episode.EpisodeNumber > 100000 {
			continue
		}
		cache[playerEpisodeKey{season: episode.SeasonNumber, episode: episode.EpisodeNumber}] = episode
	}
	result := episodeMetadataForWanted(snapshot.EpisodeSnapshots, wanted)
	completed := make(map[int]struct{}, len(snapshot.EpisodeSeasons))
	for _, season := range snapshot.EpisodeSeasons {
		if season >= 0 && season <= 10000 {
			completed[season] = struct{}{}
		}
	}
	missingSet := make(map[int]struct{})
	for key := range wanted {
		if _, ok := completed[key.season]; !ok {
			missingSet[key.season] = struct{}{}
		}
	}
	if len(missingSet) == 0 || snapshot.TMDBID <= 0 || snapshot.MediaType != "tv" || s.metadata == nil || ctx.Err() != nil {
		return result
	}

	client, err := s.metadata.Client()
	if err != nil {
		return result
	}
	metadataCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	missing := make([]int, 0, len(missingSet))
	for season := range missingSet {
		missing = append(missing, season)
	}
	sort.Ints(missing)
	if len(missing) > maxPlayerEpisodeSeasonsPerFetch {
		missing = missing[:maxPlayerEpisodeSeasonsPerFetch]
	}

	changed := false
	for _, season := range missing {
		if metadataCtx.Err() != nil {
			break
		}
		episodes, fetchErr := client.GetTVSeasonEpisodes(metadataCtx, snapshot.TMDBID, season, metadataLanguage)
		if fetchErr != nil {
			if metadataCtx.Err() == nil {
				s.log.Debug().Uint("library_id", libraryID).Int("season", season).Str("error_code", "player_episode_tmdb_unavailable").Msg("TMDB 分集元数据暂时不可用")
			}
			continue
		}
		seasonCacheFits := len(cache) <= maxPlayerEpisodeMetadata
		if seasonCacheFits {
			additional := 0
			for _, episode := range episodes {
				key := playerEpisodeKey{season: episode.SeasonNumber, episode: episode.EpisodeNumber}
				if _, exists := cache[key]; !exists {
					additional++
				}
			}
			seasonCacheFits = len(cache)+additional <= maxPlayerEpisodeMetadata
		}
		if seasonCacheFits {
			completed[season] = struct{}{}
			changed = true
		}
		for _, episode := range episodes {
			key := playerEpisodeKey{season: episode.SeasonNumber, episode: episode.EpisodeNumber}
			if _, ok := wanted[key]; ok {
				result[key] = episode
			}
			if seasonCacheFits {
				cache[key] = episode
			}
		}
	}
	if !changed {
		return result
	}

	keys := make([]playerEpisodeKey, 0, len(cache))
	for key := range cache {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].season != keys[j].season {
			return keys[i].season < keys[j].season
		}
		return keys[i].episode < keys[j].episode
	})
	snapshot.EpisodeSnapshots = snapshot.EpisodeSnapshots[:0]
	for _, key := range keys {
		snapshot.EpisodeSnapshots = append(snapshot.EpisodeSnapshots, cache[key])
		if len(snapshot.EpisodeSnapshots) == maxPlayerEpisodeMetadata {
			break
		}
	}
	snapshot.EpisodeSeasons = snapshot.EpisodeSeasons[:0]
	for season := range completed {
		snapshot.EpisodeSeasons = append(snapshot.EpisodeSeasons, season)
	}
	sort.Ints(snapshot.EpisodeSeasons)
	if len(snapshot.EpisodeSeasons) > 64 {
		snapshot.EpisodeSeasons = snapshot.EpisodeSeasons[:64]
	}
	metadataJSON, marshalErr := marshalRecognitionMetadata(MediaRecognitionResult{Metadata: classificationMetadata, Snapshot: snapshot})
	if marshalErr == nil {
		update := s.db.WithContext(ctx).Model(&models.MediaLibraryRecognition{}).
			Where("id = ? AND metadata_json = ?", recognition.ID, recognition.MetadataJSON).
			UpdateColumn("metadata_json", metadataJSON)
		if update.Error != nil {
			s.log.Warn().Uint("library_id", libraryID).Str("error_code", "player_episode_snapshot_write_failed").Msg("保存分集元数据快照失败")
		}
	}
	return result
}

func episodeMetadataForWanted(values []tmdb.EpisodeSnapshot, wanted map[playerEpisodeKey]struct{}) map[playerEpisodeKey]tmdb.EpisodeSnapshot {
	result := make(map[playerEpisodeKey]tmdb.EpisodeSnapshot, min(len(values), len(wanted)))
	for _, value := range values {
		key := playerEpisodeKey{season: value.SeasonNumber, episode: value.EpisodeNumber}
		if _, ok := wanted[key]; ok {
			result[key] = value
		}
	}
	return result
}

func preservePlayerEpisodeMetadata(next MediaRecognitionResult, storedJSON, metadataLanguage string) MediaRecognitionResult {
	if next.Snapshot.TMDBID <= 0 || next.Snapshot.MediaType != "tv" || strings.TrimSpace(storedJSON) == "" {
		return next
	}
	_, stored, err := decodeRecognitionMetadata(storedJSON)
	metadataLanguage = strings.TrimSpace(metadataLanguage)
	if err != nil || stored.TMDBID != next.Snapshot.TMDBID || stored.MediaType != next.Snapshot.MediaType || stored.EpisodeLanguage != metadataLanguage {
		return next
	}
	if len(stored.EpisodeSnapshots) > maxPlayerEpisodeMetadata {
		stored.EpisodeSnapshots = stored.EpisodeSnapshots[:maxPlayerEpisodeMetadata]
	}
	if len(stored.EpisodeSeasons) > 64 {
		stored.EpisodeSeasons = stored.EpisodeSeasons[:64]
	}
	next.Snapshot.EpisodeSnapshots = append([]tmdb.EpisodeSnapshot(nil), stored.EpisodeSnapshots...)
	next.Snapshot.EpisodeSeasons = append([]int(nil), stored.EpisodeSeasons...)
	next.Snapshot.EpisodeLanguage = metadataLanguage
	return next
}

func playerEpisodeFallbackTitle(entry models.MediaLibraryEntry, episode *int) string {
	normalized := strings.ReplaceAll(entry.RelativePath, "\\", "/")
	base := path.Base(normalized)
	stem := strings.TrimSpace(strings.TrimSuffix(base, path.Ext(base)))
	if stem != "" {
		return stem
	}
	if episode != nil {
		return "第 " + strconv.Itoa(*episode) + " 集"
	}
	return "未命名分集"
}
