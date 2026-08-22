package services

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

type onlinePluginRuntime struct {
	responses map[string][]byte
	handler   func(string, []byte) ([]byte, error)
}

func (*onlinePluginRuntime) Validate(context.Context, string) error              { return nil }
func (*onlinePluginRuntime) Start(context.Context, string, string, uint64) error { return nil }
func (*onlinePluginRuntime) Stop(string) error                                   { return nil }
func (*onlinePluginRuntime) Close(context.Context) error                         { return nil }
func (runtime *onlinePluginRuntime) Invoke(_ context.Context, _ string, operation string, request []byte) ([]byte, error) {
	if runtime.handler != nil {
		return runtime.handler(operation, request)
	}
	response, exists := runtime.responses[operation]
	if !exists {
		return nil, errors.New("missing response")
	}
	return append([]byte(nil), response...), nil
}

func TestPluginOnlineLibraryPlaybackHistoryAndDisableBoundary(t *testing.T) {
	service, actor, _ := pluginRepositoryFixture(t)
	actor.Permissions[authz.PermissionMediaLibrariesRead] = struct{}{}
	assetID := uuid.NewString()
	runtime := &onlinePluginRuntime{responses: map[string][]byte{
		"site.navigation":        []byte(`[{"id":"recommended","title":"推荐","pageType":"feed","routeKey":"recommended"}]`),
		"media.playback":         []byte(`{"workId":"BV1234567890","segmentId":"cid:1","versionId":"v1","variantId":"qn:80","variants":[],"assets":[{"kind":"progressive","urlRef":"` + assetID + `"}],"delivery":"server-gateway","danmaku":[{"id":"dm","label":"弹幕","urlRef":"` + assetID + `"}]}`),
		"site.history":           []byte(`{"list":[{"work":{"id":"BV1234567890","title":"测试视频","kind":"video","identity":{"scheme":"bilibili.bvid","value":"BV1234567890"}}}],"cursor":"123","hasMore":true}`),
		"playback.progress_sync": []byte(`{"accepted":true,"remote":true}`),
	}}
	service.runtime = runtime

	manifestJSON := strings.ReplaceAll(`{
      "schemaVersion":1,"id":"org.ohmycine.online-test","name":"在线测试","description":"fixture",
      "version":"0.1.0","apiVersion":"1","minServerVersion":"0.1.0","runtime":"wasm","entry":"plugin.wasm",
      "capabilities":["site.navigation","site.feed","site.detail","media.playback","home.contribution","site.history","playback.progress_sync"],
      "permissions":[],"configSchema":{"type":"object"},"author":"test","license":"MIT",
      "homepage":"https://example.test/plugin","source":"https://github.com/example/plugin","packageSha256":"${SHA}"
    }`, "${SHA}", strings.Repeat("a", 64))
	now := time.Now().UTC()
	pluginPackage := models.PluginPackage{PluginID: "org.ohmycine.online-test", Version: "0.1.0", RepositoryOwner: "example", RepositoryRepo: "plugin", RegistryCommit: strings.Repeat("b", 40), RegistryEntryJSON: `{}`, ManifestURL: "https://github.com/example/plugin/releases/download/v0.1.0/manifest.json", PackageURL: "https://github.com/example/plugin/releases/download/v0.1.0/plugin.omcp", PackageSHA256: strings.Repeat("a", 64), ExtractedTreeSHA256: strings.Repeat("c", 64), ManifestJSON: manifestJSON, PackagePath: "managed", VerifiedAt: now, CreatedAt: now}
	if err := service.db.Create(&pluginPackage).Error; err != nil {
		t.Fatal(err)
	}
	installation := models.PluginInstallation{PluginID: pluginPackage.PluginID, ActivePackageID: pluginPackage.ID, Status: models.PluginInstallationEnabled, Revision: 1, RuntimeGeneration: 1, InstalledAt: now, UpdatedAt: now, EnabledAt: &now}
	if err := service.db.Create(&installation).Error; err != nil {
		t.Fatal(err)
	}
	connection := models.PluginConnection{ID: uuid.NewString(), PluginID: pluginPackage.PluginID, Name: "我的在线库", ConfigJSON: `{}`, CredentialMode: models.PluginCredentialModeNone, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	secondConnection := models.PluginConnection{ID: uuid.NewString(), PluginID: pluginPackage.PluginID, Name: "第二在线库", ConfigJSON: `{}`, CredentialMode: models.PluginCredentialModeNone, Enabled: true, Revision: 1, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	if err := service.db.Create(&secondConnection).Error; err != nil {
		t.Fatal(err)
	}

	libraries, err := service.OnlineLibraries(actor)
	if err != nil || len(libraries) != 2 || libraries[0].ID != connection.ID || len(libraries[0].HomeContributions) != 1 || libraries[0].HomeContributions[0] != "recommended" {
		t.Fatalf("libraries=%+v err=%v", libraries, err)
	}
	playback, err := service.OnlinePlayback(context.Background(), actor, connection.ID, "BV1234567890", "cid:1", "v1", "qn:80")
	if err != nil || strings.Count(string(playback), "/api/v1/player/online-assets/"+assetID) != 2 {
		t.Fatalf("playback=%s err=%v", playback, err)
	}
	history, err := service.OnlineHistory(context.Background(), actor, "", "", 24)
	if err != nil || len(history.List) != 2 || !history.HasMore || history.Cursor == "" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	var item map[string]any
	if err := json.Unmarshal(history.List[0], &item); err != nil || item["libraryId"] != connection.ID {
		t.Fatalf("history item=%s err=%v", history.List[0], err)
	}
	runtime.handler = func(operation string, request []byte) ([]byte, error) {
		if operation != "site.history" {
			return runtime.responses[operation], nil
		}
		var input struct {
			ConnectionID string `json:"connectionId"`
			PageSize     int    `json:"pageSize"`
		}
		if err := json.Unmarshal(request, &input); err != nil || input.PageSize != 1 {
			return nil, errors.New("invalid history request")
		}
		title := "第一页"
		if input.ConnectionID == secondConnection.ID {
			title = "第二页"
		}
		return []byte(`{"list":[{"work":{"id":"BV1234567890","title":"` + title + `","kind":"video","identity":{"scheme":"bilibili.bvid","value":"BV1234567890"}}}],"hasMore":false}`), nil
	}
	firstAggregate, err := service.OnlineHistory(context.Background(), actor, "", "", 1)
	if err != nil || len(firstAggregate.List) != 1 || !firstAggregate.HasMore || firstAggregate.Cursor == "" {
		t.Fatalf("first aggregate=%+v err=%v", firstAggregate, err)
	}
	secondAggregate, err := service.OnlineHistory(context.Background(), actor, "", firstAggregate.Cursor, 1)
	if err != nil || len(secondAggregate.List) != 1 || secondAggregate.HasMore || secondAggregate.Cursor != "" {
		t.Fatalf("second aggregate=%+v err=%v", secondAggregate, err)
	}
	var secondItem map[string]any
	if err := json.Unmarshal(secondAggregate.List[0], &secondItem); err != nil || secondItem["libraryId"] != secondConnection.ID {
		t.Fatalf("second aggregate item=%s err=%v", secondAggregate.List[0], err)
	}
	runtime.handler = func(operation string, request []byte) ([]byte, error) {
		if operation != "site.history" {
			return runtime.responses[operation], nil
		}
		var input struct {
			ConnectionID string `json:"connectionId"`
		}
		if err := json.Unmarshal(request, &input); err != nil {
			return nil, err
		}
		if input.ConnectionID == connection.ID {
			return []byte(`[]`), nil
		}
		return []byte(`{"list":[{"work":{"id":"BV1234567890","title":"可用来源","kind":"video","identity":{"scheme":"bilibili.bvid","value":"BV1234567890"}}}],"hasMore":false}`), nil
	}
	malformedAggregate, err := service.OnlineHistory(context.Background(), actor, "", "", 1)
	if err != nil || len(malformedAggregate.List) != 1 || malformedAggregate.HasMore || malformedAggregate.Cursor != "" {
		t.Fatalf("malformed aggregate=%+v err=%v", malformedAggregate, err)
	}
	if _, err := service.OnlineHistory(context.Background(), actor, connection.ID, "", 1); ErrorCode(err) != CodePluginResponseInvalid {
		t.Fatalf("single malformed history error=%v code=%s", err, ErrorCode(err))
	}
	runtime.handler = nil
	runtime.responses["site.navigation"] = []byte(`{"pluginError":{"code":"upstream-unavailable","message":"站点暂时不可用"}}`)
	if _, err := service.OnlineNavigation(context.Background(), actor, connection.ID); ErrorCode(err) != CodePluginOnlineLibraryUnavailable || ErrorMessage(err) != "在线媒体来源暂时不可用" {
		t.Fatalf("provider error=%v code=%s", err, ErrorCode(err))
	}
	if err := service.db.Model(&models.PluginInstallation{}).Where("plugin_id = ?", installation.PluginID).Update("status", models.PluginInstallationDisabled).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.OnlinePlayback(context.Background(), actor, connection.ID, "BV1234567890", "cid:1", "v1", "qn:80"); ErrorCode(err) != CodeNotFound {
		t.Fatalf("disabled error=%v code=%s", err, ErrorCode(err))
	}
}
