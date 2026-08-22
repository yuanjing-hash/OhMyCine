package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/packagefs"
	pluginrepository "github.com/yuanjing-hash/ohmycine/server/internal/plugins/repository"
	pluginruntime "github.com/yuanjing-hash/ohmycine/server/internal/plugins/runtime"
	"gorm.io/gorm"
)

const pluginPreviewTTL = 15 * time.Minute

type PluginPermissionDiff struct {
	Added     []contract.Permission `json:"added"`
	Removed   []contract.Permission `json:"removed"`
	Unchanged []contract.Permission `json:"unchanged"`
}

type PluginInstallPreviewSummary struct {
	ID                    string                `json:"id"`
	PluginID              string                `json:"plugin_id"`
	Name                  string                `json:"name"`
	Version               string                `json:"version"`
	Operation             string                `json:"operation"`
	RepositoryID          uint                  `json:"repository_id"`
	RepositoryName        string                `json:"repository_name"`
	Capabilities          []contract.Capability `json:"capabilities"`
	Permissions           []contract.Permission `json:"permissions"`
	PermissionDiff        PluginPermissionDiff  `json:"permission_diff"`
	PermissionFingerprint string                `json:"permission_fingerprint"`
	InstallationRevision  uint64                `json:"installation_revision"`
	ExpiresAt             time.Time             `json:"expires_at"`
}

type InstalledPluginSummary struct {
	ID                   string                `json:"id"`
	Name                 string                `json:"name"`
	Description          string                `json:"description"`
	Version              string                `json:"version"`
	PreviousVersion      string                `json:"previous_version,omitempty"`
	RepositoryID         *uint                 `json:"repository_id"`
	RepositoryName       string                `json:"repository_name"`
	Status               string                `json:"status"`
	Revision             uint64                `json:"revision"`
	RuntimeGeneration    uint64                `json:"runtime_generation"`
	LastRuntimeErrorCode string                `json:"last_runtime_error_code"`
	Capabilities         []contract.Capability `json:"capabilities"`
	Permissions          []contract.Permission `json:"permissions"`
	InstalledAt          time.Time             `json:"installed_at"`
	UpdatedAt            time.Time             `json:"updated_at"`
}

func (s *PluginRepositoryService) PrepareInstall(ctx context.Context, actor Actor, pluginID string, repositoryID uint, version string, request RequestContext) (PluginInstallPreviewSummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return PluginInstallPreviewSummary{}, appError(CodePermissionDenied, "无权安装插件", nil)
	}
	if s.assets == nil || s.runtime == nil {
		return PluginInstallPreviewSummary{}, appError(CodePluginRuntimeUnavailable, "插件安装运行时不可用", nil)
	}
	root, err := validatePluginRoot(s.pluginRoot)
	if err != nil {
		return PluginInstallPreviewSummary{}, appError(CodePluginRuntimeUnavailable, "插件隔离目录未正确配置", err)
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if err := s.prunePluginArtifacts(); err != nil {
		return PluginInstallPreviewSummary{}, appError(CodePluginCleanupFailed, "插件暂存清理失败", err)
	}

	repository, entry, err := s.resolveInstallCandidate(pluginID, repositoryID, version)
	if err != nil {
		return PluginInstallPreviewSummary{}, err
	}
	var installed models.PluginInstallation
	if err := s.db.First(&installed, "plugin_id = ?", pluginID).Error; err == nil {
		var activePackage models.PluginPackage
		if err := s.db.First(&activePackage, installed.ActivePackageID).Error; err != nil {
			return PluginInstallPreviewSummary{}, err
		}
		if !strings.EqualFold(activePackage.RepositoryOwner, repository.GitHubOwner) || !strings.EqualFold(activePackage.RepositoryRepo, repository.GitHubRepo) {
			return PluginInstallPreviewSummary{}, appError(CodePluginSourceChange, "插件已安装来源与所选仓库不同，不能静默跨仓库更新", nil)
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginInstallPreviewSummary{}, err
	}
	started := time.Now()
	serverlog.OperationPluginRuntime.Event(s.log.Info()).Str("plugin_id", entry.ID).Uint("repository_id", repository.ID).Msg(serverlog.OperationPluginRuntime.Message("开始校验安装包"))
	source := contract.GitHubRepository{Owner: repository.GitHubOwner, Name: repository.GitHubRepo}
	manifestBytes, err := s.assets.FetchManifest(ctx, source, entry.ManifestURL)
	if err != nil {
		return PluginInstallPreviewSummary{}, s.installAssetError(entry.ID, repository.ID, started, err)
	}
	manifest, err := contract.ParseManifest(manifestBytes)
	if err != nil {
		return PluginInstallPreviewSummary{}, s.installValidationError(entry.ID, repository.ID, started, CodePluginManifestInvalid, err)
	}
	if err := validateManifestAgainstRegistry(manifest, entry, source); err != nil {
		return PluginInstallPreviewSummary{}, s.installValidationError(entry.ID, repository.ID, started, CodePluginManifestMismatch, err)
	}
	if manifest.Signature != nil {
		return PluginInstallPreviewSummary{}, s.installValidationError(entry.ID, repository.ID, started, CodePluginSignatureUntrusted, errors.New("no trusted plugin signing key is configured"))
	}
	archive, err := s.assets.FetchPackage(ctx, source, entry.PackageURL)
	if err != nil {
		return PluginInstallPreviewSummary{}, s.installAssetError(entry.ID, repository.ID, started, err)
	}
	digest := sha256.Sum256(archive)
	digestHex := hex.EncodeToString(digest[:])
	if digestHex != entry.PackageSHA256 || digestHex != manifest.PackageSHA256 {
		return PluginInstallPreviewSummary{}, s.installValidationError(entry.ID, repository.ID, started, CodePluginPackageDigest, errors.New("plugin package digest mismatch"))
	}
	var knownPackageCount int64
	if err := s.db.Model(&models.PluginPackage{}).Where("package_sha256 = ?", digestHex).Count(&knownPackageCount).Error; err != nil {
		return PluginInstallPreviewSummary{}, err
	}
	packagePath, extractedTreeSHA256, err := packagefs.ExtractVerified(root, digestHex, manifest, archive)
	if err != nil {
		return PluginInstallPreviewSummary{}, s.installValidationError(entry.ID, repository.ID, started, CodePluginPackageInvalid, err)
	}
	entryPath := filepath.Join(packagePath, filepath.FromSlash(manifest.Entry))
	if err := s.runtime.Validate(ctx, entryPath); err != nil {
		if knownPackageCount == 0 {
			_ = packagefs.RemoveManagedPackage(root, packagePath)
		}
		return PluginInstallPreviewSummary{}, s.installValidationError(entry.ID, repository.ID, started, pluginruntime.ErrorCode(err), err)
	}
	canonicalManifest, err := json.Marshal(manifest)
	if err != nil {
		if knownPackageCount == 0 {
			_ = packagefs.RemoveManagedPackage(root, packagePath)
		}
		return PluginInstallPreviewSummary{}, err
	}
	pluginPackage, err := s.persistVerifiedPackage(repository, entry, manifest, digestHex, extractedTreeSHA256, packagePath, string(canonicalManifest))
	if err != nil {
		if knownPackageCount == 0 {
			_ = packagefs.RemoveManagedPackage(root, packagePath)
		}
		return PluginInstallPreviewSummary{}, err
	}
	current, currentManifest, err := s.currentInstallationManifest(entry.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return PluginInstallPreviewSummary{}, err
	}
	operation, revision := "install", uint64(0)
	if err == nil {
		if current.ActivePackageID == pluginPackage.ID {
			return PluginInstallPreviewSummary{}, appError(CodePluginAlreadyInstalled, "该插件版本已经安装", nil)
		}
		comparison, compareErr := contract.CompareVersions(manifest.Version, currentManifest.Version)
		if compareErr != nil {
			return PluginInstallPreviewSummary{}, appError(CodePluginManifestInvalid, "插件版本不可比较", compareErr)
		}
		if comparison <= 0 {
			return PluginInstallPreviewSummary{}, appError(CodePluginAlreadyInstalled, "普通更新只允许安装更高版本；降级请使用回滚", nil)
		}
		operation, revision = "update", current.Revision
	}
	diff, fingerprint, err := permissionDifference(currentManifest.Permissions, manifest.Permissions)
	if err != nil {
		return PluginInstallPreviewSummary{}, err
	}
	now := time.Now().UTC()
	preview := models.PluginInstallPreview{
		ID: uuid.NewString(), PluginID: entry.ID, PluginPackageID: pluginPackage.ID, Operation: operation,
		PermissionFingerprint: fingerprint, InstallationRevision: revision, CreatedBy: actor.User.ID,
		ExpiresAt: now.Add(pluginPreviewTTL), CreatedAt: now,
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("plugin_id = ? AND consumed_at IS NULL", entry.ID).Delete(&models.PluginInstallPreview{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&preview).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin.preview", "plugin", entry.ID, "success", map[string]any{
			"repository_id": repository.ID, "version": entry.Version, "operation": operation,
			"permission_added": len(diff.Added), "permission_removed": len(diff.Removed),
		}, request)
	}); err != nil {
		return PluginInstallPreviewSummary{}, err
	}
	serverlog.OperationPluginRuntime.Event(s.log.Info()).Str("plugin_id", entry.ID).Uint("repository_id", repository.ID).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationPluginRuntime.Message("安装包校验完成"))
	return PluginInstallPreviewSummary{
		ID: preview.ID, PluginID: entry.ID, Name: manifest.Name, Version: manifest.Version, Operation: operation,
		RepositoryID: repository.ID, RepositoryName: repository.Name, Capabilities: append([]contract.Capability(nil), manifest.Capabilities...),
		Permissions: append([]contract.Permission(nil), manifest.Permissions...), PermissionDiff: diff,
		PermissionFingerprint: fingerprint, InstallationRevision: revision, ExpiresAt: preview.ExpiresAt,
	}, nil
}

func (s *PluginRepositoryService) ConfirmInstall(ctx context.Context, actor Actor, pluginID, expectedOperation, previewID, permissionFingerprint string, revision uint64, request RequestContext) (InstalledPluginSummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return InstalledPluginSummary{}, appError(CodePermissionDenied, "无权安装插件", nil)
	}
	if s.runtime == nil {
		return InstalledPluginSummary{}, appError(CodePluginRuntimeUnavailable, "插件运行时不可用", nil)
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	var preview models.PluginInstallPreview
	if err := s.db.First(&preview, "id = ? AND plugin_id = ? AND created_by = ?", previewID, pluginID, actor.User.ID).Error; err != nil {
		return InstalledPluginSummary{}, pluginPreviewNotFound(err)
	}
	if preview.ConsumedAt != nil || !preview.ExpiresAt.After(time.Now().UTC()) {
		return InstalledPluginSummary{}, appError(CodePluginPreviewExpired, "插件安装确认已过期，请重新预览", nil)
	}
	if (preview.Operation != "install" && preview.Operation != "update") || preview.Operation != expectedOperation {
		return InstalledPluginSummary{}, appError(CodePluginRevisionConflict, "插件安装操作与预览不一致，请重新预览", nil)
	}
	if permissionFingerprint == "" || permissionFingerprint != preview.PermissionFingerprint || revision != preview.InstallationRevision {
		return InstalledPluginSummary{}, appError(CodePluginPermissionChanged, "插件权限或安装状态已变化，请重新确认", nil)
	}
	var pluginPackage models.PluginPackage
	if err := s.db.First(&pluginPackage, preview.PluginPackageID).Error; err != nil {
		return InstalledPluginSummary{}, err
	}
	if pluginPackage.PluginID != pluginID {
		return InstalledPluginSummary{}, appError(CodePluginPackageConflict, "已验证插件包身份与请求不一致", nil)
	}
	manifest, err := contract.ParseManifest([]byte(pluginPackage.ManifestJSON))
	if err != nil {
		return InstalledPluginSummary{}, appError(CodePluginManifestInvalid, "已验证插件清单不可用", err)
	}
	if manifest.ID != pluginID || manifest.Version != pluginPackage.Version || manifest.PackageSHA256 != pluginPackage.PackageSHA256 {
		return InstalledPluginSummary{}, appError(CodePluginPackageConflict, "已验证插件包身份已变化", nil)
	}
	if err := packagefs.ValidateManagedPackage(s.pluginRoot, pluginPackage.PackagePath, manifest, pluginPackage.ExtractedTreeSHA256); err != nil {
		return InstalledPluginSummary{}, appError(CodePluginPackageInvalid, "已验证插件包已损坏或被替换", err)
	}
	if err := s.revalidatePreviewSource(pluginPackage, manifest); err != nil {
		return InstalledPluginSummary{}, err
	}
	current := models.PluginInstallation{}
	currentErr := s.db.First(&current, "plugin_id = ?", pluginID).Error
	if preview.Operation == "install" {
		if !errors.Is(currentErr, gorm.ErrRecordNotFound) || revision != 0 {
			return InstalledPluginSummary{}, appError(CodePluginRevisionConflict, "插件安装状态已变化，请重新预览", currentErr)
		}
	} else if currentErr != nil || current.Revision != revision {
		return InstalledPluginSummary{}, appError(CodePluginRevisionConflict, "插件安装状态已变化，请重新预览", currentErr)
	}

	now := time.Now().UTC()
	wasEnabled := currentErr == nil && current.Status == models.PluginInstallationEnabled
	oldPackageID, oldPreviousID, oldGeneration := current.ActivePackageID, current.PreviousPackageID, current.RuntimeGeneration
	newGeneration := oldGeneration
	if wasEnabled {
		newGeneration++
	}
	installRevision := uint64(1)
	if currentErr == nil {
		installRevision = current.Revision + 1
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		consume := tx.Model(&models.PluginInstallPreview{}).Where("id = ? AND consumed_at IS NULL AND expires_at > ?", preview.ID, now).Update("consumed_at", now)
		if consume.Error != nil {
			return consume.Error
		}
		if consume.RowsAffected != 1 {
			return appError(CodePluginPreviewExpired, "插件安装确认已过期，请重新预览", nil)
		}
		if currentErr == nil {
			updates := map[string]any{
				"active_package_id": pluginPackage.ID, "previous_package_id": current.ActivePackageID,
				"revision": installRevision, "runtime_generation": newGeneration, "updated_at": now,
				"last_runtime_error_code": "",
			}
			result := tx.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", pluginID, revision).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return appError(CodePluginRevisionConflict, "插件安装状态已变化，请重新预览", nil)
			}
		} else {
			current = models.PluginInstallation{PluginID: pluginID, ActivePackageID: pluginPackage.ID, Status: models.PluginInstallationDisabled, Revision: 1, InstalledAt: now, UpdatedAt: now}
			if err := tx.Create(&current).Error; err != nil {
				return err
			}
		}
		if err := replacePermissionGrants(tx, actor.User.ID, pluginID, pluginPackage.ID, manifest.Permissions, now); err != nil {
			return err
		}
		if wasEnabled {
			generation := models.PluginRuntimeGeneration{PluginID: pluginID, PluginPackageID: pluginPackage.ID, Generation: newGeneration, Status: models.PluginRuntimeStarting, StartedAt: now}
			if err := tx.Create(&generation).Error; err != nil {
				return err
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin."+preview.Operation, "plugin", pluginID, "success", map[string]any{
			"version": manifest.Version, "permission_count": len(manifest.Permissions), "enabled": wasEnabled,
		}, request)
	})
	if err != nil {
		return InstalledPluginSummary{}, err
	}
	if wasEnabled {
		if err := packagefs.ValidateManagedPackage(s.pluginRoot, pluginPackage.PackagePath, manifest, pluginPackage.ExtractedTreeSHA256); err != nil {
			compensateErr := s.compensateFailedUpdate(actor, pluginID, installRevision, oldPackageID, oldPreviousID, oldGeneration, newGeneration, CodePluginPackageInvalid, true, request)
			if compensateErr != nil {
				if stopErr := s.runtime.Stop(pluginID); stopErr != nil {
					_ = s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(stopErr))
				}
				return InstalledPluginSummary{}, appError(CodePluginRuntimeStateFailed, "插件包校验失败且安装状态补偿失败，插件已故障关闭", errors.Join(err, compensateErr))
			}
			return InstalledPluginSummary{}, appError(CodePluginPackageInvalid, "插件包在启动前校验失败，已保留旧版本", err)
		}
		entryPath := filepath.Join(pluginPackage.PackagePath, filepath.FromSlash(manifest.Entry))
		if startErr := s.runtime.Start(ctx, pluginID, entryPath, newGeneration); startErr != nil {
			code := pluginruntime.ErrorCode(startErr)
			if compensateErr := s.compensateFailedUpdate(actor, pluginID, installRevision, oldPackageID, oldPreviousID, oldGeneration, newGeneration, code, true, request); compensateErr != nil {
				if stopErr := s.runtime.Stop(pluginID); stopErr != nil {
					_ = s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(stopErr))
				}
				return InstalledPluginSummary{}, appError(CodePluginRuntimeStateFailed, "插件新版本启动失败且安装状态补偿失败，插件已故障关闭", errors.Join(startErr, compensateErr))
			}
			return InstalledPluginSummary{}, appError(CodePluginRuntimeStartFailed, "插件新版本启动失败，已保留旧版本", startErr)
		}
		finished := time.Now().UTC()
		finalizeErr := s.db.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.PluginRuntimeGeneration{}).Where("plugin_id = ? AND generation = ? AND status = ?", pluginID, newGeneration, models.PluginRuntimeStarting).Update("status", models.PluginRuntimeRunning)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("new plugin runtime generation is no longer starting")
			}
			return tx.Model(&models.PluginRuntimeGeneration{}).Where("plugin_id = ? AND generation = ? AND status = ?", pluginID, oldGeneration, models.PluginRuntimeRunning).Updates(map[string]any{"status": models.PluginRuntimeStopped, "stopped_at": finished}).Error
		})
		if finalizeErr != nil {
			runtimeRestored := false
			var oldPackage models.PluginPackage
			if err := s.db.First(&oldPackage, oldPackageID).Error; err == nil {
				if oldManifest, err := contract.ParseManifest([]byte(oldPackage.ManifestJSON)); err == nil {
					if packagefs.ValidateManagedPackage(s.pluginRoot, oldPackage.PackagePath, oldManifest, oldPackage.ExtractedTreeSHA256) == nil {
						oldEntryPath := filepath.Join(oldPackage.PackagePath, filepath.FromSlash(oldManifest.Entry))
						runtimeRestored = s.runtime.Start(ctx, pluginID, oldEntryPath, oldGeneration) == nil
					}
				}
			}
			compensateErr := s.compensateFailedUpdate(actor, pluginID, installRevision, oldPackageID, oldPreviousID, oldGeneration, newGeneration, CodePluginRuntimeStateFailed, runtimeRestored, request)
			if compensateErr != nil || !runtimeRestored {
				if stopErr := s.runtime.Stop(pluginID); stopErr != nil {
					_ = s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(stopErr))
				}
			}
			message := "插件运行状态保存失败，已恢复旧版本"
			if !runtimeRestored || compensateErr != nil {
				message = "插件运行状态保存失败，旧版本恢复也失败，插件已标记故障"
			}
			return InstalledPluginSummary{}, appError(CodePluginRuntimeStateFailed, message, errors.Join(finalizeErr, compensateErr))
		}
	}
	return s.installedByID(pluginID)
}

func (s *PluginRepositoryService) SetPluginEnabled(ctx context.Context, actor Actor, pluginID string, enabled bool, revision uint64, request RequestContext) (InstalledPluginSummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return InstalledPluginSummary{}, appError(CodePermissionDenied, "无权修改插件状态", nil)
	}
	if revision == 0 || s.runtime == nil {
		return InstalledPluginSummary{}, appError(CodeInvalidRequest, "插件状态请求无效", nil)
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	installation, pluginPackage, manifest, err := s.loadInstalled(pluginID)
	if err != nil {
		return InstalledPluginSummary{}, err
	}
	if installation.Revision != revision {
		return InstalledPluginSummary{}, appError(CodePluginRevisionConflict, "插件状态已变化，请刷新后重试", nil)
	}
	if enabled && installation.Status == models.PluginInstallationEnabled {
		return s.installedByID(pluginID)
	}
	if !enabled && installation.Status == models.PluginInstallationDisabled {
		return s.installedByID(pluginID)
	}
	now := time.Now().UTC()
	if enabled {
		if err := packagefs.ValidateManagedPackage(s.pluginRoot, pluginPackage.PackagePath, manifest, pluginPackage.ExtractedTreeSHA256); err != nil {
			return InstalledPluginSummary{}, appError(CodePluginPackageInvalid, "插件包在启动前校验失败", err)
		}
		generation := installation.RuntimeGeneration + 1
		entryPath := filepath.Join(pluginPackage.PackagePath, filepath.FromSlash(manifest.Entry))
		if err := s.runtime.Start(ctx, pluginID, entryPath, generation); err != nil {
			code := pluginruntime.ErrorCode(err)
			_ = s.db.Transaction(func(tx *gorm.DB) error {
				result := tx.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", pluginID, revision).Updates(map[string]any{"status": models.PluginInstallationFailed, "revision": revision + 1, "runtime_generation": generation, "last_runtime_error_code": code, "updated_at": now})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return appError(CodePluginRevisionConflict, "插件状态已变化，请刷新后重试", nil)
				}
				if err := tx.Create(&models.PluginRuntimeGeneration{PluginID: pluginID, PluginPackageID: pluginPackage.ID, Generation: generation, Status: models.PluginRuntimeFailed, SafeErrorCode: code, StartedAt: now, StoppedAt: &now}).Error; err != nil {
					return err
				}
				return s.audit.Record(tx, &actor.User.ID, "plugin.enable", "plugin", pluginID, "failure", map[string]any{"error_code": code}, request)
			})
			return InstalledPluginSummary{}, appError(CodePluginRuntimeStartFailed, "插件启动失败", err)
		}
		err = s.db.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", pluginID, revision).Updates(map[string]any{"status": models.PluginInstallationEnabled, "revision": revision + 1, "runtime_generation": generation, "last_runtime_error_code": "", "enabled_at": now, "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return appError(CodePluginRevisionConflict, "插件状态已变化，请刷新后重试", nil)
			}
			if err := tx.Create(&models.PluginRuntimeGeneration{PluginID: pluginID, PluginPackageID: pluginPackage.ID, Generation: generation, Status: models.PluginRuntimeRunning, StartedAt: now}).Error; err != nil {
				return err
			}
			return s.audit.Record(tx, &actor.User.ID, "plugin.enable", "plugin", pluginID, "success", map[string]any{"version": manifest.Version}, request)
		})
		if err != nil {
			if stopErr := s.runtime.Stop(pluginID); stopErr != nil {
				_ = s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(stopErr))
				return InstalledPluginSummary{}, appError(CodePluginRuntimeStopFailed, "插件状态保存失败，运行时已进入故障关闭", errors.Join(err, stopErr))
			}
			return InstalledPluginSummary{}, err
		}
	} else {
		if err := s.runtime.Stop(pluginID); err != nil {
			if markErr := s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(err)); markErr != nil {
				return InstalledPluginSummary{}, appError(CodePluginRuntimeStateFailed, "插件停止失败且故障状态保存失败", errors.Join(err, markErr))
			}
			return InstalledPluginSummary{}, appError(CodePluginRuntimeStopFailed, "插件停止失败，已按故障关闭处理", err)
		}
		err = s.db.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", pluginID, revision).Updates(map[string]any{"status": models.PluginInstallationDisabled, "revision": revision + 1, "enabled_at": nil, "last_runtime_error_code": "", "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return appError(CodePluginRevisionConflict, "插件状态已变化，请刷新后重试", nil)
			}
			if err := tx.Model(&models.PluginRuntimeGeneration{}).Where("plugin_id = ? AND generation = ? AND status = ?", pluginID, installation.RuntimeGeneration, models.PluginRuntimeRunning).Updates(map[string]any{"status": models.PluginRuntimeStopped, "stopped_at": now}).Error; err != nil {
				return err
			}
			return s.audit.Record(tx, &actor.User.ID, "plugin.disable", "plugin", pluginID, "success", map[string]any{"version": manifest.Version}, request)
		})
		if err != nil {
			entryPath := filepath.Join(pluginPackage.PackagePath, filepath.FromSlash(manifest.Entry))
			if restartErr := s.runtime.Start(ctx, pluginID, entryPath, installation.RuntimeGeneration); restartErr != nil {
				s.markRuntimeCompensationFailure(installation, pluginruntime.ErrorCode(restartErr))
				return InstalledPluginSummary{}, appError(CodePluginRuntimeStartFailed, "插件状态保存失败且运行时恢复失败", errors.Join(err, restartErr))
			}
			return InstalledPluginSummary{}, err
		}
	}
	return s.installedByID(pluginID)
}

func (s *PluginRepositoryService) RollbackPlugin(ctx context.Context, actor Actor, pluginID string, revision uint64, request RequestContext) (InstalledPluginSummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return InstalledPluginSummary{}, appError(CodePermissionDenied, "无权回滚插件", nil)
	}
	if revision == 0 || s.runtime == nil {
		return InstalledPluginSummary{}, appError(CodeInvalidRequest, "插件回滚请求无效", nil)
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	installation, currentPackage, currentManifest, err := s.loadInstalled(pluginID)
	if err != nil {
		return InstalledPluginSummary{}, err
	}
	if installation.Revision != revision || installation.PreviousPackageID == nil {
		return InstalledPluginSummary{}, appError(CodePluginRollbackUnavailable, "没有可回滚的插件版本，或状态已经变化", nil)
	}
	var previous models.PluginPackage
	if err := s.db.First(&previous, *installation.PreviousPackageID).Error; err != nil {
		return InstalledPluginSummary{}, err
	}
	manifest, err := contract.ParseManifest([]byte(previous.ManifestJSON))
	if err != nil {
		return InstalledPluginSummary{}, appError(CodePluginManifestInvalid, "回滚版本清单不可用", err)
	}
	if err := packagefs.ValidateManagedPackage(s.pluginRoot, previous.PackagePath, manifest, previous.ExtractedTreeSHA256); err != nil {
		return InstalledPluginSummary{}, appError(CodePluginPackageInvalid, "回滚版本插件包已损坏或被替换", err)
	}
	newGeneration := installation.RuntimeGeneration
	if installation.Status == models.PluginInstallationEnabled {
		newGeneration++
		entryPath := filepath.Join(previous.PackagePath, filepath.FromSlash(manifest.Entry))
		if err := s.runtime.Start(ctx, pluginID, entryPath, newGeneration); err != nil {
			return InstalledPluginSummary{}, appError(CodePluginRuntimeStartFailed, "回滚版本启动失败，当前版本保持运行", err)
		}
	}
	now := time.Now().UTC()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", pluginID, revision).Updates(map[string]any{
			"active_package_id": previous.ID, "previous_package_id": installation.ActivePackageID,
			"revision": revision + 1, "runtime_generation": newGeneration, "last_runtime_error_code": "", "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodePluginRevisionConflict, "插件状态已变化，请刷新后重试", nil)
		}
		if installation.Status == models.PluginInstallationEnabled {
			if err := tx.Create(&models.PluginRuntimeGeneration{PluginID: pluginID, PluginPackageID: previous.ID, Generation: newGeneration, Status: models.PluginRuntimeRunning, StartedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.PluginRuntimeGeneration{}).Where("plugin_id = ? AND generation = ? AND status = ?", pluginID, installation.RuntimeGeneration, models.PluginRuntimeRunning).Updates(map[string]any{"status": models.PluginRuntimeStopped, "stopped_at": now}).Error; err != nil {
				return err
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin.rollback", "plugin", pluginID, "success", map[string]any{"version": manifest.Version}, request)
	})
	if err != nil {
		if installation.Status == models.PluginInstallationEnabled {
			validateErr := packagefs.ValidateManagedPackage(s.pluginRoot, currentPackage.PackagePath, currentManifest, currentPackage.ExtractedTreeSHA256)
			var restartErr error
			if validateErr == nil {
				currentEntryPath := filepath.Join(currentPackage.PackagePath, filepath.FromSlash(currentManifest.Entry))
				restartErr = s.runtime.Start(ctx, pluginID, currentEntryPath, installation.RuntimeGeneration)
			} else {
				restartErr = validateErr
			}
			if restartErr != nil {
				if stopErr := s.runtime.Stop(pluginID); stopErr != nil {
					_ = s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(stopErr))
				}
				s.markRuntimeCompensationFailure(installation, pluginruntime.ErrorCode(restartErr))
				return InstalledPluginSummary{}, appError(CodePluginRuntimeStartFailed, "插件回滚保存失败且当前版本恢复失败", errors.Join(err, restartErr))
			}
		}
		return InstalledPluginSummary{}, err
	}
	return s.installedByID(pluginID)
}

func (s *PluginRepositoryService) UninstallPlugin(actor Actor, pluginID string, revision uint64, request RequestContext) error {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return appError(CodePermissionDenied, "无权卸载插件", nil)
	}
	if revision == 0 {
		return appError(CodeInvalidRequest, "插件卸载请求无效", nil)
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	var installation models.PluginInstallation
	if err := s.db.First(&installation, "plugin_id = ?", pluginID).Error; err != nil {
		return pluginInstallationNotFound(err)
	}
	if installation.Revision != revision {
		return appError(CodePluginRevisionConflict, "插件状态已变化，请刷新后重试", nil)
	}
	var pluginPackage models.PluginPackage
	if err := s.db.First(&pluginPackage, installation.ActivePackageID).Error; err != nil {
		return err
	}
	manifest, err := contract.ParseManifest([]byte(pluginPackage.ManifestJSON))
	if err != nil {
		return appError(CodePluginManifestInvalid, "已安装插件清单不可用", err)
	}
	wasEnabled := installation.Status == models.PluginInstallationEnabled
	if wasEnabled {
		if s.runtime == nil {
			return appError(CodePluginRuntimeUnavailable, "插件运行时不可用", nil)
		}
		if err := s.runtime.Stop(pluginID); err != nil {
			_ = s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(err))
			return appError(CodePluginRuntimeStopFailed, "插件停止失败，未执行卸载", err)
		}
	}
	var packages []models.PluginPackage
	if err := s.db.Where("plugin_id = ?", pluginID).Order("id ASC").Find(&packages).Error; err != nil {
		return err
	}
	packagePaths := make([]string, 0, len(packages))
	for _, item := range packages {
		packagePaths = append(packagePaths, item.PackagePath)
	}
	quarantine, err := packagefs.QuarantinePackages(s.pluginRoot, packagePaths)
	if err != nil {
		if wasEnabled {
			entryPath := filepath.Join(pluginPackage.PackagePath, filepath.FromSlash(manifest.Entry))
			if restartErr := s.runtime.Start(context.Background(), pluginID, entryPath, installation.RuntimeGeneration); restartErr != nil {
				s.markRuntimeCompensationFailure(installation, pluginruntime.ErrorCode(restartErr))
				return appError(CodePluginRuntimeStartFailed, "卸载隔离失败且插件恢复失败", errors.Join(err, restartErr))
			}
		}
		return appError(CodePluginCleanupFailed, "插件包无法安全移入卸载隔离区", err)
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("plugin_id = ? AND revision = ?", pluginID, revision).Delete(&models.PluginInstallation{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodePluginRevisionConflict, "插件状态已变化，请刷新后重试", nil)
		}
		if err := tx.Where("plugin_id = ?", pluginID).Delete(&models.PluginInstallPreview{}).Error; err != nil {
			return err
		}
		if err := tx.Where("plugin_id = ?", pluginID).Delete(&models.PluginPermissionGrant{}).Error; err != nil {
			return err
		}
		if err := tx.Where("plugin_id = ?", pluginID).Delete(&models.PluginRuntimeGeneration{}).Error; err != nil {
			return err
		}
		if err := tx.Where("plugin_id = ?", pluginID).Delete(&models.PluginPackage{}).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin.uninstall", "plugin", pluginID, "success", map[string]any{"package_count": len(packages)}, request)
	})
	if err != nil {
		restoreErr := packagefs.RestoreQuarantine(s.pluginRoot, quarantine)
		if wasEnabled {
			if restoreErr != nil {
				s.markRuntimeCompensationFailure(installation, CodePluginCleanupFailed)
				return appError(CodePluginCleanupFailed, "卸载事务失败且插件包恢复失败", errors.Join(err, restoreErr))
			}
			entryPath := filepath.Join(pluginPackage.PackagePath, filepath.FromSlash(manifest.Entry))
			if restartErr := s.runtime.Start(context.Background(), pluginID, entryPath, installation.RuntimeGeneration); restartErr != nil {
				s.markRuntimeCompensationFailure(installation, pluginruntime.ErrorCode(restartErr))
				return appError(CodePluginRuntimeStartFailed, "卸载事务失败且插件恢复失败", errors.Join(err, restartErr))
			}
		} else if restoreErr != nil {
			return appError(CodePluginCleanupFailed, "卸载事务失败且插件包恢复失败", errors.Join(err, restoreErr))
		}
		return err
	}
	if err := packagefs.RemoveQuarantine(s.pluginRoot, quarantine); err != nil {
		return appError(CodePluginCleanupFailed, "插件已卸载，但隔离包清理失败，Server 重启时将再次清理", err)
	}
	return nil
}

func (s *PluginRepositoryService) RestorePlugins(ctx context.Context) error {
	if s.runtime == nil {
		return appError(CodePluginRuntimeUnavailable, "插件运行时不可用", nil)
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if _, err := validatePluginRoot(s.pluginRoot); err != nil {
		return appError(CodePluginRuntimeUnavailable, "插件隔离目录未正确配置", err)
	}
	if err := s.prunePluginArtifacts(); err != nil {
		return appError(CodePluginCleanupFailed, "插件暂存清理失败", err)
	}
	// In-memory modules never survive a Server process. Close all generations
	// left active by the previous process before appending a fresh generation.
	restoredAt := time.Now().UTC()
	if err := s.db.Model(&models.PluginRuntimeGeneration{}).
		Where("status IN ?", []string{models.PluginRuntimeStarting, models.PluginRuntimeRunning}).
		Updates(map[string]any{"status": models.PluginRuntimeStopped, "stopped_at": restoredAt}).Error; err != nil {
		return err
	}
	var installations []models.PluginInstallation
	if err := s.db.Where("status = ?", models.PluginInstallationEnabled).Order("plugin_id ASC").Find(&installations).Error; err != nil {
		return err
	}
	for _, installation := range installations {
		var pluginPackage models.PluginPackage
		if err := s.db.First(&pluginPackage, installation.ActivePackageID).Error; err != nil {
			s.recordRestoreFailure(installation, CodePluginPackageInvalid)
			continue
		}
		manifest, err := contract.ParseManifest([]byte(pluginPackage.ManifestJSON))
		if err != nil {
			s.recordRestoreFailure(installation, CodePluginManifestInvalid)
			continue
		}
		if err := packagefs.ValidateManagedPackage(s.pluginRoot, pluginPackage.PackagePath, manifest, pluginPackage.ExtractedTreeSHA256); err != nil {
			s.recordRestoreFailure(installation, CodePluginPackageInvalid)
			continue
		}
		generation := installation.RuntimeGeneration + 1
		entryPath := filepath.Join(pluginPackage.PackagePath, filepath.FromSlash(manifest.Entry))
		if err := s.runtime.Start(ctx, installation.PluginID, entryPath, generation); err != nil {
			s.recordRestoreFailure(installation, pluginruntime.ErrorCode(err))
			continue
		}
		now := time.Now().UTC()
		persistErr := s.db.Transaction(func(tx *gorm.DB) error {
			result := tx.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", installation.PluginID, installation.Revision).Updates(map[string]any{"runtime_generation": generation, "revision": installation.Revision + 1, "last_runtime_error_code": "", "updated_at": now})
			if result.Error != nil || result.RowsAffected != 1 {
				return result.Error
			}
			return tx.Create(&models.PluginRuntimeGeneration{PluginID: installation.PluginID, PluginPackageID: pluginPackage.ID, Generation: generation, Status: models.PluginRuntimeRunning, StartedAt: now}).Error
		})
		if persistErr != nil {
			if stopErr := s.runtime.Stop(installation.PluginID); stopErr != nil {
				_ = s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(stopErr))
				return errors.Join(persistErr, stopErr)
			}
			return persistErr
		}
	}
	return nil
}

func (s *PluginRepositoryService) ClosePlugins(ctx context.Context) error {
	if s.runtime == nil {
		return nil
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if err := s.runtime.Close(ctx); err != nil {
		_ = s.markRuntimeHostUnavailable(pluginruntime.ErrorCode(err))
		return err
	}
	now := time.Now().UTC()
	return s.db.Model(&models.PluginRuntimeGeneration{}).
		Where("status IN ?", []string{models.PluginRuntimeStarting, models.PluginRuntimeRunning}).
		Updates(map[string]any{"status": models.PluginRuntimeStopped, "stopped_at": now}).Error
}

func (s *PluginRepositoryService) prunePluginArtifacts() error {
	var allPackages []models.PluginPackage
	if err := s.db.Select("package_sha256").Find(&allPackages).Error; err != nil {
		return err
	}
	referenced := make(map[string]struct{}, len(allPackages))
	for _, pluginPackage := range allPackages {
		referenced[pluginPackage.PackageSHA256] = struct{}{}
	}
	if err := packagefs.ReconcileStaging(s.pluginRoot, referenced); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.db.Where("consumed_at IS NOT NULL OR expires_at <= ?", now).Delete(&models.PluginInstallPreview{}).Error; err != nil {
		return err
	}
	var packages []models.PluginPackage
	query := `NOT EXISTS (SELECT 1 FROM plugin_installations WHERE active_package_id = plugin_packages.id OR previous_package_id = plugin_packages.id)
		AND NOT EXISTS (SELECT 1 FROM plugin_install_previews WHERE plugin_package_id = plugin_packages.id AND consumed_at IS NULL AND expires_at > ?)
		AND NOT EXISTS (SELECT 1 FROM plugin_runtime_generations WHERE plugin_package_id = plugin_packages.id)`
	if err := s.db.Where(query, now).Order("id ASC").Find(&packages).Error; err != nil {
		return err
	}
	if len(packages) == 0 {
		return nil
	}
	paths := make([]string, 0, len(packages))
	ids := make([]uint, 0, len(packages))
	for _, pluginPackage := range packages {
		paths = append(paths, pluginPackage.PackagePath)
		ids = append(ids, pluginPackage.ID)
	}
	quarantine, err := packagefs.QuarantinePackages(s.pluginRoot, paths)
	if err != nil {
		return err
	}
	if err := s.db.Where("id IN ?", ids).Delete(&models.PluginPackage{}).Error; err != nil {
		_ = packagefs.RestoreQuarantine(s.pluginRoot, quarantine)
		return err
	}
	if err := packagefs.RemoveQuarantine(s.pluginRoot, quarantine); err != nil {
		return err
	}
	serverlog.OperationPluginRuntime.Event(s.log.Info()).Int("package_count", len(packages)).Msg(serverlog.OperationPluginRuntime.Message("已清理孤立插件包"))
	return nil
}

func (s *PluginRepositoryService) Installed(actor Actor) ([]InstalledPluginSummary, error) {
	if !actor.Can(authz.PermissionPluginsRead) {
		return nil, appError(CodePermissionDenied, "无权查看已安装插件", nil)
	}
	var installations []models.PluginInstallation
	if err := s.db.Order("plugin_id ASC").Find(&installations).Error; err != nil {
		return nil, err
	}
	items := make([]InstalledPluginSummary, 0, len(installations))
	for _, installation := range installations {
		item, err := s.installedSummary(installation)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *PluginRepositoryService) resolveInstallCandidate(pluginID string, repositoryID uint, version string) (models.PluginRepository, contract.RegistryEntry, error) {
	var repository models.PluginRepository
	if repositoryID == 0 || s.db.First(&repository, "id = ? AND enabled = ?", repositoryID, true).Error != nil {
		return repository, contract.RegistryEntry{}, appError(CodeNotFound, "插件仓库不存在或未启用", nil)
	}
	if repository.CachedRegistryJSON == "" || !pluginCommitSHAPattern.MatchString(repository.LastCommitSHA) {
		return repository, contract.RegistryEntry{}, appError(CodePluginRepositoryUnavailable, "插件仓库尚无有效缓存，请先刷新", nil)
	}
	source := contract.GitHubRepository{Owner: repository.GitHubOwner, Name: repository.GitHubRepo}
	registry, err := contract.ParseRegistry([]byte(repository.CachedRegistryJSON), source)
	if err != nil {
		return repository, contract.RegistryEntry{}, appError(CodePluginRegistryInvalid, "插件仓库缓存已失效，请重新刷新", err)
	}
	var selected *contract.RegistryEntry
	for index := range registry.Plugins {
		entry := &registry.Plugins[index]
		if entry.ID != pluginID || (version != "" && entry.Version != version) || compatibility(s.version, *entry) != "compatible" {
			continue
		}
		if selected == nil || betterRegistryEntry(*entry, *selected) {
			selected = entry
		}
	}
	if selected == nil {
		return repository, contract.RegistryEntry{}, appError(CodeNotFound, "未找到兼容的插件版本", nil)
	}
	return repository, *selected, nil
}

// revalidatePreviewSource prevents a verified package from being confirmed
// after its repository was disabled, deleted, refreshed to another commit, or
// changed the exact release entry that authorized the download.
func (s *PluginRepositoryService) revalidatePreviewSource(pluginPackage models.PluginPackage, manifest contract.Manifest) error {
	if pluginPackage.RepositoryID == nil {
		return appError(CodePluginPreviewExpired, "插件来源已不存在，请重新预览", nil)
	}
	var repository models.PluginRepository
	if err := s.db.First(&repository, "id = ? AND enabled = ?", *pluginPackage.RepositoryID, true).Error; err != nil {
		return appError(CodePluginPreviewExpired, "插件仓库已删除或停用，请重新预览", err)
	}
	if !strings.EqualFold(repository.GitHubOwner, pluginPackage.RepositoryOwner) ||
		!strings.EqualFold(repository.GitHubRepo, pluginPackage.RepositoryRepo) ||
		repository.LastCommitSHA != pluginPackage.RegistryCommit ||
		!pluginCommitSHAPattern.MatchString(repository.LastCommitSHA) {
		return appError(CodePluginPreviewExpired, "插件仓库来源或固定版本已变化，请重新预览", nil)
	}
	source := contract.GitHubRepository{Owner: repository.GitHubOwner, Name: repository.GitHubRepo}
	registry, err := contract.ParseRegistry([]byte(repository.CachedRegistryJSON), source)
	if err != nil {
		return appError(CodePluginPreviewExpired, "插件仓库缓存已变化，请重新预览", err)
	}
	for _, entry := range registry.Plugins {
		canonicalEntry, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			return appError(CodePluginPreviewExpired, "插件仓库发布条目不可用，请重新预览", marshalErr)
		}
		if entry.ID == pluginPackage.PluginID &&
			entry.Version == pluginPackage.Version &&
			entry.PackageSHA256 == pluginPackage.PackageSHA256 &&
			entry.ManifestURL == pluginPackage.ManifestURL &&
			entry.PackageURL == pluginPackage.PackageURL &&
			string(canonicalEntry) == pluginPackage.RegistryEntryJSON &&
			entry.Name == manifest.Name &&
			entry.MinServerVersion == manifest.MinServerVersion &&
			entry.MaxServerVersion == manifest.MaxServerVersion &&
			validateManifestAgainstRegistry(manifest, entry, source) == nil {
			return nil
		}
	}
	return appError(CodePluginPreviewExpired, "插件仓库中的发布条目已变化，请重新预览", nil)
}

func validateManifestAgainstRegistry(manifest contract.Manifest, entry contract.RegistryEntry, source contract.GitHubRepository) error {
	if manifest.ID != entry.ID || manifest.Version != entry.Version || manifest.Name != entry.Name || manifest.PackageSHA256 != entry.PackageSHA256 || manifest.MinServerVersion != entry.MinServerVersion || manifest.MaxServerVersion != entry.MaxServerVersion {
		return errors.New("plugin manifest identity or version does not match registry")
	}
	manifestSource, err := contract.ParseGitHubRepositoryURL(manifest.Source)
	if err != nil || !strings.EqualFold(manifestSource.Owner, source.Owner) || !strings.EqualFold(manifestSource.Name, source.Name) {
		return errors.New("plugin manifest source does not match repository")
	}
	return nil
}

func validatePluginRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return "", errors.New("plugin root must be an absolute path")
	}
	cleaned := filepath.Clean(root)
	if cleaned == filepath.VolumeName(cleaned)+string(filepath.Separator) {
		return "", errors.New("plugin root must not be a filesystem root")
	}
	return cleaned, nil
}

func (s *PluginRepositoryService) persistVerifiedPackage(repository models.PluginRepository, entry contract.RegistryEntry, manifest contract.Manifest, digest, extractedTreeSHA256, packagePath, manifestJSON string) (models.PluginPackage, error) {
	canonicalEntry, err := json.Marshal(entry)
	if err != nil {
		return models.PluginPackage{}, err
	}
	var existing models.PluginPackage
	err = s.db.First(&existing, "package_sha256 = ?", digest).Error
	if err == nil {
		if existing.PluginID != manifest.ID || existing.Version != manifest.Version || !strings.EqualFold(existing.RepositoryOwner, repository.GitHubOwner) || !strings.EqualFold(existing.RepositoryRepo, repository.GitHubRepo) || existing.RegistryEntryJSON != string(canonicalEntry) || existing.ManifestURL != entry.ManifestURL || existing.PackageURL != entry.PackageURL || existing.ExtractedTreeSHA256 != extractedTreeSHA256 || existing.ManifestJSON != manifestJSON || filepath.Clean(existing.PackagePath) != filepath.Clean(packagePath) {
			return models.PluginPackage{}, appError(CodePluginPackageConflict, "相同摘要的插件包身份冲突", nil)
		}
		return existing, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.PluginPackage{}, err
	}
	now := time.Now().UTC()
	record := models.PluginPackage{PluginID: manifest.ID, Version: manifest.Version, RepositoryID: &repository.ID, RepositoryOwner: repository.GitHubOwner, RepositoryRepo: repository.GitHubRepo, RegistryCommit: repository.LastCommitSHA, RegistryEntryJSON: string(canonicalEntry), ManifestURL: entry.ManifestURL, PackageURL: entry.PackageURL, PackageSHA256: digest, ExtractedTreeSHA256: extractedTreeSHA256, ManifestJSON: manifestJSON, PackagePath: packagePath, VerifiedAt: now, CreatedAt: now}
	if err := s.db.Create(&record).Error; err != nil {
		if retry := s.db.First(&existing, "package_sha256 = ?", digest).Error; retry == nil {
			return existing, nil
		}
		return models.PluginPackage{}, err
	}
	return record, nil
}

func permissionDifference(oldPermissions, newPermissions []contract.Permission) (PluginPermissionDiff, string, error) {
	oldMap, _, err := canonicalPermissions(oldPermissions)
	if err != nil {
		return PluginPermissionDiff{}, "", err
	}
	newMap, canonical, err := canonicalPermissions(newPermissions)
	if err != nil {
		return PluginPermissionDiff{}, "", err
	}
	diff := PluginPermissionDiff{}
	for key, permission := range newMap {
		if _, ok := oldMap[key]; ok {
			diff.Unchanged = append(diff.Unchanged, permission)
		} else {
			diff.Added = append(diff.Added, permission)
		}
	}
	for key, permission := range oldMap {
		if _, ok := newMap[key]; !ok {
			diff.Removed = append(diff.Removed, permission)
		}
	}
	sortPermissions(diff.Added)
	sortPermissions(diff.Removed)
	sortPermissions(diff.Unchanged)
	fingerprint := sha256.Sum256([]byte(strings.Join(canonical, "\n")))
	return diff, hex.EncodeToString(fingerprint[:]), nil
}

func canonicalPermissions(permissions []contract.Permission) (map[string]contract.Permission, []string, error) {
	result := make(map[string]contract.Permission, len(permissions))
	canonical := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		data, err := json.Marshal(permission)
		if err != nil {
			return nil, nil, err
		}
		keyDigest := sha256.Sum256(data)
		key := hex.EncodeToString(keyDigest[:])
		result[key] = permission
		canonical = append(canonical, string(data))
	}
	sort.Strings(canonical)
	return result, canonical, nil
}

func sortPermissions(permissions []contract.Permission) {
	sort.Slice(permissions, func(i, j int) bool {
		left, _ := json.Marshal(permissions[i])
		right, _ := json.Marshal(permissions[j])
		return string(left) < string(right)
	})
}

func replacePermissionGrants(tx *gorm.DB, actorID uint, pluginID string, packageID uint, permissions []contract.Permission, now time.Time) error {
	if err := tx.Where("plugin_id = ? AND plugin_package_id = ?", pluginID, packageID).Delete(&models.PluginPermissionGrant{}).Error; err != nil {
		return err
	}
	canonical, _, err := canonicalPermissions(permissions)
	if err != nil {
		return err
	}
	for key, permission := range canonical {
		data, _ := json.Marshal(permission)
		grantActorID := actorID
		grant := models.PluginPermissionGrant{PluginID: pluginID, PluginPackageID: packageID, PermissionKey: key, PermissionJSON: string(data), GrantedBy: &grantActorID, CreatedAt: now}
		if err := tx.Create(&grant).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *PluginRepositoryService) currentInstallationManifest(pluginID string) (models.PluginInstallation, contract.Manifest, error) {
	installation, _, manifest, err := s.loadInstalled(pluginID)
	return installation, manifest, err
}

func (s *PluginRepositoryService) loadInstalled(pluginID string) (models.PluginInstallation, models.PluginPackage, contract.Manifest, error) {
	var installation models.PluginInstallation
	if err := s.db.First(&installation, "plugin_id = ?", pluginID).Error; err != nil {
		return installation, models.PluginPackage{}, contract.Manifest{}, pluginInstallationNotFound(err)
	}
	var pluginPackage models.PluginPackage
	if err := s.db.First(&pluginPackage, installation.ActivePackageID).Error; err != nil {
		return installation, pluginPackage, contract.Manifest{}, err
	}
	manifest, err := contract.ParseManifest([]byte(pluginPackage.ManifestJSON))
	return installation, pluginPackage, manifest, err
}

func (s *PluginRepositoryService) installedByID(pluginID string) (InstalledPluginSummary, error) {
	var installation models.PluginInstallation
	if err := s.db.First(&installation, "plugin_id = ?", pluginID).Error; err != nil {
		return InstalledPluginSummary{}, pluginInstallationNotFound(err)
	}
	return s.installedSummary(installation)
}

func (s *PluginRepositoryService) installedSummary(installation models.PluginInstallation) (InstalledPluginSummary, error) {
	var pluginPackage models.PluginPackage
	if err := s.db.First(&pluginPackage, installation.ActivePackageID).Error; err != nil {
		return InstalledPluginSummary{}, err
	}
	manifest, err := contract.ParseManifest([]byte(pluginPackage.ManifestJSON))
	if err != nil {
		return InstalledPluginSummary{}, err
	}
	item := InstalledPluginSummary{ID: installation.PluginID, Name: manifest.Name, Description: manifest.Description, Version: manifest.Version, RepositoryID: pluginPackage.RepositoryID, Status: installation.Status, Revision: installation.Revision, RuntimeGeneration: installation.RuntimeGeneration, LastRuntimeErrorCode: installation.LastRuntimeErrorCode, Capabilities: append([]contract.Capability(nil), manifest.Capabilities...), Permissions: append([]contract.Permission(nil), manifest.Permissions...), InstalledAt: installation.InstalledAt, UpdatedAt: installation.UpdatedAt}
	if pluginPackage.RepositoryID != nil {
		var repository models.PluginRepository
		if err := s.db.Select("name").First(&repository, *pluginPackage.RepositoryID).Error; err == nil {
			item.RepositoryName = repository.Name
		}
	}
	if installation.PreviousPackageID != nil {
		var previous models.PluginPackage
		if err := s.db.Select("version").First(&previous, *installation.PreviousPackageID).Error; err == nil {
			item.PreviousVersion = previous.Version
		}
	}
	return item, nil
}

func (s *PluginRepositoryService) compensateFailedUpdate(actor Actor, pluginID string, failedRevision uint64, oldPackageID uint, oldPreviousID *uint, oldGeneration, failedGeneration uint64, code string, runtimeRestored bool, request RequestContext) error {
	now := time.Now().UTC()
	status := models.PluginInstallationEnabled
	if !runtimeRestored {
		status = models.PluginInstallationFailed
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", pluginID, failedRevision).Updates(map[string]any{
			"active_package_id": oldPackageID, "previous_package_id": oldPreviousID, "status": status,
			"revision": failedRevision + 1, "runtime_generation": oldGeneration, "last_runtime_error_code": code, "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("plugin update compensation lost revision ownership")
		}
		if err := tx.Model(&models.PluginRuntimeGeneration{}).Where("plugin_id = ? AND generation = ?", pluginID, failedGeneration).Updates(map[string]any{"status": models.PluginRuntimeFailed, "safe_error_code": code, "stopped_at": now}).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin.update", "plugin", pluginID, "failure", map[string]any{"error_code": code, "rollback": true}, request)
	})
}

func (s *PluginRepositoryService) markRuntimeCompensationFailure(installation models.PluginInstallation, code string) {
	now := time.Now().UTC()
	_ = s.db.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", installation.PluginID, installation.Revision).Updates(map[string]any{
		"status": models.PluginInstallationFailed, "revision": installation.Revision + 1,
		"last_runtime_error_code": safeLabel(code, 96), "updated_at": now,
	}).Error
}

func (s *PluginRepositoryService) markRuntimeHostUnavailable(code string) error {
	now := time.Now().UTC()
	code = safeLabel(code, 96)
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.PluginInstallation{}).
			Where("status = ?", models.PluginInstallationEnabled).
			Updates(map[string]any{
				"status":                  models.PluginInstallationFailed,
				"revision":                gorm.Expr("revision + 1"),
				"last_runtime_error_code": code,
				"updated_at":              now,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&models.PluginRuntimeGeneration{}).
			Where("status IN ?", []string{models.PluginRuntimeStarting, models.PluginRuntimeRunning}).
			Updates(map[string]any{"status": models.PluginRuntimeFailed, "safe_error_code": code, "stopped_at": now}).Error
	})
}

func (s *PluginRepositoryService) recordRestoreFailure(installation models.PluginInstallation, code string) {
	now := time.Now().UTC()
	generation := installation.RuntimeGeneration + 1
	_ = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PluginInstallation{}).Where("plugin_id = ? AND revision = ?", installation.PluginID, installation.Revision).Updates(map[string]any{"status": models.PluginInstallationFailed, "revision": installation.Revision + 1, "runtime_generation": generation, "last_runtime_error_code": code, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return result.Error
		}
		if err := tx.Create(&models.PluginRuntimeGeneration{PluginID: installation.PluginID, PluginPackageID: installation.ActivePackageID, Generation: generation, Status: models.PluginRuntimeFailed, SafeErrorCode: code, StartedAt: now, StoppedAt: &now}).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, nil, "plugin.restore", "plugin", installation.PluginID, "failure", map[string]any{"error_code": code}, RequestContext{})
	})
}

func (s *PluginRepositoryService) installAssetError(pluginID string, repositoryID uint, started time.Time, err error) error {
	code := pluginrepository.ErrorCode(err)
	s.logPluginInstallFailure(pluginID, repositoryID, started, code)
	switch code {
	case pluginrepository.CodeAssetTooLarge:
		return appError(CodePluginPackageTooLarge, "插件发布资产过大", err)
	case pluginrepository.CodeAssetInvalid:
		return appError(CodePluginAssetInvalid, "插件发布资产地址不安全", err)
	default:
		return appError(CodePluginAssetUnavailable, "暂时无法下载插件发布资产", err)
	}
}

func (s *PluginRepositoryService) installValidationError(pluginID string, repositoryID uint, started time.Time, code string, err error) error {
	s.logPluginInstallFailure(pluginID, repositoryID, started, code)
	return appError(code, "插件安装包校验失败", err)
}

func (s *PluginRepositoryService) logPluginInstallFailure(pluginID string, repositoryID uint, started time.Time, code string) {
	serverlog.OperationPluginRuntime.Event(s.log.Warn()).Str("plugin_id", safeLabel(pluginID, 128)).Uint("repository_id", repositoryID).Str("error_code", safeLabel(code, 96)).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationPluginRuntime.Message("安装包校验失败"))
}

func pluginPreviewNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodePluginPreviewExpired, "插件安装确认不存在或已过期", err)
	}
	return err
}

func pluginInstallationNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "插件尚未安装", err)
	}
	return err
}
