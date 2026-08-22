package services

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	pluginrepository "github.com/yuanjing-hash/ohmycine/server/internal/plugins/repository"
	"gorm.io/gorm"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const CurrentServerVersion = "0.1.0"

var pluginCommitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type PluginRegistryFetcher interface {
	Fetch(context.Context, contract.GitHubRepository) (pluginrepository.Snapshot, error)
}

type PluginAssetFetcher interface {
	FetchManifest(context.Context, contract.GitHubRepository, string) ([]byte, error)
	FetchPackage(context.Context, contract.GitHubRepository, string) ([]byte, error)
}

type PluginRuntimeHost interface {
	Validate(context.Context, string) error
	Start(context.Context, string, string, uint64) error
	Invoke(context.Context, string, string, []byte) ([]byte, error)
	Stop(string) error
	Close(context.Context) error
}

type PluginServiceOption func(*PluginRepositoryService)

func WithPluginRoot(root string) PluginServiceOption {
	return func(service *PluginRepositoryService) { service.pluginRoot = root }
}

func WithPluginAssetFetcher(fetcher PluginAssetFetcher) PluginServiceOption {
	return func(service *PluginRepositoryService) { service.assets = fetcher }
}

func WithPluginRuntimeHost(host PluginRuntimeHost) PluginServiceOption {
	return func(service *PluginRepositoryService) { service.runtime = host }
}

func WithPluginCredentialStore(store *credential.Store) PluginServiceOption {
	return func(service *PluginRepositoryService) { service.credentials = store }
}

type PluginRepositoryService struct {
	db          *gorm.DB
	audit       *AuditService
	fetcher     PluginRegistryFetcher
	log         zerolog.Logger
	version     string
	assets      PluginAssetFetcher
	runtime     PluginRuntimeHost
	credentials *credential.Store
	pluginRoot  string
	lifecycleMu sync.Mutex
}

type PluginRepositorySummary struct {
	ID              uint       `json:"id"`
	Name            string     `json:"name"`
	GitHubURL       string     `json:"github_url"`
	Enabled         bool       `json:"enabled"`
	Priority        int64      `json:"priority"`
	Revision        uint64     `json:"revision"`
	LastCommitSHA   string     `json:"last_commit_sha"`
	LastRefreshedAt *time.Time `json:"last_refreshed_at"`
	LastErrorCode   string     `json:"last_error_code"`
	RegistryName    string     `json:"registry_name"`
	PluginCount     int        `json:"plugin_count"`
	CacheValid      bool       `json:"cache_valid"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type CreatePluginRepositoryInput struct {
	Name      string
	GitHubURL string
	Enabled   bool
}

type UpdatePluginRepositoryInput struct {
	Name     *string
	Enabled  *bool
	Revision uint64
}

type PluginRepositoryOrderInput struct {
	ID       uint   `json:"id"`
	Revision uint64 `json:"revision"`
}

type PluginMarketplaceSource struct {
	RepositoryID   uint   `json:"repository_id"`
	RepositoryName string `json:"repository_name"`
	RepositoryURL  string `json:"repository_url"`
	Priority       int64  `json:"priority"`
	Version        string `json:"version"`
	Channel        string `json:"channel"`
	Selected       bool   `json:"selected"`
}

type PluginMarketplaceEntry struct {
	ID                   string                    `json:"id"`
	Name                 string                    `json:"name"`
	Description          string                    `json:"description"`
	Version              string                    `json:"version"`
	Channel              string                    `json:"channel"`
	Categories           []string                  `json:"categories"`
	IconURL              string                    `json:"icon_url,omitempty"`
	MinServerVersion     string                    `json:"min_server_version"`
	MaxServerVersion     string                    `json:"max_server_version,omitempty"`
	ReleaseNotes         string                    `json:"release_notes,omitempty"`
	Compatibility        string                    `json:"compatibility"`
	SourceConflict       bool                      `json:"source_conflict"`
	Sources              []PluginMarketplaceSource `json:"sources"`
	PermissionsAvailable bool                      `json:"permissions_available"`
	InstallStatus        string                    `json:"install_status"`
}

type pluginMarketplaceCandidate struct {
	repository models.PluginRepository
	entry      contract.RegistryEntry
}

func NewPluginRepositoryService(db *gorm.DB, audit *AuditService, fetcher PluginRegistryFetcher, log zerolog.Logger, options ...PluginServiceOption) *PluginRepositoryService {
	service := &PluginRepositoryService{
		db: db, audit: audit, fetcher: fetcher, log: log, version: CurrentServerVersion,
		assets: pluginrepository.NewAssetClient(nil),
	}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *PluginRepositoryService) RuntimeAvailable() bool {
	return s.runtime != nil && strings.TrimSpace(s.pluginRoot) != ""
}

func (s *PluginRepositoryService) List(actor Actor) ([]PluginRepositorySummary, error) {
	if !actor.Can(authz.PermissionPluginsRead) {
		return nil, appError(CodePermissionDenied, "无权查看插件仓库", nil)
	}
	return s.list()
}

func (s *PluginRepositoryService) list() ([]PluginRepositorySummary, error) {
	var records []models.PluginRepository
	if err := s.db.Order("priority ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]PluginRepositorySummary, 0, len(records))
	for _, record := range records {
		items = append(items, pluginRepositorySummary(record))
	}
	return items, nil
}

func (s *PluginRepositoryService) Create(actor Actor, input CreatePluginRepositoryInput, request RequestContext) (PluginRepositorySummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return PluginRepositorySummary{}, appError(CodePermissionDenied, "无权添加插件仓库", nil)
	}
	source, err := contract.ParseGitHubRepositoryURL(input.GitHubURL)
	if err != nil {
		return PluginRepositorySummary{}, appError(CodePluginRepositoryURLInvalid, "请填写 GitHub 仓库主页地址", nil)
	}
	source.Owner = strings.ToLower(source.Owner)
	source.Name = strings.ToLower(source.Name)
	name, err := normalizePluginRepositoryName(input.Name, source)
	if err != nil {
		return PluginRepositorySummary{}, err
	}
	now := time.Now().UTC()
	record := models.PluginRepository{
		Name: name, GitHubURL: source.CanonicalURL(), GitHubOwner: source.Owner, GitHubRepo: source.Name,
		Enabled: input.Enabled, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var maximum int64
		if err := tx.Model(&models.PluginRepository{}).Select("COALESCE(MAX(priority), 0)").Scan(&maximum).Error; err != nil {
			return err
		}
		if maximum > math.MaxInt64-1000 {
			return appError(CodeConflict, "插件仓库排序值已用尽", nil)
		}
		record.Priority = maximum + 1000
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_repository.create", "plugin_repository", uintID(record.ID), "success", map[string]any{"github_owner": source.Owner, "github_repo": source.Name, "enabled": record.Enabled}, request)
	})
	if err != nil {
		return PluginRepositorySummary{}, pluginRepositoryConstraintError(err)
	}
	return pluginRepositorySummary(record), nil
}

func (s *PluginRepositoryService) Update(actor Actor, id uint, input UpdatePluginRepositoryInput, request RequestContext) (PluginRepositorySummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return PluginRepositorySummary{}, appError(CodePermissionDenied, "无权修改插件仓库", nil)
	}
	if input.Revision == 0 || input.Revision >= math.MaxInt64 {
		return PluginRepositorySummary{}, appError(CodeInvalidRequest, "插件仓库 revision 无效", nil)
	}
	var existing models.PluginRepository
	if err := s.db.First(&existing, id).Error; err != nil {
		return PluginRepositorySummary{}, pluginRepositoryNotFound(err)
	}
	updates := map[string]any{"revision": input.Revision + 1, "updated_at": time.Now().UTC()}
	if input.Name != nil {
		name, err := normalizePluginRepositoryName(*input.Name, contract.GitHubRepository{Owner: existing.GitHubOwner, Name: existing.GitHubRepo})
		if err != nil {
			return PluginRepositorySummary{}, err
		}
		updates["name"] = name
		existing.Name = name
	}
	if input.Enabled != nil {
		updates["enabled"] = *input.Enabled
		existing.Enabled = *input.Enabled
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PluginRepository{}).Where("id = ? AND revision = ?", id, input.Revision).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodePluginRepositoryRevision, "插件仓库配置已变化，请刷新后重试", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_repository.update", "plugin_repository", uintID(id), "success", map[string]any{"enabled": existing.Enabled}, request)
	})
	if err != nil {
		return PluginRepositorySummary{}, err
	}
	if err := s.db.First(&existing, id).Error; err != nil {
		return PluginRepositorySummary{}, err
	}
	return pluginRepositorySummary(existing), nil
}

func (s *PluginRepositoryService) Delete(actor Actor, id uint, revision uint64, request RequestContext) error {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return appError(CodePermissionDenied, "无权删除插件仓库", nil)
	}
	if revision == 0 {
		return appError(CodeInvalidRequest, "插件仓库 revision 无效", nil)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		var record models.PluginRepository
		if err := tx.First(&record, id).Error; err != nil {
			return pluginRepositoryNotFound(err)
		}
		if record.Revision != revision {
			return appError(CodePluginRepositoryRevision, "插件仓库配置已变化，请刷新后重试", nil)
		}
		result := tx.Where("id = ? AND revision = ?", id, revision).Delete(&models.PluginRepository{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodePluginRepositoryRevision, "插件仓库配置已变化，请刷新后重试", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_repository.delete", "plugin_repository", uintID(id), "success", map[string]any{"github_owner": record.GitHubOwner, "github_repo": record.GitHubRepo}, request)
	})
}

func (s *PluginRepositoryService) Reorder(actor Actor, order []PluginRepositoryOrderInput, request RequestContext) ([]PluginRepositorySummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return nil, appError(CodePermissionDenied, "无权调整插件仓库顺序", nil)
	}
	if len(order) == 0 || len(order) > 200 {
		return nil, appError(CodeInvalidRequest, "插件仓库顺序无效", nil)
	}
	seen := make(map[uint]struct{}, len(order))
	for _, item := range order {
		if item.ID == 0 || item.Revision == 0 {
			return nil, appError(CodeInvalidRequest, "插件仓库顺序无效", nil)
		}
		if _, exists := seen[item.ID]; exists {
			return nil, appError(CodeInvalidRequest, "插件仓库顺序包含重复项", nil)
		}
		seen[item.ID] = struct{}{}
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.PluginRepository{}).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(order)) {
			return appError(CodePluginRepositoryRevision, "插件仓库列表已变化，请刷新后重试", nil)
		}
		for index, item := range order {
			result := tx.Model(&models.PluginRepository{}).Where("id = ? AND revision = ?", item.ID, item.Revision).Updates(map[string]any{"priority": int64(index+1) * 1000, "revision": item.Revision + 1, "updated_at": time.Now().UTC()})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return appError(CodePluginRepositoryRevision, "插件仓库列表已变化，请刷新后重试", nil)
			}
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_repository.reorder", "plugin_repository", "all", "success", map[string]any{"count": len(order)}, request)
	})
	if err != nil {
		return nil, err
	}
	return s.list()
}

func (s *PluginRepositoryService) Refresh(ctx context.Context, actor Actor, id uint, request RequestContext) (PluginRepositorySummary, error) {
	if !actor.Can(authz.PermissionPluginsInstall) {
		return PluginRepositorySummary{}, appError(CodePermissionDenied, "无权刷新插件仓库", nil)
	}
	if s.fetcher == nil {
		return PluginRepositorySummary{}, appError(CodePluginRepositoryUnavailable, "插件仓库刷新服务不可用", nil)
	}
	var record models.PluginRepository
	if err := s.db.First(&record, id).Error; err != nil {
		return PluginRepositorySummary{}, pluginRepositoryNotFound(err)
	}
	started := time.Now()
	serverlog.OperationPluginRepository.Event(s.log.Info()).Uint("repository_id", id).Msg(serverlog.OperationPluginRepository.Message("开始刷新"))
	source := contract.GitHubRepository{Owner: record.GitHubOwner, Name: record.GitHubRepo}
	snapshot, fetchErr := s.fetcher.Fetch(ctx, source)
	var canonical []byte
	if fetchErr == nil {
		if !pluginCommitSHAPattern.MatchString(snapshot.CommitSHA) {
			fetchErr = &pluginrepository.Error{Code: pluginrepository.CodeInvalidSHA, Cause: errors.New("invalid pinned commit")}
		} else if validationErr := snapshot.Registry.Validate(source); validationErr != nil {
			fetchErr = &pluginrepository.Error{Code: pluginrepository.CodeInvalid, Cause: validationErr}
		} else if marshaled, marshalErr := json.Marshal(snapshot.Registry); marshalErr != nil {
			fetchErr = &pluginrepository.Error{Code: pluginrepository.CodeInvalid, Cause: marshalErr}
		} else {
			canonical = marshaled
		}
	}
	if fetchErr != nil {
		code := pluginrepository.ErrorCode(fetchErr)
		if err := s.recordRefreshFailure(actor, record, code, request); err != nil {
			s.logRefreshTerminal(id, started, false, ErrorCode(err), 0)
			return PluginRepositorySummary{}, err
		}
		s.logRefreshTerminal(id, started, false, code, 0)
		return PluginRepositorySummary{}, pluginRepositoryRefreshError(code, fetchErr)
	}
	now := time.Now().UTC()
	err := s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PluginRepository{}).Where("id = ? AND revision = ?", id, record.Revision).Updates(map[string]any{
			"last_commit_sha": snapshot.CommitSHA, "last_refreshed_at": now, "last_error_code": "", "cached_registry_json": string(canonical), "revision": record.Revision + 1, "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodePluginRepositoryRevision, "插件仓库配置已变化，请重新刷新", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_repository.refresh", "plugin_repository", uintID(id), "success", map[string]any{"plugin_count": len(snapshot.Registry.Plugins)}, request)
	})
	if err != nil {
		s.logRefreshTerminal(id, started, false, ErrorCode(err), 0)
		return PluginRepositorySummary{}, err
	}
	if err := s.db.First(&record, id).Error; err != nil {
		s.logRefreshTerminal(id, started, false, ErrorCode(err), 0)
		return PluginRepositorySummary{}, err
	}
	s.logRefreshTerminal(id, started, true, "", len(snapshot.Registry.Plugins))
	return pluginRepositorySummary(record), nil
}

func (s *PluginRepositoryService) Marketplace(actor Actor) ([]PluginMarketplaceEntry, error) {
	if !actor.Can(authz.PermissionPluginsRead) {
		return nil, appError(CodePermissionDenied, "无权查看插件市场", nil)
	}
	var repositories []models.PluginRepository
	if err := s.db.Where("enabled = ? AND cached_registry_json <> ''", true).Order("priority ASC, id ASC").Find(&repositories).Error; err != nil {
		return nil, err
	}
	var installations []models.PluginInstallation
	if err := s.db.Find(&installations).Error; err != nil {
		return nil, err
	}
	installedPackages := make(map[string]models.PluginPackage, len(installations))
	for _, installation := range installations {
		var pluginPackage models.PluginPackage
		if err := s.db.First(&pluginPackage, installation.ActivePackageID).Error; err != nil {
			return nil, err
		}
		installedPackages[installation.PluginID] = pluginPackage
	}
	byPlugin := map[string][]pluginMarketplaceCandidate{}
	for _, repository := range repositories {
		if !pluginCommitSHAPattern.MatchString(repository.LastCommitSHA) {
			continue
		}
		source := contract.GitHubRepository{Owner: repository.GitHubOwner, Name: repository.GitHubRepo}
		registry, err := contract.ParseRegistry([]byte(repository.CachedRegistryJSON), source)
		if err != nil {
			continue
		}
		best := map[string]contract.RegistryEntry{}
		for _, entry := range registry.Plugins {
			current, exists := best[entry.ID]
			if !exists || betterRegistryEntry(entry, current) {
				best[entry.ID] = entry
			}
		}
		for _, entry := range best {
			byPlugin[entry.ID] = append(byPlugin[entry.ID], pluginMarketplaceCandidate{repository: repository, entry: entry})
		}
	}
	items := make([]PluginMarketplaceEntry, 0, len(byPlugin))
	for _, candidates := range byPlugin {
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].repository.Priority != candidates[j].repository.Priority {
				return candidates[i].repository.Priority < candidates[j].repository.Priority
			}
			return candidates[i].repository.ID < candidates[j].repository.ID
		})
		selected := candidates[0]
		sources := make([]PluginMarketplaceSource, 0, len(candidates))
		for index, candidate := range candidates {
			sources = append(sources, PluginMarketplaceSource{RepositoryID: candidate.repository.ID, RepositoryName: candidate.repository.Name, RepositoryURL: candidate.repository.GitHubURL, Priority: candidate.repository.Priority, Version: candidate.entry.Version, Channel: candidate.entry.Channel, Selected: index == 0})
		}
		entry := selected.entry
		entryCompatibility := compatibility(s.version, entry)
		installStatus := "available"
		if entryCompatibility != "compatible" {
			installStatus = "incompatible"
		} else if installedPackage, ok := installedPackages[entry.ID]; ok {
			installStatus = "installed"
			comparison, _ := contract.CompareVersions(entry.Version, installedPackage.Version)
			if comparison > 0 && strings.EqualFold(installedPackage.RepositoryOwner, selected.repository.GitHubOwner) && strings.EqualFold(installedPackage.RepositoryRepo, selected.repository.GitHubRepo) {
				installStatus = "update_available"
			}
		}
		items = append(items, PluginMarketplaceEntry{
			ID: entry.ID, Name: entry.Name, Description: entry.Description, Version: entry.Version, Channel: entry.Channel,
			Categories: append([]string(nil), entry.Categories...), IconURL: entry.IconURL, MinServerVersion: entry.MinServerVersion,
			MaxServerVersion: entry.MaxServerVersion, ReleaseNotes: entry.ReleaseNotes, Compatibility: entryCompatibility,
			SourceConflict: len(candidates) > 1, Sources: sources, PermissionsAvailable: s.RuntimeAvailable(), InstallStatus: installStatus,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if left != right {
			return left < right
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (s *PluginRepositoryService) recordRefreshFailure(actor Actor, record models.PluginRepository, code string, request RequestContext) error {
	now := time.Now().UTC()
	return s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.PluginRepository{}).Where("id = ? AND revision = ?", record.ID, record.Revision).Updates(map[string]any{"last_error_code": safeLabel(code, 96), "revision": record.Revision + 1, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return appError(CodePluginRepositoryRevision, "插件仓库配置已变化，请重新刷新", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "plugin_repository.refresh", "plugin_repository", uintID(record.ID), "failure", map[string]any{"error_code": code}, request)
	})
}

func (s *PluginRepositoryService) logRefreshTerminal(repositoryID uint, started time.Time, success bool, code string, pluginCount int) {
	if success {
		serverlog.OperationPluginRepository.Event(s.log.Info()).Uint("repository_id", repositoryID).Int("plugin_count", pluginCount).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationPluginRepository.Message("刷新完成"))
		return
	}
	if strings.TrimSpace(code) == "" {
		code = "INTERNAL_ERROR"
	}
	serverlog.OperationPluginRepository.Event(s.log.Warn()).Uint("repository_id", repositoryID).Str("error_code", safeLabel(code, 96)).Int64("duration_ms", time.Since(started).Milliseconds()).Msg(serverlog.OperationPluginRepository.Message("刷新失败"))
}

func pluginRepositorySummary(record models.PluginRepository) PluginRepositorySummary {
	summary := PluginRepositorySummary{ID: record.ID, Name: record.Name, GitHubURL: record.GitHubURL, Enabled: record.Enabled, Priority: record.Priority, Revision: record.Revision, LastCommitSHA: record.LastCommitSHA, LastRefreshedAt: record.LastRefreshedAt, LastErrorCode: record.LastErrorCode, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	if record.CachedRegistryJSON == "" || !pluginCommitSHAPattern.MatchString(record.LastCommitSHA) {
		return summary
	}
	registry, err := contract.ParseRegistry([]byte(record.CachedRegistryJSON), contract.GitHubRepository{Owner: record.GitHubOwner, Name: record.GitHubRepo})
	if err == nil {
		summary.RegistryName = registry.Repository.Name
		summary.PluginCount = len(registry.Plugins)
		summary.CacheValid = true
	}
	return summary
}

func normalizePluginRepositoryName(input string, source contract.GitHubRepository) (string, error) {
	name := strings.Join(strings.Fields(input), " ")
	if name == "" {
		name = source.Owner + "/" + source.Name
	}
	if len([]rune(name)) > 128 {
		return "", appError(CodeInvalidRequest, "插件仓库名称过长", nil)
	}
	return name, nil
}

func pluginRepositoryNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "插件仓库不存在", err)
	}
	return err
}

func pluginRepositoryConstraintError(err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return appError(CodePluginRepositoryConflict, "该 GitHub 插件仓库已添加", err)
	}
	return err
}

func pluginRepositoryRefreshError(code string, cause error) error {
	switch code {
	case pluginrepository.CodeRateLimited:
		return appError(CodePluginRegistryRateLimited, "GitHub 请求受到限速，已保留上次成功缓存", cause)
	case pluginrepository.CodeTooLarge:
		return appError(CodePluginRegistryTooLarge, "插件仓库索引过大，已保留上次成功缓存", cause)
	case pluginrepository.CodeInvalid, pluginrepository.CodeInvalidSHA:
		return appError(CodePluginRegistryInvalid, "插件仓库索引校验失败，已保留上次成功缓存", cause)
	default:
		return appError(CodePluginRepositoryUnavailable, "暂时无法刷新插件仓库，已保留上次成功缓存", cause)
	}
}

func betterRegistryEntry(candidate, current contract.RegistryEntry) bool {
	if candidate.Channel != current.Channel {
		return candidate.Channel == "stable"
	}
	comparison, _ := contract.CompareVersions(candidate.Version, current.Version)
	return comparison > 0
}

func compatibility(serverVersion string, entry contract.RegistryEntry) string {
	minimumComparison, minimumErr := contract.CompareVersions(serverVersion, entry.MinServerVersion)
	if minimumErr != nil || minimumComparison < 0 {
		return "server_too_old"
	}
	if entry.MaxServerVersion != "" {
		maximumComparison, maximumErr := contract.CompareVersions(serverVersion, entry.MaxServerVersion)
		if maximumErr != nil {
			return "server_too_old"
		}
		if maximumComparison > 0 {
			return "server_too_new"
		}
	}
	return "compatible"
}
