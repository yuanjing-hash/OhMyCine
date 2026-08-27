package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

var mediaRecognitionGlobalGate = make(chan struct{}, 8)

type mediaLibraryRecognizedUnit struct {
	Unit     medialibrary.RecognitionUnit
	Result   MediaRecognitionResult
	CacheHit bool
	Manual   bool
}

type recognitionMetadataEnvelope struct {
	Version        int                     `json:"version"`
	Classification classification.Metadata `json:"classification"`
	Snapshot       tmdb.Snapshot           `json:"snapshot,omitempty"`
}

type mediaRecognitionRateGate struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func (g *mediaRecognitionRateGate) Wait(ctx context.Context) error {
	if g == nil || g.interval <= 0 {
		return nil
	}
	g.mu.Lock()
	now := time.Now()
	reserved := g.next
	if reserved.Before(now) {
		reserved = now
	}
	g.next = reserved.Add(g.interval)
	g.mu.Unlock()
	if wait := time.Until(reserved); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (s *MediaLibraryService) recognizeLibraryUnits(ctx context.Context, library models.MediaLibrary, profile models.MediaClassificationProfile, units []medialibrary.RecognitionUnit) ([]mediaLibraryRecognizedUnit, error) {
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return nil, err
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return nil, err
	}
	processor, err := builtinProcessorForCodes(organization.BuiltinRecognitionPacks)
	if err != nil {
		return nil, err
	}
	var lookup mediaRecognitionLookup
	if s.metadata != nil {
		client, _, _, clientErr := s.metadata.clientWithCredentialInfo()
		if clientErr == nil {
			lookup = client
		}
	}
	var existing []models.MediaLibraryRecognition
	if err := s.db.WithContext(ctx).Where("library_id = ?", library.ID).Find(&existing).Error; err != nil {
		return nil, err
	}
	bySource := make(map[string]models.MediaLibraryRecognition, len(existing))
	for _, record := range existing {
		bySource[record.SourceKey] = record
	}

	results := make([]mediaLibraryRecognizedUnit, len(units))
	interval := time.Duration(0)
	if library.MetadataRatePerSecond > 0 {
		interval = time.Second / time.Duration(library.MetadataRatePerSecond)
	}
	rateGate := &mediaRecognitionRateGate{interval: interval}
	workerCount := library.MetadataConcurrency
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(units) {
		workerCount = len(units)
	}
	if workerCount == 0 {
		return results, nil
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	errorsCh := make(chan error, 1)
	var workers sync.WaitGroup
	process := func(index int) error {
		unit := units[index]
		if record, ok := bySource[unit.SourceKey]; ok && record.ManualOverride {
			result, decodeErr := recognitionResultFromStored(record, rules)
			if decodeErr != nil {
				return decodeErr
			}
			results[index] = mediaLibraryRecognizedUnit{Unit: unit, Result: result, Manual: true}
			return nil
		}
		cacheKey := mediaLibraryRecognitionCacheKey(unit, profile, library)
		if cached, ok := s.loadRecognitionCache(workerCtx, cacheKey); ok {
			results[index] = mediaLibraryRecognizedUnit{Unit: unit, Result: cached, CacheHit: true}
			return nil
		}
		if lookup != nil {
			if err := rateGate.Wait(workerCtx); err != nil {
				return err
			}
		}
		select {
		case mediaRecognitionGlobalGate <- struct{}{}:
		case <-workerCtx.Done():
			return workerCtx.Err()
		}
		files := make([]recognitionSourceFile, 0, len(unit.Files))
		for _, file := range unit.Files {
			files = append(files, recognitionSourceFile{RelativePath: file.RelativePath, Size: file.Size})
		}
		result := recognizeMedia(workerCtx, lookup, MediaRecognitionRequest{
			PackageName:      unit.PackageName,
			Files:            files,
			SourceKind:       mediarecognition.SourceLibraryScan,
			MediaTypeHint:    unit.MediaTypeHint,
			BuiltinPackCodes: organization.BuiltinRecognitionPacks,
			BuiltinProcessor: processor,
			RecognitionRules: organization.RecognitionRules,
			Classification:   rules,
			Language:         library.MetadataLanguage,
			Region:           library.MetadataRegion,
			AIAssist:         s.aiRecognition,
		})
		<-mediaRecognitionGlobalGate
		_ = s.storeRecognitionCache(workerCtx, cacheKey, result)
		results[index] = mediaLibraryRecognizedUnit{Unit: unit, Result: result}
		return nil
	}
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if err := process(index); err != nil {
					select {
					case errorsCh <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}
dispatch:
	for index := range units {
		select {
		case jobs <- index:
		case <-workerCtx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	select {
	case err := <-errorsCh:
		return nil, err
	default:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func recognitionResultFromStored(record models.MediaLibraryRecognition, rules classification.RulesV1) (MediaRecognitionResult, error) {
	result := MediaRecognitionResult{
		Status:        record.Status,
		ErrorCode:     record.ErrorCode,
		Title:         record.Title,
		MediaType:     record.MediaType,
		CategoryName:  record.CategoryName,
		MatchedRuleID: record.MatchedRuleID,
		TMDBID:        cloneInt64(record.TMDBID),
		ReleaseYear:   cloneInt(record.ReleaseYear),
		Confidence:    cloneFloat64(record.Confidence),
	}
	if record.MetadataJSON != "" && record.MetadataJSON != "{}" {
		metadata, snapshot, err := decodeRecognitionMetadata(record.MetadataJSON)
		if err != nil {
			return MediaRecognitionResult{}, fmt.Errorf("decode stored recognition metadata: %w", err)
		}
		result.Metadata, result.Snapshot = metadata, snapshot
		classified := classification.Classify(result.Metadata, rules)
		result.CategoryName, result.MatchedRuleID = classified.CategoryName, classified.MatchedRuleID
	}
	return result, nil
}

func marshalRecognitionMetadata(result MediaRecognitionResult) (string, error) {
	payload, err := json.Marshal(recognitionMetadataEnvelope{Version: 1, Classification: result.Metadata, Snapshot: result.Snapshot})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func decodeRecognitionMetadata(raw string) (classification.Metadata, tmdb.Snapshot, error) {
	var envelope recognitionMetadataEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return classification.Metadata{}, tmdb.Snapshot{}, err
	}
	if envelope.Version == 1 {
		return envelope.Classification, envelope.Snapshot, nil
	}
	// v25-v26 stored classification.Metadata directly. Keep those records
	// readable until the next successful recognition refresh replaces them.
	var legacy classification.Metadata
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		return classification.Metadata{}, tmdb.Snapshot{}, err
	}
	return legacy, tmdb.Snapshot{}, nil
}

func mediaLibraryRecognitionCacheKey(unit medialibrary.RecognitionUnit, profile models.MediaClassificationProfile, library models.MediaLibrary) string {
	// v2 refreshes older derived cache entries so the additive v1 snapshot
	// receives the richer bounded detail fields without breaking persisted v1
	// envelopes or requiring a database migration.
	value := "recognition:" + mediarecognition.EngineVersion + ":snapshot-v2\x00" + unit.InputFingerprint + "\x00" + strconv.FormatUint(uint64(profile.ID), 10) + "\x00" + strconv.FormatUint(profile.Revision, 10) + "\x00" + library.MetadataLanguage + "\x00" + library.MetadataRegion
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *MediaLibraryService) loadRecognitionCache(ctx context.Context, key string) (MediaRecognitionResult, bool) {
	var cached models.MediaRecognitionCache
	if err := s.db.WithContext(ctx).Where("lookup_key = ? AND expires_at > ?", key, time.Now().UTC()).First(&cached).Error; err != nil {
		return MediaRecognitionResult{}, false
	}
	var result MediaRecognitionResult
	if err := json.Unmarshal([]byte(cached.ResultJSON), &result); err != nil {
		return MediaRecognitionResult{}, false
	}
	return result, true
}

func (s *MediaLibraryService) storeRecognitionCache(ctx context.Context, key string, result MediaRecognitionResult) error {
	// Credential/auth failures are configuration state, not durable lookup
	// answers. Avoid retaining them beyond the current scan.
	if result.ErrorCode == mediaRecognitionCredentialMissing || result.ErrorCode == "tmdb_auth_failed" {
		return nil
	}
	ttl := 30 * time.Minute
	if result.Status == mediaRecognitionStatusMatched {
		ttl = 30 * 24 * time.Hour
	} else if result.ErrorCode == "tmdb_network_unavailable" || result.ErrorCode == "tmdb_request_failed" {
		ttl = 5 * time.Minute
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	record := models.MediaRecognitionCache{LookupKey: key, Status: result.Status, ErrorCode: result.ErrorCode, ResultJSON: string(payload), ExpiresAt: now.Add(ttl), CreatedAt: now, UpdatedAt: now}
	return s.db.WithContext(ctx).Save(&record).Error
}

func recognitionWorkKey(result MediaRecognitionResult, sourceKey string) string {
	if result.Status == mediaRecognitionStatusMatched && result.TMDBID != nil {
		if result.MediaType == "tv" {
			return "series:tmdb:" + strconv.FormatInt(*result.TMDBID, 10)
		}
		return "movie:tmdb:" + strconv.FormatInt(*result.TMDBID, 10)
	}
	return "file:" + sourceKey
}
