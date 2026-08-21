package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

func TestProviderDirectoryTokensBindActorConnectionPurposeAndExpiry(t *testing.T) {
	driver := &fakeCloudDriver{items: map[string]cloud.Item{"movies": {ID: "movies", ParentID: "0", Name: "电影", IsDir: true}}, children: map[string][]cloud.Item{"0": {{ID: "movies", ParentID: "0", Name: "电影", IsDir: true}, {ID: "video", ParentID: "0", Name: "x.mkv"}}}}
	db, store, connections, actor := newConnectionTestService(t, driver)
	first, err := connections.Create(actor, ConnectionInput{Name: "Account A", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := connections.Create(actor, ConnectionInput{Name: "Account B", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewProviderDirectoryService(connections, store)
	clock := time.Now().UTC()
	service.now = func() time.Time { return clock }
	listing, err := service.Browse(context.Background(), actor, first.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 || listing.Items[0].Location != "/电影" || listing.Items[0].SelectionToken == "" {
		t.Fatalf("unexpected listing: %+v", listing)
	}
	if listing.Breadcrumbs == nil {
		t.Fatal("provider directory listing must serialize breadcrumbs as an empty array")
	}
	selection, err := service.ResolveSelection(context.Background(), actor, first.ID, listing.Items[0].SelectionToken)
	if err != nil || selection.ProviderID != "movies" || selection.DisplayPath != "/电影" {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	if _, err := service.ResolveSelection(context.Background(), actor, second.ID, listing.Items[0].SelectionToken); ErrorCode(err) != CodeDirectoryTokenInvalid {
		t.Fatalf("connection binding code=%q err=%v", ErrorCode(err), err)
	}
	replacement := byte('A')
	if listing.Items[0].SelectionToken[0] == replacement {
		replacement = 'B'
	}
	tampered := string(replacement) + listing.Items[0].SelectionToken[1:]
	if _, err := service.ResolveSelection(context.Background(), actor, first.ID, tampered); ErrorCode(err) != CodeDirectoryTokenInvalid {
		t.Fatalf("tamper code=%q err=%v", ErrorCode(err), err)
	}
	other := actor
	other.User = models.User{ID: actor.User.ID + 100}
	if _, err := service.ResolveSelection(context.Background(), other, first.ID, listing.Items[0].SelectionToken); ErrorCode(err) != CodeDirectoryTokenInvalid {
		t.Fatalf("actor binding code=%q err=%v", ErrorCode(err), err)
	}
	clock = clock.Add(11 * time.Minute)
	if _, err := service.ResolveSelection(context.Background(), actor, first.ID, listing.Items[0].SelectionToken); ErrorCode(err) != CodeDirectoryTokenExpired {
		t.Fatalf("expiry code=%q err=%v", ErrorCode(err), err)
	}
	_ = db
}

func TestProviderDirectoryStorageScopeBindsRootAndRejectsMovedDirectory(t *testing.T) {
	driver := &fakeCloudDriver{
		items: map[string]cloud.Item{
			"media":  {ID: "media", ParentID: "0", Name: "媒体", IsDir: true},
			"tv":     {ID: "tv", ParentID: "media", Name: "剧集", IsDir: true},
			"season": {ID: "season", ParentID: "tv", Name: "第一季", IsDir: true},
			"other":  {ID: "other", ParentID: "0", Name: "其他", IsDir: true},
		},
		children: map[string][]cloud.Item{
			"media": {{ID: "tv", ParentID: "media", Name: "剧集", IsDir: true}},
			"tv":    {{ID: "season", ParentID: "tv", Name: "第一季", IsDir: true}},
		},
	}
	db, store, connections, actor := newConnectionTestService(t, driver)
	firstConnection, err := connections.Create(actor, ConnectionInput{Name: "Scoped Account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	secondConnection, err := connections.Create(actor, ConnectionInput{Name: "Other Account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	storageService := NewStorageService(db, NewAuditService(db))
	storageService.SetConnectionService(connections)
	first, err := storageService.CreateContext(context.Background(), actor, StorageInput{Name: "Scoped Storage", Type: models.StorageTypePan115, RootPath: "media", RootDisplayPath: "/媒体", ConnectionID: &firstConnection.ID, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := storageService.CreateContext(context.Background(), actor, StorageInput{Name: "Other Storage", Type: models.StorageTypePan115, RootPath: "other", RootDisplayPath: "/其他", ConnectionID: &secondConnection.ID, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewProviderDirectoryService(connections, store)
	clock := time.Now().UTC()
	service.now = func() time.Time { return clock }
	root, err := service.BrowseStorage(context.Background(), actor, first.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if root.Location != "/" || root.ParentToken != "" || len(root.Items) != 1 || root.Items[0].Location != "/剧集" {
		t.Fatalf("root listing=%+v", root)
	}
	tv, err := service.BrowseStorage(context.Background(), actor, first.ID, root.Items[0].Token, "")
	if err != nil {
		t.Fatal(err)
	}
	if tv.Location != "/剧集" || tv.ParentToken == "" || len(tv.Items) != 1 || tv.Items[0].Location != "/剧集/第一季" {
		t.Fatalf("tv listing=%+v", tv)
	}
	selection, err := service.ResolveStorageSelection(context.Background(), actor, first.ID, tv.Items[0].SelectionToken)
	if err != nil || selection.ProviderID != "season" || selection.RelativeRoot != "/剧集/第一季" {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	if _, err := service.ResolveStorageSelection(context.Background(), actor, second.ID, tv.Items[0].SelectionToken); ErrorCode(err) != CodeDirectoryTokenInvalid {
		t.Fatalf("cross storage/connection code=%q err=%v", ErrorCode(err), err)
	}
	otherActor := actor
	otherActor.User = models.User{ID: actor.User.ID + 1}
	if _, err := service.ResolveStorageSelection(context.Background(), otherActor, first.ID, tv.Items[0].SelectionToken); ErrorCode(err) != CodeDirectoryTokenInvalid {
		t.Fatalf("cross actor code=%q err=%v", ErrorCode(err), err)
	}
	driver.items["season"] = cloud.Item{ID: "season", ParentID: "0", Name: "第一季", IsDir: true}
	if _, err := service.ResolveStorageSelection(context.Background(), actor, first.ID, tv.Items[0].SelectionToken); ErrorCode(err) != CodeMediaLibraryPathInvalid {
		t.Fatalf("moved outside code=%q err=%v", ErrorCode(err), err)
	}
	driver.items["season"] = cloud.Item{ID: "season", ParentID: "tv", Name: "第一季", IsDir: true}
	clock = clock.Add(11 * time.Minute)
	if _, err := service.ResolveStorageSelection(context.Background(), actor, first.ID, tv.Items[0].SelectionToken); ErrorCode(err) != CodeDirectoryTokenExpired {
		t.Fatalf("expired storage token code=%q err=%v", ErrorCode(err), err)
	}
}

func TestProviderDirectoryBrowseUsesOpaqueSinglePageCursor(t *testing.T) {
	children := make([]cloud.Item, 0, 250)
	items := make(map[string]cloud.Item, 250)
	for index := 0; index < 250; index++ {
		id := "dir-" + uintID(uint(index+1))
		item := cloud.Item{ID: id, ParentID: "0", Name: "目录 " + uintID(uint(index+1)), IsDir: true}
		children = append(children, item)
		items[id] = item
	}
	driver := &fakeCloudDriver{items: items, children: map[string][]cloud.Item{"0": children}}
	_, store, connections, actor := newConnectionTestService(t, driver)
	connection, err := connections.Create(actor, ConnectionInput{Name: "Paged Account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewProviderDirectoryService(connections, store)
	first, err := service.Browse(context.Background(), actor, connection.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 200 || first.NextPageToken == "" {
		t.Fatalf("first page items=%d next=%t", len(first.Items), first.NextPageToken != "")
	}
	second, err := service.Browse(context.Background(), actor, connection.ID, "", first.NextPageToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 50 || second.NextPageToken != "" || second.Items[0].Name != "目录 201" {
		t.Fatalf("unexpected second page: items=%d next=%t first=%+v", len(second.Items), second.NextPageToken != "", second.Items[0])
	}
	if _, err := service.Browse(context.Background(), actor, connection.ID, first.CurrentToken, first.NextPageToken); err != nil {
		t.Fatalf("page cursor must carry its own directory context: %v", err)
	}
	replacement := byte('A')
	if first.NextPageToken[0] == replacement {
		replacement = 'B'
	}
	tampered := string(replacement) + first.NextPageToken[1:]
	if _, err := service.Browse(context.Background(), actor, connection.ID, "", tampered); ErrorCode(err) != CodeDirectoryTokenInvalid {
		t.Fatalf("tampered page code=%q err=%v", ErrorCode(err), err)
	}
}

func TestPan115StorageUsesConnectionScopedStableRootIdentity(t *testing.T) {
	driver := &fakeCloudDriver{items: map[string]cloud.Item{"movies": {ID: "movies", ParentID: "0", Name: "电影", IsDir: true}}}
	_, _, connections, actor := newConnectionTestService(t, driver)
	first, _ := connections.Create(actor, ConnectionInput{Name: "Account A", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	second, _ := connections.Create(actor, ConnectionInput{Name: "Account B", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	storage := NewStorageService(connections.db, connections.audit)
	storage.SetConnectionService(connections)
	created, err := storage.CreateContext(context.Background(), actor, StorageInput{Name: "115 电影 A", Type: models.StorageTypePan115, RootPath: "movies", RootDisplayPath: "/电影", ConnectionID: &first.ID, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if created.Type != models.StorageTypePan115 || created.RootPath != "movies" || created.RootDisplayPath != "/电影" || created.ConnectionID == nil {
		t.Fatalf("unexpected storage: %+v", created)
	}
	if _, err := storage.CreateContext(context.Background(), actor, StorageInput{Name: "Duplicate", Type: models.StorageTypePan115, RootPath: "movies", RootDisplayPath: "/电影", ConnectionID: &first.ID, Enabled: true}, RequestContext{}); ErrorCode(err) != CodeStoragePathConflict {
		t.Fatalf("same connection root code=%q err=%v", ErrorCode(err), err)
	}
	other, err := storage.CreateContext(context.Background(), actor, StorageInput{Name: "115 电影 B", Type: models.StorageTypePan115, RootPath: "movies", RootDisplayPath: "/电影", ConnectionID: &second.ID, Enabled: true}, RequestContext{})
	if err != nil || other.ID == created.ID {
		t.Fatalf("different connection root=%+v err=%v", other, err)
	}
	var records []models.Storage
	if err := connections.db.Order("id").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].RootPathNormalized == records[1].RootPathNormalized || !strings.HasPrefix(records[0].RootPathNormalized, "pan115:") {
		t.Fatalf("root identities=%+v", records)
	}
}
