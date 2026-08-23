package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/directory"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/mediatool"
	"gorm.io/gorm"
)

const (
	pluginDownloadMaxAssets       = 8
	pluginDownloadMaxMediaBytes   = int64(2 << 40)
	pluginDownloadMaxSidecarBytes = int64(64 << 20)
	pluginDownloadRootName        = ".ohmycine-plugin-downloads"
)

type pluginDownloadAssetGateway interface {
	OpenAssetForPlugin(context.Context, string, string, string, string) (*hostapi.AssetStream, error)
}

type SubmitPluginDownloadInput struct {
	ConnectionID   string
	ItemID         string
	SegmentID      string
	VersionID      string
	VariantID      string
	MediaLibraryID uint
	Priority       int
	DisplayName    string
}

// PluginDownloadExecutor executes provider-neutral, host-validated
// DownloadPlans. Plugins resolve opaque assets; only the Server chooses paths,
// invokes media tools, and hands a completed manifest to the import pipeline.
type PluginDownloadExecutor struct {
	downloads *DownloadService
	plugins   *PluginRepositoryService
	assets    pluginDownloadAssetGateway
	tool      mediatool.Tool
}

func NewPluginDownloadExecutor(downloads *DownloadService, plugins *PluginRepositoryService, assets pluginDownloadAssetGateway, tool mediatool.Tool) *PluginDownloadExecutor {
	return &PluginDownloadExecutor{downloads: downloads, plugins: plugins, assets: assets, tool: tool}
}

func (e *PluginDownloadExecutor) Submit(ctx context.Context, actor Actor, input SubmitPluginDownloadInput, request RequestContext) (DownloadTaskSummary, error) {
	if e == nil || e.downloads == nil || e.plugins == nil || e.assets == nil {
		return DownloadTaskSummary{}, appError(CodePluginRuntimeUnavailable, "插件下载服务不可用", nil)
	}
	if !actor.Can(authz.PermissionDownloadsCreate) {
		return DownloadTaskSummary{}, appError(CodePermissionDenied, "无权创建下载任务", nil)
	}
	if !safeOnlineText(input.ConnectionID, 128) || !safeOnlineText(input.ItemID, maxOnlineIdentifierBytes) || !safeOnlineText(input.SegmentID, maxOnlineIdentifierBytes) || !safeOnlineText(input.VersionID, maxOnlineIdentifierBytes) || !safeOptionalOnlineText(input.VariantID, maxOnlineIdentifierBytes) || input.Priority < -100 || input.Priority > 100 {
		return DownloadTaskSummary{}, appError(CodeInvalidRequest, "插件下载请求无效", nil)
	}
	connection, manifest, err := e.plugins.onlineConnection(input.ConnectionID)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	if !manifestHasCapability(manifest, contract.CapabilityMediaDownload) {
		return DownloadTaskSummary{}, appError(CodePermissionDenied, "在线媒体来源不支持下载", nil)
	}
	if !manifestHasPermission(manifest, contract.PermissionDownloadPlan) || !e.hasActivePermission(connection.PluginID, contract.PermissionDownloadPlan) {
		return DownloadTaskSummary{}, appError(CodePermissionDenied, "在线媒体插件未获下载计划权限", nil)
	}
	target, profile, err := e.downloads.resolveDownloadTarget(ctx, models.Downloader{Type: models.DownloaderTypePluginHTTP}, input.MediaLibraryID, "plugin_plan")
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	if target.StorageType != models.StorageTypeLocal {
		return DownloadTaskSummary{}, appError(CodeMediaLibraryStorageUnavailable, "站点下载当前需要本地媒体库作为入库目标", nil)
	}
	rules, err := classification.DecodeStrict([]byte(profile.RulesJSON))
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "媒体分类规则无效", err)
	}
	canonicalRules, err := classification.CanonicalJSON(rules)
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "媒体分类规则无效", err)
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return DownloadTaskSummary{}, appError(CodeProfileValidation, "媒体识别与命名配置无效", err)
	}
	staging, err := e.downloads.settings.Snapshot(ctx, models.DownloaderTypePluginHTTP)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	displayName, err := normalizeDownloadDisplayName(input.DisplayName, connection.Name)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	taskID := uuid.NewString()
	source := downloadSourceEnvelope{Kind: "plugin_plan", PluginConnectionID: connection.ID, PluginItemID: input.ItemID, PluginSegmentID: input.SegmentID, PluginVersionID: input.VersionID, PluginVariantID: input.VariantID}
	rawSource, err := json.Marshal(source)
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	encryptedSource, err := e.downloads.credentials.Encrypt(downloadSourcePurpose(taskID), string(rawSource))
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	now := time.Now().UTC()
	record := models.DownloadTask{
		ID: taskID, OwnerID: actor.User.ID, DownloaderName: connection.Name, ProviderType: models.DownloaderTypePluginHTTP,
		SourceCiphertext: encryptedSource, StagingAbsolutePath: staging.AbsolutePath, SourceOrigin: models.DownloadSourceOriginPlugin,
		ProfileID: profile.ID, ProfileRevision: profile.Revision, ProfileRulesJSON: canonicalRules,
		ProfileBuiltinRecognitionPacksJSON: organization.BuiltinRecognitionPacksJSON, ProfileRecognitionRulesJSON: organization.RecognitionRulesJSON,
		TargetLibraryID: &target.LibraryID, TargetLibraryName: target.LibraryName, TargetStorageID: &target.StorageID,
		TargetStorageType: target.StorageType, TargetStorageRoot: target.StorageRoot, TargetRelativeRoot: target.RelativeRoot,
		TransferMode: target.TransferMode, ConflictPolicy: target.ConflictPolicy,
		MovieDirectoryTemplate: organization.MovieDirectoryTemplate, MovieFilenameTemplate: organization.MovieFilenameTemplate,
		TVDirectoryTemplate: organization.TVDirectoryTemplate, TVFilenameTemplate: organization.TVFilenameTemplate,
		DisplayName: displayName, Phase: models.DownloadTaskStatusQueued, CreatedAt: now, UpdatedAt: now,
	}
	job, err := e.downloads.queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", Priority: input.Priority, DisplayName: displayName, Provider: models.DownloaderTypePluginHTTP, ResourceKey: "plugin:" + connection.PluginID, Payload: downloadJobPayload{DownloadTaskID: taskID}}, func(tx *gorm.DB, job models.Job) error {
		record.JobID = job.ID
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return e.downloads.audit.Record(tx, &actor.User.ID, "download.create", "download_task", taskID, "success", map[string]any{"provider_type": models.DownloaderTypePluginHTTP, "plugin_id": connection.PluginID, "media_library_id": target.LibraryID, "source_origin": models.DownloadSourceOriginPlugin}, request)
	})
	if err != nil {
		return DownloadTaskSummary{}, err
	}
	serverlog.OperationPluginDownload.Event(e.downloads.log.Info()).Str("task_id", taskID).Str("plugin_id", connection.PluginID).Uint("library_id", target.LibraryID).Msg(serverlog.OperationPluginDownload.Message("下载计划已进入队列"))
	return downloadTaskSummary(record, job.Status), nil
}

func (e *PluginDownloadExecutor) hasActivePermission(pluginID string, kind contract.PermissionKind) bool {
	var installation models.PluginInstallation
	if e.downloads.db.Select("active_package_id", "status").First(&installation, "plugin_id = ?", pluginID).Error != nil || installation.Status != models.PluginInstallationEnabled {
		return false
	}
	var grants []models.PluginPermissionGrant
	if e.downloads.db.Where("plugin_id = ? AND plugin_package_id = ?", pluginID, installation.ActivePackageID).Find(&grants).Error != nil {
		return false
	}
	for _, grant := range grants {
		var permission contract.Permission
		decoder := json.NewDecoder(strings.NewReader(grant.PermissionJSON))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&permission) == nil && decoder.Decode(&struct{}{}) == io.EOF && permission.Kind == kind {
			return true
		}
	}
	return false
}

func manifestHasPermission(manifest contract.Manifest, kind contract.PermissionKind) bool {
	for _, permission := range manifest.Permissions {
		if permission.Kind == kind {
			return true
		}
	}
	return false
}

func (e *PluginDownloadExecutor) Run(ctx context.Context, runtime JobRuntime, job ClaimedJob) WorkerResult {
	var payload downloadJobPayload
	if json.Unmarshal([]byte(job.Job.PayloadJSON), &payload) != nil || payload.DownloadTaskID == "" {
		return pluginDownloadFailure("plugin_download_request_invalid", "插件下载任务参数无效", false)
	}
	var task models.DownloadTask
	if err := e.downloads.db.First(&task, "id = ? AND provider_type = ?", payload.DownloadTaskID, models.DownloaderTypePluginHTTP).Error; err != nil {
		return pluginDownloadFailure("plugin_download_task_unavailable", "插件下载任务不存在", false)
	}
	source, err := e.decryptSource(task)
	if err != nil {
		return e.fail(task, "plugin_download_request_invalid", "插件下载任务来源无效", false)
	}
	var connection models.PluginConnection
	if e.downloads.db.Select("plugin_id").First(&connection, "id = ? AND enabled = ?", source.PluginConnectionID, true).Error != nil || connection.PluginID == "" {
		return e.fail(task, CodePluginOnlineLibraryUnavailable, "在线媒体来源暂时不可用", true)
	}
	taskRoot, err := e.prepareTaskRoot(ctx, task)
	if err != nil {
		return e.fail(task, CodeDownloadStagingUnavailable, "下载暂存目录不可用", false)
	}
	// Every attempt recomputes the plan from immutable identities. Opaque asset
	// references are intentionally never persisted in the job checkpoint.
	for resolveAttempt := 0; resolveAttempt < 2; resolveAttempt++ {
		if err := e.cleanAttemptFiles(taskRoot); err != nil {
			return e.fail(task, "plugin_download_cleanup_failed", "插件下载暂存清理失败", false)
		}
		plan, err := e.resolvePlan(ctx, task, source)
		if err != nil {
			if ctx.Err() != nil {
				return WorkerResult{}
			}
			code := ErrorCode(err)
			retryable := code == CodePluginRuntimeUnavailable || code == CodePluginOnlineLibraryUnavailable
			return e.fail(task, code, "在线媒体下载方案解析失败", retryable)
		}
		manifest, sourceManifest, err := e.executePlan(ctx, runtime, &task, taskRoot, connection.PluginID, plan)
		if err != nil {
			if ctx.Err() != nil {
				return WorkerResult{}
			}
			if hostapi.ErrorCode(err) == "plugin_asset_expired" && resolveAttempt == 0 {
				continue
			}
			code := pluginDownloadErrorCode(err)
			retryable := code == "plugin_asset_upstream_unavailable" || code == CodePluginRuntimeUnavailable
			return e.fail(task, code, pluginDownloadErrorMessage(code), retryable)
		}
		selected, err := NewDownloadWorker(e.downloads).verifyCompleted(ctx, &task, manifest)
		if err != nil {
			return e.fail(task, ErrorCode(err), "下载完成，但媒体识别或清单校验失败", false)
		}
		if e.downloads.transfers == nil {
			return e.fail(task, "transfer_service_unavailable", "入库服务不可用", false)
		}
		if err := e.downloads.transfers.EnqueuePackage(task, selected, sourceManifest); err != nil {
			return e.fail(task, ErrorCode(err), "下载完成，但入库任务创建失败", true)
		}
		now := time.Now().UTC()
		progress := 100.0
		_ = runtime.Heartbeat(&progress, nil, nil, nil, nil)
		_ = e.downloads.db.Model(&task).Updates(map[string]any{"phase": models.DownloadTaskStatusCompleted, "provider_status": "completed", "progress": progress, "last_error_code": "", "last_error_message": "", "finished_at": now, "updated_at": now}).Error
		serverlog.OperationPluginDownload.Event(e.downloads.log.Info()).Str("task_id", task.ID).Uint("library_id", *task.TargetLibraryID).Int("files", len(selected.Files)).Msg(serverlog.OperationPluginDownload.Message("下载完成，已进入媒体整理队列"))
		return WorkerResult{}
	}
	return e.fail(task, "plugin_asset_expired", "下载资源已过期，请重试", true)
}

func (e *PluginDownloadExecutor) Interrupt(_ context.Context, job ClaimedJob, action string) error {
	var task models.DownloadTask
	if err := e.downloads.db.First(&task, "job_id = ? AND provider_type = ?", job.Job.ID, models.DownloaderTypePluginHTTP).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	updates := map[string]any{"updated_at": now}
	switch action {
	case "pause":
		updates["phase"] = models.DownloadTaskStatusPaused
	case "cancel":
		updates["phase"], updates["finished_at"], updates["last_error_code"], updates["last_error_message"] = models.DownloadTaskStatusCancelled, now, "", ""
	default:
		return appError(CodeInvalidRequest, "未知下载控制操作", nil)
	}
	return e.downloads.db.Model(&task).Updates(updates).Error
}

func (e *PluginDownloadExecutor) decryptSource(task models.DownloadTask) (downloadSourceEnvelope, error) {
	plain, err := e.downloads.credentials.Decrypt(downloadSourcePurpose(task.ID), task.SourceCiphertext)
	if err != nil {
		return downloadSourceEnvelope{}, err
	}
	var source downloadSourceEnvelope
	if json.Unmarshal([]byte(plain), &source) != nil || source.Kind != "plugin_plan" || !safeOnlineText(source.PluginConnectionID, 128) || !safeOnlineText(source.PluginItemID, maxOnlineIdentifierBytes) || !safeOnlineText(source.PluginSegmentID, maxOnlineIdentifierBytes) || !safeOnlineText(source.PluginVersionID, maxOnlineIdentifierBytes) || !safeOptionalOnlineText(source.PluginVariantID, maxOnlineIdentifierBytes) {
		return downloadSourceEnvelope{}, errors.New("invalid plugin download source")
	}
	return source, nil
}

func (e *PluginDownloadExecutor) resolvePlan(ctx context.Context, task models.DownloadTask, source downloadSourceEnvelope) (contract.DownloadPlan, error) {
	_ = e.downloads.db.Model(&task).Updates(map[string]any{"phase": models.DownloadTaskStatusResolving, "provider_status": "resolving", "progress": nil, "bytes_completed": nil, "bytes_total": nil, "download_speed": nil, "eta_seconds": nil, "last_error_code": "", "last_error_message": "", "finished_at": nil, "updated_at": time.Now().UTC()}).Error
	raw, err := e.plugins.InvokePlugin(ctx, source.PluginConnectionID, string(contract.CapabilityMediaDownload), map[string]any{"connectionId": source.PluginConnectionID, "itemId": source.PluginItemID, "segmentId": source.PluginSegmentID, "versionId": source.PluginVersionID, "variantId": emptyAsNil(source.PluginVariantID)})
	if err != nil {
		return contract.DownloadPlan{}, err
	}
	var envelope pluginErrorEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.PluginError != nil {
		return contract.DownloadPlan{}, appError(CodePluginOnlineLibraryUnavailable, "在线媒体来源暂时不可用", nil)
	}
	var plan contract.DownloadPlan
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return contract.DownloadPlan{}, appError(CodePluginResponseInvalid, "插件下载方案无效", nil)
	}
	if err := validateDownloadPlan(plan, source); err != nil {
		return contract.DownloadPlan{}, appError(CodePluginResponseInvalid, "插件下载方案无效", err)
	}
	return plan, nil
}

func validateDownloadPlan(plan contract.DownloadPlan, source downloadSourceEnvelope) error {
	if plan.WorkID != source.PluginItemID || plan.SegmentID != source.PluginSegmentID || plan.VersionID != source.PluginVersionID || plan.VariantID == "" || (source.PluginVariantID != "" && plan.VariantID != source.PluginVariantID) || len(plan.Assets) == 0 || len(plan.Assets) > pluginDownloadMaxAssets {
		return errors.New("download plan identity is invalid")
	}
	if _, err := safeSuggestedFilename(plan.SuggestedFileName, plan.VariantID); err != nil {
		return err
	}
	ids := make(map[string]contract.DownloadAsset, len(plan.Assets))
	videoCount := 0
	audioCount := 0
	for _, asset := range plan.Assets {
		if !safeOnlineText(asset.ID, 128) || asset.HeadersRef != "" || asset.ExpectedBytes < 0 || asset.ExpectedBytes > pluginDownloadMaxMediaBytes {
			return errors.New("download asset is invalid")
		}
		if _, err := uuid.Parse(asset.URLRef); err != nil {
			return errors.New("download asset reference is invalid")
		}
		switch asset.Kind {
		case "video":
			videoCount++
		case "audio":
			audioCount++
		case "subtitle", "danmaku":
		default:
			return errors.New("download asset kind is invalid")
		}
		if (asset.Kind == "subtitle" || asset.Kind == "danmaku") && asset.ExpectedBytes > pluginDownloadMaxSidecarBytes {
			return errors.New("download sidecar is too large")
		}
		if _, exists := ids[asset.ID]; exists {
			return errors.New("download asset id is duplicated")
		}
		ids[asset.ID] = asset
	}
	if videoCount != 1 {
		return errors.New("download plan must contain exactly one video")
	}
	if plan.Merge != nil {
		if audioCount != 1 || plan.Merge.Kind != "dash-av" || plan.Merge.VideoAssetID == plan.Merge.AudioAssetID || ids[plan.Merge.VideoAssetID].Kind != "video" || ids[plan.Merge.AudioAssetID].Kind != "audio" {
			return errors.New("download merge topology is invalid")
		}
	} else if audioCount != 0 {
		return errors.New("detached audio requires a merge plan")
	}
	return nil
}

func (e *PluginDownloadExecutor) executePlan(ctx context.Context, runtime JobRuntime, task *models.DownloadTask, taskRoot, pluginID string, plan contract.DownloadPlan) (downloadpkg.Manifest, downloadpkg.Manifest, error) {
	paths := make(map[string]string, len(plan.Assets))
	totalExpected := int64(0)
	for _, asset := range plan.Assets {
		totalExpected += asset.ExpectedBytes
	}
	completed := int64(0)
	for index, asset := range plan.Assets {
		if err := ctx.Err(); err != nil {
			return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
		}
		stage := "downloading_" + asset.Kind
		_ = e.downloads.db.Model(task).Updates(map[string]any{"phase": models.DownloadTaskStatusDownloading, "provider_status": stage, "updated_at": time.Now().UTC()}).Error
		path, bytesWritten, err := e.downloadAsset(ctx, runtime, task, taskRoot, pluginID, asset, completed, totalExpected, index, len(plan.Assets))
		if err != nil {
			return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
		}
		paths[asset.ID], completed = path, completed+bytesWritten
	}
	suggested, _ := safeSuggestedFilename(plan.SuggestedFileName, plan.VariantID)
	output := filepath.Join(taskRoot, suggested)
	if plan.Merge != nil {
		if e.tool == nil {
			return downloadpkg.Manifest{}, downloadpkg.Manifest{}, &mediatool.Error{Code: mediatool.CodeUnavailable, Cause: errors.New("media tool is not configured")}
		}
		_ = e.downloads.db.Model(task).Updates(map[string]any{"phase": models.DownloadTaskStatusMerging, "provider_status": "merging", "updated_at": time.Now().UTC()}).Error
		partial := output + ".partial" + filepath.Ext(output)
		_ = os.Remove(partial)
		if err := e.tool.MergeDASH(ctx, paths[plan.Merge.VideoAssetID], paths[plan.Merge.AudioAssetID], partial); err != nil {
			return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
		}
		if err := verifyRegularNonEmpty(partial, pluginDownloadMaxMediaBytes); err != nil {
			return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
		}
		if err := os.Rename(partial, output); err != nil {
			return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
		}
		for _, assetID := range []string{plan.Merge.VideoAssetID, plan.Merge.AudioAssetID} {
			if err := os.Remove(paths[assetID]); err != nil {
				return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
			}
		}
	} else {
		var videoPath string
		for _, asset := range plan.Assets {
			if asset.Kind == "video" {
				videoPath = paths[asset.ID]
				break
			}
		}
		if err := os.Rename(videoPath, output); err != nil {
			return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
		}
	}
	_ = e.downloads.db.Model(task).Updates(map[string]any{"phase": models.DownloadTaskStatusVerifying, "provider_status": "verifying", "updated_at": time.Now().UTC()}).Error
	if err := verifyRegularNonEmpty(output, pluginDownloadMaxMediaBytes); err != nil {
		return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
	}
	files := []downloadpkg.File{{RelativePath: filepath.ToSlash(filepath.Join(pluginDownloadRootName, task.ID, filepath.Base(output))), Size: fileSize(output)}}
	stem := strings.TrimSuffix(filepath.Base(output), filepath.Ext(output))
	sidecarIndex := 0
	for _, asset := range plan.Assets {
		if asset.Kind != "subtitle" && asset.Kind != "danmaku" {
			continue
		}
		sidecarIndex++
		ext := sidecarExtension(asset)
		name := fmt.Sprintf("%s.%s-%d%s", stem, asset.Kind, sidecarIndex, ext)
		destination := filepath.Join(taskRoot, name)
		if err := os.Rename(paths[asset.ID], destination); err != nil {
			return downloadpkg.Manifest{}, downloadpkg.Manifest{}, err
		}
		files = append(files, downloadpkg.File{RelativePath: filepath.ToSlash(filepath.Join(pluginDownloadRootName, task.ID, name)), Size: fileSize(destination)})
	}
	manifest := downloadpkg.Manifest{Name: suggested, Files: files, Complete: true}
	return manifest, manifest, nil
}

func (e *PluginDownloadExecutor) downloadAsset(ctx context.Context, runtime JobRuntime, task *models.DownloadTask, root, pluginID string, asset contract.DownloadAsset, already, totalExpected int64, index, count int) (string, int64, error) {
	stream, err := e.assets.OpenAssetForPlugin(ctx, pluginID, asset.URLRef, http.MethodGet, "")
	if err != nil {
		return "", 0, err
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		return "", 0, errors.New("plugin asset did not return a complete response")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(stream.Header.Get("Content-Type"), ";")[0]))
	if !assetContentTypeAllowed(asset.Kind, asset.ExpectedContentType, contentType) {
		return "", 0, errors.New("plugin asset content type is invalid")
	}
	digest := sha256.Sum256([]byte(asset.ID))
	path := filepath.Join(root, hex.EncodeToString(digest[:8])+assetExtension(asset, contentType)+".part")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, err
	}
	limit := pluginDownloadMaxMediaBytes
	if asset.Kind == "subtitle" || asset.Kind == "danmaku" {
		limit = pluginDownloadMaxSidecarBytes
	}
	assetStarted := time.Now()
	reader := &progressReader{reader: io.LimitReader(stream.Body, limit+1), notify: func(written int64) error {
		var progress float64
		if totalExpected > 0 {
			progress = float64(already+written) * 100 / float64(totalExpected)
			if progress > 99 {
				progress = 99
			}
		} else {
			progress = (float64(index) + 0.5) * 90 / float64(count)
		}
		processed := already + written
		var total *int64
		var speed *float64
		var eta *int64
		if elapsed := time.Since(assetStarted).Seconds(); elapsed > 0 {
			value := float64(written) / elapsed
			speed = &value
			if totalExpected > processed && value > 0 {
				remaining := int64(float64(totalExpected-processed) / value)
				eta = &remaining
			}
		}
		if totalExpected > 0 {
			total = &totalExpected
		}
		if err := runtime.Heartbeat(&progress, &processed, total, speed, eta); err != nil {
			return err
		}
		updates := map[string]any{"progress": progress, "bytes_completed": processed, "updated_at": time.Now().UTC()}
		if total != nil {
			updates["bytes_total"] = *total
		}
		if speed != nil {
			updates["download_speed"] = int64(*speed)
		}
		if eta != nil {
			updates["eta_seconds"] = *eta
		}
		return e.downloads.db.Model(task).Updates(updates).Error
	}}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written <= 0 || written > limit || (asset.ExpectedBytes > 0 && written != asset.ExpectedBytes) {
		_ = os.Remove(path)
		if copyErr != nil {
			return "", 0, copyErr
		}
		return "", 0, errors.New("plugin asset size validation failed")
	}
	final := strings.TrimSuffix(path, ".part")
	if err := os.Rename(path, final); err != nil {
		_ = os.Remove(path)
		return "", 0, err
	}
	return final, written, nil
}

type progressReader struct {
	reader  io.Reader
	written int64
	last    time.Time
	notify  func(int64) error
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.written += int64(n)
	if n > 0 && (r.last.IsZero() || time.Since(r.last) >= time.Second) {
		r.last = time.Now()
		if notifyErr := r.notify(r.written); notifyErr != nil {
			return n, notifyErr
		}
	}
	return n, err
}

func (e *PluginDownloadExecutor) prepareTaskRoot(ctx context.Context, task models.DownloadTask) (string, error) {
	if _, err := e.downloads.settings.ResolveSnapshot(ctx, models.DownloaderTypePluginHTTP, task.StagingAbsolutePath, task.StagingStorageID, task.StagingRelativePath); err != nil {
		return "", err
	}
	root, err := e.taskRoot(task)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := (directory.NativeAdapter{}).Validate(ctx, root); err != nil {
		return "", err
	}
	return root, nil
}

func (e *PluginDownloadExecutor) taskRoot(task models.DownloadTask) (string, error) {
	if _, err := uuid.Parse(task.ID); err != nil || !filepath.IsAbs(task.StagingAbsolutePath) {
		return "", errors.New("managed task identity is invalid")
	}
	root := filepath.Join(filepath.Clean(task.StagingAbsolutePath), pluginDownloadRootName, task.ID)
	relative, err := filepath.Rel(filepath.Clean(task.StagingAbsolutePath), root)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("managed task root escaped staging")
	}
	return root, nil
}

func (e *PluginDownloadExecutor) cleanAttemptFiles(root string) error {
	if err := (directory.NativeAdapter{}).Validate(context.Background(), root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("managed task root contains an unsafe entry")
		}
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func removeManagedTaskRoot(staging, taskRoot string) error {
	if !filepath.IsAbs(staging) || !filepath.IsAbs(taskRoot) {
		return errors.New("managed cleanup path is invalid")
	}
	expectedParent := filepath.Join(filepath.Clean(staging), pluginDownloadRootName)
	if !pluginDownloadPathsEqual(filepath.Dir(taskRoot), expectedParent) {
		return errors.New("managed cleanup path escaped staging")
	}
	if _, err := uuid.Parse(filepath.Base(taskRoot)); err != nil {
		return errors.New("managed cleanup identity is invalid")
	}
	if _, err := os.Lstat(taskRoot); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := (directory.NativeAdapter{}).Validate(context.Background(), taskRoot); err != nil {
		return errors.New("managed cleanup root is unsafe")
	}
	entries, err := os.ReadDir(taskRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("managed cleanup encountered an unsafe entry")
		}
		if err := os.Remove(filepath.Join(taskRoot, entry.Name())); err != nil {
			return err
		}
	}
	return os.Remove(taskRoot)
}

func cleanupPluginDownloadOutput(download models.DownloadTask) (int, error) {
	if download.ProviderType != models.DownloaderTypePluginHTTP {
		return 0, errors.New("download is not a managed plugin task")
	}
	executor := &PluginDownloadExecutor{}
	root, err := executor.taskRoot(download)
	if err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := len(entries)
	if err := removeManagedTaskRoot(download.StagingAbsolutePath, root); err != nil {
		return 0, err
	}
	parent := filepath.Dir(root)
	_ = os.Remove(parent) // succeeds only when the managed parent is empty
	return removed, nil
}

func pluginDownloadPathsEqual(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if filepath.Separator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func safeSuggestedFilename(value, variantID string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 240 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n/\\:*?\"<>|") || filepath.Base(value) != value {
		return "", errors.New("suggested filename is unsafe")
	}
	ext := strings.ToLower(filepath.Ext(value))
	switch ext {
	case ".mp4", ".mkv", ".webm", ".mov", ".ts", ".m2ts":
	default:
		value, ext = value+".mkv", ".mkv"
	}
	variant := safeVariantFilenamePart(variantID)
	if variant != "" && !strings.Contains(strings.ToLower(value), strings.ToLower(variant)) {
		stem := strings.TrimSuffix(value, ext)
		value = stem + " [" + variant + "]" + ext
	}
	if len(value) > 240 {
		return "", errors.New("suggested filename with variant is too long")
	}
	return value, nil
}

func safeVariantFilenamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		}
		if builder.Len() >= 32 {
			break
		}
	}
	if builder.Len() >= 2 {
		return builder.String()
	}
	digest := sha256.Sum256([]byte(value))
	return "variant-" + hex.EncodeToString(digest[:4])
}

func assetContentTypeAllowed(kind, expected, actual string) bool {
	expected = strings.ToLower(strings.TrimSpace(strings.Split(expected, ";")[0]))
	if actual == "application/octet-stream" {
		return true
	}
	if expected != "" && actual != expected {
		return false
	}
	switch kind {
	case "video":
		return strings.HasPrefix(actual, "video/")
	case "audio":
		return strings.HasPrefix(actual, "audio/")
	case "subtitle", "danmaku":
		return strings.HasPrefix(actual, "text/") || actual == "application/json" || actual == "application/xml" || actual == "application/x-subrip"
	default:
		return false
	}
}

func assetExtension(asset contract.DownloadAsset, contentType string) string {
	switch asset.Kind {
	case "video":
		if contentType == "video/webm" {
			return ".webm"
		}
		return ".video"
	case "audio":
		if contentType == "audio/webm" {
			return ".webm"
		}
		return ".audio"
	case "subtitle":
		return sidecarExtension(asset)
	case "danmaku":
		if strings.Contains(strings.ToLower(asset.ExpectedContentType), "json") {
			return ".json"
		}
		return ".xml"
	default:
		return ".bin"
	}
}

func sidecarExtension(asset contract.DownloadAsset) string {
	contentType := strings.ToLower(asset.ExpectedContentType)
	if strings.Contains(contentType, "vtt") {
		return ".vtt"
	}
	if strings.Contains(contentType, "ass") {
		return ".ass"
	}
	if strings.Contains(contentType, "ssa") {
		return ".ssa"
	}
	return ".srt"
}

func verifyRegularNonEmpty(path string, max int64) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > max {
		return errors.New("managed media output is invalid")
	}
	return nil
}

func fileSize(path string) int64 {
	info, _ := os.Stat(path)
	if info == nil {
		return 0
	}
	return info.Size()
}

func pluginDownloadErrorCode(err error) string {
	if code := hostapi.ErrorCode(err); code != "plugin_host_internal" {
		return code
	}
	var mediaError *mediatool.Error
	if code := mediatool.ErrorCode(err); code != mediatool.CodeFailed || errors.As(err, &mediaError) {
		return code
	}
	if errors.Is(err, context.Canceled) {
		return "plugin_download_cancelled"
	}
	return "plugin_download_failed"
}

func pluginDownloadErrorMessage(code string) string {
	switch code {
	case mediatool.CodeUnavailable:
		return "未找到兼容的 FFmpeg，请先运行 Server 隔离工具安装脚本"
	case mediatool.CodeFailed:
		return "FFmpeg 合流失败，请检查媒体轨道兼容性后重试"
	case "plugin_asset_expired":
		return "下载资源已过期，任务将重新解析"
	case "plugin_asset_upstream_unavailable":
		return "在线媒体资源暂时不可用，任务将稍后重试"
	default:
		return "插件下载执行失败"
	}
}

func (e *PluginDownloadExecutor) fail(task models.DownloadTask, code, message string, retryable bool) WorkerResult {
	now := time.Now().UTC()
	updates := map[string]any{"last_error_code": safeLabel(code, 96), "last_error_message": safeLabel(message, 512), "updated_at": now}
	result := pluginDownloadFailure(code, message, retryable)
	if retryable {
		updates["phase"], updates["provider_status"] = models.DownloadTaskStatusQueued, "retry_wait"
	} else {
		updates["phase"], updates["provider_status"], updates["finished_at"] = models.DownloadTaskStatusFailed, "failed", now
	}
	_ = e.downloads.db.Model(&task).Updates(updates).Error
	serverlog.OperationPluginDownload.Event(e.downloads.log.Warn()).Str("task_id", task.ID).Str("error_code", code).Bool("retryable", retryable).Msg(serverlog.OperationPluginDownload.Message("执行失败"))
	return result
}

func pluginDownloadFailure(code, message string, retryable bool) WorkerResult {
	result := WorkerResult{ErrorCode: safeLabel(code, 96), ErrorMessage: safeLabel(message, 512)}
	if retryable {
		next := time.Now().UTC().Add(30 * time.Second)
		result.RetryAt = &next
	}
	return result
}
