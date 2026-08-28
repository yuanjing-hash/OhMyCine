package services

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

type transferRouteKind string

const (
	transferRouteLocal              transferRouteKind = "same_source_local"
	transferRoutePan115Native       transferRouteKind = "same_source_provider"
	transferRouteLocalToPan115      transferRouteKind = "local_to_pan115"
	transferRoutePan115ToLocal      transferRouteKind = "pan115_to_local"
	transferRoutePan115ToOtherCloud transferRouteKind = "pan115_to_other_cloud"
)

func (w *TransferWorker) resolveTransferRoute(task models.TransferTask, download models.DownloadTask) (transferRouteKind, error) {
	if task.RouteVersion == 0 {
		return w.resolveLegacyTransferRoute(download)
	}
	if task.RouteVersion != models.TransferRouteVersionCurrent || download.TransferRouteVersion != task.RouteVersion ||
		task.RouteKind != download.TransferRouteKind || task.SourceDataSourceJSON != download.SourceDataSourceJSON || task.TargetDataSourceJSON != download.TargetDataSourceJSON {
		return "", errors.New("transfer route snapshot version changed")
	}
	source, err := decodeDataSourceIdentity(task.SourceDataSourceJSON)
	if err != nil {
		return "", err
	}
	target, err := decodeDataSourceIdentity(task.TargetDataSourceJSON)
	if err != nil {
		return "", err
	}
	if selectTransferRoute(source, target) != task.RouteKind {
		return "", errors.New("transfer route snapshot is inconsistent")
	}
	if err := w.revalidateSourceIdentity(download, source); err != nil {
		return "", err
	}
	if err := w.revalidateTargetIdentity(download, target); err != nil {
		return "", err
	}
	switch task.RouteKind {
	case models.TransferRouteSameSourceLocal:
		return transferRouteLocal, nil
	case models.TransferRouteSameSourceProvider:
		if source.ProviderType != models.StorageTypePan115 || target.ProviderType != models.StorageTypePan115 {
			return "", errors.New("same-source provider executor is unavailable")
		}
		return transferRoutePan115Native, nil
	case models.TransferRouteCrossSource:
		switch {
		case source.Kind == models.DataSourceKindLocal && target.ProviderType == models.StorageTypePan115:
			return transferRouteLocalToPan115, nil
		case source.ProviderType == models.StorageTypePan115 && target.Kind == models.DataSourceKindLocal:
			return transferRoutePan115ToLocal, nil
		case source.ProviderType == models.StorageTypePan115 && target.ProviderType == models.StorageTypePan115:
			return transferRoutePan115ToOtherCloud, nil
		default:
			return "", errors.New("cross-source executor is unavailable")
		}
	default:
		return "", errors.New("transfer route kind is invalid")
	}
}

func (w *TransferWorker) resolveLegacyTransferRoute(download models.DownloadTask) (transferRouteKind, error) {
	sourcePan115 := download.ProviderType == models.DownloaderTypePan115Offline
	targetPan115 := download.TargetStorageType == models.StorageTypePan115
	if !sourcePan115 {
		if targetPan115 {
			return transferRouteLocalToPan115, nil
		}
		// Pre-route-snapshot local tasks did not persist TargetStorageType.
		// Anything other than the only historical cloud type remains local.
		return transferRouteLocal, nil
	}
	if download.StagingStorageID == nil {
		return "", errors.New("115 source storage snapshot is missing")
	}
	var source models.Storage
	if err := w.service.db.First(&source, *download.StagingStorageID).Error; err != nil {
		return "", err
	}
	if source.Type != models.StorageTypePan115 || source.ConnectionID == nil {
		return "", errors.New("115 source identity is invalid")
	}
	if targetPan115 {
		if download.TargetConnectionID == nil {
			return "", errors.New("115 target identity is missing")
		}
		if *source.ConnectionID == *download.TargetConnectionID {
			return transferRoutePan115Native, nil
		}
		return transferRoutePan115ToOtherCloud, nil
	}
	// Historical pan115 -> local rows were created by versions that had no
	// materialization checkpoint/ownership contract. Do not silently reroute an
	// already queued legacy task through a new destructive pipeline.
	return "", errors.New("legacy 115 cross-source route is unsupported")
}

func decodeDataSourceIdentity(raw string) (models.DataSourceIdentity, error) {
	var identity models.DataSourceIdentity
	if len(raw) == 0 || len(raw) > 4096 {
		return identity, errors.New("data-source identity is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil {
		return identity, errors.New("data-source identity is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return identity, errors.New("data-source identity has trailing data")
	}
	if identity.Kind == models.DataSourceKindLocal {
		if identity.ProviderType != models.StorageTypeLocal || identity.ConnectionIdentity != models.DataSourceLocalConnectionIdentity || identity.StorageScope != "" {
			return identity, errors.New("local data-source identity is invalid")
		}
		return identity, nil
	}
	if identity.Kind != models.DataSourceKindProvider || identity.ProviderType == "" || identity.ConnectionIdentity == "" || identity.StorageScope == "" {
		return identity, errors.New("provider data-source identity is invalid")
	}
	if _, err := strconv.ParseUint(identity.ConnectionIdentity, 10, 64); err != nil {
		return identity, errors.New("provider connection identity is invalid")
	}
	if _, err := strconv.ParseUint(identity.StorageScope, 10, 64); err != nil {
		return identity, errors.New("provider storage scope is invalid")
	}
	return identity, nil
}

func (w *TransferWorker) revalidateSourceIdentity(download models.DownloadTask, identity models.DataSourceIdentity) error {
	if identity.Kind == models.DataSourceKindLocal {
		if download.ProviderType == models.DownloaderTypePan115Offline {
			return errors.New("local source identity no longer matches downloader")
		}
		return nil
	}
	if identity.ProviderType != models.StorageTypePan115 || download.StagingStorageID == nil {
		return errors.New("provider source identity is unsupported")
	}
	storageID, _ := strconv.ParseUint(identity.StorageScope, 10, 64)
	connectionID, _ := strconv.ParseUint(identity.ConnectionIdentity, 10, 64)
	if uint(storageID) != *download.StagingStorageID {
		return errors.New("provider source storage snapshot changed")
	}
	var storage models.Storage
	if err := w.service.db.First(&storage, *download.StagingStorageID).Error; err != nil || storage.Type != identity.ProviderType || storage.ConnectionID == nil || *storage.ConnectionID != uint(connectionID) {
		return errors.New("provider source identity no longer matches storage")
	}
	return nil
}

func (w *TransferWorker) revalidateTargetIdentity(download models.DownloadTask, identity models.DataSourceIdentity) error {
	if download.TargetStorageID == nil {
		return errors.New("target storage snapshot is missing")
	}
	var storage models.Storage
	if err := w.service.db.First(&storage, *download.TargetStorageID).Error; err != nil {
		return err
	}
	if identity.Kind == models.DataSourceKindLocal {
		if storage.Type != models.StorageTypeLocal || download.TargetStorageType != models.StorageTypeLocal {
			return errors.New("local target identity no longer matches storage")
		}
		return nil
	}
	storageID, _ := strconv.ParseUint(identity.StorageScope, 10, 64)
	connectionID, _ := strconv.ParseUint(identity.ConnectionIdentity, 10, 64)
	if uint(storageID) != storage.ID || identity.ProviderType != storage.Type || download.TargetStorageType != storage.Type || storage.ConnectionID == nil || *storage.ConnectionID != uint(connectionID) || download.TargetConnectionID == nil || *download.TargetConnectionID != uint(connectionID) {
		return errors.New("provider target identity no longer matches storage")
	}
	return nil
}

func validateTransferRouteSnapshot(download models.DownloadTask) error {
	if download.TransferRouteVersion == 0 {
		if download.ProviderType == models.DownloaderTypePan115Offline && download.TargetStorageType != models.StorageTypePan115 {
			return errors.New("legacy 115 cross-source route is unsupported")
		}
		return nil
	}
	if download.TransferRouteVersion != models.TransferRouteVersionCurrent {
		return errors.New("transfer route version is unsupported")
	}
	source, err := decodeDataSourceIdentity(download.SourceDataSourceJSON)
	if err != nil {
		return err
	}
	target, err := decodeDataSourceIdentity(download.TargetDataSourceJSON)
	if err != nil {
		return err
	}
	if selectTransferRoute(source, target) != download.TransferRouteKind {
		return errors.New("transfer route snapshot is inconsistent")
	}
	return nil
}

func transferRouteUnsupportedMessage(download models.DownloadTask) string {
	if download.TransferRouteVersion == 0 && download.ProviderType == models.DownloaderTypePan115Offline && download.TargetStorageType != models.StorageTypePan115 {
		return "115 原生离线下载不能直接入库到本地媒体库；请选择同账号的 115 媒体库"
	}
	return "下载来源与目标媒体库之间没有可用的入库执行器"
}
