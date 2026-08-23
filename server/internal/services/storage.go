package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"gorm.io/gorm"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type StorageService struct {
	db          *gorm.DB
	audit       *AuditService
	driver      storagefs.LocalDriver
	references  []StorageReferenceChecker
	connections *ConnectionService
}

type StorageReferenceChecker interface {
	StorageReferences(storageID uint) ([]string, error)
}

func NewStorageService(db *gorm.DB, audit *AuditService) *StorageService {
	return &StorageService{db: db, audit: audit, driver: storagefs.LocalDriver{}}
}
func (s *StorageService) SetConnectionService(connections *ConnectionService) {
	s.connections = connections
}
func (s *StorageService) SetReferenceChecker(references StorageReferenceChecker) {
	s.references = []StorageReferenceChecker{references}
}
func (s *StorageService) AddReferenceChecker(references StorageReferenceChecker) {
	if references != nil {
		s.references = append(s.references, references)
	}
}

type StorageInput struct {
	Name            string
	Type            string
	RootPath        string
	RootDisplayPath string
	ConnectionID    *uint
	Enabled         bool
}

type UpdateStorageInput struct {
	Name            *string
	Type            *string
	RootPath        *string
	RootDisplayPath *string
	ConnectionID    *uint
	Enabled         *bool
}

type StorageSummary struct {
	ID              uint                   `json:"id"`
	Name            string                 `json:"name"`
	Type            string                 `json:"type"`
	RootPath        string                 `json:"root_path"`
	RootDisplayPath string                 `json:"root_display_path"`
	ConnectionID    *uint                  `json:"connection_id"`
	Enabled         bool                   `json:"enabled"`
	Capabilities    storagefs.Capabilities `json:"capabilities"`
	Probe           storagefs.Probe        `json:"probe"`
	CreatedAt       any                    `json:"created_at"`
	UpdatedAt       any                    `json:"updated_at"`
}

func (s *StorageService) List(actor Actor) ([]StorageSummary, error) {
	if !actor.Can(authz.PermissionStoragesRead) {
		return nil, appError(CodePermissionDenied, "无权查看存储", nil)
	}
	var records []models.Storage
	if err := s.db.Order("name_normalized, id").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]StorageSummary, 0, len(records))
	for _, record := range records {
		items = append(items, s.storageSummary(record))
	}
	return items, nil
}

func (s *StorageService) Get(actor Actor, id uint) (StorageSummary, error) {
	if !actor.Can(authz.PermissionStoragesRead) {
		return StorageSummary{}, appError(CodePermissionDenied, "无权查看存储", nil)
	}
	var record models.Storage
	if err := s.db.First(&record, id).Error; err != nil {
		return StorageSummary{}, storageNotFound(err)
	}
	return s.storageSummary(record), nil
}

func (s *StorageService) Create(actor Actor, input StorageInput, request RequestContext) (StorageSummary, error) {
	return s.CreateContext(context.Background(), actor, input, request)
}

func (s *StorageService) CreateContext(ctx context.Context, actor Actor, input StorageInput, request RequestContext) (StorageSummary, error) {
	if !actor.Can(authz.PermissionStoragesCreate) {
		return StorageSummary{}, appError(CodePermissionDenied, "无权创建存储", nil)
	}
	name, normalized, err := normalizeStorageName(input.Name)
	if err != nil {
		s.auditFailure(actor, "storage.create", request)
		return StorageSummary{}, err
	}
	storageType := strings.TrimSpace(input.Type)
	if storageType == "" {
		storageType = models.StorageTypeLocal
	}
	if storageType == models.StorageTypePan115 {
		return s.createPan115(ctx, actor, input, name, normalized, request)
	}
	if storageType != models.StorageTypeLocal {
		s.auditFailure(actor, "storage.create", request)
		return StorageSummary{}, appError(CodeStorageTypeUnsupported, "当前仅支持本地存储", nil)
	}
	root, err := s.driver.CanonicalizeRoot(input.RootPath)
	if err != nil {
		s.auditFailure(actor, "storage.create", request)
		return StorageSummary{}, storagePathError(err)
	}
	if err := s.ensureUnique(0, normalized, storagefs.NormalizeForComparison(root)); err != nil {
		s.auditFailure(actor, "storage.create", request)
		return StorageSummary{}, err
	}
	probe := s.driver.ProbeRoot(root)
	if !probe.Readable {
		s.auditFailure(actor, "storage.create", request)
		return StorageSummary{}, appError(storagefs.CodeUnreadable, "无法读取存储根路径", nil)
	}
	capabilities, err := json.Marshal(s.driver.Capabilities())
	if err != nil {
		if conflict := storageConstraintError(err); conflict != nil {
			return StorageSummary{}, conflict
		}
		return StorageSummary{}, err
	}
	record := models.Storage{Name: name, NameNormalized: normalized, Type: storageType, RootPath: root, RootDisplayPath: root, RootPathNormalized: storagefs.NormalizeForComparison(root), Enabled: input.Enabled, Capabilities: string(capabilities)}
	applyProbe(&record, probe)
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "storage.create", "storage", uintID(record.ID), "success", map[string]any{"type": record.Type, "enabled": record.Enabled}, request)
	})
	if err != nil {
		if conflict := storageConstraintError(err); conflict != nil {
			return StorageSummary{}, conflict
		}
		return StorageSummary{}, err
	}
	return s.storageSummary(record), nil
}

func (s *StorageService) Update(actor Actor, id uint, input UpdateStorageInput, request RequestContext) (StorageSummary, error) {
	return s.UpdateContext(context.Background(), actor, id, input, request)
}

func (s *StorageService) UpdateContext(ctx context.Context, actor Actor, id uint, input UpdateStorageInput, request RequestContext) (StorageSummary, error) {
	if !actor.Can(authz.PermissionStoragesUpdate) {
		return StorageSummary{}, appError(CodePermissionDenied, "无权编辑存储", nil)
	}
	var record models.Storage
	if err := s.db.First(&record, id).Error; err != nil {
		return StorageSummary{}, storageNotFound(err)
	}
	if record.Type == models.StorageTypePan115 {
		return s.updatePan115(ctx, actor, record, input, request)
	}
	if input.Type != nil && strings.TrimSpace(*input.Type) != models.StorageTypeLocal {
		s.auditFailure(actor, "storage.update", request)
		return StorageSummary{}, appError(CodeStorageTypeUnsupported, "当前仅支持本地存储", nil)
	}
	if input.Name != nil {
		name, normalized, err := normalizeStorageName(*input.Name)
		if err != nil {
			s.auditFailure(actor, "storage.update", request)
			return StorageSummary{}, err
		}
		record.Name, record.NameNormalized = name, normalized
	}
	if input.RootPath != nil {
		root, err := s.driver.CanonicalizeRoot(*input.RootPath)
		if err != nil {
			s.auditFailure(actor, "storage.update", request)
			return StorageSummary{}, storagePathError(err)
		}
		record.RootPath, record.RootDisplayPath, record.RootPathNormalized = root, root, storagefs.NormalizeForComparison(root)
	}
	if input.Enabled != nil {
		record.Enabled = *input.Enabled
	}
	if err := s.ensureUnique(record.ID, record.NameNormalized, record.RootPathNormalized); err != nil {
		s.auditFailure(actor, "storage.update", request)
		return StorageSummary{}, err
	}
	root, err := s.driver.CanonicalizeRoot(record.RootPath)
	if err != nil {
		s.auditFailure(actor, "storage.update", request)
		return StorageSummary{}, storagePathError(err)
	}
	record.RootPath, record.RootPathNormalized = root, storagefs.NormalizeForComparison(root)
	probe := s.driver.ProbeRoot(record.RootPath)
	if !probe.Readable {
		s.auditFailure(actor, "storage.update", request)
		return StorageSummary{}, appError(storagefs.CodeUnreadable, "无法读取存储根路径", nil)
	}
	applyProbe(&record, probe)
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "storage.update", "storage", uintID(record.ID), "success", map[string]any{"type": record.Type, "enabled": record.Enabled}, request)
	})
	if err != nil {
		return StorageSummary{}, err
	}
	return s.storageSummary(record), nil
}

func (s *StorageService) Test(actor Actor, id uint, request RequestContext) (storagefs.Probe, error) {
	return s.TestContext(context.Background(), actor, id, request)
}

func (s *StorageService) TestContext(ctx context.Context, actor Actor, id uint, request RequestContext) (storagefs.Probe, error) {
	if !actor.Can(authz.PermissionStoragesTest) {
		return storagefs.Probe{}, appError(CodePermissionDenied, "无权测试存储", nil)
	}
	var record models.Storage
	if err := s.db.First(&record, id).Error; err != nil {
		return storagefs.Probe{}, storageNotFound(err)
	}
	if record.Type == models.StorageTypePan115 {
		probe, err := s.probePan115(ctx, actor, &record)
		if saveErr := s.saveProbe(actor, &record, probe, request); saveErr != nil {
			return storagefs.Probe{}, saveErr
		}
		return probe, err
	}
	probe := s.driver.ProbeRoot(record.RootPath)
	applyProbe(&record, probe)
	outcome := "success"
	if probe.ErrorCode != "" {
		outcome = "failure"
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "storage.test", "storage", uintID(record.ID), outcome, map[string]any{"error_code": probe.ErrorCode}, request)
	})
	if err != nil {
		return storagefs.Probe{}, err
	}
	return probe, nil
}

func (s *StorageService) createPan115(ctx context.Context, actor Actor, input StorageInput, name, normalized string, request RequestContext) (StorageSummary, error) {
	if s.connections == nil || input.ConnectionID == nil || *input.ConnectionID == 0 || strings.TrimSpace(input.RootPath) == "" || !strings.HasPrefix(input.RootDisplayPath, "/") {
		return StorageSummary{}, appError(CodeInvalidRequest, "请选择 115 账号和网盘目录", nil)
	}
	connection, driver, err := s.connections.Driver(actor, *input.ConnectionID)
	if err != nil {
		return StorageSummary{}, err
	}
	item, err := driver.Stat(ctx, input.RootPath)
	if err != nil || !item.IsDir {
		return StorageSummary{}, providerDirectoryError(err)
	}
	normalizedRoot := fmt.Sprintf("pan115:%d:%s", connection.ID, item.ID)
	if err := s.ensureUnique(0, normalized, normalizedRoot); err != nil {
		return StorageSummary{}, err
	}
	capabilities, err := json.Marshal(cloudStorageCapabilities(driver.Capabilities()))
	if err != nil {
		return StorageSummary{}, err
	}
	record := models.Storage{Name: name, NameNormalized: normalized, Type: models.StorageTypePan115, RootPath: item.ID, RootDisplayPath: input.RootDisplayPath, RootPathNormalized: normalizedRoot, ConnectionID: &connection.ID, Enabled: input.Enabled, Capabilities: string(capabilities)}
	applyProbe(&record, cloudStorageProbe(connection, ""))
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "storage.create", "storage", uintID(record.ID), "success", map[string]any{"type": record.Type, "connection_id": connection.ID, "enabled": record.Enabled}, request)
	}); err != nil {
		if conflict := storageConstraintError(err); conflict != nil {
			return StorageSummary{}, conflict
		}
		return StorageSummary{}, err
	}
	return s.storageSummary(record), nil
}

func (s *StorageService) updatePan115(ctx context.Context, actor Actor, record models.Storage, input UpdateStorageInput, request RequestContext) (StorageSummary, error) {
	if input.Type != nil && strings.TrimSpace(*input.Type) != models.StorageTypePan115 {
		return StorageSummary{}, appError(CodeStorageTypeUnsupported, "不能修改数据源类型", nil)
	}
	if input.Name != nil {
		name, normalized, err := normalizeStorageName(*input.Name)
		if err != nil {
			return StorageSummary{}, err
		}
		record.Name, record.NameNormalized = name, normalized
	}
	if input.Enabled != nil {
		record.Enabled = *input.Enabled
	}
	if input.ConnectionID != nil {
		record.ConnectionID = input.ConnectionID
	}
	if input.RootPath != nil {
		record.RootPath = *input.RootPath
	}
	if input.RootDisplayPath != nil {
		record.RootDisplayPath = *input.RootDisplayPath
	}
	if record.ConnectionID == nil || !strings.HasPrefix(record.RootDisplayPath, "/") {
		return StorageSummary{}, appError(CodeInvalidRequest, "请选择 115 账号和网盘目录", nil)
	}
	connection, driver, err := s.connections.driver(*record.ConnectionID)
	if err != nil {
		return StorageSummary{}, err
	}
	item, err := driver.Stat(ctx, record.RootPath)
	if err != nil || !item.IsDir {
		return StorageSummary{}, providerDirectoryError(err)
	}
	record.RootPath, record.RootPathNormalized = item.ID, fmt.Sprintf("pan115:%d:%s", connection.ID, item.ID)
	if err := s.ensureUnique(record.ID, record.NameNormalized, record.RootPathNormalized); err != nil {
		return StorageSummary{}, err
	}
	capabilities, _ := json.Marshal(cloudStorageCapabilities(driver.Capabilities()))
	record.Capabilities = string(capabilities)
	applyProbe(&record, cloudStorageProbe(connection, ""))
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "storage.update", "storage", uintID(record.ID), "success", map[string]any{"type": record.Type, "connection_id": connection.ID, "enabled": record.Enabled}, request)
	}); err != nil {
		return StorageSummary{}, err
	}
	return s.storageSummary(record), nil
}

func (s *StorageService) probePan115(ctx context.Context, _ Actor, record *models.Storage) (storagefs.Probe, error) {
	if s.connections == nil || record.ConnectionID == nil {
		return cloudStorageProbe(models.Connection{}, CodeConnectionUnavailable), appError(CodeConnectionUnavailable, "115 连接不可用", nil)
	}
	connection, driver, err := s.connections.driver(*record.ConnectionID)
	if err != nil {
		return cloudStorageProbe(connection, CodeConnectionUnavailable), err
	}
	item, err := driver.Stat(ctx, record.RootPath)
	if err != nil || !item.IsDir {
		return cloudStorageProbe(connection, cloudpkg.CodeNotFound), providerDirectoryError(err)
	}
	return cloudStorageProbe(connection, ""), nil
}

func (s *StorageService) saveProbe(actor Actor, record *models.Storage, probe storagefs.Probe, request RequestContext) error {
	applyProbe(record, probe)
	outcome := "success"
	if probe.ErrorCode != "" {
		outcome = "failure"
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "storage.test", "storage", uintID(record.ID), outcome, map[string]any{"error_code": probe.ErrorCode}, request)
	})
}

func cloudStorageCapabilities(value cloudpkg.Capabilities) storagefs.Capabilities {
	return storagefs.Capabilities{NetworkDrive: value.NetworkDrive, DirectoryList: value.DirectoryList, Watch: value.Watch, NativeOfflineDownload: value.NativeOfflineDownload, TemporaryDirectURL: value.TemporaryDirectURL, SignedProxy: value.SignedProxy, SmallFileUpload: value.SmallFileUpload, FileUpload: value.FileUpload, ChangeCursor: value.ChangeCursor}
}
func cloudStorageProbe(connection models.Connection, errorCode string) storagefs.Probe {
	var free *uint64
	if connection.QuotaTotalBytes != nil && connection.QuotaUsedBytes != nil && *connection.QuotaTotalBytes >= *connection.QuotaUsedBytes {
		value := *connection.QuotaTotalBytes - *connection.QuotaUsedBytes
		free = &value
	}
	return storagefs.Probe{Exists: errorCode == "", Readable: errorCode == "", Available: errorCode == "", FreeBytes: free, TotalBytes: connection.QuotaTotalBytes, LastCheckedAt: time.Now().UTC(), ErrorCode: errorCode}
}

func (s *StorageService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.Can(authz.PermissionStoragesDelete) {
		return appError(CodePermissionDenied, "无权删除存储", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var record models.Storage
		if err := tx.First(&record, id).Error; err != nil {
			return storageNotFound(err)
		}
		for _, checker := range s.references {
			references, err := checker.StorageReferences(id)
			if err != nil {
				return err
			}
			if len(references) > 0 {
				return appError(CodeConflict, "存储正在被媒体库或系统设置使用", nil)
			}
		}
		if err := tx.Delete(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "storage.delete", "storage", uintID(record.ID), "success", map[string]any{"type": record.Type}, request)
	})
}

func (s *StorageService) ensureUnique(id uint, name, root string) error {
	var count int64
	query := s.db.Model(&models.Storage{}).Where("name_normalized = ?", name)
	if id != 0 {
		query = query.Where("id <> ?", id)
	}
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return appError(CodeStorageNameConflict, "存储名称已存在", nil)
	}
	query = s.db.Model(&models.Storage{}).Where("root_path_normalized = ?", root)
	if id != 0 {
		query = query.Where("id <> ?", id)
	}
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return appError(CodeStoragePathConflict, "该根路径已注册", nil)
	}
	return nil
}

func normalizeStorageName(input string) (string, string, error) {
	name := strings.Join(strings.Fields(input), " ")
	if name == "" {
		return "", "", appError(CodeStorageNameRequired, "请填写存储名称", nil)
	}
	if len([]rune(name)) > 128 {
		return "", "", appError(CodeInvalidRequest, "存储名称过长", nil)
	}
	return name, strings.ToLower(name), nil
}

func storageConstraintError(err error) error {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return nil
	}
	message := sqliteErr.Error()
	switch {
	case strings.Contains(message, "storages.name_normalized"):
		return appError(CodeStorageNameConflict, "存储名称已存在", err)
	case strings.Contains(message, "storages.root_path_normalized"):
		return appError(CodeStoragePathConflict, "该根路径已注册", err)
	default:
		return appError(CodeConflict, "存储配置冲突", err)
	}
}

func storagePathError(err error) error {
	var pathErr *storagefs.PathError
	if errors.As(err, &pathErr) {
		return appError(pathErr.Code, safePathMessage(pathErr.Code), err)
	}
	return appError(storagefs.CodeUnreadable, "无法读取存储根路径", err)
}

func safePathMessage(code string) string {
	switch code {
	case storagefs.CodePathNotAbsolute:
		return "存储根路径必须是绝对路径"
	case storagefs.CodePathNotFound:
		return "存储根路径不存在"
	case storagefs.CodePathNotDirectory:
		return "存储根路径必须是目录"
	case storagefs.CodePathReparsePoint:
		return "存储根路径不能是 Reparse Point 或符号链接"
	default:
		return "无法读取存储根路径"
	}
}

func storageNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "存储不存在", err)
	}
	return err
}

func (s *StorageService) auditFailure(actor Actor, action string, request RequestContext) {
	_ = s.audit.Record(nil, &actor.User.ID, action, "storage", "", "failure", map[string]any{}, request)
}

func applyProbe(record *models.Storage, probe storagefs.Probe) {
	record.LastProbeExists = probe.Exists
	record.LastProbeReadable = probe.Readable
	record.LastProbeAvailable = probe.Available
	record.LastProbeFreeBytes = probe.FreeBytes
	record.LastProbeTotalBytes = probe.TotalBytes
	record.LastProbeErrorCode = probe.ErrorCode
	record.LastProbeCheckedAt = &probe.LastCheckedAt
}

func storageSummary(record models.Storage) StorageSummary {
	var capabilities storagefs.Capabilities
	_ = json.Unmarshal([]byte(record.Capabilities), &capabilities)
	checked := record.UpdatedAt
	if record.LastProbeCheckedAt != nil {
		checked = *record.LastProbeCheckedAt
	}
	return StorageSummary{
		ID: record.ID, Name: record.Name, Type: record.Type, RootPath: record.RootPath, RootDisplayPath: record.RootDisplayPath,
		ConnectionID: record.ConnectionID, Enabled: record.Enabled, Capabilities: capabilities,
		Probe:     storagefs.Probe{Exists: record.LastProbeExists, Readable: record.LastProbeReadable, Available: record.LastProbeAvailable, FreeBytes: record.LastProbeFreeBytes, TotalBytes: record.LastProbeTotalBytes, LastCheckedAt: checked, ErrorCode: record.LastProbeErrorCode},
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

// storageSummary overlays the persisted capability snapshot with the current
// connection driver capabilities. Snapshots deliberately preserve what was
// known when a Storage was saved, but a Server upgrade can add a capability
// (for example 115 native offline downloads) without requiring administrators
// to recreate every existing Storage.
func (s *StorageService) storageSummary(record models.Storage) StorageSummary {
	summary := storageSummary(record)
	if record.Type != models.StorageTypePan115 || record.ConnectionID == nil || s.connections == nil {
		return summary
	}
	if _, driver, err := s.connections.driver(*record.ConnectionID); err == nil {
		summary.Capabilities = cloudStorageCapabilities(driver.Capabilities())
		if encoded, marshalErr := json.Marshal(summary.Capabilities); marshalErr == nil && string(encoded) != record.Capabilities {
			// Capabilities are a derived adapter snapshot, not administrator
			// configuration. Materialize upgrades without changing UpdatedAt or
			// requiring the Storage to be recreated.
			_ = s.db.Model(&models.Storage{}).Where("id = ? AND capabilities <> ?", record.ID, string(encoded)).UpdateColumn("capabilities", string(encoded)).Error
		}
	}
	return summary
}
