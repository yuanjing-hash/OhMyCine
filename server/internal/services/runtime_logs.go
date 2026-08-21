package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"gorm.io/gorm"
)

type RuntimeLogService struct {
	db      *gorm.DB
	manager *logging.Manager
	audit   *AuditService
}

type RuntimeLogSettings struct {
	logging.Policy
	Revision uint64         `json:"revision"`
	Health   logging.Health `json:"health"`
}

func NewRuntimeLogService(db *gorm.DB, manager *logging.Manager, audit *AuditService) (*RuntimeLogService, error) {
	s := &RuntimeLogService{db: db, manager: manager, audit: audit}
	var model models.RuntimeLogPolicy
	if err := db.First(&model, 1).Error; err != nil {
		return nil, fmt.Errorf("load runtime log policy: %w", err)
	}
	policy := policyFromModel(model)
	if err := manager.Apply(policy); err != nil {
		return nil, fmt.Errorf("apply runtime log policy: %w", err)
	}
	return s, nil
}

func (s *RuntimeLogService) Query(ctx context.Context, actor Actor, filter logging.Filter) (logging.QueryResult, error) {
	if !actor.Can(authz.PermissionLogsRead) {
		return logging.QueryResult{}, appError(CodePermissionDenied, "没有查看运行日志的权限", nil)
	}
	queryContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := s.manager.Query(queryContext, filter)
	if err != nil {
		return logging.QueryResult{}, appError(CodeRuntimeLogFilterInvalid, "运行日志筛选条件无效", err)
	}
	return result, nil
}

func (s *RuntimeLogService) Facets(ctx context.Context, actor Actor, filter logging.Filter) (logging.Facets, error) {
	if !actor.Can(authz.PermissionLogsRead) {
		return logging.Facets{}, appError(CodePermissionDenied, "没有查看运行日志的权限", nil)
	}
	queryContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := s.manager.Facets(queryContext, filter)
	if err != nil {
		return logging.Facets{}, appError(CodeRuntimeLogFilterInvalid, "运行日志筛选条件无效", err)
	}
	return result, nil
}

func (s *RuntimeLogService) Settings(actor Actor) (RuntimeLogSettings, error) {
	if !actor.Can(authz.PermissionLogsRead) {
		return RuntimeLogSettings{}, appError(CodePermissionDenied, "没有查看运行日志设置的权限", nil)
	}
	var model models.RuntimeLogPolicy
	if err := s.db.First(&model, 1).Error; err != nil {
		return RuntimeLogSettings{}, appError(CodeRuntimeLogUnavailable, "运行日志设置暂不可用", err)
	}
	return RuntimeLogSettings{Policy: policyFromModel(model), Revision: model.Revision, Health: s.manager.Health()}, nil
}

func (s *RuntimeLogService) UpdateSettings(actor Actor, policy logging.Policy, revision uint64, request RequestContext) (RuntimeLogSettings, error) {
	if !actor.Can(authz.PermissionLogsConfigure) {
		return RuntimeLogSettings{}, appError(CodePermissionDenied, "没有修改运行日志设置的权限", nil)
	}
	if err := policy.Validate(); err != nil {
		return RuntimeLogSettings{}, appError(CodeRuntimeLogPolicyInvalid, "运行日志设置无效", err)
	}
	var updated models.RuntimeLogPolicy
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var current models.RuntimeLogPolicy
		if err := tx.First(&current, 1).Error; err != nil {
			return err
		}
		if current.Revision != revision {
			return appError(CodeConflict, "运行日志设置已被其他管理员修改，请刷新后重试", nil)
		}
		old := policyFromModel(current)
		result := tx.Model(&models.RuntimeLogPolicy{}).Where("id = ? AND revision = ?", 1, revision).Updates(map[string]any{
			"level": policy.Level, "max_file_mi_b": policy.MaxFileMiB, "max_backups": policy.MaxBackups,
			"retention_days": policy.RetentionDays, "max_total_mi_b": policy.MaxTotalMiB, "revision": revision + 1, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodeConflict, "运行日志设置已被其他管理员修改，请刷新后重试", nil)
		}
		if err := s.audit.Record(tx, &actor.User.ID, "logs.settings_update", "runtime_log_policy", "1", "success", map[string]any{"from": old, "to": policy}, request); err != nil {
			return err
		}
		return tx.First(&updated, 1).Error
	})
	if err != nil {
		return RuntimeLogSettings{}, err
	}
	if err := s.manager.Apply(policy); err != nil {
		return RuntimeLogSettings{}, appError(CodeRuntimeLogUnavailable, "设置已保存，将在服务重启后重新应用", err)
	}
	return RuntimeLogSettings{Policy: policyFromModel(updated), Revision: updated.Revision, Health: s.manager.Health()}, nil
}

func (s *RuntimeLogService) Export(ctx context.Context, actor Actor, filter logging.Filter, request RequestContext) ([]byte, int, error) {
	if !actor.Can(authz.PermissionLogsExport) {
		return nil, 0, appError(CodePermissionDenied, "没有导出运行日志的权限", nil)
	}
	exportContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	filter.Limit = logging.MaxQueryLimit
	filter.Cursor = ""
	var plain bytes.Buffer
	count := 0
	const maxExportPlainBytes = 32 * 1024 * 1024
	for count < 5000 {
		result, err := s.manager.Query(exportContext, filter)
		if err != nil {
			return nil, 0, err
		}
		for _, entry := range result.List {
			line, _ := json.Marshal(entry)
			if plain.Len()+len(line)+1 > maxExportPlainBytes {
				break
			}
			plain.Write(line)
			plain.WriteByte('\n')
			count++
		}
		if result.NextCursor == "" || count >= 5000 || plain.Len() >= maxExportPlainBytes {
			break
		}
		filter.Cursor = result.NextCursor
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(plain.Bytes()); err != nil {
		return nil, 0, appError(CodeRuntimeLogUnavailable, "运行日志导出失败", err)
	}
	if err := zw.Close(); err != nil {
		return nil, 0, appError(CodeRuntimeLogUnavailable, "运行日志导出失败", err)
	}
	if err := s.audit.Record(nil, &actor.User.ID, "logs.export", "runtime_logs", "filtered", "success", map[string]any{"result_count": count, "output_bytes": compressed.Len(), "from": filter.From, "to": filter.To, "levels": filter.Levels, "modules": filter.Modules, "components": filter.Components, "operations": filter.Operations, "plugin_ids": filter.PluginIDs}, request); err != nil {
		return nil, 0, err
	}
	return compressed.Bytes(), count, nil
}

func policyFromModel(model models.RuntimeLogPolicy) logging.Policy {
	return logging.Policy{Level: model.Level, MaxFileMiB: model.MaxFileMiB, MaxBackups: model.MaxBackups, RetentionDays: model.RetentionDays, MaxTotalMiB: model.MaxTotalMiB}
}
