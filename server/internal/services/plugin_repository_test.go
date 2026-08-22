package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	pluginrepository "github.com/yuanjing-hash/ohmycine/server/internal/plugins/repository"
)

type fakePluginRegistryFetcher struct {
	snapshots map[string]pluginrepository.Snapshot
	err       error
}

func (fetcher *fakePluginRegistryFetcher) Fetch(_ context.Context, source contract.GitHubRepository) (pluginrepository.Snapshot, error) {
	if fetcher.err != nil {
		return pluginrepository.Snapshot{}, fetcher.err
	}
	return fetcher.snapshots[strings.ToLower(source.Owner+"/"+source.Name)], nil
}

func pluginRepositoryFixture(t *testing.T) (*PluginRepositoryService, Actor, *fakePluginRegistryFetcher) {
	t.Helper()
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionPluginsRead] = struct{}{}
	actor.Permissions[authz.PermissionPluginsInstall] = struct{}{}
	fetcher := &fakePluginRegistryFetcher{snapshots: map[string]pluginrepository.Snapshot{}}
	return NewPluginRepositoryService(queue.db, queue.audit, fetcher, zerolog.Nop()), actor, fetcher
}

func TestPluginRepositoryCRUDAndRevisionConflicts(t *testing.T) {
	service, actor, _ := pluginRepositoryFixture(t)
	first, err := service.Create(actor, CreatePluginRepositoryInput{GitHubURL: "https://github.com/OhMyCine/Official-Plugins.git", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "ohmycine/official-plugins" || first.GitHubURL != "https://github.com/ohmycine/official-plugins" || first.Priority != 1000 {
		t.Fatalf("first=%+v", first)
	}
	if _, err := service.Create(actor, CreatePluginRepositoryInput{GitHubURL: "https://github.com/ohmycine/OFFICIAL-PLUGINS", Enabled: true}, RequestContext{}); ErrorCode(err) != CodePluginRepositoryConflict {
		t.Fatalf("duplicate error=%v code=%s", err, ErrorCode(err))
	}
	second, err := service.Create(actor, CreatePluginRepositoryInput{Name: "社区仓库", GitHubURL: "https://github.com/community/plugins", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	updated, err := service.Update(actor, first.ID, UpdatePluginRepositoryInput{Enabled: &disabled, Revision: first.Revision}, RequestContext{})
	if err != nil || updated.Enabled || updated.Revision != first.Revision+1 {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	if _, err := service.Update(actor, first.ID, UpdatePluginRepositoryInput{Enabled: &disabled, Revision: first.Revision}, RequestContext{}); ErrorCode(err) != CodePluginRepositoryRevision {
		t.Fatalf("stale update error=%v code=%s", err, ErrorCode(err))
	}
	ordered, err := service.Reorder(actor, []PluginRepositoryOrderInput{{ID: second.ID, Revision: second.Revision}, {ID: updated.ID, Revision: updated.Revision}}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 2 || ordered[0].ID != second.ID || ordered[1].ID != first.ID {
		t.Fatalf("ordered=%+v", ordered)
	}
	if err := service.Delete(actor, first.ID, ordered[1].Revision, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	items, err := service.List(actor)
	if err != nil || len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestPluginRepositoryRefreshPreservesLastGoodCacheOnFailure(t *testing.T) {
	service, actor, fetcher := pluginRepositoryFixture(t)
	created, err := service.Create(actor, CreatePluginRepositoryInput{GitHubURL: "https://github.com/ohmycine/official-plugins", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	source := contract.GitHubRepository{Owner: "ohmycine", Name: "official-plugins"}
	registry := pluginTestRegistry(source, "0.1.0")
	fetcher.snapshots["ohmycine/official-plugins"] = pluginrepository.Snapshot{CommitSHA: strings.Repeat("a", 40), Registry: registry}
	refreshed, err := service.Refresh(context.Background(), actor, created.ID, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed.CacheValid || refreshed.PluginCount != 1 || refreshed.LastCommitSHA != strings.Repeat("a", 40) || refreshed.LastRefreshedAt == nil {
		t.Fatalf("refreshed=%+v", refreshed)
	}
	before := *refreshed.LastRefreshedAt
	fetcher.err = &pluginrepository.Error{Code: pluginrepository.CodeRateLimited, Cause: errors.New("rate limited")}
	if _, err := service.Refresh(context.Background(), actor, created.ID, RequestContext{}); ErrorCode(err) != CodePluginRegistryRateLimited {
		t.Fatalf("refresh error=%v code=%s", err, ErrorCode(err))
	}
	var record models.PluginRepository
	if err := service.db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.LastCommitSHA != strings.Repeat("a", 40) || record.CachedRegistryJSON == "" || record.LastErrorCode != pluginrepository.CodeRateLimited || record.LastRefreshedAt == nil || !record.LastRefreshedAt.Equal(before) {
		t.Fatalf("failure replaced last good cache: %+v", record)
	}
	marketplace, err := service.Marketplace(actor)
	if err != nil || len(marketplace) != 1 || marketplace[0].ID != registry.Plugins[0].ID {
		t.Fatalf("marketplace=%+v err=%v", marketplace, err)
	}
}

func TestPluginRepositoryRefreshLogsOneTerminalEvent(t *testing.T) {
	service, actor, fetcher := pluginRepositoryFixture(t)
	var output bytes.Buffer
	service.log = zerolog.New(&output)
	created, err := service.Create(actor, CreatePluginRepositoryInput{GitHubURL: "https://github.com/ohmycine/official-plugins", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	fetcher.err = &pluginrepository.Error{Code: pluginrepository.CodeRateLimited, Cause: errors.New("rate limited")}
	if _, err := service.Refresh(context.Background(), actor, created.ID, RequestContext{}); ErrorCode(err) != CodePluginRegistryRateLimited {
		t.Fatalf("refresh error=%v code=%s", err, ErrorCode(err))
	}
	logs := output.String()
	if strings.Count(logs, "【插件仓库】开始刷新") != 1 || strings.Count(logs, "【插件仓库】刷新失败") != 1 || strings.Contains(logs, "rate limited") {
		t.Fatalf("unexpected refresh logs: %s", logs)
	}
}

func TestPluginMarketplaceRejectsCacheWithoutPinnedCommit(t *testing.T) {
	service, actor, _ := pluginRepositoryFixture(t)
	source := contract.GitHubRepository{Owner: "ohmycine", Name: "official"}
	registry := pluginTestRegistry(source, "0.1.0")
	cached, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	record := models.PluginRepository{
		Name: "Official", GitHubURL: source.CanonicalURL(), GitHubOwner: source.Owner, GitHubRepo: source.Name,
		Enabled: true, Priority: 1000, Revision: 1, CachedRegistryJSON: string(cached), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := service.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	summaries, err := service.List(actor)
	if err != nil || len(summaries) != 1 || summaries[0].CacheValid {
		t.Fatalf("summaries=%+v err=%v", summaries, err)
	}
	marketplace, err := service.Marketplace(actor)
	if err != nil || len(marketplace) != 0 {
		t.Fatalf("marketplace=%+v err=%v", marketplace, err)
	}
}

func TestPluginMarketplaceUsesPriorityAndExposesSourceConflict(t *testing.T) {
	service, actor, fetcher := pluginRepositoryFixture(t)
	first, _ := service.Create(actor, CreatePluginRepositoryInput{Name: "官方", GitHubURL: "https://github.com/ohmycine/official", Enabled: true}, RequestContext{})
	second, _ := service.Create(actor, CreatePluginRepositoryInput{Name: "社区", GitHubURL: "https://github.com/community/plugins", Enabled: true}, RequestContext{})
	firstSource := contract.GitHubRepository{Owner: "ohmycine", Name: "official"}
	secondSource := contract.GitHubRepository{Owner: "community", Name: "plugins"}
	fetcher.snapshots["ohmycine/official"] = pluginrepository.Snapshot{CommitSHA: strings.Repeat("b", 40), Registry: pluginTestRegistry(firstSource, "0.1.0")}
	fetcher.snapshots["community/plugins"] = pluginrepository.Snapshot{CommitSHA: strings.Repeat("c", 40), Registry: pluginTestRegistry(secondSource, "0.2.0")}
	first, _ = service.Refresh(context.Background(), actor, first.ID, RequestContext{})
	second, _ = service.Refresh(context.Background(), actor, second.ID, RequestContext{})
	if _, err := service.Reorder(actor, []PluginRepositoryOrderInput{{ID: second.ID, Revision: second.Revision}, {ID: first.ID, Revision: first.Revision}}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	items, err := service.Marketplace(actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].SourceConflict || items[0].Version != "0.2.0" || len(items[0].Sources) != 2 || !items[0].Sources[0].Selected || items[0].Sources[0].RepositoryName != "社区" {
		t.Fatalf("marketplace=%+v", items)
	}
}

func pluginTestRegistry(source contract.GitHubRepository, version string) contract.Registry {
	base := "https://github.com/" + source.Owner + "/" + source.Name + "/releases/download/v" + version + "/"
	return contract.Registry{
		SchemaVersion: 1,
		Repository:    contract.RepositoryInfo{ID: "org.ohmycine.fixture-repository", Name: "Fixture", Homepage: source.CanonicalURL(), UpdatedAt: time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)},
		Plugins:       []contract.RegistryEntry{{ID: "org.ohmycine.fixture.static-site", Name: "Static Site", Description: "Fixture plugin.", Version: version, Channel: "stable", Categories: []string{"online-media"}, ManifestURL: base + "plugin.json", PackageURL: base + "plugin.omcp", PackageSHA256: strings.Repeat("0", 64), MinServerVersion: "0.1.0"}},
	}
}
