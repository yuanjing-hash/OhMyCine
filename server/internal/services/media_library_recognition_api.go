package services

import (
	"context"
	"encoding/base64"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MediaRecognitionSummary struct {
	Token          string    `json:"token"`
	Status         string    `json:"status"`
	ErrorCode      string    `json:"error_code"`
	Title          string    `json:"title"`
	MediaType      string    `json:"media_type"`
	ReleaseYear    *int      `json:"release_year,omitempty"`
	TMDBID         *int64    `json:"tmdb_id,omitempty"`
	Confidence     *float64  `json:"confidence,omitempty"`
	CategoryName   string    `json:"category_name"`
	ManualOverride bool      `json:"manual_override"`
	FileCount      int64     `json:"file_count"`
	SourceSummary  string    `json:"source_summary"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type MediaRecognitionPage struct {
	List     []MediaRecognitionSummary `json:"list"`
	Total    int64                     `json:"total"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
}

type MediaRecognitionOverrideInput struct {
	TMDBID    int64
	MediaType string
}

func (s *MediaLibraryService) Recognitions(actor Actor, libraryID uint, query MediaPageQuery, status string, manualOnly ...bool) (MediaRecognitionPage, error) {
	if err := s.ensureMediaLibraryReadable(actor, libraryID); err != nil {
		return MediaRecognitionPage{}, err
	}
	query, err := normalizeMediaPageQuery(query)
	if err != nil {
		return MediaRecognitionPage{}, err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && status != mediaRecognitionStatusMatched && status != mediaRecognitionStatusUnrecognized {
		return MediaRecognitionPage{}, appError(CodeInvalidRequest, "识别状态筛选无效", nil)
	}
	db := s.db.Model(&models.MediaLibraryRecognition{}).Where("library_id = ?", libraryID)
	if status != "" {
		db = db.Where("status = ?", status)
	}
	if len(manualOnly) > 0 && manualOnly[0] {
		db = db.Where("manual_override = ?", true)
	}
	if query.Query != "" {
		db = db.Where("title LIKE ? ESCAPE '\\'", "%"+escapeLike(query.Query)+"%")
	}
	if query.MediaType != "" {
		mediaType := query.MediaType
		if mediaType == "series" {
			mediaType = "tv"
		}
		db = db.Where("media_type = ?", mediaType)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return MediaRecognitionPage{}, err
	}
	var records []models.MediaLibraryRecognition
	if err := db.Order("updated_at DESC,id DESC").Offset((query.Page - 1) * query.PageSize).Limit(query.PageSize).Find(&records).Error; err != nil {
		return MediaRecognitionPage{}, err
	}
	items := make([]MediaRecognitionSummary, 0, len(records))
	for _, record := range records {
		item, itemErr := s.recognitionSummary(record)
		if itemErr != nil {
			return MediaRecognitionPage{}, itemErr
		}
		items = append(items, item)
	}
	return MediaRecognitionPage{List: items, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

func (s *MediaLibraryService) RetryRecognition(ctx context.Context, actor Actor, libraryID uint, token string, request RequestContext) (MediaRecognitionSummary, error) {
	if !actor.Can(authz.PermissionMediaLibrariesScan) {
		return MediaRecognitionSummary{}, appError(CodePermissionDenied, "无权重新识别媒体", nil)
	}
	record, library, profile, entries, err := s.recognitionContext(libraryID, token)
	if err != nil {
		return MediaRecognitionSummary{}, err
	}
	if record.ManualOverride {
		return MediaRecognitionSummary{}, appError(CodeConflict, "请先清除人工匹配再重试", nil)
	}
	result, err := s.recognizeStoredUnit(ctx, library, profile, entries)
	if err != nil {
		return MediaRecognitionSummary{}, err
	}
	if err := s.persistRecognitionResult(record, profile, result, false); err != nil {
		return MediaRecognitionSummary{}, err
	}
	if err := s.db.First(&record, record.ID).Error; err != nil {
		return MediaRecognitionSummary{}, err
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "media_recognition.retry", "media_library_recognition", strconv.FormatUint(uint64(record.ID), 10), "success", map[string]any{"library_id": libraryID, "status": record.Status, "error_code": record.ErrorCode}, request)
	return s.recognitionSummary(record)
}

func (s *MediaLibraryService) RecognitionCandidates(ctx context.Context, actor Actor, libraryID uint, token, title, mediaType string, year *int) ([]tmdb.Candidate, error) {
	if err := s.ensureMediaLibraryReadable(actor, libraryID); err != nil {
		return nil, err
	}
	record, library, _, _, err := s.recognitionContext(libraryID, token)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		title = record.Title
	}
	if strings.TrimSpace(mediaType) == "" {
		mediaType = record.MediaType
	}
	if year == nil {
		year = record.ReleaseYear
	}
	if s.metadata == nil {
		return nil, appError(CodeTMDBUnavailable, "TMDB 未配置", nil)
	}
	client, _, _, err := s.metadata.clientWithCredentialInfo()
	if err != nil {
		return nil, err
	}
	items, err := client.SearchCandidates(ctx, mediaType, title, year, library.MetadataLanguage, library.MetadataRegion, 10)
	if err != nil {
		return nil, appError(tmdb.ErrorCode(err), classificationFallbackMessage(tmdb.ErrorCode(err)), nil)
	}
	return items, nil
}

func (s *MediaLibraryService) OverrideRecognition(ctx context.Context, actor Actor, libraryID uint, token string, input MediaRecognitionOverrideInput, request RequestContext) (MediaRecognitionSummary, error) {
	if !actor.Can(authz.PermissionMediaLibrariesScan) {
		return MediaRecognitionSummary{}, appError(CodePermissionDenied, "无权人工匹配媒体", nil)
	}
	record, library, profile, _, err := s.recognitionContext(libraryID, token)
	if err != nil {
		return MediaRecognitionSummary{}, err
	}
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	if input.TMDBID <= 0 || (input.MediaType != "movie" && input.MediaType != "tv") {
		return MediaRecognitionSummary{}, appError(CodeInvalidRequest, "TMDB 匹配选择无效", nil)
	}
	if s.metadata == nil {
		return MediaRecognitionSummary{}, appError(CodeTMDBUnavailable, "TMDB 未配置", nil)
	}
	client, _, _, err := s.metadata.clientWithCredentialInfo()
	if err != nil {
		return MediaRecognitionSummary{}, err
	}
	match, err := client.GetByID(ctx, input.MediaType, input.TMDBID, library.MetadataLanguage)
	if err != nil {
		return MediaRecognitionSummary{}, appError(tmdb.ErrorCode(err), "TMDB 项目验证失败", nil)
	}
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return MediaRecognitionSummary{}, err
	}
	metadata := classification.Metadata{MediaType: classification.MediaType(match.MediaType), GenreIDs: match.GenreIDs, OriginalLanguage: match.OriginalLanguage, ProductionCountries: match.ProductionCountries, OriginCountries: match.OriginCountries, ReleaseYear: match.ReleaseYear}
	classified := classification.Classify(metadata, rules)
	result := MediaRecognitionResult{Status: mediaRecognitionStatusMatched, Title: match.Title, MediaType: match.MediaType, CategoryName: classified.CategoryName, MatchedRuleID: classified.MatchedRuleID, TMDBID: cloneInt64(&match.ID), ReleaseYear: cloneInt(match.ReleaseYear), Confidence: cloneFloat64(&match.Confidence), Metadata: metadata, Snapshot: match.Snapshot}
	if err := s.persistRecognitionResult(record, profile, result, true); err != nil {
		return MediaRecognitionSummary{}, err
	}
	if err := s.db.First(&record, record.ID).Error; err != nil {
		return MediaRecognitionSummary{}, err
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "media_recognition.override", "media_library_recognition", strconv.FormatUint(uint64(record.ID), 10), "success", map[string]any{"library_id": libraryID, "media_type": match.MediaType, "tmdb_id": match.ID}, request)
	return s.recognitionSummary(record)
}

func (s *MediaLibraryService) ClearRecognitionOverride(ctx context.Context, actor Actor, libraryID uint, token string, request RequestContext) (MediaRecognitionSummary, error) {
	if !actor.Can(authz.PermissionMediaLibrariesScan) {
		return MediaRecognitionSummary{}, appError(CodePermissionDenied, "无权清除人工匹配", nil)
	}
	record, _, _, _, err := s.recognitionContext(libraryID, token)
	if err != nil {
		return MediaRecognitionSummary{}, err
	}
	if !record.ManualOverride {
		return MediaRecognitionSummary{}, appError(CodeConflict, "当前项目没有人工匹配", nil)
	}
	if err := s.db.Model(&record).Update("manual_override", false).Error; err != nil {
		return MediaRecognitionSummary{}, err
	}
	item, err := s.RetryRecognition(ctx, actor, libraryID, token, request)
	if err == nil {
		_ = s.audit.Record(s.db, &actor.User.ID, "media_recognition.override_clear", "media_library_recognition", strconv.FormatUint(uint64(record.ID), 10), "success", map[string]any{"library_id": libraryID}, request)
	}
	return item, err
}

func (s *MediaLibraryService) recognitionContext(libraryID uint, token string) (models.MediaLibraryRecognition, models.MediaLibrary, models.MediaClassificationProfile, []models.MediaLibraryEntry, error) {
	recognitionID, err := decodeRecognitionToken(token)
	if err != nil {
		return models.MediaLibraryRecognition{}, models.MediaLibrary{}, models.MediaClassificationProfile{}, nil, err
	}
	var record models.MediaLibraryRecognition
	if err := s.db.Where("id = ? AND library_id = ?", recognitionID, libraryID).First(&record).Error; err != nil {
		return models.MediaLibraryRecognition{}, models.MediaLibrary{}, models.MediaClassificationProfile{}, nil, recognitionNotFound(err)
	}
	var library models.MediaLibrary
	if err := s.db.First(&library, libraryID).Error; err != nil {
		return models.MediaLibraryRecognition{}, models.MediaLibrary{}, models.MediaClassificationProfile{}, nil, mediaLibraryNotFound(err)
	}
	var profile models.MediaClassificationProfile
	if err := s.db.First(&profile, library.ProfileID).Error; err != nil {
		return models.MediaLibraryRecognition{}, models.MediaLibrary{}, models.MediaClassificationProfile{}, nil, err
	}
	var entries []models.MediaLibraryEntry
	if err := s.db.Where("library_id = ? AND recognition_id = ?", libraryID, record.ID).Order("relative_path").Find(&entries).Error; err != nil {
		return models.MediaLibraryRecognition{}, models.MediaLibrary{}, models.MediaClassificationProfile{}, nil, err
	}
	if len(entries) == 0 {
		return models.MediaLibraryRecognition{}, models.MediaLibrary{}, models.MediaClassificationProfile{}, nil, recognitionNotFound(gorm.ErrRecordNotFound)
	}
	return record, library, profile, entries, nil
}

func (s *MediaLibraryService) recognizeStoredUnit(ctx context.Context, library models.MediaLibrary, profile models.MediaClassificationProfile, entries []models.MediaLibraryEntry) (MediaRecognitionResult, error) {
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return MediaRecognitionResult{}, err
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return MediaRecognitionResult{}, err
	}
	processor, err := builtinProcessorForCodes(organization.BuiltinRecognitionPacks)
	if err != nil {
		return MediaRecognitionResult{}, err
	}
	var lookup mediaRecognitionLookup
	if s.metadata != nil {
		client, _, _, clientErr := s.metadata.clientWithCredentialInfo()
		if clientErr == nil {
			lookup = client
		}
	}
	files := make([]recognitionSourceFile, 0, len(entries))
	for _, entry := range entries {
		files = append(files, recognitionSourceFile{RelativePath: entry.RelativePath, Size: entry.Size})
	}
	packageName := recognitionPackageName(entries)
	mediaTypeHint := ""
	if len(entries) > 0 {
		mediaTypeHint = entries[0].MediaType
	}
	return recognizeMedia(ctx, lookup, MediaRecognitionRequest{PackageName: packageName, Files: files, SourceKind: mediarecognition.SourceLibraryScan, MediaTypeHint: mediaTypeHint, BuiltinPackCodes: organization.BuiltinRecognitionPacks, BuiltinProcessor: processor, RecognitionRules: organization.RecognitionRules, Classification: rules, Language: library.MetadataLanguage, Region: library.MetadataRegion}), nil
}

func (s *MediaLibraryService) persistRecognitionResult(record models.MediaLibraryRecognition, profile models.MediaClassificationProfile, result MediaRecognitionResult, manual bool) error {
	lock := s.scanLock(record.LibraryID)
	lock.Lock()
	defer lock.Unlock()

	metadataJSON, err := marshalRecognitionMetadata(result)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	var committedChange models.MediaLibraryChange
	var artifactGeneration uint64
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var library models.MediaLibrary
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&library, record.LibraryID).Error; err != nil {
			return err
		}
		if library.ProfileID != profile.ID || library.ProfileRevision != profile.Revision {
			return appError(CodeConflict, "媒体库识别配置已变化，请重试", nil)
		}
		var current models.MediaLibraryRecognition
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND library_id = ?", record.ID, record.LibraryID).First(&current).Error; err != nil {
			return err
		}
		generation := current.LastGeneration
		var storage models.Storage
		if err := tx.First(&storage, library.StorageID).Error; err != nil {
			return err
		}
		requiresArtifacts := mediaLibraryRequiresArtifacts(storage.Type, library, s.artifacts != nil)
		if requiresArtifacts {
			generation = max(max(library.DirtyGeneration, library.ArtifactGeneration), current.LastGeneration) + 1
			artifactGeneration = generation
		}
		updates := map[string]any{"profile_id": profile.ID, "profile_revision": profile.Revision, "status": result.Status, "error_code": result.ErrorCode, "media_type": result.MediaType, "title": result.Title, "release_year": result.ReleaseYear, "tmdb_id": result.TMDBID, "confidence": result.Confidence, "category_name": result.CategoryName, "matched_rule_id": result.MatchedRuleID, "metadata_json": string(metadataJSON), "manual_override": manual, "last_generation": generation, "updated_at": now}
		if err := tx.Model(&models.MediaLibraryRecognition{}).Where("id = ? AND library_id = ?", record.ID, record.LibraryID).Updates(updates).Error; err != nil {
			return err
		}
		entryUpdates := map[string]any{"media_type": result.MediaType, "title": result.Title, "series_title": "", "work_key": recognitionWorkKey(result, record.SourceKey), "match_status": result.Status, "tmdb_id": result.TMDBID, "release_year": result.ReleaseYear, "match_confidence": result.Confidence, "recognition_error_code": result.ErrorCode, "category_name": result.CategoryName, "matched_rule_id": result.MatchedRuleID, "updated_at": now}
		if result.MediaType == "tv" {
			entryUpdates["series_title"] = result.Title
		}
		if err := tx.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND recognition_id = ?", record.LibraryID, record.ID).Updates(entryUpdates).Error; err != nil {
			return err
		}
		if artifactGeneration > 0 {
			// A manual metadata correction is a complete logical projection
			// generation. Carry all unchanged recognition/source-asset facts into
			// it so the artifact worker can safely rewrite the full sidecar set;
			// cleanup remains ineligible because this was not a complete scan.
			if err := tx.Model(&models.MediaLibraryRecognition{}).Where("library_id = ?", record.LibraryID).Updates(map[string]any{"last_generation": artifactGeneration, "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.MediaLibrarySourceAsset{}).Where("library_id = ?", record.LibraryID).Updates(map[string]any{"generation": artifactGeneration, "updated_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", record.LibraryID).Updates(map[string]any{"dirty_generation": artifactGeneration, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if s.changes != nil {
			change, err := s.changes.RecordTx(tx, record.LibraryID, generation, models.MediaLibraryChangeMetadata, artifactGeneration == 0)
			if err != nil {
				return err
			}
			committedChange = change
		}
		return nil
	})
	if err != nil {
		return err
	}
	if artifactGeneration > 0 {
		return s.artifacts.ScheduleGeneration(record.LibraryID, artifactGeneration)
	}
	if committedChange.State == models.MediaLibraryChangeReady && s.changes != nil {
		s.changes.NotifyCommitted(committedChange.LibraryID, committedChange.Revision)
	}
	return nil
}

func (s *MediaLibraryService) recognitionSummary(record models.MediaLibraryRecognition) (MediaRecognitionSummary, error) {
	var count int64
	if err := s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND recognition_id = ?", record.LibraryID, record.ID).Count(&count).Error; err != nil {
		return MediaRecognitionSummary{}, err
	}
	var first models.MediaLibraryEntry
	_ = s.db.Where("library_id = ? AND recognition_id = ?", record.LibraryID, record.ID).Order("relative_path").First(&first).Error
	return MediaRecognitionSummary{Token: encodeRecognitionToken(record.ID), Status: record.Status, ErrorCode: record.ErrorCode, Title: record.Title, MediaType: record.MediaType, ReleaseYear: cloneInt(record.ReleaseYear), TMDBID: cloneInt64(record.TMDBID), Confidence: cloneFloat64(record.Confidence), CategoryName: record.CategoryName, ManualOverride: record.ManualOverride, FileCount: count, SourceSummary: path.Base(first.RelativePath), UpdatedAt: record.UpdatedAt}, nil
}

func recognitionPackageName(entries []models.MediaLibraryEntry) string {
	if len(entries) == 0 {
		return ""
	}
	relative := strings.ReplaceAll(entries[0].RelativePath, "\\", "/")
	directory := path.Dir(relative)
	if directory == "." || directory == "/" {
		return strings.TrimSuffix(path.Base(relative), path.Ext(relative))
	}
	base := path.Base(directory)
	if strings.HasPrefix(strings.ToLower(base), "season ") {
		base = path.Base(path.Dir(directory))
	}
	return base
}

func encodeRecognitionToken(id uint) string {
	return base64.RawURLEncoding.EncodeToString([]byte("recognition:" + strconv.FormatUint(uint64(id), 10)))
}

func decodeRecognitionToken(token string) (uint, error) {
	if token == "" || len(token) > 128 {
		return 0, appError(CodeInvalidRequest, "媒体识别标识无效", nil)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || !strings.HasPrefix(string(decoded), "recognition:") {
		return 0, appError(CodeInvalidRequest, "媒体识别标识无效", err)
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(string(decoded), "recognition:"), 10, 64)
	if err != nil || id == 0 {
		return 0, appError(CodeInvalidRequest, "媒体识别标识无效", err)
	}
	return uint(id), nil
}

func recognitionNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "媒体识别项目不存在", err)
	}
	return err
}
