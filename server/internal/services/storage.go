package services

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	"gorm.io/gorm"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type StorageService struct {
	db     *gorm.DB
	audit  *AuditService
	driver storagefs.LocalDriver
}

func NewStorageService(db *gorm.DB, audit *AuditService) *StorageService {
	return &StorageService{db: db, audit: audit, driver: storagefs.LocalDriver{}}
}

type StorageInput struct {
	Name     string
	Type     string
	RootPath string
	Enabled  bool
}

type UpdateStorageInput struct {
	Name     *string
	Type     *string
	RootPath *string
	Enabled  *bool
}

type StorageSummary struct {
	ID           uint                   `json:"id"`
	Name         string                 `json:"name"`
	Type         string                 `json:"type"`
	RootPath     string                 `json:"root_path"`
	ConnectionID *uint                  `json:"connection_id"`
	Enabled      bool                   `json:"enabled"`
	Capabilities storagefs.Capabilities `json:"capabilities"`
	Probe        storagefs.Probe        `json:"probe"`
	CreatedAt    any                    `json:"created_at"`
	UpdatedAt    any                    `json:"updated_at"`
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
		items = append(items, storageSummary(record))
	}
	return items, nil
}

func (s *StorageService) Create(actor Actor, input StorageInput, request RequestContext) (StorageSummary, error) {
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
	record := models.Storage{Name: name, NameNormalized: normalized, Type: storageType, RootPath: root, RootPathNormalized: storagefs.NormalizeForComparison(root), Enabled: input.Enabled, Capabilities: string(capabilities)}
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
	return storageSummary(record), nil
}

func (s *StorageService) Update(actor Actor, id uint, input UpdateStorageInput, request RequestContext) (StorageSummary, error) {
	if !actor.Can(authz.PermissionStoragesUpdate) {
		return StorageSummary{}, appError(CodePermissionDenied, "无权编辑存储", nil)
	}
	var record models.Storage
	if err := s.db.First(&record, id).Error; err != nil {
		return StorageSummary{}, storageNotFound(err)
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
	rootChanged := false
	if input.RootPath != nil {
		root, err := s.driver.CanonicalizeRoot(*input.RootPath)
		if err != nil {
			s.auditFailure(actor, "storage.update", request)
			return StorageSummary{}, storagePathError(err)
		}
		rootChanged = root != record.RootPath
		record.RootPath, record.RootPathNormalized = root, storagefs.NormalizeForComparison(root)
	}
	if input.Enabled != nil {
		record.Enabled = *input.Enabled
	}
	if err := s.ensureUnique(record.ID, record.NameNormalized, record.RootPathNormalized); err != nil {
		s.auditFailure(actor, "storage.update", request)
		return StorageSummary{}, err
	}
	if rootChanged {
		probe := s.driver.ProbeRoot(record.RootPath)
		if !probe.Readable {
			s.auditFailure(actor, "storage.update", request)
			return StorageSummary{}, appError(storagefs.CodeUnreadable, "无法读取存储根路径", nil)
		}
		applyProbe(&record, probe)
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "storage.update", "storage", uintID(record.ID), "success", map[string]any{"type": record.Type, "enabled": record.Enabled}, request)
	})
	if err != nil {
		return StorageSummary{}, err
	}
	return storageSummary(record), nil
}

func (s *StorageService) Test(actor Actor, id uint, request RequestContext) (storagefs.Probe, error) {
	if !actor.Can(authz.PermissionStoragesTest) {
		return storagefs.Probe{}, appError(CodePermissionDenied, "无权测试存储", nil)
	}
	var record models.Storage
	if err := s.db.First(&record, id).Error; err != nil {
		return storagefs.Probe{}, storageNotFound(err)
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

func (s *StorageService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.Can(authz.PermissionStoragesDelete) {
		return appError(CodePermissionDenied, "无权删除存储", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var record models.Storage
		if err := tx.First(&record, id).Error; err != nil {
			return storageNotFound(err)
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
		ID: record.ID, Name: record.Name, Type: record.Type, RootPath: record.RootPath,
		ConnectionID: record.ConnectionID, Enabled: record.Enabled, Capabilities: capabilities,
		Probe:     storagefs.Probe{Exists: record.LastProbeExists, Readable: record.LastProbeReadable, Available: record.LastProbeAvailable, FreeBytes: record.LastProbeFreeBytes, TotalBytes: record.LastProbeTotalBytes, LastCheckedAt: checked, ErrorCode: record.LastProbeErrorCode},
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}
