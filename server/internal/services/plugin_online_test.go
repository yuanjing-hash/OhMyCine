package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
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
	credentialStore, err := credential.Open(filepath.Join(t.TempDir(), "plugin-online.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service.credentials = credentialStore
	actor.Permissions[authz.PermissionMediaLibrariesRead] = struct{}{}
	assetID := uuid.NewString()
	runtime := &onlinePluginRuntime{responses: map[string][]byte{
		"site.navigation":        []byte(`[{"id":"recommended","title":"推荐","pageType":"feed","routeKey":"recommended"}]`),
		"site.feed":              []byte(`[{"id":"recommended","title":"推荐","layout":"hero","refreshable":true,"homeEligible":true,"items":[{"work":{"id":"video-1","title":"视频","kind":"video","identity":{"scheme":"fixture.video","value":"video-1"}},"actions":["favorite.add"]}]}]`),
		"media.playback":         []byte(`{"workId":"BV1234567890","segmentId":"cid:1","versionId":"v1","variantId":"qn:80","variants":[],"assets":[{"kind":"progressive","urlRef":"` + assetID + `"}],"delivery":"server-gateway","danmaku":[{"id":"dm","label":"弹幕","urlRef":"` + assetID + `"}]}`),
		"site.interaction":       []byte(`{"accepted":true,"state":true}`),
		"site.history":           []byte(`{"list":[{"work":{"id":"BV1234567890","title":"测试视频","kind":"video","identity":{"scheme":"bilibili.bvid","value":"BV1234567890"}}}],"cursor":"123","hasMore":true}`),
		"playback.progress_sync": []byte(`{"accepted":true,"remote":true}`),
	}}
	service.runtime = runtime

	manifestJSON := strings.ReplaceAll(`{
      "schemaVersion":1,"id":"org.ohmycine.online-test","name":"在线测试","description":"fixture",
      "version":"0.1.0","apiVersion":"1","minServerVersion":"0.1.0","runtime":"wasm","entry":"plugin.wasm",
      "capabilities":["site.navigation","site.feed","site.detail","site.interaction","media.playback","home.contribution","feed.refresh","site.history","playback.progress_sync"],
      "permissions":[{"kind":"network.http","domains":["login.example.test"]},{"kind":"credential.use","scopes":["site.session"]}],"configSchema":{"type":"object"},"author":"test","license":"MIT",
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
	if err := service.db.Create(&models.PluginOnlineLibrary{ID: connection.ID, PluginID: connection.PluginID, ConnectionID: connection.ID, ExternalKey: "default", Name: connection.Name, HomeContributionsJSON: `[]`, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	secondConnection := models.PluginConnection{ID: uuid.NewString(), PluginID: pluginPackage.PluginID, Name: "第二在线库", ConfigJSON: `{}`, CredentialMode: models.PluginCredentialModeNone, Enabled: true, Revision: 1, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}
	if err := service.db.Create(&secondConnection).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Create(&models.PluginOnlineLibrary{ID: secondConnection.ID, PluginID: secondConnection.PluginID, ConnectionID: secondConnection.ID, ExternalKey: "default", Name: secondConnection.Name, HomeContributionsJSON: `[]`, Enabled: true, Revision: 1, CreatedAt: secondConnection.CreatedAt, UpdatedAt: secondConnection.UpdatedAt}).Error; err != nil {
		t.Fatal(err)
	}

	libraries, err := service.OnlineLibraries(actor)
	if err != nil || len(libraries) != 2 || libraries[0].ID != connection.ID || len(libraries[0].HomeContributions) != 1 || libraries[0].HomeContributions[0] != "recommended" {
		t.Fatalf("libraries=%+v err=%v", libraries, err)
	}
	feedCalls, actionCalls := 0, 0
	runtime.handler = func(operation string, request []byte) ([]byte, error) {
		switch operation {
		case "site.feed":
			feedCalls++
		case "site.interaction":
			actionCalls++
		}
		return runtime.responses[operation], nil
	}
	feed, err := service.OnlineFeed(context.Background(), actor, connection.ID, "recommended", "", "")
	var feedSections []struct {
		RefreshSession string `json:"refreshSession"`
	}
	if err != nil || json.Unmarshal(feed, &feedSections) != nil || len(feedSections) != 1 || feedSections[0].RefreshSession == "" {
		t.Fatalf("feed=%s parsed=%+v err=%v", feed, feedSections, err)
	}
	if _, err := service.OnlineFeed(context.Background(), actor, connection.ID, "recommended", "", feedSections[0].RefreshSession); err != nil || feedCalls != 1 {
		t.Fatalf("feed cache calls=%d err=%v", feedCalls, err)
	}
	if err := service.db.Model(&models.PluginFeedCache{}).Where("library_id = ?", connection.ID).Update("updated_at", now.Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.RefreshOnlineFeed(context.Background(), actor, connection.ID, "recommended")
	if err != nil || string(refreshed) == string(feed) || feedCalls != 2 {
		t.Fatalf("refreshed=%s feedCalls=%d err=%v", refreshed, feedCalls, err)
	}
	actionInput := PluginSiteActionInput{IdempotencyKey: "action-1"}
	firstAction, err := service.InvokeSiteAction(context.Background(), actor, connection.ID, "video-1", "favorite.add", actionInput)
	secondAction, duplicateErr := service.InvokeSiteAction(context.Background(), actor, connection.ID, "video-1", "favorite.add", actionInput)
	if err != nil || duplicateErr != nil || actionCalls != 1 || strings.Contains(string(firstAction), `"duplicate":true`) || !strings.Contains(string(secondAction), `"duplicate":true`) {
		t.Fatalf("first=%s second=%s calls=%d err=%v duplicateErr=%v", firstAction, secondAction, actionCalls, err, duplicateErr)
	}
	credentialScope, credentialMode := "site.session", models.PluginCredentialModeCookie
	migrated, err := service.UpdateConnection(actor, pluginPackage.PluginID, connection.ID, UpdatePluginConnectionInput{
		CredentialScope: &credentialScope,
		CredentialMode:  &credentialMode,
		Revision:        connection.Revision,
	}, RequestContext{})
	if err != nil || migrated.Revision != connection.Revision+1 || migrated.CredentialScope != credentialScope || migrated.CredentialMode != credentialMode || migrated.CredentialConfigured {
		t.Fatalf("anonymous QR migration=%+v err=%v", migrated, err)
	}
	encodedMigration, err := json.Marshal(migrated)
	if err != nil || bytes.Contains(encodedMigration, []byte("ciphertext")) || bytes.Contains(encodedMigration, []byte("SESSDATA")) {
		t.Fatalf("anonymous QR migration leaked credential material: %s err=%v", encodedMigration, err)
	}
	runtime.handler = func(operation string, _ []byte) ([]byte, error) {
		switch operation {
		case "site.auth.start":
			return []byte(fmt.Sprintf(`{"loginSession":"session-1","qrCodeUrl":"https://login.example.test/qr","expiresAt":%q,"pollAfterSeconds":2}`, time.Now().UTC().Add(3*time.Minute).Format(time.RFC3339Nano))), nil
		case "site.auth.poll":
			return []byte(`{"state":"confirmed","authenticated":true,"account":{"id":"account-1","name":"测试账号","avatarUrl":"https://login.example.test/avatar.jpg"}}`), nil
		default:
			return runtime.responses[operation], nil
		}
	}
	started, err := service.StartConnectionAuth(context.Background(), actor, pluginPackage.PluginID, connection.ID)
	if err != nil || started.LoginSession != "session-1" {
		t.Fatalf("auth start=%+v err=%v", started, err)
	}
	polled, err := service.PollConnectionAuth(context.Background(), actor, pluginPackage.PluginID, connection.ID, started.LoginSession)
	if err != nil || !polled.Authenticated || polled.Account == nil || polled.Account.Name != "测试账号" {
		t.Fatalf("auth poll=%+v err=%v", polled, err)
	}
	var healthy models.PluginConnection
	if err := service.db.First(&healthy, "id = ?", connection.ID).Error; err != nil || healthy.LastHealthStatus != "healthy" {
		t.Fatalf("connection health=%+v err=%v", healthy, err)
	}
	runtime.handler = nil
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
	if _, err := service.OnlinePlayback(context.Background(), actor, connection.ID, "BV1234567890", "cid:1", "v1", "qn:80"); ErrorCode(err) != CodePluginOnlineLibraryUnavailable {
		t.Fatalf("disabled error=%v code=%s", err, ErrorCode(err))
	}
}
