package services

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	sitepkg "github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

type stubSiteAdapter struct {
	mu              sync.Mutex
	testErr         map[string]error
	searchErr       map[string]error
	downloadErr     error
	downloads       int
	downloadStarted chan struct{}
	downloadRelease chan struct{}
}

func (*stubSiteAdapter) Kind() string { return "pttime" }
func (a *stubSiteAdapter) Test(_ context.Context, config sitepkg.Config) (sitepkg.Health, error) {
	if err := a.testErr[config.BaseURL]; err != nil {
		return sitepkg.Health{}, err
	}
	return sitepkg.Health{Status: "online", Username: "fixture-user"}, nil
}
func (a *stubSiteAdapter) Search(_ context.Context, config sitepkg.Config, query sitepkg.Query) (sitepkg.Page, error) {
	if err := a.searchErr[config.BaseURL]; err != nil {
		return sitepkg.Page{}, err
	}
	seeders, leechers := 12, 1
	return sitepkg.Page{Page: query.Page, Items: []sitepkg.Result{{TorrentID: "42", Title: "Seven.Samurai.1954.1080p", SizeBytes: 1024, Seeders: &seeders, Leechers: &leechers}}, HasNext: true}, nil
}
func (a *stubSiteAdapter) Download(_ context.Context, _ sitepkg.Config, torrentID string) ([]byte, string, error) {
	if a.downloadStarted != nil {
		select {
		case a.downloadStarted <- struct{}{}:
		default:
		}
	}
	if a.downloadRelease != nil {
		<-a.downloadRelease
	}
	if a.downloadErr != nil {
		return nil, "", a.downloadErr
	}
	if torrentID != "42" {
		return nil, "", sitepkg.ErrNotFound
	}
	a.mu.Lock()
	a.downloads++
	a.mu.Unlock()
	return []byte("d4:infod4:name4:testee"), "seven-samurai.torrent", nil
}

func siteFixture(t *testing.T) (*SiteService, *stubSiteAdapter, Actor, *credential.Store, *DownloadService, *DownloaderService) {
	t.Helper()
	downloads, downloaders, queue, actor, _ := downloadFixture(t)
	actor.Permissions[authz.PermissionSystemAdmin] = struct{}{}
	actor.Permissions[authz.PermissionDiscoveryRead] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "site-credentials.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	adapter := &stubSiteAdapter{testErr: map[string]error{}, searchErr: map[string]error{}}
	stagingRoot := t.TempDir()
	staging := models.Storage{Name: "PT staging", NameNormalized: "pt-staging", Type: models.StorageTypeLocal, RootPath: stagingRoot, RootPathNormalized: strings.ToLower(stagingRoot), Enabled: true, Capabilities: `{}`}
	if err := queue.db.Create(&staging).Error; err != nil {
		t.Fatal(err)
	}
	configureDownloadStaging(t, queue, staging.ID)
	service := NewSiteServiceWithAdapters(queue.db, queue.audit, store, downloads, []sitepkg.Adapter{adapter}, zerolog.Nop())
	return service, adapter, actor, store, downloads, downloaders
}

func validSiteInput(name, baseURL string) SiteInput {
	return SiteInput{Name: name, Kind: "pttime", BaseURL: baseURL, Cookie: "uid=1; token=server-only-secret", Passkey: "passkey-server-only", Enabled: true, Priority: 100, TimeoutSeconds: 12, RateLimitPerMinute: 120}
}

func TestSiteCredentialsAreEncryptedAndRedacted(t *testing.T) {
	service, _, actor, store, _, _ := siteFixture(t)
	created, err := service.Create(context.Background(), actor, validSiteInput("PTTime", "https://one.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var record models.Site
	if err := service.db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.CredentialCiphertext == "" || strings.Contains(record.CredentialCiphertext, "server-only") {
		t.Fatalf("credential was not encrypted: %q", record.CredentialCiphertext)
	}
	plaintext, err := store.Decrypt(siteCredentialPurpose(record.ID, record.Kind), record.CredentialCiphertext)
	if err != nil || !strings.Contains(plaintext, "server-only-secret") {
		t.Fatalf("decrypt err=%v plaintext=%q", err, plaintext)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"server-only-secret", "passkey-server-only", "credential_ciphertext", "Cookie", "Passkey"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"base_url"`) || strings.Contains(string(encoded), `"BaseURL"`) {
		t.Fatalf("summary uses unstable JSON contract: %s", encoded)
	}
}

func TestSiteCandidateUpdateFailureRetainsOldCredential(t *testing.T) {
	service, adapter, actor, _, _, _ := siteFixture(t)
	created, err := service.Create(context.Background(), actor, validSiteInput("PTTime", "https://one.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var before models.Site
	if err := service.db.First(&before, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	failedURL := "https://failed.example.test"
	badCookie := "uid=2; token=rejected"
	adapter.testErr[failedURL] = sitepkg.ErrAuthentication
	if _, err := service.Update(context.Background(), actor, created.ID, SiteUpdateInput{BaseURL: &failedURL, Cookie: &badCookie, Revision: created.Revision}, RequestContext{}); ErrorCode(err) != CodeSiteAuthentication {
		t.Fatalf("err=%v code=%s", err, ErrorCode(err))
	}
	var after models.Site
	if err := service.db.First(&after, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.BaseURL != before.BaseURL || after.CredentialCiphertext != before.CredentialCiphertext || after.Revision != before.Revision {
		t.Fatalf("candidate failure mutated record: before=%+v after=%+v", before, after)
	}
}

func TestSiteCanDisableWhileUnavailableButMustProbeBeforeReenable(t *testing.T) {
	service, adapter, actor, _, _, _ := siteFixture(t)
	created, err := service.Create(context.Background(), actor, validSiteInput("PTTime", "https://one.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	adapter.testErr[created.BaseURL] = sitepkg.ErrUnavailable
	disabled := false
	updated, err := service.Update(context.Background(), actor, created.ID, SiteUpdateInput{Enabled: &disabled, Revision: created.Revision}, RequestContext{})
	if err != nil || updated.Enabled || updated.Revision != created.Revision+1 {
		t.Fatalf("disable result=%+v err=%v", updated, err)
	}
	enabled := true
	if _, err := service.Update(context.Background(), actor, created.ID, SiteUpdateInput{Enabled: &enabled, Revision: updated.Revision}, RequestContext{}); ErrorCode(err) != CodeSiteUnavailable {
		t.Fatalf("reenable without successful probe err=%v", err)
	}
	var record models.Site
	if err := service.db.First(&record, created.ID).Error; err != nil || record.Enabled {
		t.Fatalf("failed reenable mutated site: enabled=%v err=%v", record.Enabled, err)
	}
}

func TestSiteSearchIsolatesFailuresAndBindsClaimsToActor(t *testing.T) {
	service, adapter, actor, _, _, _ := siteFixture(t)
	for _, input := range []SiteInput{validSiteInput("Working", "https://working.example.test"), validSiteInput("Broken", "https://broken.example.test")} {
		if _, err := service.Create(context.Background(), actor, input, RequestContext{}); err != nil {
			t.Fatal(err)
		}
	}
	adapter.searchErr["https://broken.example.test"] = sitepkg.ErrUnavailable
	groups, err := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "七武士", Page: 1})
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	var token string
	statuses := map[string]string{}
	for _, group := range groups {
		statuses[group.SiteName] = group.Status
		if len(group.Items) == 1 {
			token = group.Items[0].Token
		}
	}
	if statuses["Working"] != "success" || statuses["Broken"] != "error" || token == "" {
		t.Fatalf("statuses=%v token=%q", statuses, token)
	}
	if _, err := service.resolveClaim(token, actor.User.ID+1); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("cross-actor token err=%v", err)
	}
	service.now = func() time.Time { return time.Now().UTC().Add(ptResultTTL + time.Second) }
	if _, err := service.resolveClaim(token, actor.User.ID); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("expired token err=%v", err)
	}
}

func TestSiteDownloadUsesExistingDownloadPipelineAndConsumesToken(t *testing.T) {
	service, adapter, actor, _, _, downloaders := siteFixture(t)
	created, err := service.Create(context.Background(), actor, validSiteInput("PTTime", "https://one.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "Seven Samurai", SiteID: &created.ID, Page: 1})
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	downloader, err := downloaders.Create(actor, DownloaderInput{Name: "PT qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	token := groups[0].Items[0].Token
	adapter.downloadErr = sitepkg.ErrUnavailable
	if _, err := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: token, DownloaderID: downloader.ID, Priority: 10}, RequestContext{}); ErrorCode(err) != CodeSiteUnavailable {
		t.Fatalf("failed provider call err=%v", err)
	}
	adapter.downloadErr = nil
	result, err := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: token, DownloaderID: downloader.ID, Priority: 10}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := service.db.First(&task, "id = ?", result.ID).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := service.downloads.credentials.Decrypt(downloadSourcePurpose(task.ID), task.SourceCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	var source downloadSourceEnvelope
	if err := json.Unmarshal([]byte(plaintext), &source); err != nil {
		t.Fatal(err)
	}
	if source.Kind != downloadpkg.SourceTorrent || task.JobID == "" || strings.Contains(task.SourceCiphertext, "passkey-server-only") {
		t.Fatalf("download did not enter normal pipeline: %+v", task)
	}
	if adapter.downloads != 1 {
		t.Fatalf("downloads=%d", adapter.downloads)
	}
	if _, err := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: token, DownloaderID: downloader.ID}, RequestContext{}); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("reused token err=%v", err)
	}
}

func TestSiteDownloadClaimAllowsOnlyOneConcurrentConsumer(t *testing.T) {
	service, adapter, actor, _, _, downloaders := siteFixture(t)
	created, err := service.Create(context.Background(), actor, validSiteInput("PTTime", "https://one.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "Seven Samurai", SiteID: &created.ID, Page: 1})
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	downloader, err := downloaders.Create(actor, DownloaderInput{Name: "Concurrent PT qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	adapter.downloadStarted = make(chan struct{}, 1)
	adapter.downloadRelease = make(chan struct{})
	token := groups[0].Items[0].Token
	firstDone := make(chan error, 1)
	go func() {
		_, downloadErr := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: token, DownloaderID: downloader.ID}, RequestContext{})
		firstDone <- downloadErr
	}()
	select {
	case <-adapter.downloadStarted:
	case <-time.After(time.Second):
		t.Fatal("first consumer did not reserve claim")
	}
	if _, err := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: token, DownloaderID: downloader.ID}, RequestContext{}); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("concurrent consumer err=%v", err)
	}
	close(adapter.downloadRelease)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first consumer did not finish")
	}
	if adapter.downloads != 1 {
		t.Fatalf("provider download calls=%d", adapter.downloads)
	}
}

func TestSiteAdapterErrorsAreStable(t *testing.T) {
	for err, code := range map[error]string{sitepkg.ErrAuthentication: CodeSiteAuthentication, sitepkg.ErrRateLimited: CodeSiteRateLimited, sitepkg.ErrInvalidReply: CodeSiteResponseInvalid, sitepkg.ErrUnavailable: CodeSiteUnavailable} {
		if got := ErrorCode(siteAdapterError(err, "failed")); got != code {
			t.Fatalf("err=%v got=%s want=%s", err, got, code)
		}
	}
	if !errors.Is(sitepkg.ErrUnavailable, sitepkg.ErrUnavailable) {
		t.Fatal("sentinel sanity")
	}
}
