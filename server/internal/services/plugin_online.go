package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"gorm.io/gorm"
)

const (
	maxOnlineIdentifierBytes = 512
	maxOnlineQueryBytes      = 512
	maxOnlineHistorySources  = 8
	pluginHistoryExhausted   = "!"
)

type PluginOnlineLibrarySummary struct {
	ID                string                `json:"id"`
	PluginID          string                `json:"pluginId"`
	ConnectionID      string                `json:"connectionId"`
	Name              string                `json:"name"`
	ProviderLabel     string                `json:"providerLabel"`
	Capabilities      []contract.Capability `json:"capabilities"`
	Available         bool                  `json:"available"`
	ErrorCode         string                `json:"errorCode,omitempty"`
	HomeContributions []string              `json:"homeContributions"`
}

type PluginOnlineHistoryPage struct {
	List    []json.RawMessage `json:"list"`
	Cursor  string            `json:"cursor,omitempty"`
	HasMore bool              `json:"hasMore"`
}

type pluginHistoryCursor struct {
	Sources map[string]string `json:"sources"`
}

type pluginHistoryResponse struct {
	List    []json.RawMessage `json:"list"`
	Cursor  string            `json:"cursor"`
	HasMore bool              `json:"hasMore"`
}

type pluginErrorEnvelope struct {
	PluginError *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"pluginError"`
}

func (s *PluginRepositoryService) OnlineLibraries(actor Actor) ([]PluginOnlineLibrarySummary, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看在线媒体库", nil)
	}
	connections, err := s.enabledOnlineConnections("")
	if err != nil {
		return nil, err
	}
	result := make([]PluginOnlineLibrarySummary, 0, len(connections))
	for _, connection := range connections {
		manifest, err := s.enabledManifest(connection.PluginID)
		if err != nil {
			continue
		}
		if !manifestHasCapability(manifest, contract.CapabilitySiteFeed) || !manifestHasCapability(manifest, contract.CapabilityMediaPlayback) {
			continue
		}
		home := []string{}
		if manifestHasCapability(manifest, contract.CapabilityHomeContribution) {
			home = configuredHomeContributions(connection.ConfigJSON)
			if len(home) == 0 {
				home = []string{"recommended"}
			}
		}
		result = append(result, PluginOnlineLibrarySummary{
			ID: connection.ID, PluginID: connection.PluginID, ConnectionID: connection.ID,
			Name: connection.Name, ProviderLabel: manifest.Name, Capabilities: append([]contract.Capability(nil), manifest.Capabilities...),
			Available: true, HomeContributions: home,
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *PluginRepositoryService) OnlineNavigation(ctx context.Context, actor Actor, libraryID string) (json.RawMessage, error) {
	return s.invokeOnline(ctx, actor, libraryID, contract.CapabilitySiteNavigation, map[string]any{"connectionId": libraryID})
}

func (s *PluginRepositoryService) OnlineFeed(ctx context.Context, actor Actor, libraryID, routeKey, cursor, refreshSession string) (json.RawMessage, error) {
	if !safeOnlineText(routeKey, 256) || !safeOptionalOnlineText(cursor, maxOnlineIdentifierBytes) || !safeOptionalOnlineText(refreshSession, maxOnlineIdentifierBytes) {
		return nil, appError(CodeInvalidRequest, "在线媒体栏目请求无效", nil)
	}
	return s.invokeOnline(ctx, actor, libraryID, contract.CapabilitySiteFeed, map[string]any{
		"connectionId": libraryID, "routeKey": routeKey, "cursor": emptyAsNil(cursor), "refreshSession": emptyAsNil(refreshSession),
	})
}

func (s *PluginRepositoryService) OnlineSearch(ctx context.Context, actor Actor, libraryID, query, cursor string) (json.RawMessage, error) {
	query = strings.TrimSpace(query)
	if !safeOnlineText(query, maxOnlineQueryBytes) || !safeOptionalOnlineText(cursor, maxOnlineIdentifierBytes) {
		return nil, appError(CodeInvalidRequest, "在线媒体搜索请求无效", nil)
	}
	return s.invokeOnline(ctx, actor, libraryID, contract.CapabilitySiteSearch, map[string]any{
		"connectionId": libraryID, "query": query, "cursor": emptyAsNil(cursor),
	})
}

func (s *PluginRepositoryService) OnlineDetail(ctx context.Context, actor Actor, libraryID, itemID string) (json.RawMessage, error) {
	if !safeOnlineText(itemID, maxOnlineIdentifierBytes) {
		return nil, appError(CodeInvalidRequest, "在线媒体标识无效", nil)
	}
	return s.invokeOnline(ctx, actor, libraryID, contract.CapabilitySiteDetail, map[string]any{"connectionId": libraryID, "itemId": itemID})
}

func (s *PluginRepositoryService) OnlinePlayback(ctx context.Context, actor Actor, libraryID, itemID, segmentID, versionID, variantID string) (json.RawMessage, error) {
	if !safeOnlineText(itemID, maxOnlineIdentifierBytes) || !safeOnlineText(segmentID, maxOnlineIdentifierBytes) || !safeOnlineText(versionID, maxOnlineIdentifierBytes) || !safeOptionalOnlineText(variantID, maxOnlineIdentifierBytes) {
		return nil, appError(CodeInvalidRequest, "在线媒体播放请求无效", nil)
	}
	response, err := s.invokeOnline(ctx, actor, libraryID, contract.CapabilityMediaPlayback, map[string]any{
		"connectionId": libraryID, "itemId": itemID, "segmentId": segmentID, "versionId": versionID, "variantId": emptyAsNil(variantID),
	})
	if err != nil {
		return nil, err
	}
	return rewriteOnlineAssetReferences(response)
}

func (s *PluginRepositoryService) SyncOnlineProgress(ctx context.Context, actor Actor, libraryID, itemID, segmentID, versionID, event string, positionSeconds float64, durationSeconds *float64, idempotencyKey, occurredAt string) (json.RawMessage, error) {
	if !safeOnlineText(itemID, maxOnlineIdentifierBytes) || !safeOnlineText(segmentID, maxOnlineIdentifierBytes) || !safeOnlineText(versionID, maxOnlineIdentifierBytes) || !safeOnlineText(idempotencyKey, 128) || !safeOptionalOnlineText(occurredAt, 128) || positionSeconds < 0 || positionSeconds > 365*24*60*60 {
		return nil, appError(CodeInvalidRequest, "在线播放进度请求无效", nil)
	}
	switch event {
	case "started", "progress", "paused", "resumed", "stopped", "completed":
	default:
		return nil, appError(CodeInvalidRequest, "在线播放进度事件无效", nil)
	}
	if durationSeconds != nil && (*durationSeconds < 0 || *durationSeconds > 365*24*60*60) {
		return nil, appError(CodeInvalidRequest, "在线播放时长无效", nil)
	}
	return s.invokeOnline(ctx, actor, libraryID, contract.CapabilityPlaybackProgress, map[string]any{
		"connectionId": libraryID, "itemId": itemID, "segmentId": segmentID, "versionId": versionID,
		"event": event, "positionSeconds": positionSeconds, "durationSeconds": durationSeconds,
		"idempotencyKey": idempotencyKey, "occurredAt": emptyAsNil(occurredAt),
	})
}

func (s *PluginRepositoryService) OnlineHistory(ctx context.Context, actor Actor, libraryID, encodedCursor string, pageSize int) (PluginOnlineHistoryPage, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return PluginOnlineHistoryPage{}, appError(CodePermissionDenied, "无权查看在线媒体历史", nil)
	}
	if pageSize < 1 || pageSize > 100 || !safeOptionalOnlineText(encodedCursor, 8192) || !safeOptionalOnlineText(libraryID, 128) {
		return PluginOnlineHistoryPage{}, appError(CodeInvalidRequest, "在线媒体历史分页请求无效", nil)
	}
	cursors, err := decodePluginHistoryCursor(encodedCursor, libraryID)
	if err != nil {
		return PluginOnlineHistoryPage{}, err
	}
	connections, err := s.enabledOnlineConnections(libraryID)
	if err != nil {
		return PluginOnlineHistoryPage{}, err
	}
	if len(connections) > maxOnlineHistorySources {
		connections = connections[:maxOnlineHistorySources]
	}
	page := PluginOnlineHistoryPage{List: make([]json.RawMessage, 0, pageSize)}
	next := pluginHistoryCursor{Sources: make(map[string]string, len(cursors.Sources))}
	for id, cursor := range cursors.Sources {
		next.Sources[id] = cursor
	}
	for _, connection := range connections {
		if next.Sources[connection.ID] == pluginHistoryExhausted {
			continue
		}
		manifest, manifestErr := s.enabledManifest(connection.PluginID)
		if manifestErr != nil || !manifestHasCapability(manifest, contract.CapabilitySiteHistory) {
			next.Sources[connection.ID] = pluginHistoryExhausted
			continue
		}
		remaining := pageSize - len(page.List)
		raw, invokeErr := s.invokeOnline(ctx, actor, connection.ID, contract.CapabilitySiteHistory, map[string]any{
			"connectionId": connection.ID,
			"cursor":       emptyAsNil(cursors.Sources[connection.ID]),
			"pageSize":     remaining,
		})
		if invokeErr != nil {
			if libraryID != "" {
				return PluginOnlineHistoryPage{}, invokeErr
			}
			next.Sources[connection.ID] = pluginHistoryExhausted
			continue
		}
		var providerPage pluginHistoryResponse
		if err := json.Unmarshal(raw, &providerPage); err != nil {
			if libraryID != "" {
				return PluginOnlineHistoryPage{}, appError(CodePluginResponseInvalid, "在线媒体历史响应无效", err)
			}
			next.Sources[connection.ID] = pluginHistoryExhausted
			continue
		}
		if len(providerPage.List) > remaining || providerPage.HasMore != (providerPage.Cursor != "") || providerPage.Cursor == pluginHistoryExhausted || (providerPage.Cursor != "" && !safeOnlineText(providerPage.Cursor, maxOnlineIdentifierBytes)) {
			if libraryID != "" {
				return PluginOnlineHistoryPage{}, appError(CodePluginResponseInvalid, "在线媒体历史响应无效", nil)
			}
			next.Sources[connection.ID] = pluginHistoryExhausted
			continue
		}
		for _, item := range providerPage.List {
			annotated, err := annotateHistoryLibrary(item, connection.ID)
			if err == nil {
				page.List = append(page.List, annotated)
				if len(page.List) == pageSize {
					break
				}
			}
		}
		if providerPage.HasMore {
			next.Sources[connection.ID] = providerPage.Cursor
		} else {
			next.Sources[connection.ID] = pluginHistoryExhausted
		}
		if len(page.List) == pageSize {
			break
		}
	}
	for _, connection := range connections {
		if next.Sources[connection.ID] != pluginHistoryExhausted {
			page.HasMore = true
			break
		}
	}
	if page.HasMore {
		page.Cursor, err = encodePluginHistoryCursor(next, libraryID)
		if err != nil {
			return PluginOnlineHistoryPage{}, err
		}
	}
	return page, nil
}

func (s *PluginRepositoryService) invokeOnline(ctx context.Context, actor Actor, libraryID string, capability contract.Capability, request any) (json.RawMessage, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权使用在线媒体库", nil)
	}
	connection, manifest, err := s.onlineConnection(libraryID)
	if err != nil {
		return nil, err
	}
	if !manifestHasCapability(manifest, capability) {
		return nil, appError(CodePermissionDenied, "在线媒体库不支持此操作", nil)
	}
	raw, err := s.InvokePlugin(ctx, connection.ID, string(capability), request)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, appError(CodePluginResponseInvalid, "插件返回的数据格式无效", nil)
	}
	var envelope pluginErrorEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, appError(CodePluginResponseInvalid, "插件返回的数据格式无效", err)
	}
	if envelope.PluginError != nil {
		// Plugin text is untrusted and may contain an upstream URL, credential,
		// cookie, or provider diagnostic. Keep the public error stable and safe.
		return nil, appError(CodePluginOnlineLibraryUnavailable, "在线媒体来源暂时不可用", nil)
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (s *PluginRepositoryService) onlineConnection(libraryID string) (models.PluginConnection, contract.Manifest, error) {
	if _, err := uuid.Parse(libraryID); err != nil {
		return models.PluginConnection{}, contract.Manifest{}, appError(CodeNotFound, "在线媒体库不存在", nil)
	}
	connections, err := s.enabledOnlineConnections(libraryID)
	if err != nil {
		return models.PluginConnection{}, contract.Manifest{}, err
	}
	if len(connections) != 1 {
		return models.PluginConnection{}, contract.Manifest{}, appError(CodeNotFound, "在线媒体库不存在", nil)
	}
	manifest, err := s.enabledManifest(connections[0].PluginID)
	return connections[0], manifest, err
}

func (s *PluginRepositoryService) enabledOnlineConnections(libraryID string) ([]models.PluginConnection, error) {
	query := s.db.Model(&models.PluginConnection{}).
		Joins("JOIN plugin_installations ON plugin_installations.plugin_id = plugin_connections.plugin_id AND plugin_installations.status = ?", models.PluginInstallationEnabled).
		Where("plugin_connections.enabled = ?", true)
	if libraryID != "" {
		if _, err := uuid.Parse(libraryID); err != nil {
			return nil, appError(CodeNotFound, "在线媒体库不存在", nil)
		}
		query = query.Where("plugin_connections.id = ?", libraryID)
	}
	var connections []models.PluginConnection
	if err := query.Order("plugin_connections.created_at ASC, plugin_connections.id ASC").Find(&connections).Error; err != nil {
		return nil, err
	}
	return connections, nil
}

func (s *PluginRepositoryService) enabledManifest(pluginID string) (contract.Manifest, error) {
	var installation models.PluginInstallation
	if err := s.db.First(&installation, "plugin_id = ? AND status = ?", pluginID, models.PluginInstallationEnabled).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return contract.Manifest{}, appError(CodePluginOnlineLibraryUnavailable, "在线媒体插件未启用", err)
		}
		return contract.Manifest{}, err
	}
	_, _, manifest, err := s.loadInstalled(pluginID)
	return manifest, err
}

func configuredHomeContributions(configJSON string) []string {
	var config struct {
		HomeContributions []string `json:"homeContributions"`
	}
	if json.Unmarshal([]byte(configJSON), &config) != nil {
		return nil
	}
	result := make([]string, 0, len(config.HomeContributions))
	seen := make(map[string]struct{})
	for _, item := range config.HomeContributions {
		item = strings.TrimSpace(item)
		if !safeOnlineText(item, 256) {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
		if len(result) == 16 {
			break
		}
	}
	return result
}

func rewriteOnlineAssetReferences(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, appError(CodePluginResponseInvalid, "在线播放方案无效", err)
	}
	if err := walkOnlineAssetReferences(value, 0); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, appError(CodePluginResponseInvalid, "在线播放方案无效", err)
	}
	return encoded, nil
}

func walkOnlineAssetReferences(value any, depth int) error {
	if depth > 12 {
		return appError(CodePluginResponseInvalid, "在线播放方案层级过深", nil)
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "urlRef" {
				reference, ok := child.(string)
				if !ok {
					return appError(CodePluginResponseInvalid, "在线播放资源标识无效", nil)
				}
				if _, err := uuid.Parse(reference); err != nil {
					return appError(CodePluginResponseInvalid, "在线播放资源未通过安全网关", nil)
				}
				typed[key] = "/api/v1/player/online-assets/" + reference
				continue
			}
			if err := walkOnlineAssetReferences(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := walkOnlineAssetReferences(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func annotateHistoryLibrary(raw json.RawMessage, libraryID string) (json.RawMessage, error) {
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || len(value) == 0 {
		return nil, errors.New("invalid plugin history item")
	}
	value["libraryId"] = libraryID
	return json.Marshal(value)
}

func decodePluginHistoryCursor(encoded, libraryID string) (pluginHistoryCursor, error) {
	cursor := pluginHistoryCursor{Sources: make(map[string]string)}
	if encoded == "" {
		return cursor, nil
	}
	if libraryID != "" {
		cursor.Sources[libraryID] = encoded
		return cursor, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) > 4096 || json.Unmarshal(payload, &cursor) != nil || len(cursor.Sources) > maxOnlineHistorySources {
		return pluginHistoryCursor{}, appError(CodeInvalidRequest, "在线媒体历史游标无效", err)
	}
	for id, value := range cursor.Sources {
		if _, err := uuid.Parse(id); err != nil || (value != pluginHistoryExhausted && !safeOnlineText(value, maxOnlineIdentifierBytes)) {
			return pluginHistoryCursor{}, appError(CodeInvalidRequest, "在线媒体历史游标无效", err)
		}
	}
	return cursor, nil
}

func encodePluginHistoryCursor(cursor pluginHistoryCursor, libraryID string) (string, error) {
	if libraryID != "" {
		return cursor.Sources[libraryID], nil
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", appError(CodePluginResponseInvalid, "在线媒体历史游标生成失败", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func safeOnlineText(value string, limit int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func safeOptionalOnlineText(value string, limit int) bool {
	return value == "" || safeOnlineText(value, limit)
}

func emptyAsNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
