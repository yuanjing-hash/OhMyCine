package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"gorm.io/gorm"
)

const (
	maxOnlineIdentifierBytes = 512
	maxOnlineQueryBytes      = 512
	maxOnlineHistorySources  = 8
	pluginHistoryExhausted   = "!"
	pluginFeedCacheTTL       = 30 * time.Second
	pluginActionReceiptTTL   = 24 * time.Hour
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

type PluginHomeContribution struct {
	ID            string          `json:"id"`
	LibraryID     string          `json:"libraryId"`
	PluginID      string          `json:"pluginId"`
	ProviderLabel string          `json:"providerLabel"`
	RouteKey      string          `json:"routeKey"`
	Title         string          `json:"title"`
	Layout        string          `json:"layout"`
	Refreshable   bool            `json:"refreshable"`
	Sections      json.RawMessage `json:"sections,omitempty"`
	ErrorCode     string          `json:"errorCode,omitempty"`
}

type PluginSiteActionInput struct {
	SegmentID      string
	VersionID      string
	Value          *bool
	IdempotencyKey string
	Confirmed      bool
}

type pluginSiteActionResponse struct {
	Accepted  bool  `json:"accepted"`
	State     *bool `json:"state,omitempty"`
	Duplicate bool  `json:"duplicate,omitempty"`
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
	libraries, err := s.enabledOnlineLibraries("")
	if err != nil {
		return nil, err
	}
	result := make([]PluginOnlineLibrarySummary, 0, len(libraries))
	for _, library := range libraries {
		manifest, err := s.enabledManifest(library.PluginID)
		if err != nil {
			continue
		}
		if !manifestHasCapability(manifest, contract.CapabilitySiteFeed) || !manifestHasCapability(manifest, contract.CapabilityMediaPlayback) {
			continue
		}
		home := []string{}
		if manifestHasCapability(manifest, contract.CapabilityHomeContribution) {
			_ = json.Unmarshal([]byte(library.HomeContributionsJSON), &home)
			if len(home) == 0 {
				home = []string{"recommended"}
			}
		}
		result = append(result, PluginOnlineLibrarySummary{
			ID: library.ID, PluginID: library.PluginID, ConnectionID: library.ConnectionID,
			Name: library.Name, ProviderLabel: manifest.Name, Capabilities: append([]contract.Capability(nil), manifest.Capabilities...),
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
	_, _, manifest, err := s.onlineLibrary(libraryID)
	if err != nil {
		return nil, err
	}
	raw, err := s.invokeOnline(ctx, actor, libraryID, contract.CapabilitySiteNavigation, map[string]any{"connectionId": libraryID, "depth": 0})
	if err != nil {
		return nil, err
	}
	normalized, err := s.normalizeOnlineNavigation(libraryID, manifest, raw)
	if err != nil {
		s.logInvalidOnlineNavigation(libraryID, manifest.ID, err)
		return nil, err
	}
	return normalized, nil
}

func (s *PluginRepositoryService) OnlineFeed(ctx context.Context, actor Actor, libraryID, routeKey, cursor, refreshSession string) (json.RawMessage, error) {
	if !safeOnlineText(routeKey, 256) || !safeOptionalOnlineText(cursor, maxOnlineIdentifierBytes) || !safeOptionalOnlineText(refreshSession, maxOnlineIdentifierBytes) {
		return nil, appError(CodeInvalidRequest, "在线媒体栏目请求无效", nil)
	}
	if refreshSession != "" {
		if _, err := uuid.Parse(refreshSession); err != nil {
			return nil, appError(CodeInvalidRequest, "在线媒体刷新会话无效", nil)
		}
	}
	if refreshSession == "" {
		refreshSession = uuid.NewString()
	}
	return s.onlineFeed(ctx, actor, libraryID, routeKey, cursor, refreshSession, false)
}

func (s *PluginRepositoryService) RefreshOnlineFeed(ctx context.Context, actor Actor, libraryID, routeKey string) (json.RawMessage, error) {
	if !safeOnlineText(routeKey, 256) {
		return nil, appError(CodeInvalidRequest, "在线媒体栏目请求无效", nil)
	}
	return s.onlineFeed(ctx, actor, libraryID, routeKey, "", uuid.NewString(), true)
}

func (s *PluginRepositoryService) onlineFeed(ctx context.Context, actor Actor, libraryID, routeKey, cursor, refreshSession string, forceRefresh bool) (json.RawMessage, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权使用在线媒体库", nil)
	}
	library, connection, manifest, err := s.onlineLibrary(libraryID)
	if err != nil {
		return nil, err
	}
	if !manifestHasCapability(manifest, contract.CapabilitySiteFeed) || (forceRefresh && !manifestHasCapability(manifest, contract.CapabilityFeedRefresh)) {
		return nil, appError(CodePermissionDenied, "在线媒体库不支持此操作", nil)
	}
	if forceRefresh {
		var recent int64
		if err := s.db.Model(&models.PluginFeedCache{}).Where("library_id = ? AND route_key = ? AND updated_at > ?", library.ID, routeKey, time.Now().UTC().Add(-2*time.Second)).Count(&recent).Error; err != nil {
			return nil, err
		}
		if recent > 0 {
			return nil, appError(CodePluginFeedRateLimited, "在线栏目刷新过于频繁，请稍后重试", nil)
		}
	}
	cursorKey := fmt.Sprintf("%x", sha256.Sum256([]byte(cursor)))
	if !forceRefresh {
		var cached models.PluginFeedCache
		err := s.db.Where("library_id = ? AND route_key = ? AND cursor_key = ? AND refresh_session = ? AND expires_at > ?", library.ID, routeKey, cursorKey, refreshSession, time.Now().UTC()).First(&cached).Error
		if err == nil && json.Valid([]byte(cached.ResponseJSON)) {
			return json.RawMessage(cached.ResponseJSON), nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	raw, err := s.InvokePlugin(ctx, connection.ID, string(contract.CapabilitySiteFeed), map[string]any{
		"connectionId": connection.ID, "routeKey": routeKey, "cursor": emptyAsNil(cursor), "refreshSession": refreshSession,
	})
	if err != nil {
		return nil, err
	}
	normalized, err := contract.NormalizeFeedSections(raw, refreshSession)
	if err != nil {
		return nil, appError(CodePluginResponseInvalid, "在线媒体栏目响应无效", err)
	}
	now := time.Now().UTC()
	record := models.PluginFeedCache{LibraryID: library.ID, RouteKey: routeKey, CursorKey: cursorKey, RefreshSession: refreshSession, ResponseJSON: string(normalized), ExpiresAt: now.Add(pluginFeedCacheTTL), CreatedAt: now, UpdatedAt: now}
	if err := s.db.Where("library_id = ? AND route_key = ? AND cursor_key = ? AND refresh_session = ?", library.ID, routeKey, cursorKey, refreshSession).
		Assign(map[string]any{"response_json": record.ResponseJSON, "expires_at": record.ExpiresAt, "updated_at": now}).FirstOrCreate(&record).Error; err != nil {
		return nil, err
	}
	// Cleanup is bounded and best-effort; cache failure must not hide a valid
	// provider response from the Player.
	_ = s.db.Where("expires_at < ?", now.Add(-time.Hour)).Delete(&models.PluginFeedCache{}).Error
	return append(json.RawMessage(nil), normalized...), nil
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
	var plan contract.PlaybackPlan
	decoder := json.NewDecoder(strings.NewReader(string(response)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return nil, appError(CodePluginResponseInvalid, "在线播放方案无效", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, appError(CodePluginResponseInvalid, "在线播放方案无效", err)
	}
	if err := contract.ValidatePlaybackPlan(plan, time.Now().UTC()); err != nil {
		return nil, appError(CodePluginResponseInvalid, "在线播放方案无效", err)
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
	connections := []models.PluginConnection{}
	if libraryID != "" {
		_, connection, _, resolveErr := s.onlineLibrary(libraryID)
		if resolveErr != nil {
			return PluginOnlineHistoryPage{}, resolveErr
		}
		connections = append(connections, connection)
	} else {
		connections, err = s.enabledOnlineConnections("")
		if err != nil {
			return PluginOnlineHistoryPage{}, err
		}
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
		invokeLibraryID := connection.ID
		if libraryID != "" {
			invokeLibraryID = libraryID
		}
		raw, invokeErr := s.invokeOnline(ctx, actor, invokeLibraryID, contract.CapabilitySiteHistory, map[string]any{
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
			historyLibraryID := connection.ID
			if libraryID != "" {
				historyLibraryID = libraryID
			}
			annotated, err := annotateHistoryLibrary(item, historyLibraryID)
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

func (s *PluginRepositoryService) HomeContributions(ctx context.Context, actor Actor) ([]PluginHomeContribution, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权查看在线媒体主页栏目", nil)
	}
	libraries, err := s.OnlineLibraries(actor)
	if err != nil {
		return nil, err
	}
	result := make([]PluginHomeContribution, 0)
	for _, library := range libraries {
		for _, routeKey := range library.HomeContributions {
			item := PluginHomeContribution{
				ID: library.ID + ":" + routeKey, LibraryID: library.ID, PluginID: library.PluginID,
				ProviderLabel: library.ProviderLabel, RouteKey: routeKey, Title: routeKey, Layout: "row", Refreshable: true,
			}
			sections, feedErr := s.OnlineFeed(ctx, actor, library.ID, routeKey, "", "")
			if feedErr != nil {
				item.ErrorCode = CodePluginOnlineLibraryUnavailable
				result = append(result, item)
				continue
			}
			var parsed []contract.FeedSection
			if json.Unmarshal(sections, &parsed) != nil || len(parsed) == 0 {
				item.ErrorCode = CodePluginResponseInvalid
				result = append(result, item)
				continue
			}
			item.Title, item.Layout = parsed[0].Title, parsed[0].Layout
			item.Sections = sections
			result = append(result, item)
		}
	}
	return result, nil
}

var standardPluginSiteActions = map[string]struct {
	ConfirmationRequired bool
}{
	"like.add": {}, "like.remove": {}, "favorite.add": {}, "favorite.remove": {},
	"watch-later.add": {}, "watch-later.remove": {}, "follow.add": {}, "follow.remove": {ConfirmationRequired: true},
	"history.remove": {ConfirmationRequired: true},
}

func (s *PluginRepositoryService) InvokeSiteAction(ctx context.Context, actor Actor, libraryID, itemID, action string, input PluginSiteActionInput) (json.RawMessage, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权操作在线媒体", nil)
	}
	policy, supported := standardPluginSiteActions[action]
	if !supported || !safeOnlineText(itemID, maxOnlineIdentifierBytes) || !safeOptionalOnlineText(input.SegmentID, maxOnlineIdentifierBytes) || !safeOptionalOnlineText(input.VersionID, maxOnlineIdentifierBytes) || !safeOnlineText(input.IdempotencyKey, 128) {
		return nil, appError(CodeInvalidRequest, "在线媒体操作请求无效", nil)
	}
	if policy.ConfirmationRequired && !input.Confirmed {
		return nil, appError(CodeConflict, "该在线媒体操作需要明确确认", nil)
	}
	library, connection, manifest, err := s.onlineLibrary(libraryID)
	if err != nil {
		return nil, err
	}
	if !manifestHasCapability(manifest, contract.CapabilitySiteInteraction) {
		return nil, appError(CodePermissionDenied, "在线媒体库不支持此操作", nil)
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("v1\x00%d\x00%s", actor.User.ID, input.IdempotencyKey)))
	idempotencyHash := fmt.Sprintf("%x", hash)
	var receipt models.PluginActionReceipt
	if err := s.db.First(&receipt, "library_id = ? AND action = ? AND idempotency_hash = ? AND created_at > ?", library.ID, action, idempotencyHash, time.Now().UTC().Add(-pluginActionReceiptTTL)).Error; err == nil {
		return markPluginActionDuplicate(json.RawMessage(receipt.ResponseJSON))
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	raw, err := s.InvokePlugin(ctx, connection.ID, string(contract.CapabilitySiteInteraction), map[string]any{
		"connectionId": connection.ID, "itemId": itemID, "action": action,
		"segmentId": emptyAsNil(input.SegmentID), "versionId": emptyAsNil(input.VersionID), "value": input.Value,
		"idempotencyKey": input.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	var response pluginSiteActionResponse
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || !response.Accepted {
		return nil, appError(CodePluginResponseInvalid, "在线媒体操作响应无效", err)
	}
	normalized, _ := json.Marshal(response)
	receipt = models.PluginActionReceipt{LibraryID: library.ID, Action: action, IdempotencyHash: idempotencyHash, ResponseJSON: string(normalized), CreatedAt: time.Now().UTC()}
	if err := s.db.Create(&receipt).Error; err != nil {
		var existing models.PluginActionReceipt
		if loadErr := s.db.First(&existing, "library_id = ? AND action = ? AND idempotency_hash = ?", library.ID, action, idempotencyHash).Error; loadErr == nil {
			return markPluginActionDuplicate(json.RawMessage(existing.ResponseJSON))
		}
		return nil, err
	}
	_ = s.db.Where("created_at < ?", time.Now().UTC().Add(-pluginActionReceiptTTL)).Delete(&models.PluginActionReceipt{}).Error
	return json.RawMessage(normalized), nil
}

func markPluginActionDuplicate(raw json.RawMessage) (json.RawMessage, error) {
	var response pluginSiteActionResponse
	if json.Unmarshal(raw, &response) != nil || !response.Accepted {
		return nil, appError(CodePluginResponseInvalid, "在线媒体操作记录无效", nil)
	}
	response.Duplicate = true
	encoded, err := json.Marshal(response)
	return json.RawMessage(encoded), err
}

func (s *PluginRepositoryService) invokeOnline(ctx context.Context, actor Actor, libraryID string, capability contract.Capability, request any) (json.RawMessage, error) {
	if !actor.Can(authz.PermissionMediaLibrariesRead) {
		return nil, appError(CodePermissionDenied, "无权使用在线媒体库", nil)
	}
	_, connection, manifest, err := s.onlineLibrary(libraryID)
	if err != nil {
		return nil, err
	}
	if !manifestHasCapability(manifest, capability) {
		return nil, appError(CodePermissionDenied, "在线媒体库不支持此操作", nil)
	}
	if object, ok := request.(map[string]any); ok {
		object["connectionId"] = connection.ID
	}
	raw, err := s.InvokePlugin(ctx, connection.ID, string(capability), request)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(raw)
	if !json.Valid(trimmed) {
		return nil, appError(CodePluginResponseInvalid, "插件返回的数据格式无效", nil)
	}
	var object map[string]json.RawMessage
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return nil, appError(CodePluginResponseInvalid, "插件返回的数据格式无效", err)
		}
	}
	pluginErrorJSON, hasPluginError := object["pluginError"]
	if hasPluginError {
		var envelope pluginErrorEnvelope
		if err := json.Unmarshal(trimmed, &envelope); err != nil || envelope.PluginError == nil || strings.TrimSpace(envelope.PluginError.Code) == "" {
			serverlog.OperationPluginRuntime.Event(s.log.Warn()).
				Str("plugin_id", safeLabel(connection.PluginID, 128)).
				Str("library_id", safeLabel(libraryID, 128)).
				Str("capability", safeLabel(string(capability), 96)).
				Str("error_code", CodePluginResponseInvalid).
				Msg(serverlog.OperationPluginRuntime.Message("插件错误响应格式无效"))
			return nil, appError(CodePluginResponseInvalid, "插件返回的数据格式无效", err)
		}
		_ = pluginErrorJSON
		serverlog.OperationPluginRuntime.Event(s.log.Warn()).
			Str("plugin_id", safeLabel(connection.PluginID, 128)).
			Str("library_id", safeLabel(libraryID, 128)).
			Str("capability", safeLabel(string(capability), 96)).
			Str("error_code", safeLabel(envelope.PluginError.Code, 96)).
			Msg(serverlog.OperationPluginRuntime.Message("在线媒体能力调用失败"))
		// Plugin text is untrusted and may contain an upstream URL, credential,
		// cookie, or provider diagnostic. Only the bounded code selects a stable,
		// Server-owned message.
		return nil, mapPluginOnlineError(envelope.PluginError.Code, capability)
	}
	return append(json.RawMessage(nil), trimmed...), nil
}

func mapPluginOnlineError(pluginCode string, capability contract.Capability) error {
	switch strings.TrimSpace(pluginCode) {
	case "not-authenticated":
		return appError(CodePluginOnlineAuthentication, "在线媒体账号登录已失效，请在 Server 插件设置中重新登录", nil)
	case "access-restricted":
		return appError(CodePluginOnlineAccessRestricted, "当前账号或地区无法播放此在线媒体", nil)
	case "not-found":
		return appError(CodeNotFound, "在线媒体不存在或不可访问", nil)
	case "quality-unavailable":
		return appError(CodePluginOnlineQualityUnavailable, "所选在线媒体清晰度不可用，请选择其他清晰度", nil)
	case "rate-limited":
		return appError(CodePluginOnlineRateLimited, "在线媒体服务请求过于频繁，请稍后重试", nil)
	case "invalid-response":
		if capability == contract.CapabilityMediaPlayback {
			return appError(CodePluginResponseInvalid, "在线媒体播放方案无效，请更新插件后重试", nil)
		}
		return appError(CodePluginResponseInvalid, "在线媒体来源返回的数据无效，请更新插件后重试", nil)
	case "playback-audio-unavailable", "asset-domain-denied":
		if capability == contract.CapabilityMediaPlayback {
			return appError(CodePluginResponseInvalid, "在线媒体播放方案无效，请更新插件后重试", nil)
		}
		return appError(CodePluginOnlineLibraryUnavailable, "在线媒体来源暂时不可用", nil)
	default:
		return appError(CodePluginOnlineLibraryUnavailable, "在线媒体来源暂时不可用", nil)
	}
}

func (s *PluginRepositoryService) onlineConnection(libraryID string) (models.PluginConnection, contract.Manifest, error) {
	_, connection, manifest, err := s.onlineLibrary(libraryID)
	return connection, manifest, err
}

func (s *PluginRepositoryService) onlineLibrary(libraryID string) (models.PluginOnlineLibrary, models.PluginConnection, contract.Manifest, error) {
	if _, err := uuid.Parse(libraryID); err != nil {
		return models.PluginOnlineLibrary{}, models.PluginConnection{}, contract.Manifest{}, appError(CodeNotFound, "在线媒体库不存在", nil)
	}
	var library models.PluginOnlineLibrary
	if err := s.db.First(&library, "id = ? AND enabled = ?", libraryID, true).Error; err != nil {
		return models.PluginOnlineLibrary{}, models.PluginConnection{}, contract.Manifest{}, appError(CodeNotFound, "在线媒体库不存在", err)
	}
	var connection models.PluginConnection
	if err := s.db.First(&connection, "id = ? AND plugin_id = ? AND enabled = ?", library.ConnectionID, library.PluginID, true).Error; err != nil {
		return models.PluginOnlineLibrary{}, models.PluginConnection{}, contract.Manifest{}, appError(CodeNotFound, "在线媒体库不存在", err)
	}
	manifest, err := s.enabledManifest(connection.PluginID)
	return library, connection, manifest, err
}

func (s *PluginRepositoryService) enabledOnlineLibraries(libraryID string) ([]models.PluginOnlineLibrary, error) {
	query := s.db.Model(&models.PluginOnlineLibrary{}).
		Joins("JOIN plugin_connections ON plugin_connections.id = plugin_online_libraries.connection_id AND plugin_connections.enabled = ?", true).
		Joins("JOIN plugin_installations ON plugin_installations.plugin_id = plugin_online_libraries.plugin_id AND plugin_installations.status = ?", models.PluginInstallationEnabled).
		Where("plugin_online_libraries.enabled = ?", true)
	if libraryID != "" {
		if _, err := uuid.Parse(libraryID); err != nil {
			return nil, appError(CodeNotFound, "在线媒体库不存在", nil)
		}
		query = query.Where("plugin_online_libraries.id = ?", libraryID)
	}
	var libraries []models.PluginOnlineLibrary
	if err := query.Order("plugin_online_libraries.sort_order ASC, plugin_online_libraries.created_at ASC, plugin_online_libraries.id ASC").Find(&libraries).Error; err != nil {
		return nil, err
	}
	return libraries, nil
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
