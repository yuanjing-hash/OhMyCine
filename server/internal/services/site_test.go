package services

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	sitepkg "github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

type stubSiteAdapter struct {
	kind            string
	mu              sync.Mutex
	testErr         map[string]error
	searchErr       map[string]error
	downloadErr     error
	downloads       int
	downloadStarted chan struct{}
	downloadRelease chan struct{}
	searchTitle     string
	searchSubtitle  string
	lastConfig      sitepkg.Config
}

type stubResolverAdapter struct {
	*stubSiteAdapter
	resolved   sitepkg.Source
	resolveErr error
}

func (a *stubResolverAdapter) ResolveSource(_ context.Context, _ sitepkg.Config, identity string) (sitepkg.Source, error) {
	if identity != "42" {
		return sitepkg.Source{}, sitepkg.ErrNotFound
	}
	return a.resolved, a.resolveErr
}

func (a *stubSiteAdapter) Kind() string {
	if a.kind == "" {
		return "pttime"
	}
	return a.kind
}
func (a *stubSiteAdapter) Test(_ context.Context, config sitepkg.Config) (sitepkg.Health, error) {
	a.mu.Lock()
	a.lastConfig = config
	a.mu.Unlock()
	if err := a.testErr[config.BaseURL]; err != nil {
		return sitepkg.Health{}, err
	}
	return sitepkg.Health{Status: "online", Username: "fixture-user"}, nil
}

func TestPublicBTAndTorznabCredentialContracts(t *testing.T) {
	service, _, actor, _, _, _ := siteFixture(t)
	publicAdapter := &stubSiteAdapter{kind: "nyaa", testErr: map[string]error{}, searchErr: map[string]error{}}
	torznabAdapter := &stubSiteAdapter{kind: "torznab", testErr: map[string]error{}, searchErr: map[string]error{}}
	service.adapters["nyaa"] = publicAdapter
	service.adapters["torznab"] = torznabAdapter

	public, err := service.Create(context.Background(), actor, SiteInput{Name: "Nyaa", Kind: "nyaa", BaseURL: "https://nyaa.si", Enabled: true, Priority: 100, TimeoutSeconds: 12, RateLimitPerMinute: 12}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if public.SiteType != "bt" || public.CredentialKind != "none" || public.CredentialConfigured {
		t.Fatalf("unexpected public BT summary: %+v", public)
	}
	if _, err := service.Create(context.Background(), actor, SiteInput{Name: "Bad Nyaa", Kind: "nyaa", BaseURL: "https://mirror.example.test", Enabled: true, Priority: 100, TimeoutSeconds: 12, RateLimitPerMinute: 12}, RequestContext{}); ErrorCode(err) != CodeSiteURLInvalid {
		t.Fatalf("custom RSS host accepted: %v", err)
	}

	const secret = "torznab-server-only-secret"
	torznab, err := service.Create(context.Background(), actor, SiteInput{Name: "Prowlarr", Kind: "torznab", BaseURL: "https://prowlarr.example.test", APIKey: secret, Enabled: true, Priority: 110, TimeoutSeconds: 12, RateLimitPerMinute: 12}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if torznab.SiteType != "bt" || torznab.CredentialKind != "api_key" || !torznab.CredentialConfigured {
		t.Fatalf("unexpected Torznab summary: %+v", torznab)
	}
	if torznabAdapter.lastConfig.APIKey != secret {
		t.Fatal("adapter did not receive API key")
	}
	var record models.Site
	if err := service.db.First(&record, torznab.ID).Error; err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(torznab)
	if strings.Contains(record.CredentialCiphertext, secret) || strings.Contains(string(encoded), secret) {
		t.Fatal("Torznab API key leaked")
	}
	credential, err := service.decryptCredential(record)
	if err != nil || credential.APIKey != secret || credential.Cookie != "" || credential.Passkey != "" {
		t.Fatalf("Torznab API key did not round-trip through the site AES-GCM envelope: %+v err=%v", credential, err)
	}
}

func TestBTAddressResolutionDoesNotProbeAndCreateResolvesAgain(t *testing.T) {
	service, _, actor, _, _, _ := siteFixture(t)
	adapter := &stubSiteAdapter{kind: "nyaa", testErr: map[string]error{}, searchErr: map[string]error{}}
	service.adapters["nyaa"] = adapter

	resolved, err := service.ResolveBT(actor, "https://nyaa.si/")
	if err != nil || resolved.Kind != "nyaa" || resolved.CanonicalBaseURL != "https://nyaa.si" || !resolved.Capabilities.Search {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	if adapter.lastConfig.BaseURL != "" {
		t.Fatalf("resolve unexpectedly probed network with config=%+v", adapter.lastConfig)
	}
	groups, err := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "anime", Page: 1})
	if err != nil || len(groups) != 0 || adapter.lastConfig.BaseURL != "" {
		t.Fatalf("unconfigured built-in BT adapter was contacted: groups=%+v config=%+v err=%v", groups, adapter.lastConfig, err)
	}
	created, err := service.Create(context.Background(), actor, SiteInput{Name: "My Nyaa", Kind: "auto_bt", BaseURL: "https://nyaa.si", Enabled: true, Priority: 100, TimeoutSeconds: 12, RateLimitPerMinute: 12}, RequestContext{})
	if err != nil || created.Kind != "nyaa" || created.BaseURL != "https://nyaa.si" || !created.Capabilities.Search || adapter.lastConfig.BaseURL != "https://nyaa.si" {
		t.Fatalf("created=%+v config=%+v err=%v", created, adapter.lastConfig, err)
	}
	if _, err := service.ResolveBT(actor, "https://nyaa.si.evil.test"); ErrorCode(err) != CodeSiteBTHostUnsupported {
		t.Fatalf("lookalike host error=%v", err)
	}
	catalog, err := service.Catalog(actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog {
		if item.Key == "nyaa" {
			t.Fatal("concrete public BT provider leaked into add catalog")
		}
	}
}
func (a *stubSiteAdapter) Search(_ context.Context, config sitepkg.Config, query sitepkg.Query) (sitepkg.Page, error) {
	if err := a.searchErr[config.BaseURL]; err != nil {
		return sitepkg.Page{}, err
	}
	seeders, leechers := 12, 1
	title := a.searchTitle
	if title == "" {
		title = "Seven.Samurai.1954.1080p"
	}
	return sitepkg.Page{Page: query.Page, Items: []sitepkg.Result{{TorrentID: "42", Title: title, Subtitle: a.searchSubtitle, SizeBytes: 1024, Seeders: &seeders, Leechers: &leechers}}, HasNext: true}, nil
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

func TestSiteSummaryReportsExactCredentialFieldsAndFailsClosed(t *testing.T) {
	service, _, actor, _, _, _ := siteFixture(t)
	input := validSiteInput("Cookie only", "https://cookie-only.example.test")
	input.Passkey = ""
	created, err := service.Create(context.Background(), actor, input, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !created.CredentialConfigured || !created.CookieConfigured || created.PasskeyConfigured || created.APIKeyConfigured {
		t.Fatalf("unexpected per-field flags: %+v", created)
	}
	if err := service.db.Model(&models.Site{}).Where("id = ?", created.ID).Update("credential_ciphertext", "not-a-valid-envelope").Error; err != nil {
		t.Fatal(err)
	}
	items, err := service.List(actor)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if !items[0].CredentialConfigured || items[0].CookieConfigured || items[0].PasskeyConfigured || items[0].APIKeyConfigured {
		t.Fatalf("decrypt failure must keep aggregate presence but fail closed per field: %+v", items[0])
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

func TestSiteResultRecognitionUsesServerClaimSharedRecognizerAndDoesNotConsumeDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/movie":
			_, _ = writer.Write([]byte(`{"results":[{"id":346,"title":"七武士","original_title":"Seven Samurai","original_language":"ja","release_date":"1954-04-26","popularity":80}]}`))
		case "/movie/346":
			_, _ = writer.Write([]byte(`{"id":346,"title":"七武士","original_title":"Seven Samurai","original_language":"ja","release_date":"1954-04-26","poster_path":"/seven.jpg","genres":[{"id":18,"name":"剧情"}],"production_countries":[{"iso_3166_1":"JP"}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()

	service, adapter, actor, store, _, downloaders := siteFixture(t)
	metadata := NewMetadataSettingsService(service.db, service.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	}
	service.SetMetadataSettings(metadata)
	adapter.searchTitle = "Seven.Samurai.1954.2160p.UHD.BluRay.REMUX.HDR10.DoVi.x265.DTS-HD.[GROUP]"
	created, err := service.Create(context.Background(), actor, validSiteInput("PTTime", "https://one.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "Seven Samurai", SiteID: &created.ID, Page: 1})
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	token := groups[0].Items[0].Token
	result, err := service.RecognizeResult(context.Background(), actor, token)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != mediaRecognitionStatusMatched || result.Title != "七武士" || result.OriginalTitle != "Seven Samurai" || result.MediaType != "movie" || result.Year == nil || *result.Year != 1954 || result.TMDBID == nil || *result.TMDBID != 346 {
		t.Fatalf("recognition=%+v", result)
	}
	if !strings.HasPrefix(result.PosterURL, "/api/v1/discovery/images/tmdb/") || result.Specifications.Resolution != "2160p" || result.Specifications.Source != "UHD BluRay REMUX" || result.Specifications.VideoCodec != "H.265/HEVC" || result.Specifications.AudioCodec != "DTS-HD" || result.Specifications.HDR != "HDR10 / Dolby Vision" || result.Specifications.ReleaseGroup != "GROUP" {
		t.Fatalf("recognition presentation=%+v", result)
	}
	if adapter.downloads != 0 {
		t.Fatalf("recognition fetched torrent %d time(s)", adapter.downloads)
	}
	if _, err := service.resolveClaim(token, actor.User.ID); err != nil {
		t.Fatalf("recognition consumed claim: %v", err)
	}
	claim, err := service.resolveClaim(token, actor.User.ID)
	if err != nil || claim.ManualTMDBID == nil || *claim.ManualTMDBID != 346 || claim.ManualMediaType != "movie" || claim.RecognitionManual || claim.RecognitionSource != mediaIdentitySourceAutomatic || claim.RecognitionStatus != mediaIdentityStatusVerified || claim.RecognitionLocked {
		t.Fatalf("automatic verified identity was not bound to claim: claim=%+v err=%v", claim, err)
	}
	service.SetMetadataSettings(nil)
	fallback, err := service.RecognizeResult(context.Background(), actor, token)
	if err != nil || fallback.Status != mediaRecognitionStatusUnrecognized || fallback.ErrorCode != mediaRecognitionCredentialMissing || fallback.Title != "Seven Samurai" || fallback.Year == nil || *fallback.Year != 1954 || fallback.PosterURL != "" || fallback.Specifications.Resolution != "2160p" || fallback.Specifications.VideoCodec != "H.265/HEVC" {
		t.Fatalf("metadata-free fallback=%+v err=%v", fallback, err)
	}
	foreign := actor
	foreign.User.ID++
	if _, err := service.RecognizeResult(context.Background(), foreign, token); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("foreign recognition error=%v", err)
	}
	downloader, err := downloaders.Create(actor, DownloaderInput{Name: "Automatic PT qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	download, err := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: token, DownloaderID: downloader.ID}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := service.db.First(&task, "id = ?", download.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.IdentitySource != mediaIdentitySourceAutomatic || task.IdentityStatus != mediaIdentityStatusVerified || task.IdentityLocked || task.IdentityRevision != 1 || task.RecognitionOverrideTMDBID != nil || !strings.Contains(task.IdentitySnapshotJSON, `"tmdb_id":346`) {
		t.Fatalf("automatic identity was promoted to a manual override: %+v", task)
	}
}

func TestMediaIdentitySearchAggregatesAliasesDeduplicatesAndBindsVerifiedIdentity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/movie/346" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"id":346,"title":"七武士","original_title":"Seven Samurai","original_language":"ja","release_date":"1954-04-26","alternative_titles":{"titles":[{"iso_3166_1":"TW","title":"七武士"},{"iso_3166_1":"US","title":"Seven Samurai"}]},"translations":{"translations":[{"iso_639_1":"en","iso_3166_1":"US","data":{"title":"Seven Samurai"}}]}}`))
	}))
	defer upstream.Close()

	service, adapter, actor, store, _, _ := siteFixture(t)
	metadata := NewMetadataSettingsService(service.db, service.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	}
	service.SetMetadataSettings(metadata)
	adapter.searchTitle = "Seven.Samurai.1954.1080p.BluRay.x265-GROUP"
	created, err := service.Create(context.Background(), actor, validSiteInput("PTTime", "https://identity.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var streamedMetadata MediaIdentitySearchResult
	streamedGroups := []SiteSearchGroup{}
	callbackOrder := []string{}
	if err := service.SearchMediaIdentityEach(context.Background(), actor, MediaIdentitySearchInput{MediaType: "movie", TMDBID: 346, SiteID: &created.ID, Page: 1}, func(metadata MediaIdentitySearchResult) {
		streamedMetadata = metadata
		callbackOrder = append(callbackOrder, "media")
	}, func(group SiteSearchGroup) {
		streamedGroups = append(streamedGroups, group)
		callbackOrder = append(callbackOrder, "site")
	}); err != nil {
		t.Fatal(err)
	}
	if len(callbackOrder) != 2 || callbackOrder[0] != "media" || callbackOrder[1] != "site" || streamedMetadata.TMDBID != 346 || len(streamedGroups) != 1 || len(streamedGroups[0].Items) != 1 {
		t.Fatalf("stream metadata=%+v groups=%+v order=%v", streamedMetadata, streamedGroups, callbackOrder)
	}
	result, err := service.SearchMediaIdentity(context.Background(), actor, MediaIdentitySearchInput{MediaType: "movie", TMDBID: 346, SiteID: &created.ID, Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "七武士" || len(result.QueryNames) != 2 || len(result.Groups) != 1 || len(result.Groups[0].Items) != 1 || result.Groups[0].Items[0].MatchedName == "" {
		t.Fatalf("result=%+v", result)
	}
	if streamedMetadata.QueryNames[0] != result.QueryNames[0] || streamedGroups[0].Items[0].Title != result.Groups[0].Items[0].Title || streamedGroups[0].Items[0].MatchedName != result.Groups[0].Items[0].MatchedName {
		t.Fatalf("JSON/SSE service projections diverged: stream=%+v json=%+v", streamedGroups, result.Groups)
	}
	claim, err := service.resolveClaim(result.Groups[0].Items[0].Token, actor.User.ID)
	if err != nil || claim.ManualTMDBID == nil || *claim.ManualTMDBID != 346 || claim.ManualMediaType != "movie" || claim.RecognitionSource != mediaIdentitySourceDirectID || claim.RecognitionStatus != mediaIdentityStatusVerified || claim.RecognitionLocked {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"passkey-server-only", "token=server-only-secret", "https://identity.example.test", `"torrent_id"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("identity search leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestMediaIdentityResultMatchesMultilingualReleaseTitle(t *testing.T) {
	names := []tmdb.SearchName{
		{Value: "迪迦奥特曼", Locale: "zh-CN", Kind: "localized"},
		{Value: "Ultraman Tiga", Locale: "en", Kind: "english"},
		{Value: "ウルトラマンティガ", Locale: "ja", Kind: "original"},
	}
	title := "[DBD-Raws][迪迦奥特曼/Ultraman Tiga/ウルトラマンティガ][01-52TV全集+剧场+OV+特典][1080P][BDRip][HEVC-10bit][FLAC][MKV]"
	if !mediaIdentityResultMatches(title, names, nil, nil) {
		t.Fatalf("multilingual release title was filtered: %q", title)
	}
	if mediaIdentityResultMatches("[DBD-Raws][戴拿奥特曼][01-51][1080P]", names, nil, nil) {
		t.Fatal("unrelated release title matched the verified identity")
	}
	seasonTwo := 2
	if mediaIdentityResultMatches("Ultraman Tiga 1080p WEB-DL", names, nil, &seasonTwo) || mediaIdentityResultMatches("Ultraman Tiga S01 1080p WEB-DL", names, nil, &seasonTwo) {
		t.Fatal("season-scoped identity search accepted a release without the exact season")
	}
	if !mediaIdentityResultMatches("Ultraman Tiga S02 1080p WEB-DL", names, nil, &seasonTwo) {
		t.Fatal("season-scoped identity search rejected the exact season")
	}
}

func TestSiteResultRecognitionReturnsStructuredEpisodeFactsWithoutInventingSpecialEpisodes(t *testing.T) {
	service, adapter, actor, _, _, _ := siteFixture(t)
	created, err := service.Create(context.Background(), actor, validSiteInput("Episode facts", "https://episodes.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(title string) SiteRecognitionSummary {
		adapter.searchTitle = title
		groups, searchErr := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "Ultraman", SiteID: &created.ID, Page: 1})
		if searchErr != nil || len(groups) != 1 || len(groups[0].Items) != 1 {
			t.Fatalf("groups=%+v err=%v", groups, searchErr)
		}
		result, recognizeErr := service.RecognizeResult(context.Background(), actor, groups[0].Items[0].Token)
		if recognizeErr != nil {
			t.Fatal(recognizeErr)
		}
		return result
	}

	complete := lookup("[DBD-Raws][迪迦奥特曼/Ultraman Tiga/ウルトラマンティガ][01-52TV全集+剧场+OV+特典][1080P][BDRip][HEVC-10bit][简体字幕外挂][FLAC][MKV]")
	if complete.EngineVersion != mediarecognition.EngineVersion || complete.MediaType != "tv" || complete.Episodes == nil || complete.Episodes.EpisodeMin == nil || *complete.Episodes.EpisodeMin != 1 || complete.Episodes.EpisodeMax == nil || *complete.Episodes.EpisodeMax != 52 || complete.Episodes.Count != 52 {
		t.Fatalf("complete=%+v", complete)
	}
	payload, err := json.Marshal(complete)
	if err != nil || !strings.Contains(string(payload), `"engine_version":"`+mediarecognition.EngineVersion+`"`) || !strings.Contains(string(payload), `"episode_min":1`) || !strings.Contains(string(payload), `"episode_max":52`) {
		t.Fatalf("payload=%s err=%v", payload, err)
	}

	season := lookup("Ai qing gong yu 2012 S03 2160p WEB-DL H.265 AAC-ZmWeb")
	if season.Episodes == nil || season.Episodes.Season == nil || *season.Episodes.Season != 3 || season.Episodes.SeasonYear == nil || *season.Episodes.SeasonYear != 2012 || season.Episodes.EpisodeMin != nil {
		t.Fatalf("season=%+v", season)
	}

	special := lookup("Ultraman Tiga Gaiden Revival of the Ancient Giant WEB-DL 2160P HEVC AAC-Side")
	if special.Episodes != nil {
		t.Fatalf("special feature was disguised as an ordinary episode: %+v", special)
	}
}

func TestSiteResultRecognitionUsesBoundedSubtitleAndSearchTypeAsWeakContext(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query().Get("query")
		switch request.URL.Path {
		case "/search/movie":
			if query != "Ultraman Tiga The Final Odyssey" {
				_, _ = writer.Write([]byte(`{"results":[]}`))
				return
			}
			_, _ = writer.Write([]byte(`{"results":[{"id":58443,"title":"迪迦奥特曼：最终圣战","original_title":"Ultraman Tiga: The Final Odyssey","original_language":"ja","release_date":"2000-03-11","popularity":30}]}`))
		case "/search/tv":
			_, _ = writer.Write([]byte(`{"results":[]}`))
		case "/movie/58443":
			_, _ = writer.Write([]byte(`{"id":58443,"title":"迪迦奥特曼：最终圣战","original_title":"Ultraman Tiga: The Final Odyssey","original_language":"ja","release_date":"2000-03-11","genres":[{"id":878,"name":"科幻"}],"production_countries":[{"iso_3166_1":"JP"}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()

	service, adapter, actor, store, _, _ := siteFixture(t)
	metadata := NewMetadataSettingsService(service.db, service.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	}
	service.SetMetadataSettings(metadata)
	adapter.searchTitle = "Final.Odyssey.1080p.WEB-DL.H264.AAC-Side"
	adapter.searchSubtitle = "Ultraman Tiga The Final Odyssey"
	created, err := service.Create(context.Background(), actor, validSiteInput("Context", "https://context.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "Final Odyssey", MediaType: "movie", SiteID: &created.ID, Page: 1})
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	claim, err := service.resolveClaim(groups[0].Items[0].Token, actor.User.ID)
	if err != nil || claim.Subtitle != adapter.searchSubtitle || claim.MediaTypeHint != "movie" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	result, err := service.RecognizeResult(context.Background(), actor, groups[0].Items[0].Token)
	if err != nil || result.Status != mediaRecognitionStatusMatched || result.TMDBID == nil || *result.TMDBID != 58443 || result.MediaType != "movie" {
		t.Fatalf("recognition=%+v err=%v", result, err)
	}

	if got := safeRecognitionClaimSubtitle("https://example.invalid/private"); got != "" {
		t.Fatalf("URL-like subtitle entered recognition context: %q", got)
	}
	if got := safeRecognitionClaimSubtitle("C:/private/media/title.mkv"); got != "" {
		t.Fatalf("path-like subtitle entered recognition context: %q", got)
	}
	if got := safeRecognitionClaimSubtitle("Title token=private"); got != "" {
		t.Fatalf("credential-like subtitle entered recognition context: %q", got)
	}
	if got := safeRecognitionClaimSubtitle("Title [tmdbid=58443]"); got != "" {
		t.Fatalf("untrusted direct hint entered recognition context: %q", got)
	}
	if got := safeRecognitionMediaTypeHint("anime"); got != "" {
		t.Fatalf("unsupported media type entered recognition context: %q", got)
	}
}

func TestSiteManualRecognitionSearchesSafeCandidatesAndBindsVerifiedIdentityToDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/movie":
			if request.URL.Query().Get("query") != "The Final Odyssey" {
				t.Fatalf("query=%q", request.URL.Query().Get("query"))
			}
			_, _ = writer.Write([]byte(`{"results":[{"id":58443,"title":"迪迦奥特曼：最终圣战","original_title":"Ultraman Tiga: The Final Odyssey","original_language":"ja","release_date":"2000-03-11","poster_path":"/final.jpg","popularity":30}]}`))
		case "/movie/58443":
			_, _ = writer.Write([]byte(`{"id":58443,"title":"迪迦奥特曼：最终圣战","original_title":"Ultraman Tiga: The Final Odyssey","original_language":"ja","release_date":"2000-03-11","poster_path":"/final.jpg","genres":[{"id":878,"name":"科幻"}],"production_countries":[{"iso_3166_1":"JP"}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()

	service, adapter, actor, store, _, downloaders := siteFixture(t)
	metadata := NewMetadataSettingsService(service.db, service.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", upstream.URL, upstream.Client())
	}
	service.SetMetadataSettings(metadata)
	adapter.searchTitle = "The Final Odyssey 1080p WEB-DL H264 AAC-Side"
	created, err := service.Create(context.Background(), actor, validSiteInput("PTTime", "https://one.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "The Final Odyssey", SiteID: &created.ID, Page: 1})
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	token := groups[0].Items[0].Token
	foreign := actor
	foreign.User.ID++
	if _, err := service.RecognitionCandidates(context.Background(), foreign, token, "The Final Odyssey", "movie", nil); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("foreign candidate search err=%v", err)
	}
	candidates, err := service.RecognitionCandidates(context.Background(), actor, token, "The Final Odyssey", "movie", nil)
	if err != nil || len(candidates) != 1 || candidates[0].ID != 58443 || !strings.HasPrefix(candidates[0].PosterURL, "/api/v1/discovery/images/tmdb/") {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	verified, err := service.OverrideResultRecognition(context.Background(), actor, SiteManualRecognitionInput{ResultToken: token, TMDBID: 58443, MediaType: "movie"})
	if err != nil || verified.Status != mediaRecognitionStatusMatched || !verified.ManualOverride || verified.TMDBID == nil || *verified.TMDBID != 58443 || verified.Title != "迪迦奥特曼：最终圣战" {
		t.Fatalf("verified=%+v err=%v", verified, err)
	}
	claim, err := service.resolveClaim(token, actor.User.ID)
	if err != nil || claim.ManualTMDBID == nil || *claim.ManualTMDBID != 58443 || claim.ManualMediaType != "movie" || claim.TorrentID != "42" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}

	downloader, err := downloaders.Create(actor, DownloaderInput{Name: "Manual PT qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: token, DownloaderID: downloader.ID}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := service.db.First(&task, "id = ?", result.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.RecognitionOverrideTMDBID == nil || *task.RecognitionOverrideTMDBID != 58443 || task.RecognitionOverrideMediaType != "movie" {
		t.Fatalf("manual identity did not cross the claim/download boundary: %+v", task)
	}
	encoded, _ := json.Marshal(candidates)
	for _, forbidden := range []string{"torrent_id", "provider", "passkey", "final.jpg"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("candidate response leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSiteRecognitionRejectsReservedClaimBeforeMetadataLookup(t *testing.T) {
	service, _, actor, _, _, _ := siteFixture(t)
	token, err := service.issueClaim(siteResultClaim{
		ActorID:   actor.User.ID,
		SiteID:    1,
		TorrentID: "private-torrent-id",
		Title:     "Reserved Result",
		ExpiresAt: service.now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.reserveClaim(token, actor.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecognizeResult(context.Background(), actor, token); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("quick recognition accepted an in-flight claim: %v", err)
	}
	if _, err := service.RecognitionCandidates(context.Background(), actor, token, "Reserved Result", "movie", nil); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("candidate search accepted an in-flight claim: %v", err)
	}
	if _, err := service.OverrideResultRecognition(context.Background(), actor, SiteManualRecognitionInput{ResultToken: token, TMDBID: 1, MediaType: "movie"}); ErrorCode(err) != CodeSiteResultExpired {
		t.Fatalf("manual override accepted an in-flight claim: %v", err)
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

func TestBTResolverUsesExistingDownloadPipelineWithoutPublicSourceLeak(t *testing.T) {
	service, _, actor, _, _, downloaders := siteFixture(t)
	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	adapter := &stubResolverAdapter{stubSiteAdapter: &stubSiteAdapter{kind: "nyaa", testErr: map[string]error{}, searchErr: map[string]error{}}, resolved: sitepkg.Source{Magnet: magnet}}
	service.adapters["nyaa"] = adapter
	created, err := service.Create(context.Background(), actor, SiteInput{Name: "Nyaa", Kind: "nyaa", BaseURL: "https://nyaa.si", Enabled: true, Priority: 100, TimeoutSeconds: 12, RateLimitPerMinute: 120}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	groups, err := service.Search(context.Background(), actor, SiteSearchInput{Keyword: "Seven Samurai", SiteID: &created.ID, Page: 1})
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 1 || groups[0].SiteType != "bt" {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	public, _ := json.Marshal(groups)
	if strings.Contains(string(public), "magnet:") || strings.Contains(string(public), "0123456789abcdef") {
		t.Fatal("BT source leaked into search DTO")
	}
	downloader, err := downloaders.Create(actor, DownloaderInput{Name: "BT qBit", Type: models.DownloaderTypeQBittorrent, BaseURL: "http://qbit.example.test", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: groups[0].Items[0].Token, DownloaderID: downloader.ID, Priority: 10}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	var task models.DownloadTask
	if err := service.db.First(&task, "id = ?", result.ID).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := service.downloads.credentials.Decrypt(downloadSourcePurpose(task.ID), task.SourceCiphertext)
	if err != nil {
		t.Fatal(err)
	}
	var source downloadSourceEnvelope
	if json.Unmarshal([]byte(raw), &source) != nil || source.Kind != downloadpkg.SourceURL || source.URL != magnet {
		t.Fatalf("unexpected encrypted source: %+v", source)
	}
	groups, err = service.Search(context.Background(), actor, SiteSearchInput{Keyword: "Seven Samurai", SiteID: &created.ID, Page: 1})
	if err != nil || len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("second search groups=%+v err=%v", groups, err)
	}
	adapter.resolved = sitepkg.Source{Magnet: magnet, Torrent: []byte("d4:infod4:name7:samuraiee"), Filename: "both.torrent"}
	if _, err := service.Download(context.Background(), actor, SiteDownloadInput{ResultToken: groups[0].Items[0].Token, DownloaderID: downloader.ID}, RequestContext{}); ErrorCode(err) != CodeSiteResponseInvalid {
		t.Fatalf("ambiguous resolver source err=%v", err)
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
