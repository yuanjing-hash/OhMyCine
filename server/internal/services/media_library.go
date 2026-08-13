package services

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	"gorm.io/gorm"
)

var defaultVideoExtensions = []string{".mp4", ".mkv", ".ts", ".iso", ".rmvb", ".avi", ".mov", ".mpeg", ".mpg", ".wmv", ".3gp", ".asf", ".m4v", ".flv", ".m2ts", ".tp", ".f4v"}

type MediaLibraryService struct {
	db          *gorm.DB
	audit       *AuditService
	log         zerolog.Logger
	mu          sync.Mutex
	supervisors map[uint]supervisorHandle
	scanLocks   map[uint]*sync.Mutex
	closed      bool
}

type supervisorHandle struct {
	cancel context.CancelFunc
	done   <-chan struct{}
}

type MediaLibraryInput struct {
	Name                  string
	StorageID             uint
	ProfileID             uint
	RelativeRoot          string
	Enabled               bool
	Recursive             bool
	FullScanIntervalHours int
	IncrementalMinutes    int
	VideoExtensions       []string
	IgnorePatterns        []string
	MetadataLanguage      string
	MetadataRegion        string
	MatchStrategy         string
	ProviderRatePerSecond int
	ProviderConcurrency   int
	MetadataRatePerSecond int
	MetadataConcurrency   int
	STRMEnabled           bool
	STRMLocalRoot         string
}
type UpdateMediaLibraryInput struct {
	MediaLibraryInput
	RevisionUpdated bool
}
type MediaLibraryDetail struct {
	models.MediaLibrary
	StorageName     string   `json:"storage_name"`
	ProfileName     string   `json:"profile_name"`
	VideoExtensions []string `json:"video_extensions"`
	IgnorePatterns  []string `json:"ignore_patterns"`
	EntryCount      int64    `json:"entry_count"`
}

func NewMediaLibraryService(db *gorm.DB, audit *AuditService, log zerolog.Logger) *MediaLibraryService {
	return &MediaLibraryService{db: db, audit: audit, log: log, supervisors: map[uint]supervisorHandle{}, scanLocks: map[uint]*sync.Mutex{}}
}
func (s *MediaLibraryService) Start(ctx context.Context) error {
	var libraries []models.MediaLibrary
	if err := s.db.Where("enabled = ?", true).Find(&libraries).Error; err != nil {
		return err
	}
	for _, library := range libraries {
		s.startSupervisor(ctx, library.ID)
	}
	return nil
}
func (s *MediaLibraryService) Close() {
	s.mu.Lock()
	s.closed = true
	handles := make([]supervisorHandle, 0, len(s.supervisors))
	for _, handle := range s.supervisors {
		handle.cancel()
		handles = append(handles, handle)
	}
	s.supervisors = map[uint]supervisorHandle{}
	s.mu.Unlock()
	for _, handle := range handles {
		<-handle.done
	}
}

func (s *MediaLibraryService) List(actor Actor) ([]MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	var records []models.MediaLibrary
	if err := s.db.Order("name_normalized,id").Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]MediaLibraryDetail, 0, len(records))
	for _, record := range records {
		detail, err := s.detail(record)
		if err != nil {
			return nil, err
		}
		out = append(out, detail)
	}
	return out, nil
}
func (s *MediaLibraryService) Get(actor Actor, id uint) (MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return MediaLibraryDetail{}, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	var record models.MediaLibrary
	if err := s.db.First(&record, id).Error; err != nil {
		return MediaLibraryDetail{}, mediaLibraryNotFound(err)
	}
	return s.detail(record)
}

func (s *MediaLibraryService) Create(ctx context.Context, actor Actor, input MediaLibraryInput, request RequestContext) (MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesCreate) {
		return MediaLibraryDetail{}, appError(CodePermissionDenied, "无权创建媒体库", nil)
	}
	record, err := s.validateInput(0, input)
	if err != nil {
		return MediaLibraryDetail{}, err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		// Select writes explicit false/zero configuration values instead of
		// replacing them with GORM tag defaults (notably enabled=false).
		if err := tx.Select("*").Create(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.create", "media_library", uintID(record.ID), "success", map[string]any{"storage_id": record.StorageID, "profile_id": record.ProfileID, "relative_root": record.RelativeRoot, "enabled": record.Enabled}, request)
	})
	if err != nil {
		return MediaLibraryDetail{}, mediaLibraryConstraint(err)
	}
	if record.Enabled {
		s.startSupervisor(context.Background(), record.ID)
	}
	return s.detail(record)
}

func (s *MediaLibraryService) Update(ctx context.Context, actor Actor, id uint, input MediaLibraryInput, request RequestContext) (MediaLibraryDetail, error) {
	if !actor.Can(authz.PermissionMediaLibrariesUpdate) {
		return MediaLibraryDetail{}, appError(CodePermissionDenied, "无权编辑媒体库", nil)
	}
	var existing models.MediaLibrary
	if err := s.db.First(&existing, id).Error; err != nil {
		return MediaLibraryDetail{}, mediaLibraryNotFound(err)
	}
	record, err := s.validateInput(id, input)
	if err != nil {
		return MediaLibraryDetail{}, err
	}
	record.ID = id
	record.CreatedAt = existing.CreatedAt
	record.BaselineGeneration = existing.BaselineGeneration
	record.DirtyGeneration = existing.DirtyGeneration
	record.LastScanAt = existing.LastScanAt
	record.LastSuccessfulScanAt = existing.LastSuccessfulScanAt
	if record.Enabled {
		record.Status = models.MediaLibraryStatusInitializing
	} else {
		record.Status = models.MediaLibraryStatusDisabled
	}
	s.stopSupervisor(id)
	lock := s.scanLock(id)
	lock.Lock()
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.update", "media_library", uintID(id), "success", map[string]any{"storage_id": record.StorageID, "profile_id": record.ProfileID, "relative_root": record.RelativeRoot, "enabled": record.Enabled}, request)
	}); err != nil {
		lock.Unlock()
		if existing.Enabled {
			s.startSupervisor(context.Background(), id)
		}
		return MediaLibraryDetail{}, mediaLibraryConstraint(err)
	}
	lock.Unlock()
	if record.Enabled {
		s.startSupervisor(context.Background(), id)
	}
	return s.detail(record)
}

func (s *MediaLibraryService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.Can(authz.PermissionMediaLibrariesDelete) {
		return appError(CodePermissionDenied, "无权删除媒体库", nil)
	}
	s.stopSupervisor(id)
	lock := s.scanLock(id)
	lock.Lock()
	defer lock.Unlock()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var record models.MediaLibrary
		if err := tx.First(&record, id).Error; err != nil {
			return mediaLibraryNotFound(err)
		}
		if err := tx.Delete(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_library.delete", "media_library", uintID(id), "success", map[string]any{"storage_id": record.StorageID, "relative_root": record.RelativeRoot}, request)
	})
	if err == nil {
		s.mu.Lock()
		delete(s.scanLocks, id)
		s.mu.Unlock()
	}
	return err
}
func (s *MediaLibraryService) Retry(actor Actor, id uint) error {
	if !actor.Can(authz.PermissionMediaLibrariesScan) {
		return appError(CodePermissionDenied, "无权扫描媒体库", nil)
	}
	var record models.MediaLibrary
	if err := s.db.First(&record, id).Error; err != nil {
		return mediaLibraryNotFound(err)
	}
	if !record.Enabled {
		return appError(CodeConflict, "媒体库已停用", nil)
	}
	s.stopSupervisor(id)
	s.startSupervisor(context.Background(), id)
	return nil
}
func (s *MediaLibraryService) ScanNow(ctx context.Context, actor Actor, id uint) (models.MediaLibraryScanRun, error) {
	if !actor.Can(authz.PermissionMediaLibrariesScan) {
		return models.MediaLibraryScanRun{}, appError(CodePermissionDenied, "无权扫描媒体库", nil)
	}
	return s.reconcile(ctx, id, "manual")
}
func (s *MediaLibraryService) Entries(actor Actor, id uint, limit int) ([]models.MediaLibraryEntry, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看媒体库", nil)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	var items []models.MediaLibraryEntry
	if err := s.db.Where("library_id = ?", id).Order("relative_path").Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}
func (s *MediaLibraryService) Runs(actor Actor, id uint, limit int) ([]models.MediaLibraryScanRun, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看扫描记录", nil)
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var items []models.MediaLibraryScanRun
	if err := s.db.Where("library_id = ?", id).Order("id DESC").Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}
func (s *MediaLibraryService) References(profileID uint) ([]string, error) {
	var records []models.MediaLibrary
	if err := s.db.Where("profile_id = ?", profileID).Find(&records).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(records))
	for i := range records {
		names[i] = records[i].Name
	}
	return names, nil
}
func (s *MediaLibraryService) StorageReferences(storageID uint) ([]string, error) {
	var records []models.MediaLibrary
	if err := s.db.Where("storage_id = ?", storageID).Find(&records).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(records))
	for i := range records {
		names[i] = records[i].Name
	}
	return names, nil
}
func (s *MediaLibraryService) ProfileRevisionChanged(profileID uint, revision uint64) error {
	return s.db.Model(&models.MediaLibrary{}).Where("profile_id = ? AND profile_revision <> ?", profileID, revision).Updates(map[string]any{"reclassification_due": true}).Error
}

func (s *MediaLibraryService) validateInput(id uint, input MediaLibraryInput) (models.MediaLibrary, error) {
	name := strings.Join(strings.Fields(input.Name), " ")
	if name == "" {
		return models.MediaLibrary{}, appError(CodeMediaLibraryNameRequired, "请填写媒体库名称", nil)
	}
	if len([]rune(name)) > 128 {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "媒体库名称过长", nil)
	}
	relativeRoot, err := medialibrary.NormalizeRelativeRoot(input.RelativeRoot)
	if err != nil {
		return models.MediaLibrary{}, appError(CodeMediaLibraryPathInvalid, "媒体库相对路径无效", err)
	}
	var storage models.Storage
	if err := s.db.First(&storage, input.StorageID).Error; err != nil || !storage.Enabled || storage.Type != models.StorageTypeLocal {
		return models.MediaLibrary{}, appError(CodeMediaLibraryStorageUnavailable, "来源 Storage 不可用", err)
	}
	if _, err := medialibrary.ResolveRoot(storage.RootPath, relativeRoot); err != nil {
		return models.MediaLibrary{}, appError(CodeMediaLibraryPathInvalid, "媒体库目录不可读或越过 Storage 边界", err)
	}
	if input.STRMEnabled || input.STRMLocalRoot != "" {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "本地来源不能启用 STRM 投影", nil)
	}
	var profile models.MediaClassificationProfile
	if err := s.db.First(&profile, input.ProfileID).Error; err != nil {
		return models.MediaLibrary{}, appError(CodeMediaLibraryProfileUnavailable, "媒体分类规则不可用", err)
	}
	var overlaps []models.MediaLibrary
	query := s.db.Where("storage_id = ?", input.StorageID)
	if id != 0 {
		query = query.Where("id <> ?", id)
	}
	if err := query.Find(&overlaps).Error; err != nil {
		return models.MediaLibrary{}, err
	}
	for _, other := range overlaps {
		if rootsOverlap(relativeRoot, other.RelativeRoot) {
			return models.MediaLibrary{}, appError(CodeMediaLibraryOverlap, "媒体库扫描范围与现有媒体库重叠", nil)
		}
	}
	if input.FullScanIntervalHours == 0 {
		input.FullScanIntervalHours = 24
	}
	if input.IncrementalMinutes == 0 {
		input.IncrementalMinutes = 15
	}
	if input.FullScanIntervalHours < 1 || input.FullScanIntervalHours > 24*30 || input.IncrementalMinutes < 1 || input.IncrementalMinutes > 24*60 {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "扫描周期超出允许范围", nil)
	}
	if len(input.VideoExtensions) == 0 {
		input.VideoExtensions = append([]string(nil), defaultVideoExtensions...)
	}
	extensions := normalizeExtensions(input.VideoExtensions)
	if len(extensions) == 0 {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "至少需要一个视频扩展名", nil)
	}
	extJSON, _ := json.Marshal(extensions)
	ignoreJSON, _ := json.Marshal(input.IgnorePatterns)
	if input.MetadataLanguage == "" {
		input.MetadataLanguage = "zh-CN"
	}
	if input.MetadataRegion == "" {
		input.MetadataRegion = "CN"
	}
	if input.MatchStrategy == "" {
		input.MatchStrategy = "balanced"
	}
	if input.ProviderRatePerSecond == 0 {
		input.ProviderRatePerSecond = 100
	}
	if input.ProviderConcurrency == 0 {
		input.ProviderConcurrency = 2
	}
	if input.MetadataRatePerSecond == 0 {
		input.MetadataRatePerSecond = 5
	}
	if input.MetadataConcurrency == 0 {
		input.MetadataConcurrency = 1
	}
	if input.ProviderRatePerSecond < 1 || input.ProviderRatePerSecond > 1000 || input.ProviderConcurrency < 1 || input.ProviderConcurrency > 32 || input.MetadataRatePerSecond < 1 || input.MetadataRatePerSecond > 100 || input.MetadataConcurrency < 1 || input.MetadataConcurrency > 16 {
		return models.MediaLibrary{}, appError(CodeInvalidRequest, "媒体库限速或并发配置超出允许范围", nil)
	}
	status := models.MediaLibraryStatusDisabled
	if input.Enabled {
		status = models.MediaLibraryStatusInitializing
	}
	return models.MediaLibrary{Name: name, NameNormalized: strings.ToLower(name), StorageID: input.StorageID, ProfileID: input.ProfileID, ProfileRevision: profile.Revision, RelativeRoot: relativeRoot, Enabled: input.Enabled, Recursive: input.Recursive, FullScanIntervalHours: input.FullScanIntervalHours, IncrementalMinutes: input.IncrementalMinutes, VideoExtensionsJSON: string(extJSON), IgnorePatternsJSON: string(ignoreJSON), MetadataLanguage: input.MetadataLanguage, MetadataRegion: input.MetadataRegion, MatchStrategy: input.MatchStrategy, ProviderRatePerSecond: input.ProviderRatePerSecond, ProviderConcurrency: input.ProviderConcurrency, MetadataRatePerSecond: input.MetadataRatePerSecond, MetadataConcurrency: input.MetadataConcurrency, Status: status}, nil
}

func (s *MediaLibraryService) detail(record models.MediaLibrary) (MediaLibraryDetail, error) {
	var storage models.Storage
	var profile models.MediaClassificationProfile
	if err := s.db.First(&storage, record.StorageID).Error; err != nil {
		return MediaLibraryDetail{}, err
	}
	if err := s.db.First(&profile, record.ProfileID).Error; err != nil {
		return MediaLibraryDetail{}, err
	}
	var extensions, ignores []string
	_ = json.Unmarshal([]byte(record.VideoExtensionsJSON), &extensions)
	_ = json.Unmarshal([]byte(record.IgnorePatternsJSON), &ignores)
	var count int64
	_ = s.db.Model(&models.MediaLibraryEntry{}).Where("library_id = ?", record.ID).Count(&count).Error
	return MediaLibraryDetail{MediaLibrary: record, StorageName: storage.Name, ProfileName: profile.Name, VideoExtensions: extensions, IgnorePatterns: ignores, EntryCount: count}, nil
}
func (s *MediaLibraryService) startSupervisor(parent context.Context, id uint) {
	s.stopSupervisor(id)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.supervisors[id] = supervisorHandle{cancel: cancel, done: done}
	s.mu.Unlock()
	go func() {
		defer close(done)
		s.supervise(ctx, id)
	}()
}
func (s *MediaLibraryService) stopSupervisor(id uint) {
	s.mu.Lock()
	handle, ok := s.supervisors[id]
	if ok {
		handle.cancel()
		delete(s.supervisors, id)
	}
	s.mu.Unlock()
	if ok {
		<-handle.done
	}
}

func (s *MediaLibraryService) supervise(ctx context.Context, id uint) {
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		var library models.MediaLibrary
		if s.db.First(&library, id).Error != nil || !library.Enabled {
			return
		}
		if library.BaselineGeneration == 0 {
			_ = s.setStatus(id, models.MediaLibraryStatusInitializing, "", nil)
			if _, err := s.reconcile(ctx, id, "initial"); err != nil {
				next := time.Now().UTC().Add(delay)
				_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
				if !waitForRetry(ctx, delay) {
					return
				}
				delay = nextRetryDelay(delay)
				continue
			}
		}
		delay = time.Second
		_ = s.setStatus(id, models.MediaLibraryStatusAttachingListener, "", nil)
		root, err := s.libraryRoot(id)
		if err != nil {
			next := time.Now().UTC().Add(delay)
			_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
			if !waitForRetry(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
			continue
		}
		watcher, err := newRecursiveWatcher(root)
		if err != nil {
			next := time.Now().UTC().Add(delay)
			_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
			if !waitForRetry(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
			continue
		}
		_ = s.setStatus(id, models.MediaLibraryStatusReconciling, "", nil)
		if _, err := s.reconcile(ctx, id, "catch_up"); err != nil {
			_ = watcher.Close()
			next := time.Now().UTC().Add(delay)
			_ = s.setStatus(id, models.MediaLibraryStatusInitializationFailed, CodeMediaLibraryScanFailed, &next)
			if !waitForRetry(ctx, delay) {
				return
			}
			delay = nextRetryDelay(delay)
			continue
		}
		_ = s.setStatus(id, models.MediaLibraryStatusListening, "", nil)
		s.listen(ctx, id, watcher)
		_ = watcher.Close()
		if ctx.Err() != nil {
			return
		}
	}
}
func (s *MediaLibraryService) listen(ctx context.Context, id uint, watcher *fsnotify.Watcher) {
	var library models.MediaLibrary
	if s.db.First(&library, id).Error != nil {
		return
	}
	incremental := time.NewTicker(time.Duration(library.IncrementalMinutes) * time.Minute)
	full := time.NewTicker(time.Duration(library.FullScanIntervalHours) * time.Hour)
	defer incremental.Stop()
	defer full.Stop()
	var debounce *time.Timer
	var debounceC <-chan time.Time
	pending := map[string]fsnotify.Op{}
	needsReconciliation := false
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := osStatDir(event.Name); err == nil && info {
					_ = addWatchTree(watcher, event.Name)
					needsReconciliation = true
				}
			}
			pending[event.Name] |= event.Op
			if debounce == nil {
				debounce = time.NewTimer(600 * time.Millisecond)
			} else {
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(600 * time.Millisecond)
			}
			debounceC = debounce.C
		case <-debounceC:
			if needsReconciliation {
				_, _ = s.reconcile(ctx, id, "event")
			} else {
				_ = s.applyLocalEvents(ctx, id, pending)
			}
			pending = map[string]fsnotify.Op{}
			needsReconciliation = false
			debounceC = nil
		case <-incremental.C:
			_, _ = s.reconcile(ctx, id, "incremental")
		case <-full.C:
			_, _ = s.reconcile(ctx, id, "full")
		case <-watcher.Errors:
			return
		}
	}
}

func (s *MediaLibraryService) applyLocalEvents(ctx context.Context, id uint, events map[string]fsnotify.Op) error {
	lock := s.scanLock(id)
	lock.Lock()
	defer lock.Unlock()
	var library models.MediaLibrary
	var storage models.Storage
	var profile models.MediaClassificationProfile
	if err := s.db.First(&library, id).Error; err != nil {
		return err
	}
	if err := s.db.First(&storage, library.StorageID).Error; err != nil {
		return err
	}
	if err := s.db.First(&profile, library.ProfileID).Error; err != nil {
		return err
	}
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return err
	}
	root, err := medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
	if err != nil {
		return err
	}
	var extensions, ignores []string
	_ = json.Unmarshal([]byte(library.VideoExtensionsJSON), &extensions)
	_ = json.Unmarshal([]byte(library.IgnorePatternsJSON), &ignores)
	generation := library.DirtyGeneration + 1
	now := time.Now().UTC()
	run := models.MediaLibraryScanRun{LibraryID: id, Kind: "event", Status: "running", Generation: generation, StartedAt: now}
	if err := s.db.Create(&run).Error; err != nil {
		return err
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		for path, op := range events {
			constrained, constrainErr := storageConstrain(root, path)
			if constrainErr != nil {
				continue
			}
			rel, relErr := filepath.Rel(root, constrained)
			if relErr != nil || rel == "." {
				continue
			}
			providerRel := "/" + filepath.ToSlash(rel)
			if op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				result := tx.Where("library_id = ? AND (relative_path = ? OR relative_path LIKE ?)", id, providerRel, providerRel+"/%").Delete(&models.MediaLibraryEntry{})
				if result.Error != nil {
					return result.Error
				}
				run.Removed += int(result.RowsAffected)
				continue
			}
			file, accepted, inspectErr := medialibrary.InspectLocalFile(ctx, root, constrained, extensions, ignores)
			if inspectErr != nil {
				if os.IsNotExist(inspectErr) {
					continue
				}
				return inspectErr
			}
			if !accepted {
				continue
			}
			var entry models.MediaLibraryEntry
			findErr := tx.Where("library_id = ? AND relative_path = ?", id, file.RelativePath).First(&entry).Error
			if errors.Is(findErr, gorm.ErrRecordNotFound) {
				run.Added++
				entry = models.MediaLibraryEntry{LibraryID: id, RelativePath: file.RelativePath, CreatedAt: now}
			} else if findErr != nil {
				return findErr
			} else if entry.Size != file.Size || !entry.ModifiedAt.Equal(file.ModifiedAt) {
				run.Updated++
			}
			match := classification.Classify(classification.Metadata{MediaType: classification.MediaType(file.MediaType)}, rules)
			entry.ProviderID, entry.Size, entry.ModifiedAt = file.ProviderID, file.Size, file.ModifiedAt
			entry.MediaType, entry.Title, entry.Season, entry.Episode = file.MediaType, file.Title, file.Season, file.Episode
			entry.MatchStatus, entry.CategoryName, entry.MatchedRuleID = "unmatched", match.CategoryName, match.MatchedRuleID
			entry.LastGeneration, entry.UpdatedAt = generation, now
			if err := tx.Save(&entry).Error; err != nil {
				return err
			}
			run.Discovered++
		}
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", id).Updates(map[string]any{"dirty_generation": generation, "last_scan_at": now, "last_successful_scan_at": now}).Error; err != nil {
			return err
		}
		run.Status = "success"
		run.FinishedAt = &now
		return tx.Save(&run).Error
	})
	if err != nil {
		finished := time.Now().UTC()
		run.Status, run.ErrorCode, run.FinishedAt = "failed", CodeMediaLibraryScanFailed, &finished
		_ = s.db.Save(&run).Error
	}
	return err
}

func storageConstrain(root, path string) (string, error) {
	return storagefs.Constrain(root, path)
}

func waitForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextRetryDelay(delay time.Duration) time.Duration {
	if delay >= 5*time.Minute {
		return 5 * time.Minute
	}
	delay *= 2
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}

func (s *MediaLibraryService) reconcile(ctx context.Context, id uint, kind string) (models.MediaLibraryScanRun, error) {
	lock := s.scanLock(id)
	lock.Lock()
	defer lock.Unlock()
	var library models.MediaLibrary
	var storage models.Storage
	var profile models.MediaClassificationProfile
	if err := s.db.First(&library, id).Error; err != nil {
		return models.MediaLibraryScanRun{}, mediaLibraryNotFound(err)
	}
	if err := s.db.First(&storage, library.StorageID).Error; err != nil {
		return models.MediaLibraryScanRun{}, err
	}
	if err := s.db.First(&profile, library.ProfileID).Error; err != nil {
		return models.MediaLibraryScanRun{}, err
	}
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return models.MediaLibraryScanRun{}, err
	}
	var extensions, ignores []string
	_ = json.Unmarshal([]byte(library.VideoExtensionsJSON), &extensions)
	_ = json.Unmarshal([]byte(library.IgnorePatternsJSON), &ignores)
	generation := library.DirtyGeneration + 1
	run := models.MediaLibraryScanRun{LibraryID: id, Kind: kind, Status: "running", Generation: generation, StartedAt: time.Now().UTC()}
	if err := s.db.Create(&run).Error; err != nil {
		return run, err
	}
	result, scanErr := medialibrary.ScanLocal(ctx, storage.RootPath, library.RelativeRoot, library.Recursive, extensions, ignores)
	finished := time.Now().UTC()
	if scanErr != nil {
		run.Status = "failed"
		run.ErrorCode = CodeMediaLibraryScanFailed
		run.FinishedAt = &finished
		_ = s.db.Save(&run).Error
		// The provider error can contain a physical path. Persist/log only the
		// stable code and scoped identifiers; callers receive the safe envelope.
		s.log.Error().Str("error_code", CodeMediaLibraryScanFailed).Uint("library_id", id).Uint("scan_run_id", run.ID).Str("kind", kind).Msg("Media library scan failed")
		return run, appError(CodeMediaLibraryScanFailed, "媒体库扫描失败", scanErr)
	}
	run.Discovered = len(result.Files)
	run.Partial = result.Partial
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var existing []models.MediaLibraryEntry
		if err := tx.Where("library_id = ?", id).Find(&existing).Error; err != nil {
			return err
		}
		byPath := map[string]models.MediaLibraryEntry{}
		for _, entry := range existing {
			byPath[entry.RelativePath] = entry
		}
		now := time.Now().UTC()
		for _, file := range result.Files {
			entry, exists := byPath[file.RelativePath]
			match := classification.Classify(classification.Metadata{MediaType: classification.MediaType(file.MediaType)}, rules)
			if !exists {
				run.Added++
				entry = models.MediaLibraryEntry{LibraryID: id, RelativePath: file.RelativePath, CreatedAt: now}
			} else if entry.Size != file.Size || !entry.ModifiedAt.Equal(file.ModifiedAt) {
				run.Updated++
			}
			entry.ProviderID = file.ProviderID
			entry.Size = file.Size
			entry.ModifiedAt = file.ModifiedAt
			entry.MediaType = file.MediaType
			entry.Title = file.Title
			entry.Season = file.Season
			entry.Episode = file.Episode
			entry.MatchStatus = "unmatched"
			entry.CategoryName = match.CategoryName
			entry.MatchedRuleID = match.MatchedRuleID
			entry.LastGeneration = generation
			entry.UpdatedAt = now
			if err := tx.Save(&entry).Error; err != nil {
				return err
			}
			delete(byPath, file.RelativePath)
		}
		// A bounded partial enumeration is not proof of deletion. Preserve
		// unseen entries until a complete reconciliation can confirm absence.
		if !result.Partial {
			for _, entry := range byPath {
				run.Removed++
				if err := tx.Delete(&entry).Error; err != nil {
					return err
				}
			}
		}
		updates := map[string]any{"dirty_generation": generation, "baseline_generation": generation, "last_scan_at": finished, "last_successful_scan_at": finished, "profile_revision": profile.Revision, "reclassification_due": false, "status_error_code": "", "next_retry_at": nil}
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		run.Status = "success"
		run.FinishedAt = &finished
		return tx.Save(&run).Error
	})
	if err != nil {
		finished := time.Now().UTC()
		run.Status = "failed"
		run.ErrorCode = CodeMediaLibraryScanFailed
		run.FinishedAt = &finished
		_ = s.db.Save(&run).Error
		return run, err
	}
	s.log.Info().Uint("library_id", id).Uint("scan_run_id", run.ID).Str("kind", kind).Int("discovered", run.Discovered).Int("added", run.Added).Int("updated", run.Updated).Int("removed", run.Removed).Msg("Media library reconciliation completed")
	return run, nil
}

func (s *MediaLibraryService) scanLock(id uint) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock := s.scanLocks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		s.scanLocks[id] = lock
	}
	return lock
}

func (s *MediaLibraryService) setStatus(id uint, status, code string, next *time.Time) error {
	return s.db.Model(&models.MediaLibrary{}).Where("id = ?", id).Updates(map[string]any{"status": status, "status_error_code": code, "next_retry_at": next}).Error
}
func (s *MediaLibraryService) libraryRoot(id uint) (string, error) {
	var library models.MediaLibrary
	var storage models.Storage
	if err := s.db.First(&library, id).Error; err != nil {
		return "", err
	}
	if err := s.db.First(&storage, library.StorageID).Error; err != nil {
		return "", err
	}
	return medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
}
func rootsOverlap(a, b string) bool {
	a = strings.TrimSuffix(a, "/")
	b = strings.TrimSuffix(b, "/")
	if a == "" {
		a = "/"
	}
	if b == "" {
		b = "/"
	}
	return a == b || a == "/" || b == "/" || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func normalizeExtensions(values []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, ".") {
			value = "." + value
		}
		if len(value) > 12 {
			continue
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}
func mediaLibraryNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "媒体库不存在", err)
	}
	return err
}
func mediaLibraryConstraint(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique") && strings.Contains(err.Error(), "name_normalized") {
		return appError(CodeMediaLibraryNameConflict, "媒体库名称已存在", err)
	}
	return err
}

func newRecursiveWatcher(root string) (*fsnotify.Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := addWatchTree(watcher, root); err != nil {
		_ = watcher.Close()
		return nil, err
	}
	return watcher, nil
}
func addWatchTree(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && !medialibrary.IsUnsafeDirectory(path, entry) {
			return watcher.Add(path)
		}
		return nil
	})
}
func osStatDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
