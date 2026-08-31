package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/builtin"
)

type DownloadRoutePreviewInput struct {
	DownloaderID  string `json:"downloader_id"`
	SourceKind    string `json:"source_kind"`
	SiteID        *uint  `json:"site_id,omitempty"`
	ExpectedBytes *int64 `json:"expected_bytes,omitempty"`
}

type DownloadRouteTargetOption struct {
	MediaLibraryID         uint   `json:"media_library_id"`
	LibraryName            string `json:"library_name"`
	StorageName            string `json:"storage_name"`
	RouteKind              string `json:"route_kind"`
	RouteLabel             string `json:"route_label"`
	Enabled                bool   `json:"enabled"`
	ReasonCode             string `json:"reason_code"`
	ReasonMessage          string `json:"reason_message"`
	RequiresManagedStaging bool   `json:"requires_managed_staging"`
	ExpectedBytes          *int64 `json:"expected_bytes,omitempty"`
	RequiredBytes          *int64 `json:"required_bytes,omitempty"`
	AvailableBytes         *int64 `json:"available_bytes,omitempty"`
}

type DownloadRoutePreview struct {
	DownloaderID string                      `json:"downloader_id"`
	SourceKind   string                      `json:"source_kind"`
	Options      []DownloadRouteTargetOption `json:"options"`
}

// PreviewRoutes returns the same authoritative route matrix used by Submit.
// Disabled targets remain in the response with a stable reason so clients do
// not need to duplicate provider/storage compatibility rules.
func (s *DownloadService) PreviewRoutes(ctx context.Context, actor Actor, input DownloadRoutePreviewInput) (DownloadRoutePreview, error) {
	if !actor.Can(authz.PermissionDownloadsCreate) {
		return DownloadRoutePreview{}, appError(CodePermissionDenied, "无权创建下载任务", nil)
	}
	input.DownloaderID = strings.TrimSpace(input.DownloaderID)
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	if input.DownloaderID == "" || !isPreviewSourceKind(input.SourceKind) || input.ExpectedBytes != nil && *input.ExpectedBytes < 0 {
		return DownloadRoutePreview{}, appError(CodeInvalidRequest, "下载路由预览参数无效", nil)
	}
	var downloader models.Downloader
	if err := s.db.WithContext(ctx).First(&downloader, "id = ?", input.DownloaderID).Error; err != nil {
		return DownloadRoutePreview{}, downloaderNotFound(err)
	}
	if !downloader.Enabled {
		return DownloadRoutePreview{}, appError(CodeDownloaderUnavailable, "下载器已停用", nil)
	}
	if err := s.validatePreviewSource(downloader, input.SourceKind, input.SiteID); err != nil {
		return DownloadRoutePreview{}, err
	}
	type libraryRow struct {
		models.MediaLibrary
		StorageName string
	}
	var rows []libraryRow
	if err := s.db.WithContext(ctx).Table("media_libraries").
		Select("media_libraries.*, storages.name AS storage_name").
		Joins("JOIN storages ON storages.id = media_libraries.storage_id").
		Where("media_libraries.enabled = ?", true).
		Order("media_libraries.sort_order, media_libraries.id").Scan(&rows).Error; err != nil {
		return DownloadRoutePreview{}, err
	}
	preview := DownloadRoutePreview{DownloaderID: downloader.ID, SourceKind: input.SourceKind, Options: make([]DownloadRouteTargetOption, 0, len(rows))}
	for _, row := range rows {
		option := DownloadRouteTargetOption{MediaLibraryID: row.ID, LibraryName: row.Name, StorageName: row.StorageName, ExpectedBytes: cloneOptionalInt64(input.ExpectedBytes)}
		target, _, err := s.previewDownloadTarget(ctx, downloader, row.MediaLibrary, input.SourceKind)
		if err != nil {
			option.ReasonCode = ErrorCode(err)
			option.ReasonMessage = safeErrorMessage(err, "该媒体库当前不可作为目标")
		} else {
			option.RouteKind = target.RouteKind
			option.RouteLabel = transferRouteLabel(target.RouteKind)
			option.RequiresManagedStaging = target.RouteKind == models.TransferRouteCrossSource
			if option.RequiresManagedStaging {
				if spaceErr := s.applyManagedStagingPreview(ctx, downloader.Type, input.ExpectedBytes, &option); spaceErr != nil {
					option.ReasonCode = ErrorCode(spaceErr)
					option.ReasonMessage = safeErrorMessage(spaceErr, "Server 暂存空间不可用")
					preview.Options = append(preview.Options, option)
					continue
				}
			}
			option.Enabled = true
		}
		preview.Options = append(preview.Options, option)
	}
	return preview, nil
}

func (s *DownloadService) applyManagedStagingPreview(ctx context.Context, providerType string, expected *int64, option *DownloadRouteTargetOption) error {
	if option == nil {
		return appError(CodeInvalidRequest, "下载路由预览参数无效", nil)
	}
	snapshot, err := s.settings.SnapshotForRoute(ctx, providerType, models.TransferRouteCrossSource)
	if err != nil {
		return err
	}
	probe := (storagefs.LocalDriver{}).ProbeRoot(snapshot.AbsolutePath)
	if !probe.Available || probe.FreeBytes == nil {
		return appError("cross_source_space_unknown", "无法确认 Server 暂存空间", nil)
	}
	available := *probe.FreeBytes
	availableInt := int64(math.MaxInt64)
	if available <= math.MaxInt64 {
		availableInt = int64(available)
	}
	option.AvailableBytes = &availableInt
	if expected == nil {
		return nil
	}
	total := uint64(*expected)
	margin := total / 20
	if margin < crossSourceMinimumFreeBytes {
		margin = crossSourceMinimumFreeBytes
	}
	if total > math.MaxUint64-margin {
		return appError("cross_source_space_insufficient", "Server 暂存空间不足", nil)
	}
	required := total + margin
	requiredInt := int64(math.MaxInt64)
	if required <= math.MaxInt64 {
		requiredInt = int64(required)
	}
	option.RequiredBytes = &requiredInt
	if available < required {
		return appError("cross_source_space_insufficient", "Server 暂存空间不足", nil)
	}
	return nil
}

func cloneOptionalInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func safeErrorMessage(err error, fallback string) string {
	var application *AppError
	if errors.As(err, &application) && strings.TrimSpace(application.Message) != "" {
		return application.Message
	}
	return fallback
}

func isPreviewSourceKind(kind string) bool {
	switch kind {
	case downloadpkg.SourceURL, downloadpkg.SourceTorrent, downloadpkg.SourcePan115Share:
		return true
	default:
		return false
	}
}

func (s *DownloadService) validatePreviewSource(downloader models.Downloader, sourceKind string, siteID *uint) error {
	if sourceKind == downloadpkg.SourcePan115Share && downloader.Type != models.DownloaderTypePan115Offline {
		return appError(CodeDownloadSourceInvalid, "所选下载器不支持 115 分享转存", nil)
	}
	authoritativeBT := false
	if siteID != nil && *siteID != 0 {
		var site models.Site
		if err := s.db.Select("kind", "enabled").First(&site, *siteID).Error; err != nil || !site.Enabled {
			return appError(CodeSiteUnavailable, "站点不存在或已停用", err)
		}
		definition, ok := builtin.DefinitionForKey(site.Kind)
		authoritativeBT = ok && definition.SiteType == builtin.SiteTypeBT
		if downloader.Type == models.DownloaderTypePan115Offline && !authoritativeBT {
			return appError(CodeDownloadSourceInvalid, "PT 资源不能提交到 115 离线下载", nil)
		}
	}
	if sourceKind == downloadpkg.SourceTorrent && downloader.Type == models.DownloaderTypePan115Offline && !authoritativeBT {
		return appError(CodeDownloadSourceInvalid, "种子文件只能提交到非网盘 BT 下载器；公开 BT 站点会先安全转换为磁力链接", nil)
	}
	return nil
}

func (s *DownloadService) previewDownloadTarget(ctx context.Context, downloader models.Downloader, library models.MediaLibrary, sourceKind string) (*downloadTargetSnapshot, models.MediaClassificationProfile, error) {
	return s.buildDownloadTargetSnapshot(ctx, downloader, library, sourceKind, false)
}

func (s *DownloadService) buildDownloadTargetSnapshot(ctx context.Context, downloader models.Downloader, library models.MediaLibrary, sourceKind string, validateProviderRoots bool) (*downloadTargetSnapshot, models.MediaClassificationProfile, error) {
	var targetStorage models.Storage
	if err := s.db.WithContext(ctx).First(&targetStorage, library.StorageID).Error; err != nil || !targetStorage.Enabled {
		return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "目标媒体库 Storage 不可用", err)
	}
	sourceIdentity, sourceStorage, err := s.downloaderDataSourceIdentity(ctx, downloader)
	if err != nil {
		return nil, models.MediaClassificationProfile{}, err
	}
	targetIdentity, err := mediaLibraryDataSourceIdentity(targetStorage)
	if err != nil {
		return nil, models.MediaClassificationProfile{}, err
	}
	routeKind := selectTransferRoute(sourceIdentity, targetIdentity)
	ingestProviderRootID := strings.TrimSpace(library.IngestProviderRootID)
	var targetConnectionID *uint
	targetProviderRootID := ""

	switch targetStorage.Type {
	case models.StorageTypeLocal:
		if _, err := medialibrary.ResolveRoot(targetStorage.RootPath, library.RelativeRoot); err != nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryPathInvalid, "目标媒体库目录不可用", err)
		}
		if routeKind == models.TransferRouteCrossSource && library.TransferMode == models.MediaLibraryTransferSymlink {
			return nil, models.MediaClassificationProfile{}, appError(CodeTransferRouteUnsupported, "跨数据源入库不能使用软链接", nil)
		}
	case models.StorageTypePan115:
		if targetStorage.ConnectionID == nil || strings.TrimSpace(library.ProviderRootID) == "" || s.downloader == nil || s.downloader.connections == nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 目标媒体库连接不可用", nil)
		}
		if library.TransferMode != models.MediaLibraryTransferMove && library.TransferMode != models.MediaLibraryTransferCopy {
			return nil, models.MediaClassificationProfile{}, appError(CodeTransferRouteUnsupported, "115 媒体库仅支持移动或复制入库", nil)
		}
		_, driver, driverErr := s.downloader.connections.driver(*targetStorage.ConnectionID)
		if driverErr != nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "115 目标连接不可用", driverErr)
		}
		capabilities := driver.Capabilities()
		if routeKind == models.TransferRouteSameSourceProvider {
			_, ok := driver.(cloudpkg.MutationDriver)
			if !ok || !capabilities.CreateDirectory || !capabilities.Rename || !capabilities.Recycle || library.TransferMode == models.MediaLibraryTransferMove && !capabilities.Move || library.TransferMode == models.MediaLibraryTransferCopy && !capabilities.Copy {
				return nil, models.MediaClassificationProfile{}, appError(CodeTransferRouteUnsupported, "115 目标缺少所需的同源云端整理能力", nil)
			}
		} else {
			_, ok := driver.(cloudpkg.UploadDriver)
			if !ok || !capabilities.FileUpload || !capabilities.CreateDirectory || !capabilities.Recycle {
				return nil, models.MediaClassificationProfile{}, appError(CodeTransferRouteUnsupported, "115 目标缺少跨数据源文件上传能力", nil)
			}
		}
		if validateProviderRoots {
			root, rootErr := providerItemWithinRoot(cloudpkg.WithReadClass(ctx, cloudpkg.ReadClassPipeline), driver, library.ProviderRootID, targetStorage.RootPath)
			if rootErr != nil || !root.IsDir {
				return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryPathInvalid, "115 目标媒体库目录不可用", rootErr)
			}
		}
		value := *targetStorage.ConnectionID
		targetConnectionID, targetProviderRootID = &value, strings.TrimSpace(library.ProviderRootID)
	default:
		return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryStorageUnavailable, "目标媒体库 Storage 类型不受支持", nil)
	}

	if sourceIdentity.Kind == models.DataSourceKindProvider {
		if sourceStorage == nil || sourceStorage.ConnectionID == nil || s.downloader == nil || s.downloader.connections == nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeDownloaderStorageUnavailable, "下载器来源数据源不可用", nil)
		}
		_, sourceDriver, driverErr := s.downloader.connections.driver(*sourceStorage.ConnectionID)
		if driverErr != nil {
			return nil, models.MediaClassificationProfile{}, appError(CodeDownloaderStorageUnavailable, "下载器来源连接不可用", driverErr)
		}
		_, readable := sourceDriver.(cloudpkg.ReadDriver)
		if routeKind == models.TransferRouteCrossSource && (!readable || !sourceDriver.Capabilities().TemporaryDirectURL) {
			return nil, models.MediaClassificationProfile{}, appError(CodeTransferRouteUnsupported, "来源网盘不支持安全下载到 Server 暂存区", nil)
		}
		if sourceKind == downloadpkg.SourcePan115Share || sourceKind == downloadpkg.SourceProviderItem {
			ingestProviderRootID = strings.TrimSpace(downloader.ProviderDirectoryID)
			if ingestProviderRootID == "" {
				return nil, models.MediaClassificationProfile{}, appError(CodeDownloaderStorageUnavailable, "115 下载器目录不可用", nil)
			}
			if validateProviderRoots {
				ingestRoot, rootErr := providerItemWithinRoot(cloudpkg.WithReadClass(ctx, cloudpkg.ReadClassPipeline), sourceDriver, ingestProviderRootID, sourceStorage.RootPath)
				if rootErr != nil || !ingestRoot.IsDir {
					return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryPathInvalid, "115 下载目录不可用", rootErr)
				}
			}
		}
	}

	var profile models.MediaClassificationProfile
	if err := s.db.WithContext(ctx).First(&profile, library.ProfileID).Error; err != nil {
		return nil, models.MediaClassificationProfile{}, appError(CodeMediaLibraryProfileUnavailable, "目标媒体库分类规则不可用", err)
	}
	organization, err := storedProfileOrganizationConfig(profile)
	if err != nil {
		return nil, models.MediaClassificationProfile{}, appError(CodeProfileValidation, "目标媒体库识别与命名配置无效", err)
	}
	sourceJSON, err := marshalDataSourceIdentity(sourceIdentity)
	if err != nil {
		return nil, models.MediaClassificationProfile{}, err
	}
	targetJSON, err := marshalDataSourceIdentity(targetIdentity)
	if err != nil {
		return nil, models.MediaClassificationProfile{}, err
	}
	return &downloadTargetSnapshot{
		LibraryID: library.ID, LibraryName: library.Name, StorageID: targetStorage.ID, StorageType: targetStorage.Type,
		ConnectionID: targetConnectionID, ProviderRootID: targetProviderRootID, StorageRoot: targetStorage.RootPath,
		RelativeRoot: library.RelativeRoot, TransferMode: library.TransferMode, ConflictPolicy: library.ConflictPolicy,
		MovieDirectoryTemplate: organization.MovieDirectoryTemplate, MovieFilenameTemplate: organization.MovieFilenameTemplate,
		TVDirectoryTemplate: organization.TVDirectoryTemplate, TVFilenameTemplate: organization.TVFilenameTemplate,
		IngestProviderRootID: ingestProviderRootID, SourceDataSourceJSON: sourceJSON, TargetDataSourceJSON: targetJSON,
		RouteKind: routeKind, RouteVersion: models.TransferRouteVersionCurrent,
	}, profile, nil
}

func (s *DownloadService) downloaderDataSourceIdentity(ctx context.Context, downloader models.Downloader) (models.DataSourceIdentity, *models.Storage, error) {
	if downloader.Type == models.DownloaderTypePan115Offline {
		if downloader.StorageID == nil {
			return models.DataSourceIdentity{}, nil, appError(CodeDownloaderStorageUnavailable, "115 下载器 Storage 不可用", nil)
		}
		var storage models.Storage
		if err := s.db.WithContext(ctx).First(&storage, *downloader.StorageID).Error; err != nil || !storage.Enabled || storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil {
			return models.DataSourceIdentity{}, nil, appError(CodeDownloaderStorageUnavailable, "115 下载器 Storage 不可用", err)
		}
		return models.DataSourceIdentity{Kind: models.DataSourceKindProvider, ProviderType: models.StorageTypePan115, ConnectionIdentity: strconv.FormatUint(uint64(*storage.ConnectionID), 10), StorageScope: strconv.FormatUint(uint64(storage.ID), 10)}, &storage, nil
	}
	if downloader.Type == models.DownloaderTypePluginHTTP {
		return localDataSourceIdentity(), nil, nil
	}
	capabilities, ok := s.downloader.registry.Capabilities(downloader.Type)
	if !ok || capabilities.OutputConstraint != "" && capabilities.OutputConstraint != downloadpkg.OutputConstraintLocalStaging && capabilities.OutputConstraint != downloadpkg.OutputConstraintNone {
		return models.DataSourceIdentity{}, nil, appError(CodeTransferRouteUnsupported, "下载器没有可路由的数据源身份", nil)
	}
	return localDataSourceIdentity(), nil, nil
}

func localDataSourceIdentity() models.DataSourceIdentity {
	return models.DataSourceIdentity{Kind: models.DataSourceKindLocal, ProviderType: models.StorageTypeLocal, ConnectionIdentity: models.DataSourceLocalConnectionIdentity}
}

func mediaLibraryDataSourceIdentity(storage models.Storage) (models.DataSourceIdentity, error) {
	switch storage.Type {
	case models.StorageTypeLocal:
		return localDataSourceIdentity(), nil
	case models.StorageTypePan115:
		if storage.ConnectionID == nil {
			return models.DataSourceIdentity{}, appError(CodeMediaLibraryStorageUnavailable, "115 媒体库连接不可用", nil)
		}
		return models.DataSourceIdentity{Kind: models.DataSourceKindProvider, ProviderType: models.StorageTypePan115, ConnectionIdentity: strconv.FormatUint(uint64(*storage.ConnectionID), 10), StorageScope: strconv.FormatUint(uint64(storage.ID), 10)}, nil
	default:
		return models.DataSourceIdentity{}, appError(CodeMediaLibraryStorageUnavailable, "目标媒体库 Storage 类型不受支持", nil)
	}
}

func selectTransferRoute(source, target models.DataSourceIdentity) string {
	if source.Kind == models.DataSourceKindLocal && target.Kind == models.DataSourceKindLocal {
		return models.TransferRouteSameSourceLocal
	}
	if source.Kind == models.DataSourceKindProvider && target.Kind == models.DataSourceKindProvider && source.ProviderType == target.ProviderType && source.ConnectionIdentity == target.ConnectionIdentity {
		return models.TransferRouteSameSourceProvider
	}
	return models.TransferRouteCrossSource
}

func marshalDataSourceIdentity(identity models.DataSourceIdentity) (string, error) {
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("marshal data source identity: %w", err)
	}
	return string(raw), nil
}

func transferRouteLabel(kind string) string {
	switch kind {
	case models.TransferRouteSameSourceLocal:
		return "本地整理"
	case models.TransferRouteSameSourceProvider:
		return "同源云端整理"
	case models.TransferRouteCrossSource:
		return "跨数据源（需要 Server 暂存）"
	default:
		return ""
	}
}
