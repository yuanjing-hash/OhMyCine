package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/releaseversion"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	JobTypeMediaReorganization = "media_reorganization"
	reorganizationPreviewTTL   = 5 * time.Minute
	maxReorganizationItems     = 2000
)

type MediaReorganizationPreviewInput struct {
	TransferTaskID string `json:"transfer_task_id"`
	TMDBID         int64  `json:"tmdb_id"`
	MediaType      string `json:"media_type"`
	ConflictPolicy string `json:"conflict_policy"`
}

type MediaReorganizationPlanItem struct {
	Kind            string `json:"kind"`
	OldRelativePath string `json:"old_relative_path"`
	NewRelativePath string `json:"new_relative_path"`
	Action          string `json:"action"`
}

type MediaReorganizationPreviewResult struct {
	LibraryID         uint                          `json:"library_id"`
	IdentityRevision  uint64                        `json:"identity_revision"`
	Title             string                        `json:"title"`
	MediaType         string                        `json:"media_type"`
	Items             []MediaReorganizationPlanItem `json:"items"`
	ConflictCount     int                           `json:"conflict_count"`
	ConfirmationToken string                        `json:"confirmation_token"`
	ExpiresAt         time.Time                     `json:"expires_at"`
}

type MediaReorganizationTaskSummary struct {
	ID                     string     `json:"id"`
	JobID                  string     `json:"job_id"`
	LibraryID              uint       `json:"library_id"`
	SourceIdentityRevision uint64     `json:"source_identity_revision"`
	TargetIdentityRevision uint64     `json:"target_identity_revision"`
	Phase                  string     `json:"phase"`
	TotalItems             int        `json:"total_items"`
	ProcessedItems         int        `json:"processed_items"`
	LastErrorCode          string     `json:"last_error_code"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	FinishedAt             *time.Time `json:"finished_at"`
}

type reorganizationPlan struct {
	Version         int                      `json:"version"`
	LibraryID       uint                     `json:"library_id"`
	TransferTaskID  string                   `json:"transfer_task_id"`
	StorageType     string                   `json:"storage_type"`
	RuleFingerprint string                   `json:"rule_fingerprint"`
	Items           []reorganizationPlanItem `json:"items"`
}

type reorganizationPlanItem struct {
	ManagedItemID    uint   `json:"managed_item_id"`
	Kind             string `json:"kind"`
	OldRelativePath  string `json:"old_relative_path"`
	NewRelativePath  string `json:"new_relative_path"`
	ProviderItemID   string `json:"provider_item_id,omitempty"`
	ProviderParentID string `json:"provider_parent_id,omitempty"`
	Size             int64  `json:"size"`
	Action           string `json:"action"`
}

type reorganizationState struct {
	Version   int           `json:"version"`
	Completed map[uint]bool `json:"completed"`
}

type mediaReorganizationJobPayload struct {
	ReorganizationTaskID string `json:"reorganization_task_id"`
}

type MediaReorganizationService struct {
	db          *gorm.DB
	audit       *AuditService
	queue       *QueueService
	metadata    *MetadataSettingsService
	connections *ConnectionService
	libraries   *MediaLibraryService
	log         zerolog.Logger
}

func NewMediaReorganizationService(db *gorm.DB, audit *AuditService, queue *QueueService, metadata *MetadataSettingsService, connections *ConnectionService, log zerolog.Logger) *MediaReorganizationService {
	return &MediaReorganizationService{db: db, audit: audit, queue: queue, metadata: metadata, connections: connections, log: log}
}

func (s *MediaReorganizationService) SetMediaLibraryService(libraries *MediaLibraryService) {
	s.libraries = libraries
}

func (s *MediaReorganizationService) Preview(ctx context.Context, actor Actor, input MediaReorganizationPreviewInput, request RequestContext) (MediaReorganizationPreviewResult, error) {
	input.TransferTaskID = strings.TrimSpace(input.TransferTaskID)
	input.MediaType = strings.ToLower(strings.TrimSpace(input.MediaType))
	input.ConflictPolicy = strings.ToLower(strings.TrimSpace(input.ConflictPolicy))
	if input.TransferTaskID == "" || input.TMDBID <= 0 || (input.MediaType != "movie" && input.MediaType != "tv") || !validReorganizationConflictPolicy(input.ConflictPolicy) {
		return MediaReorganizationPreviewResult{}, appError(CodeInvalidRequest, "重新整理参数无效", nil)
	}
	transfer, download, library, storage, items, err := s.loadBoundary(actor, input.TransferTaskID)
	if err != nil {
		return MediaReorganizationPreviewResult{}, err
	}
	if s.metadata == nil {
		return MediaReorganizationPreviewResult{}, appError(CodeTMDBUnavailable, "TMDB 未配置", nil)
	}
	client, err := s.metadata.Client()
	if err != nil {
		return MediaReorganizationPreviewResult{}, err
	}
	match, err := client.GetByID(ctx, input.MediaType, input.TMDBID, library.MetadataLanguage)
	if err != nil || match.ID != input.TMDBID || match.MediaType != input.MediaType {
		return MediaReorganizationPreviewResult{}, appError(CodeInvalidRequest, "TMDB 项目验证失败", nil)
	}
	var profile models.MediaClassificationProfile
	if err := s.db.First(&profile, library.ProfileID).Error; err != nil || profile.Revision != library.ProfileRevision {
		return MediaReorganizationPreviewResult{}, appError(CodeReorganizationBoundaryChanged, "媒体库分类规则已变化，请刷新后重试", err)
	}
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return MediaReorganizationPreviewResult{}, appError(CodeProfileValidation, "媒体分类规则无效", err)
	}
	classified := classification.Classify(classificationMetadataForMatch(match), rules)
	if strings.TrimSpace(classified.CategoryName) == "" {
		return MediaReorganizationPreviewResult{}, appError(CodeProfileValidation, "媒体分类结果无效", nil)
	}
	current, err := decodeMediaIdentity(download.IdentitySnapshotJSON)
	if err != nil || download.IdentityRevision == 0 || current.Revision != download.IdentityRevision {
		return MediaReorganizationPreviewResult{}, appError(CodeReorganizationUnavailable, "当前媒体身份不可用于重新整理", nil)
	}
	confidence := match.Confidence
	target := current
	target.Revision = current.Revision + 1
	target.Source, target.Status, target.Locked = mediaIdentitySourceManual, mediaIdentityStatusVerified, true
	target.TMDBID, target.MediaType, target.Title, target.Year, target.Confidence = &match.ID, match.MediaType, match.Title, match.ReleaseYear, &confidence
	target.Category = classified.CategoryName
	targetJSON, err := json.Marshal(target)
	if err != nil {
		return MediaReorganizationPreviewResult{}, err
	}
	plan, conflicts, err := buildReorganizationPlan(library, storage, transfer, current, target, items, input.ConflictPolicy)
	if err != nil {
		return MediaReorganizationPreviewResult{}, err
	}
	conflicts, err = s.applyConflictPreview(ctx, library, storage, &plan, input.ConflictPolicy)
	if err != nil {
		return MediaReorganizationPreviewResult{}, err
	}
	if conflicts > 0 && input.ConflictPolicy == models.MediaLibraryConflictAsk {
		return MediaReorganizationPreviewResult{}, appError(CodeReorganizationConflict, "新位置存在冲突，请选择跳过或重命名后重新预览", nil)
	}
	planRaw, _ := json.Marshal(plan)
	digest := managedManifestDigest(items)
	token, tokenHash, err := newOpaqueConfirmationToken()
	if err != nil {
		return MediaReorganizationPreviewResult{}, err
	}
	now, expires := time.Now().UTC(), time.Now().UTC().Add(reorganizationPreviewTTL)
	preview := models.MediaReorganizationPreview{ID: uuid.NewString(), TokenHash: tokenHash, ActorID: actor.User.ID, LibraryID: library.ID, TransferTaskID: transfer.ID, SourceIdentityRevision: current.Revision, TargetIdentityJSON: string(targetJSON), ManagedManifestDigest: digest, RuleRevision: library.ProfileRevision, ConflictPolicy: input.ConflictPolicy, PlanJSON: string(planRaw), ExpiresAt: expires, CreatedAt: now}
	if err := s.db.Create(&preview).Error; err != nil {
		return MediaReorganizationPreviewResult{}, err
	}
	serverlog.OperationMediaReorganization.Event(s.log.Info()).Uint("library_id", library.ID).Int("items", len(plan.Items)).Int("conflicts", conflicts).Msg(serverlog.OperationMediaReorganization.Message("重新整理预览已生成"))
	_ = s.audit.Record(s.db, &actor.User.ID, "media.reorganization.preview", "transfer_task", transfer.ID, "success", map[string]any{"library_id": library.ID, "items": len(plan.Items), "conflicts": conflicts}, request)
	return publicReorganizationPreview(preview, target, plan, conflicts, token), nil
}

func (s *MediaReorganizationService) Confirm(actor Actor, token string, request RequestContext) (MediaReorganizationTaskSummary, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return MediaReorganizationTaskSummary{}, appError(CodeInvalidRequest, "重新整理确认令牌无效", nil)
	}
	hash := sha256.Sum256([]byte(token))
	var created models.MediaReorganizationTask
	var enqueueInput EnqueueJobInput
	// Read only enough public scheduling metadata here. The callback repeats all
	// security checks while holding the queue/domain transaction.
	var schedulingPreview models.MediaReorganizationPreview
	if err := s.db.Where("token_hash = ?", hex.EncodeToString(hash[:])).First(&schedulingPreview).Error; err != nil {
		return MediaReorganizationTaskSummary{}, appError(CodeReorganizationPreviewExpired, "重新整理预览已失效，请重新预览", nil)
	}
	enqueueInput = EnqueueJobInput{OwnerID: actor.User.ID, JobType: JobTypeMediaReorganization, DisplayName: "修正识别并重新整理", Provider: "media_library", ResourceKey: "library:" + strconv.FormatUint(uint64(schedulingPreview.LibraryID), 10), Payload: mediaReorganizationJobPayload{}}
	jobID := uuid.NewString()
	enqueueInput.Payload = mediaReorganizationJobPayload{ReorganizationTaskID: jobID}
	queued, err := s.queue.EnqueueWith(enqueueInput, func(tx *gorm.DB, job models.Job) error {
		var preview models.MediaReorganizationPreview
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("token_hash = ?", hex.EncodeToString(hash[:])).First(&preview).Error; err != nil {
			return appError(CodeReorganizationPreviewExpired, "重新整理预览已失效，请重新预览", nil)
		}
		if preview.ActorID != actor.User.ID || preview.ConsumedAt != nil || !preview.ExpiresAt.After(time.Now().UTC()) {
			return appError(CodeReorganizationPreviewExpired, "重新整理预览已失效，请重新预览", nil)
		}
		transfer, download, library, _, items, err := s.loadBoundaryWithDB(tx, actor, preview.TransferTaskID)
		if err != nil {
			return err
		}
		var plan reorganizationPlan
		if decodeStrictJSON(preview.PlanJSON, &plan) != nil || plan.LibraryID != library.ID || plan.TransferTaskID != transfer.ID {
			return appError(CodeReorganizationBoundaryChanged, "重新整理计划已变化，请重新预览", nil)
		}
		if download.IdentityRevision != preview.SourceIdentityRevision || library.ProfileRevision != preview.RuleRevision || libraryRuleFingerprint(library) != plan.RuleFingerprint || managedManifestDigest(items) != preview.ManagedManifestDigest {
			return appError(CodeReorganizationBoundaryChanged, "媒体身份、规则或托管清单已变化，请重新预览", nil)
		}
		var target MediaIdentitySnapshot
		if decodeStrictJSON(preview.TargetIdentityJSON, &target) != nil || target.Revision != preview.SourceIdentityRevision+1 || !target.Locked || target.Source != mediaIdentitySourceManual {
			return appError(CodeReorganizationBoundaryChanged, "目标媒体身份无效，请重新预览", nil)
		}
		now, id := time.Now().UTC(), jobID
		stateRaw, _ := json.Marshal(reorganizationState{Version: 1, Completed: map[uint]bool{}})
		created = models.MediaReorganizationTask{ID: id, OwnerID: actor.User.ID, LibraryID: library.ID, TransferTaskID: transfer.ID, SourceIdentityRevision: preview.SourceIdentityRevision, TargetIdentityRevision: target.Revision, TargetIdentityJSON: preview.TargetIdentityJSON, ManagedManifestDigest: preview.ManagedManifestDigest, RuleRevision: preview.RuleRevision, ConflictPolicy: preview.ConflictPolicy, PlanJSON: preview.PlanJSON, StateJSON: string(stateRaw), Phase: models.MediaReorganizationPhaseQueued, TotalItems: len(plan.Items), CreatedAt: now, UpdatedAt: now}
		created.JobID = job.ID
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		if err := tx.Model(&preview).Updates(map[string]any{"consumed_at": now}).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media.reorganization.confirm", "media_reorganization_task", id, "success", map[string]any{"library_id": library.ID, "items": len(plan.Items)}, request)
	})
	if err != nil {
		return MediaReorganizationTaskSummary{}, err
	}
	created.JobID = queued.ID
	serverlog.OperationMediaReorganization.Event(s.log.Info()).Str("task_id", created.ID).Uint("library_id", created.LibraryID).Int("items", created.TotalItems).Msg(serverlog.OperationMediaReorganization.Message("重新整理任务已入队"))
	return reorganizationTaskSummary(created), nil
}

func (s *MediaReorganizationService) Get(actor Actor, id string) (MediaReorganizationTaskSummary, error) {
	var task models.MediaReorganizationTask
	if err := s.db.First(&task, "id = ?", strings.TrimSpace(id)).Error; err != nil {
		return MediaReorganizationTaskSummary{}, appError(CodeNotFound, "重新整理任务不存在", nil)
	}
	if task.OwnerID != actor.User.ID && !actor.Can(authz.PermissionJobsReadAll) {
		return MediaReorganizationTaskSummary{}, appError(CodePermissionDenied, "无权查看该重新整理任务", nil)
	}
	return reorganizationTaskSummary(task), nil
}

func (s *MediaReorganizationService) loadBoundary(actor Actor, transferID string) (models.TransferTask, models.DownloadTask, models.MediaLibrary, models.Storage, []models.MediaManagedItem, error) {
	return s.loadBoundaryWithDB(s.db, actor, transferID)
}

func (s *MediaReorganizationService) loadBoundaryWithDB(db *gorm.DB, actor Actor, transferID string) (models.TransferTask, models.DownloadTask, models.MediaLibrary, models.Storage, []models.MediaManagedItem, error) {
	var transfer models.TransferTask
	if err := db.First(&transfer, "id = ?", transferID).Error; err != nil {
		return transfer, models.DownloadTask{}, models.MediaLibrary{}, models.Storage{}, nil, appError(CodeNotFound, "媒体整理任务不存在", nil)
	}
	if transfer.OwnerID != actor.User.ID && !actor.Can(authz.PermissionJobsControlAll) {
		return transfer, models.DownloadTask{}, models.MediaLibrary{}, models.Storage{}, nil, appError(CodePermissionDenied, "无权重新整理该媒体", nil)
	}
	var download models.DownloadTask
	var library models.MediaLibrary
	var storage models.Storage
	if db.First(&download, "id = ?", transfer.DownloadTaskID).Error != nil || db.First(&library, transfer.LibraryID).Error != nil || db.First(&storage, library.StorageID).Error != nil {
		return transfer, download, library, storage, nil, appError(CodeReorganizationUnavailable, "重新整理边界不可用", nil)
	}
	var items []models.MediaManagedItem
	if err := db.Where("transfer_task_id = ? AND library_id = ? AND managed = ? AND active = ?", transfer.ID, library.ID, true, true).Order("id").Limit(maxReorganizationItems + 1).Find(&items).Error; err != nil {
		return transfer, download, library, storage, nil, err
	}
	if len(items) == 0 || len(items) > maxReorganizationItems {
		return transfer, download, library, storage, nil, appError(CodeReorganizationUnavailable, "没有完整的托管产物清单，不能安全重新整理", nil)
	}
	return transfer, download, library, storage, items, nil
}

func buildReorganizationPlan(library models.MediaLibrary, storage models.Storage, transfer models.TransferTask, source, target MediaIdentitySnapshot, items []models.MediaManagedItem, policy string) (reorganizationPlan, int, error) {
	plan := reorganizationPlan{Version: 1, LibraryID: library.ID, TransferTaskID: transfer.ID, StorageType: storage.Type, RuleFingerprint: libraryRuleFingerprint(library), Items: make([]reorganizationPlanItem, 0, len(items))}
	videoFacts := make([]mediarecognition.FileFact, 0)
	for _, item := range items {
		if item.Kind == models.MediaManagedItemKindVideo {
			videoFacts = append(videoFacts, mediarecognition.FileFact{RelativePath: item.RelativePath, Size: item.Size})
		}
	}
	episodes := map[string]mediarecognition.FileEpisodeFact{}
	if target.MediaType == "tv" {
		for _, fact := range mediarecognition.ResolvePackageEpisodes(videoFacts, mediarecognition.MediaTypeTV).Files {
			episodes[normalizedManifestPath(fact.RelativePath)] = fact
		}
	}
	videoTargets := map[string]string{}
	for _, item := range items {
		if item.Kind != models.MediaManagedItemKindVideo {
			continue
		}
		season, episode := source.Season, source.Episode
		if fact, ok := episodes[normalizedManifestPath(item.RelativePath)]; ok {
			season, episode = fact.Season, fact.Episode
		}
		if target.MediaType == "tv" && episode == nil {
			return plan, 0, appError(CodeTransferEpisodeUnrecognized, "剧集集号无法完整确定，不能重新整理", nil)
		}
		values := transferTemplateValues{Category: target.Category, Title: target.Title, Year: target.Year, Version: releaseversion.Parse(item.RelativePath), Season: season, Episode: episode}
		dirTemplate, filenameTemplate := library.MovieDirectoryTemplate, library.MovieFilenameTemplate
		if target.MediaType == "tv" {
			dirTemplate, filenameTemplate = library.TVDirectoryTemplate, library.TVFilenameTemplate
		}
		dir, err := renderImportTemplate(dirTemplate, values, true)
		if err != nil {
			return plan, 0, appError(CodeInvalidRequest, "媒体库命名规则无效", nil)
		}
		base, err := renderImportTemplate(filenameTemplate, values, false)
		if err != nil {
			return plan, 0, appError(CodeInvalidRequest, "媒体库命名规则无效", nil)
		}
		ext := strings.ToLower(pathpkg.Ext(item.RelativePath))
		if target.MediaType == "movie" && values.Version != "" && !strings.Contains(filenameTemplate, "{version}") {
			base = appendMovieReleaseVersion(base, values.Version)
		}
		newRelative, err := sanitizeTransferRelativePath(pathpkg.Join(dir, base+ext))
		if err != nil {
			return plan, 0, appError(CodeInvalidRequest, "重新整理目标路径无效", nil)
		}
		videoTargets[normalizedManifestPath(item.RelativePath)] = newRelative
		plan.Items = append(plan.Items, managedToPlanItem(item, newRelative))
	}
	for _, item := range items {
		if item.Kind == models.MediaManagedItemKindVideo {
			continue
		}
		newRelative := sidecarReorganizationTarget(item.RelativePath, videoTargets)
		plan.Items = append(plan.Items, managedToPlanItem(item, newRelative))
	}
	sort.Slice(plan.Items, func(i, j int) bool { return plan.Items[i].ManagedItemID < plan.Items[j].ManagedItemID })
	seen, conflicts := map[string]uint{}, 0
	for index := range plan.Items {
		key := strings.ToLower(plan.Items[index].NewRelativePath)
		if prior, ok := seen[key]; ok && prior != plan.Items[index].ManagedItemID {
			return plan, 0, appError(CodeReorganizationConflict, "命名规则产生重复目标", nil)
		}
		seen[key] = plan.Items[index].ManagedItemID
		if strings.EqualFold(plan.Items[index].OldRelativePath, plan.Items[index].NewRelativePath) {
			plan.Items[index].Action = "unchanged"
		}
	}
	_ = policy
	return plan, conflicts, nil
}

func managedToPlanItem(item models.MediaManagedItem, target string) reorganizationPlanItem {
	action := "move"
	if strings.EqualFold(item.RelativePath, target) {
		action = "unchanged"
	}
	return reorganizationPlanItem{ManagedItemID: item.ID, Kind: item.Kind, OldRelativePath: item.RelativePath, NewRelativePath: target, ProviderItemID: item.ProviderItemID, ProviderParentID: item.ProviderParentID, Size: item.Size, Action: action}
}

func sidecarReorganizationTarget(old string, videos map[string]string) string {
	old = normalizedManifestPath(old)
	oldDir, oldBase, ext := pathpkg.Dir(old), strings.TrimSuffix(pathpkg.Base(old), pathpkg.Ext(old)), strings.ToLower(pathpkg.Ext(old))
	for source, target := range videos {
		if pathpkg.Dir(source) != oldDir {
			continue
		}
		sourceBase := strings.TrimSuffix(pathpkg.Base(source), pathpkg.Ext(source))
		if strings.EqualFold(oldBase, sourceBase) || strings.HasPrefix(strings.ToLower(oldBase), strings.ToLower(sourceBase)+".") || strings.HasPrefix(strings.ToLower(oldBase), strings.ToLower(sourceBase)+"-") {
			suffix := oldBase[len(sourceBase):]
			return pathpkg.Join(pathpkg.Dir(target), strings.TrimSuffix(pathpkg.Base(target), pathpkg.Ext(target))+suffix+ext)
		}
	}
	for source, target := range videos {
		if pathpkg.Dir(source) == oldDir {
			return pathpkg.Join(pathpkg.Dir(target), pathpkg.Base(old))
		}
	}
	return old
}

func managedManifestDigest(items []models.MediaManagedItem) string {
	h := sha256.New()
	for _, item := range items {
		_, _ = fmt.Fprintf(h, "%d\x00%d\x00%s\x00%s\x00%s\x00%d\x00%t\n", item.ID, item.IdentityRevision, item.RelativePath, item.ProviderItemID, item.ProviderParentID, item.Size, item.Managed && item.Active)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func libraryRuleFingerprint(library models.MediaLibrary) string {
	h := sha256.Sum256([]byte(strings.Join([]string{strconv.FormatUint(library.ProfileRevision, 10), library.MovieDirectoryTemplate, library.MovieFilenameTemplate, library.TVDirectoryTemplate, library.TVFilenameTemplate, library.RelativeRoot, library.ProviderRootID}, "\x00")))
	return hex.EncodeToString(h[:])
}

func decodeMediaIdentity(raw string) (MediaIdentitySnapshot, error) {
	var snapshot MediaIdentitySnapshot
	if err := decodeStrictJSON(raw, &snapshot); err != nil || snapshot.Version != 1 {
		return snapshot, errors.New("invalid identity snapshot")
	}
	return snapshot, nil
}

func decodeStrictJSON(raw string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func newOpaqueConfirmationToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	hash := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(hash[:]), nil
}

func validReorganizationConflictPolicy(value string) bool {
	return value == models.MediaLibraryConflictAsk || value == models.MediaLibraryConflictSkip || value == models.MediaLibraryConflictRename || value == models.MediaLibraryConflictOverwrite
}

func (s *MediaReorganizationService) applyConflictPreview(ctx context.Context, library models.MediaLibrary, storage models.Storage, plan *reorganizationPlan, policy string) (int, error) {
	conflicts := 0
	for index := range plan.Items {
		item := &plan.Items[index]
		if item.Action == "unchanged" {
			continue
		}
		exists, err := s.reorganizationTargetExists(ctx, library, storage, item.NewRelativePath)
		if err != nil {
			return 0, err
		}
		if !exists {
			continue
		}
		conflicts++
		switch policy {
		case models.MediaLibraryConflictSkip:
			item.Action, item.NewRelativePath = "skip", item.OldRelativePath
		case models.MediaLibraryConflictRename:
			base, ext := strings.TrimSuffix(item.NewRelativePath, pathpkg.Ext(item.NewRelativePath)), pathpkg.Ext(item.NewRelativePath)
			resolved := ""
			for candidate := 2; candidate <= 100; candidate++ {
				value := fmt.Sprintf("%s (corrected %d)%s", base, candidate, ext)
				occupied, checkErr := s.reorganizationTargetExists(ctx, library, storage, value)
				if checkErr != nil {
					return 0, checkErr
				}
				if !occupied {
					resolved = value
					break
				}
			}
			if resolved == "" {
				return 0, appError(CodeReorganizationConflict, "无法生成无冲突的新名称", nil)
			}
			item.NewRelativePath = resolved
		case models.MediaLibraryConflictOverwrite:
			// Reorganization never adopts or deletes a path merely because it
			// exists. Explicit overwrite still requires a separate managed-item
			// ownership claim, which this preview does not have.
			return 0, appError(CodeReorganizationConflict, "目标文件已存在且不属于本次托管清单，请选择跳过或重命名", nil)
		}
	}
	return conflicts, nil
}

func (s *MediaReorganizationService) reorganizationTargetExists(ctx context.Context, library models.MediaLibrary, storage models.Storage, relative string) (bool, error) {
	relative, err := sanitizeTransferRelativePath(relative)
	if err != nil {
		return false, appError(CodeReorganizationBoundaryChanged, "重新整理目标路径无效", nil)
	}
	if storage.Type == models.StorageTypeLocal {
		root, err := medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
		if err != nil {
			return false, appError(CodeReorganizationBoundaryChanged, "媒体库目录无效", nil)
		}
		root, err = (storagefs.LocalDriver{}).CanonicalizeRoot(root)
		if err != nil {
			return false, appError(CodeReorganizationBoundaryChanged, "媒体库目录不可用", nil)
		}
		target := filepath.Join(root, filepath.FromSlash(relative))
		if ensureWithin(root, target) != nil {
			return false, appError(CodeReorganizationBoundaryChanged, "重新整理目标越界", nil)
		}
		_, err = os.Lstat(target)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, appError(CodeReorganizationUnavailable, "重新整理目标不可用", nil)
	}
	if storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil || s.connections == nil || library.ProviderRootID == "" {
		return false, appError(CodeReorganizationUnavailable, "当前存储类型不支持重新整理", nil)
	}
	connection, driver, err := s.connections.driver(*storage.ConnectionID)
	if err != nil || connection.Provider != cloudpkg.ProviderPan115 {
		return false, appError(CodeReorganizationUnavailable, "115 连接不可用", nil)
	}
	root, err := providerItemWithinRoot(ctx, driver, library.ProviderRootID, storage.RootPath)
	if err != nil || !root.IsDir {
		return false, appError(CodeReorganizationBoundaryChanged, "115 媒体库边界已变化", nil)
	}
	parent := root.ID
	for _, segment := range strings.Split(pathpkg.Dir(relative), "/") {
		if segment == "." || segment == "" {
			continue
		}
		items, listErr := listCloudDirectory(ctx, driver, parent)
		if listErr != nil {
			return false, listErr
		}
		matches := namedCloudItems(items, segment)
		if len(matches) == 0 {
			return false, nil
		}
		if len(matches) != 1 || !matches[0].IsDir {
			return false, appError(CodeReorganizationConflict, "115 目标目录存在冲突", nil)
		}
		parent = matches[0].ID
	}
	items, err := listCloudDirectory(ctx, driver, parent)
	if err != nil {
		return false, err
	}
	return len(namedCloudItems(items, pathpkg.Base(relative))) > 0, nil
}

func publicReorganizationPreview(preview models.MediaReorganizationPreview, target MediaIdentitySnapshot, plan reorganizationPlan, conflicts int, token string) MediaReorganizationPreviewResult {
	items := make([]MediaReorganizationPlanItem, 0, len(plan.Items))
	for _, item := range plan.Items {
		items = append(items, MediaReorganizationPlanItem{Kind: item.Kind, OldRelativePath: item.OldRelativePath, NewRelativePath: item.NewRelativePath, Action: item.Action})
	}
	return MediaReorganizationPreviewResult{LibraryID: preview.LibraryID, IdentityRevision: target.Revision, Title: target.Title, MediaType: target.MediaType, Items: items, ConflictCount: conflicts, ConfirmationToken: token, ExpiresAt: preview.ExpiresAt}
}

func reorganizationTaskSummary(task models.MediaReorganizationTask) MediaReorganizationTaskSummary {
	return MediaReorganizationTaskSummary{ID: task.ID, JobID: task.JobID, LibraryID: task.LibraryID, SourceIdentityRevision: task.SourceIdentityRevision, TargetIdentityRevision: task.TargetIdentityRevision, Phase: task.Phase, TotalItems: task.TotalItems, ProcessedItems: task.ProcessedItems, LastErrorCode: task.LastErrorCode, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, FinishedAt: task.FinishedAt}
}

type MediaReorganizationWorker struct{ service *MediaReorganizationService }

func NewMediaReorganizationWorker(service *MediaReorganizationService) *MediaReorganizationWorker {
	return &MediaReorganizationWorker{service: service}
}

func (w *MediaReorganizationWorker) Run(ctx context.Context, runtime JobRuntime, job ClaimedJob) WorkerResult {
	var payload mediaReorganizationJobPayload
	if json.Unmarshal([]byte(job.Job.PayloadJSON), &payload) != nil || payload.ReorganizationTaskID == "" {
		return WorkerResult{ErrorCode: "media_reorganization_payload_invalid", ErrorMessage: "重新整理任务参数无效"}
	}
	var task models.MediaReorganizationTask
	if w.service.db.First(&task, "id = ?", payload.ReorganizationTaskID).Error != nil {
		return WorkerResult{ErrorCode: "media_reorganization_task_missing", ErrorMessage: "重新整理任务不存在"}
	}
	if task.Phase == models.MediaReorganizationPhaseCompleted {
		return WorkerResult{}
	}
	serverlog.OperationMediaReorganization.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Int("items", task.TotalItems).Msg(serverlog.OperationMediaReorganization.Message("开始执行重新整理"))
	var plan reorganizationPlan
	var state reorganizationState
	if decodeStrictJSON(task.PlanJSON, &plan) != nil || decodeStrictJSON(task.StateJSON, &state) != nil || state.Version != 1 || state.Completed == nil {
		return w.fail(task, "media_reorganization_state_invalid", "重新整理任务状态无效")
	}
	var library models.MediaLibrary
	var storage models.Storage
	var download models.DownloadTask
	var transfer models.TransferTask
	if w.service.db.First(&library, task.LibraryID).Error != nil || w.service.db.First(&storage, library.StorageID).Error != nil || w.service.db.First(&transfer, "id = ?", task.TransferTaskID).Error != nil || w.service.db.First(&download, "id = ?", transfer.DownloadTaskID).Error != nil {
		return w.fail(task, CodeReorganizationUnavailable, "重新整理边界不可用")
	}
	if library.ProfileRevision != task.RuleRevision || libraryRuleFingerprint(library) != plan.RuleFingerprint || download.IdentityRevision != task.SourceIdentityRevision {
		return w.fail(task, CodeReorganizationBoundaryChanged, "媒体身份或规则已变化，请重新预览")
	}
	if err := w.validatePlanBoundary(task, plan, state); err != nil {
		return w.fail(task, ErrorCode(err), ErrorMessage(err))
	}
	_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.MediaReorganizationPhaseExecuting, "last_error_code": "", "updated_at": time.Now().UTC()}).Error
	var err error
	if storage.Type == models.StorageTypeLocal {
		err = w.runLocal(ctx, runtime, &task, library, storage, plan, &state)
	} else if storage.Type == models.StorageTypePan115 {
		err = w.runCloud(ctx, runtime, &task, library, storage, plan, &state)
	} else {
		err = appError(CodeReorganizationUnavailable, "当前存储类型不支持重新整理", nil)
	}
	if err != nil {
		return w.fail(task, ErrorCode(err), ErrorMessage(err))
	}
	if err := w.finalize(task, download, library, plan); err != nil {
		return w.fail(task, "media_reorganization_reconcile_failed", "重新整理对账失败")
	}
	// Re-scan through the normal library pipeline so metadata snapshots,
	// NFO/JPG/STRM manifests, stale-artifact cleanup and downstream refresh all
	// keep their existing ownership and rate-limit contracts.
	if w.service.libraries != nil {
		if _, scanErr := w.service.libraries.reconcile(ctx, library.ID, "reorganization"); scanErr != nil {
			serverlog.OperationMediaReorganization.Event(w.service.log.Warn()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Str("error_code", ErrorCode(scanErr)).Msg(serverlog.OperationMediaReorganization.Message("文件已安全重整，媒体产物刷新将在后续扫描重试"))
		}
	}
	serverlog.OperationMediaReorganization.Event(w.service.log.Info()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Int("items", task.TotalItems).Msg(serverlog.OperationMediaReorganization.Message("重新整理完成"))
	return WorkerResult{}
}

func (w *MediaReorganizationWorker) validatePlanBoundary(task models.MediaReorganizationTask, plan reorganizationPlan, state reorganizationState) error {
	if len(plan.Items) == 0 || len(plan.Items) > maxReorganizationItems {
		return appError(CodeReorganizationBoundaryChanged, "托管清单已变化", nil)
	}
	for _, planned := range plan.Items {
		var current models.MediaManagedItem
		if err := w.service.db.First(&current, "id = ?", planned.ManagedItemID).Error; err != nil {
			return appError(CodeReorganizationBoundaryChanged, "托管清单已变化", nil)
		}
		expectedPath := planned.OldRelativePath
		if state.Completed[planned.ManagedItemID] && planned.Action != "skip" && planned.Action != "unchanged" {
			expectedPath = planned.NewRelativePath
		}
		if current.LibraryID != task.LibraryID || current.TransferTaskID != task.TransferTaskID || !current.Managed || !current.Active || current.RelativePath != expectedPath || current.Size != planned.Size {
			return appError(CodeReorganizationBoundaryChanged, "托管清单已变化", nil)
		}
		if planned.ProviderItemID != "" && current.ProviderItemID != planned.ProviderItemID {
			return appError(CodeReorganizationBoundaryChanged, "115 托管文件身份已变化", nil)
		}
	}
	return nil
}

func (w *MediaReorganizationWorker) runLocal(ctx context.Context, runtime JobRuntime, task *models.MediaReorganizationTask, library models.MediaLibrary, storage models.Storage, plan reorganizationPlan, state *reorganizationState) error {
	root, err := medialibrary.ResolveRoot(storage.RootPath, library.RelativeRoot)
	if err != nil {
		return appError(CodeReorganizationBoundaryChanged, "媒体库目录无效", nil)
	}
	root, err = (storagefs.LocalDriver{}).CanonicalizeRoot(root)
	if err != nil {
		return appError(CodeReorganizationBoundaryChanged, "媒体库目录不可用", nil)
	}
	for _, item := range plan.Items {
		if state.Completed[item.ManagedItemID] || item.Action == "unchanged" || item.Action == "skip" {
			state.Completed[item.ManagedItemID] = true
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		oldPath, newPath := filepath.Join(root, filepath.FromSlash(item.OldRelativePath)), filepath.Join(root, filepath.FromSlash(item.NewRelativePath))
		if ensureWithin(root, oldPath) != nil || ensureWithin(root, newPath) != nil || ensureSafeDirectoryPath(root, filepath.Dir(oldPath), false) != nil || ensureSafeDirectoryPath(root, filepath.Dir(newPath), true) != nil {
			return appError(CodeReorganizationBoundaryChanged, "托管文件路径越界", nil)
		}
		oldInfo, oldErr := os.Lstat(oldPath)
		newInfo, newErr := os.Lstat(newPath)
		if errors.Is(oldErr, os.ErrNotExist) && newErr == nil && newInfo.Size() == item.Size { /* previous attempt */
		} else {
			if oldErr != nil || oldInfo.IsDir() || oldInfo.Size() != item.Size {
				return appError(CodeReorganizationBoundaryChanged, "托管文件已变化", nil)
			}
			if newErr == nil {
				return appError(CodeReorganizationConflict, "重新整理目标已存在", nil)
			}
			if !errors.Is(newErr, os.ErrNotExist) {
				return appError(CodeReorganizationUnavailable, "重新整理目标不可用", nil)
			}
			if err := os.Rename(oldPath, newPath); err != nil {
				return appError(CodeReorganizationUnavailable, "移动托管文件失败", nil)
			}
		}
		state.Completed[item.ManagedItemID] = true
		if err := w.persistProgress(task, state, item.ManagedItemID, item.NewRelativePath, "", ""); err != nil {
			return err
		}
		if err := heartbeatReorganization(runtime, len(state.Completed), len(plan.Items)); err != nil {
			return err
		}
	}
	return nil
}

func (w *MediaReorganizationWorker) runCloud(ctx context.Context, runtime JobRuntime, task *models.MediaReorganizationTask, library models.MediaLibrary, storage models.Storage, plan reorganizationPlan, state *reorganizationState) error {
	if storage.ConnectionID == nil || w.service.connections == nil || library.ProviderRootID == "" {
		return appError(CodeReorganizationUnavailable, "115 重新整理边界不可用", nil)
	}
	connection, driver, err := w.service.connections.driver(*storage.ConnectionID)
	if err != nil {
		return err
	}
	mutations, ok := driver.(cloudpkg.MutationDriver)
	if !ok || connection.Provider != cloudpkg.ProviderPan115 {
		return appError(CodeReorganizationUnavailable, "115 移动能力不可用", nil)
	}
	root, err := providerItemWithinRoot(ctx, driver, library.ProviderRootID, storage.RootPath)
	if err != nil || !root.IsDir {
		return appError(CodeReorganizationBoundaryChanged, "115 媒体库边界已变化", nil)
	}
	directories := map[string]string{".": root.ID}
	for _, item := range plan.Items {
		if state.Completed[item.ManagedItemID] || item.Action == "unchanged" || item.Action == "skip" {
			state.Completed[item.ManagedItemID] = true
			continue
		}
		current, err := providerItemWithinRoot(ctx, driver, item.ProviderItemID, root.ID)
		if err != nil || current.IsDir || current.Size != item.Size {
			return appError(CodeReorganizationBoundaryChanged, "115 托管文件已变化", nil)
		}
		parentID, err := ensureReorganizationCloudDirectory(ctx, driver, mutations, root.ID, pathpkg.Dir(item.NewRelativePath), directories)
		if err != nil {
			return err
		}
		name := pathpkg.Base(item.NewRelativePath)
		// The preview is only advisory. Re-check the exact cloud target before
		// every mutation so a file created after preview is never overwritten or
		// adopted. The current managed item itself is allowed for retry recovery.
		if err := ensureReorganizationCloudTargetAvailable(ctx, driver, parentID, name, current.ID); err != nil {
			return err
		}
		if current.ParentID != parentID {
			if err := mutations.Move(ctx, current.ID, parentID); err != nil {
				return appError(CodeReorganizationUnavailable, "115 移动失败", nil)
			}
		}
		current, err = driver.Stat(ctx, current.ID)
		if err != nil {
			return appError(CodeReorganizationUnavailable, "115 移动对账失败", nil)
		}
		if current.Name != name {
			if err := mutations.Rename(ctx, current.ID, name); err != nil {
				return appError(CodeReorganizationUnavailable, "115 重命名失败", nil)
			}
		}
		state.Completed[item.ManagedItemID] = true
		if err := w.persistProgress(task, state, item.ManagedItemID, item.NewRelativePath, current.ID, parentID); err != nil {
			return err
		}
		if err := heartbeatReorganization(runtime, len(state.Completed), len(plan.Items)); err != nil {
			return err
		}
	}
	return nil
}

func ensureReorganizationCloudTargetAvailable(ctx context.Context, driver cloudpkg.Driver, parentID, name, currentItemID string) error {
	targets, err := listCloudDirectory(ctx, driver, parentID)
	if err != nil {
		return appError(CodeReorganizationUnavailable, "115 目标目录检查失败", nil)
	}
	for _, target := range namedCloudItems(targets, name) {
		if target.ID != currentItemID {
			return appError(CodeReorganizationConflict, "重新整理目标已存在，请重新预览", nil)
		}
	}
	return nil
}

func ensureReorganizationCloudDirectory(ctx context.Context, driver cloudpkg.Driver, mutations cloudpkg.MutationDriver, rootID, relative string, cache map[string]string) (string, error) {
	relative = pathpkg.Clean(relative)
	if relative == "." || relative == "" {
		return rootID, nil
	}
	parent, walked := rootID, "."
	for _, segment := range strings.Split(relative, "/") {
		walked = pathpkg.Join(walked, segment)
		if cached := cache[walked]; cached != "" {
			parent = cached
			continue
		}
		items, err := listCloudDirectory(ctx, driver, parent)
		if err != nil {
			return "", err
		}
		matches := namedCloudItems(items, segment)
		if len(matches) > 1 || (len(matches) == 1 && !matches[0].IsDir) {
			return "", appError(CodeReorganizationConflict, "115 目标目录存在冲突", nil)
		}
		if len(matches) == 1 {
			parent = matches[0].ID
		} else {
			created, err := mutations.CreateDirectory(ctx, parent, segment)
			if err != nil {
				return "", err
			}
			parent = created.ID
		}
		if _, err := providerItemWithinRoot(ctx, driver, parent, rootID); err != nil {
			return "", appError(CodeReorganizationBoundaryChanged, "115 目录越界", nil)
		}
		cache[walked] = parent
	}
	return parent, nil
}

func (w *MediaReorganizationWorker) persistProgress(task *models.MediaReorganizationTask, state *reorganizationState, itemID uint, relative, providerID, parentID string) error {
	raw, _ := json.Marshal(state)
	now := time.Now().UTC()
	return w.service.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{"relative_path": relative, "identity_revision": task.TargetIdentityRevision, "updated_at": now}
		if providerID != "" {
			updates["provider_item_id"], updates["provider_parent_id"] = providerID, parentID
		}
		result := tx.Model(&models.MediaManagedItem{}).Where("id = ? AND library_id = ? AND managed = ? AND active = ?", itemID, task.LibraryID, true, true).Updates(updates)
		if result.Error != nil || result.RowsAffected != 1 {
			return appError(CodeReorganizationBoundaryChanged, "托管清单已变化", result.Error)
		}
		return tx.Model(task).Updates(map[string]any{"state_json": string(raw), "phase": models.MediaReorganizationPhaseExecuting, "processed_items": len(state.Completed), "updated_at": now}).Error
	})
}

func (w *MediaReorganizationWorker) finalize(task models.MediaReorganizationTask, download models.DownloadTask, library models.MediaLibrary, plan reorganizationPlan) error {
	var target MediaIdentitySnapshot
	if decodeStrictJSON(task.TargetIdentityJSON, &target) != nil {
		return errors.New("invalid target identity")
	}
	now := time.Now().UTC()
	return w.service.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.DownloadTask{}).Where("id = ? AND identity_revision = ?", download.ID, task.SourceIdentityRevision).Updates(map[string]any{"identity_source": mediaIdentitySourceManual, "identity_status": mediaIdentityStatusVerified, "identity_locked": true, "identity_revision": target.Revision, "identity_snapshot_json": task.TargetIdentityJSON, "recognition_override_tmdb_id": target.TMDBID, "recognition_override_media_type": target.MediaType, "scrape_tmdb_id": target.TMDBID, "scrape_media_type": target.MediaType, "scrape_title": target.Title, "scrape_year": target.Year, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return appError(CodeReorganizationBoundaryChanged, "媒体身份已变化", result.Error)
		}
		if err := tx.Model(&models.MediaManagedItem{}).Where("transfer_task_id = ? AND library_id = ? AND managed = ? AND active = ?", task.TransferTaskID, library.ID, true, true).Updates(map[string]any{"identity_revision": target.Revision, "updated_at": now}).Error; err != nil {
			return err
		}
		for _, item := range plan.Items {
			if err := tx.Model(&models.MediaLibraryEntry{}).Where("library_id = ? AND relative_path = ?", library.ID, item.OldRelativePath).Updates(map[string]any{"relative_path": item.NewRelativePath, "title": target.Title, "media_type": target.MediaType, "tmdb_id": target.TMDBID, "release_year": target.Year, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Updates(map[string]any{"dirty_generation": gorm.Expr("dirty_generation + 1"), "content_revision": gorm.Expr("content_revision + 1"), "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&task).Updates(map[string]any{"phase": models.MediaReorganizationPhaseCompleted, "processed_items": task.TotalItems, "last_error_code": "", "finished_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		return w.service.audit.Record(tx, &task.OwnerID, "media.reorganization.complete", "media_reorganization_task", task.ID, "success", map[string]any{"library_id": library.ID, "items": task.TotalItems}, RequestContext{})
	})
}

func heartbeatReorganization(runtime JobRuntime, processed, total int) error {
	p, t := int64(processed), int64(total)
	progress := float64(100)
	if total > 0 {
		progress = float64(processed) * 100 / float64(total)
	}
	return runtime.Heartbeat(&progress, &p, &t, nil, nil)
}

func (w *MediaReorganizationWorker) fail(task models.MediaReorganizationTask, code, message string) WorkerResult {
	if code == "INTERNAL_ERROR" || code == "" {
		code = "media_reorganization_failed"
	}
	_ = w.service.db.Model(&task).Updates(map[string]any{"phase": models.MediaReorganizationPhaseFailed, "last_error_code": code, "updated_at": time.Now().UTC()}).Error
	serverlog.OperationMediaReorganization.Event(w.service.log.Error()).Str("task_id", task.ID).Uint("library_id", task.LibraryID).Str("error_code", code).Msg(serverlog.OperationMediaReorganization.Message("重新整理失败"))
	return WorkerResult{ErrorCode: code, ErrorMessage: message}
}
