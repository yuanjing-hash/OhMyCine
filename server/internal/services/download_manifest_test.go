package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

func TestDownloadSearchTitlesStripsReleaseSuffixFromSevenSamurai(t *testing.T) {
	candidates := downloadSearchTitles("Seven.Samurai.1954.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD.mkv")
	if len(candidates) == 0 || candidates[0] != "Seven Samurai 1954" {
		t.Fatalf("candidates=%v", candidates)
	}
}

func TestDownloadSearchTitlesUsesRealReleaseFolderWithoutBreakingTitleHyphens(t *testing.T) {
	folder := "【高清影视之家发布 www.HDBTHD.com】七武士[简繁英字幕].Seven.Samurai.1954.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD"
	candidates := downloadSearchTitles(folder)
	if len(candidates) < 2 || candidates[0] != "七武士" || candidates[1] != "Seven Samurai 1954" {
		t.Fatalf("candidates=%v", candidates)
	}
	hyphenated := downloadSearchTitles("Spider-Man.2002.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD.mkv")
	if len(hyphenated) == 0 || hyphenated[0] != "Spider-Man 2002" {
		t.Fatalf("hyphenated=%v", hyphenated)
	}
}

func TestDownloadSearchTitlesCleansMingDynastyReleaseWithoutDroppingNumericTitle(t *testing.T) {
	candidates := downloadSearchTitles("Ming Dynasty in 1566 HQ -BlackTV")
	if len(candidates) == 0 || candidates[0] != "Ming Dynasty in 1566" {
		t.Fatalf("candidates=%v", candidates)
	}

	files := make([]downloadpkg.File, 0, 49)
	for episode := 1; episode <= 49; episode++ {
		files = append(files, downloadpkg.File{
			RelativePath: fmt.Sprintf("Ming Dynasty in 1566 HQ -BlackTV/Ming.Dynasty.in.1566.S01E%02d.HQ-BlackTV.mkv", episode),
			Size:         6 * 1024 * 1024 * 1024,
		})
	}
	manifest := downloadpkg.Manifest{Name: "Ming Dynasty in 1566 HQ -BlackTV", Complete: true, Files: files}
	recognition := downloadRecognitionCandidates(manifest, nil)
	if len(recognition) == 0 || recognition[0].Title != "Ming Dynasty in 1566" || recognition[0].MediaType != "tv" || recognition[0].Year != nil {
		t.Fatalf("recognition=%+v", recognition)
	}
}

func TestSelectDownloadPackageManifestRejectsAdvertisementVideos(t *testing.T) {
	mainPath := "Seven Samurai CC MA 2 0 SONYHD/Seven Samurai CC MA 2 0 SONYHD.mkv"
	manifest := downloadpkg.Manifest{
		Name:     "Seven Samurai CC MA 2 0 SONYHD",
		Complete: true,
		Files: []downloadpkg.File{
			{RelativePath: mainPath, Size: 285 * 1024 * 1024 * 1024 / 10, ProviderItemID: "main"},
			{RelativePath: "【更多无水印蓝光原盘请访问 www.example.invalid】/ad-one.mp4", Size: 622 * 1024, ProviderItemID: "ad-one"},
			{RelativePath: "【更多无水印高清电影请访问 www.example.invalid】/ad-two.mp4", Size: 289 * 1024, ProviderItemID: "ad-two"},
			{RelativePath: "Seven Samurai CC MA 2 0 SONYHD/Seven Samurai CC MA 2 0 SONYHD.zh-CN.ass", Size: 128 * 1024, ProviderItemID: "subtitle"},
			{RelativePath: "【更多无水印高清电影请访问 www.example.invalid】/poster.jpg", Size: 64 * 1024, ProviderItemID: "ad-poster"},
		},
	}
	selected, err := selectDownloadPackageManifest(manifest, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Files) != 2 || selected.Files[0].RelativePath != mainPath || !strings.HasSuffix(selected.Files[1].RelativePath, ".ass") {
		t.Fatalf("selected=%+v", selected.Files)
	}
}

func TestCompletedDownloadManifestIsBoundedCanonicalAndPrivate(t *testing.T) {
	valid := downloadpkg.Manifest{Name: "Series", Complete: true, Files: []downloadpkg.File{{RelativePath: "Series/Series.S01E1210.mkv", Size: 1024, ProviderItemID: "item-1"}}}
	raw, err := encodeCompletedDownloadManifest(valid)
	if err != nil {
		t.Fatal(err)
	}
	decoded, exists, err := completedDownloadManifest(raw)
	if err != nil || !exists || len(decoded.Files) != 1 || decoded.Files[0].RelativePath != valid.Files[0].RelativePath {
		t.Fatalf("decoded=%+v exists=%v err=%v", decoded, exists, err)
	}
	for _, manifest := range []downloadpkg.Manifest{
		{Name: "unsafe", Complete: true, Files: []downloadpkg.File{{RelativePath: "../escape.mkv", Size: 1}}},
		{Name: "duplicate", Complete: true, Files: []downloadpkg.File{{RelativePath: "same.mkv", Size: 1}, {RelativePath: "same.mkv", Size: 1}}},
		{Name: "incomplete", Complete: false, Files: []downloadpkg.File{{RelativePath: "Movie.mkv", Size: 1}}},
	} {
		if _, err := encodeCompletedDownloadManifest(manifest); ErrorCode(err) != "download_completion_manifest_invalid" {
			t.Fatalf("manifest=%+v error=%v code=%s", manifest, err, ErrorCode(err))
		}
	}
	tooMany := downloadpkg.Manifest{Name: "too-many", Complete: true, Files: make([]downloadpkg.File, maxCompletedManifestFiles+1)}
	if _, err := encodeCompletedDownloadManifest(tooMany); ErrorCode(err) != "download_completion_manifest_invalid" {
		t.Fatalf("too-many error=%v code=%s", err, ErrorCode(err))
	}
	if _, exists, err := completedDownloadManifest(strings.Repeat("x", maxCompletedManifestBytes+1)); !exists || ErrorCode(err) != "download_completion_manifest_invalid" {
		t.Fatalf("oversized exists=%v err=%v code=%s", exists, err, ErrorCode(err))
	}
	serialized, err := json.Marshal(models.DownloadTask{CompletedManifestJSON: raw, StagingCategory: "未识别"})
	if err != nil || strings.Contains(string(serialized), "completed_manifest") || strings.Contains(string(serialized), "staging_category") || strings.Contains(string(serialized), "item-1") {
		t.Fatalf("private fields leaked: %s err=%v", serialized, err)
	}
}

func TestClassifyRealSevenSamuraiReleaseQueriesCleanTitleAndYear(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/search/movie":
			if request.URL.Query().Get("query") != "Seven Samurai" || request.URL.Query().Get("year") != "1954" {
				_, _ = w.Write([]byte(`{"results":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"id":346,"title":"Seven Samurai","original_title":"七人の侍","original_language":"ja","genre_ids":[18],"release_date":"1954-04-26"}]}`))
		case "/movie/346":
			_, _ = w.Write([]byte(`{"id":346,"title":"Seven Samurai","original_title":"七人の侍","original_language":"ja","release_date":"1954-04-26","genres":[{"id":18,"name":"Drama"}],"production_countries":[{"iso_3166_1":"JP"}]}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	downloads, _, queue, _, _ := downloadFixture(t)
	metadata := NewMetadataSettingsService(queue.db, queue.audit, downloads.credentials, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", server.URL, server.Client())
	}
	downloads.SetMetadataSettings(metadata)
	rules, _ := json.Marshal(classification.DefaultRules())
	task := models.DownloadTask{ProfileRulesJSON: string(rules), ProfileRecognitionRulesJSON: "[]"}
	manifest := downloadpkg.Manifest{Name: "【高清影视之家发布 www.HDBTHD.com】七武士[简繁英字幕].Seven.Samurai.1954.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD", Complete: true, Files: []downloadpkg.File{{RelativePath: "【高清影视之家发布 www.HDBTHD.com】七武士[简繁英字幕].Seven.Samurai.1954.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD/Seven.Samurai.1954.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD.mkv", Size: 285 * 1024 * 1024 * 1024 / 10}}}
	for _, providerType := range []string{models.DownloaderTypeQBittorrent, models.DownloaderTypePan115Offline, "transmission", "future-provider"} {
		task.ProviderType = providerType
		match, err := NewDownloadWorker(downloads).classify(context.Background(), task, manifest)
		if err != nil {
			t.Fatalf("provider=%s error=%v", providerType, err)
		}
		if !match.Confident || match.Title != "Seven Samurai" || match.Year == nil || *match.Year != 1954 || match.TMDBID == nil || *match.TMDBID != 346 {
			t.Fatalf("provider=%s match=%+v", providerType, match)
		}
	}
}

func TestClassifyBackfillsLegacyAutomaticIdentityWithoutFuzzyResearch(t *testing.T) {
	searchCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/search/") {
			searchCalls++
			http.Error(w, "unexpected fuzzy search", http.StatusInternalServerError)
			return
		}
		if request.URL.Path != "/movie/346" {
			http.NotFound(w, request)
			return
		}
		_, _ = w.Write([]byte(`{"id":346,"title":"Seven Samurai","original_title":"七人の侍","original_language":"ja","release_date":"1954-04-26","genres":[{"id":18,"name":"Drama"}],"production_countries":[{"iso_3166_1":"JP"}]}`))
	}))
	defer server.Close()
	downloads, _, queue, actor, _ := downloadFixture(t)
	metadata := NewMetadataSettingsService(queue.db, queue.audit, downloads.credentials, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", server.URL, server.Client())
	}
	downloads.SetMetadataSettings(metadata)
	rules, _ := json.Marshal(classification.DefaultRules())
	id := int64(346)
	year := 1954
	task := models.DownloadTask{ID: "legacy-automatic-backfill", OwnerID: actor.User.ID, ProfileRulesJSON: string(rules), ProfileRecognitionRulesJSON: "[]", ScrapeTMDBID: &id, ScrapeMediaType: "movie", ScrapeYear: &year, IdentitySource: mediaIdentitySourceAutomatic, IdentityStatus: mediaIdentityStatusVerified, IdentityRevision: 1, IdentitySnapshotJSON: `{}`}
	_, err := queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: "legacy automatic backfill", Payload: downloadJobPayload{DownloadTaskID: task.ID}}, func(tx *gorm.DB, job models.Job) error {
		task.JobID = job.ID
		return tx.Create(&task).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := downloadpkg.Manifest{Name: "unparseable legacy label", Complete: true, Files: []downloadpkg.File{{RelativePath: "opaque-video.mkv", Size: 2 * 1024 * 1024 * 1024}}}
	worker := NewDownloadWorker(downloads)
	match, err := worker.classify(context.Background(), task, manifest)
	if err != nil || match.TMDBID == nil || *match.TMDBID != id || match.IdentitySource != mediaIdentitySourceAutomatic || searchCalls != 0 {
		t.Fatalf("match=%+v searchCalls=%d err=%v", match, searchCalls, err)
	}
	if err := worker.persistScrape(&task, match, "completed_verified", len(manifest.Files)); err != nil {
		t.Fatal(err)
	}
	identity, err := decodeMediaIdentity(task.IdentitySnapshotJSON)
	if err != nil || identity.Revision != 1 || identity.TMDBID == nil || *identity.TMDBID != id || identity.Locked {
		t.Fatalf("backfilled identity=%+v err=%v raw=%s", identity, err, task.IdentitySnapshotJSON)
	}
}

func TestRecognitionUsesMeaningfulPackageFolderForGenericDiscFilename(t *testing.T) {
	folder := "【高清影视之家发布 www.HDBTHD.com】七武士[简繁英字幕].Seven.Samurai.1954.CC.2160p.UHD.BluRay.x265.10bit.DTS-HD.MA.2.0-SONYHD"
	manifest := downloadpkg.Manifest{Name: folder, Complete: true, Files: []downloadpkg.File{{RelativePath: folder + "/BDMV/STREAM/00000.m2ts", Size: 285 * 1024 * 1024 * 1024 / 10}}}
	candidates := downloadRecognitionCandidates(manifest, nil)
	found := false
	for _, candidate := range candidates {
		if candidate.Title == "Seven Samurai" && candidate.MediaType == "movie" && candidate.Year != nil && *candidate.Year == 1954 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("candidates=%+v", candidates)
	}
}

func TestRecognitionRulesRunBeforeMediaTypeAndYearParsing(t *testing.T) {
	raw := []byte(`[{"enabled":true,"media_type":"tv","pattern":"FIXED\\.1999","replacement":"S01E01.2024"}]`)
	_, rules, err := canonicalRecognitionRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest := downloadpkg.Manifest{Name: "Series.FIXED.1999", Complete: true, Files: []downloadpkg.File{{RelativePath: "Series.FIXED.1999.mkv", Size: 2 * 1024 * 1024 * 1024}}}
	candidates := downloadRecognitionCandidates(manifest, rules)
	if len(candidates) == 0 || candidates[0].Title != "Series" || candidates[0].MediaType != "tv" || candidates[0].Year == nil || *candidates[0].Year != 2024 {
		t.Fatalf("candidates=%+v", candidates)
	}
}

func TestSelectDownloadPackageManifestKeepsTVEpisodesAndDropsTinySample(t *testing.T) {
	manifest := downloadpkg.Manifest{Name: "Series", Complete: true, Files: []downloadpkg.File{
		{RelativePath: "Series/Series.S01E01.mkv", Size: 2 * 1024 * 1024 * 1024},
		{RelativePath: "Series/Series.S01E02.mkv", Size: 1800 * 1024 * 1024},
		{RelativePath: "Series/sample.mp4", Size: 2 * 1024 * 1024},
		{RelativePath: "Series/Series.S01E02.zh.ass", Size: 96 * 1024},
	}}
	selected, err := selectDownloadPackageManifest(manifest, "tv")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Files) != 3 {
		t.Fatalf("selected=%+v", selected.Files)
	}
	for _, file := range selected.Files {
		if strings.Contains(strings.ToLower(file.RelativePath), "sample") {
			t.Fatalf("sample entered transfer manifest: %+v", selected.Files)
		}
	}
}

func TestSelectDownloadPackageManifestKeepsAllBracketedAnimeEpisodes(t *testing.T) {
	manifest := downloadpkg.Manifest{Name: "Megami-ryou no Ryoubo-kun", Complete: true}
	for episode := 1; episode <= 10; episode++ {
		manifest.Files = append(manifest.Files, downloadpkg.File{RelativePath: fmt.Sprintf("Megami-ryou/[Lilith-Raws] Megami-ryou no Ryoubo-kun. [%02d][Baha][WEB-DL][1080p][AVC AAC][BIG5][MP4].mp4", episode), Size: 400 << 20})
	}
	selected, err := selectDownloadPackageManifest(manifest, "tv")
	if err != nil || len(selected.Files) != 10 {
		t.Fatalf("selected=%+v err=%v", selected.Files, err)
	}
}

func TestSelectDownloadPackageManifestDoesNotFallbackToLargestUnknownTVVideo(t *testing.T) {
	manifest := downloadpkg.Manifest{Name: "Unknown Series", Complete: true, Files: []downloadpkg.File{
		{RelativePath: "Unknown Series/video-a.mkv", Size: 500 << 20},
		{RelativePath: "Unknown Series/video-b.mkv", Size: 400 << 20},
	}}
	if _, err := selectDownloadPackageManifest(manifest, "tv"); !errors.Is(err, errPackageEpisodeUnrecognized) {
		t.Fatalf("err=%v", err)
	}
}

func TestTransferEnqueueRejectsUnrecognizedPackageBeforePlanning(t *testing.T) {
	queue, _, download, _, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	download.ScrapeStatus = "completed_unrecognized"
	download.ScrapeTitle = ""
	download.ScrapeTMDBID = nil
	download.ScrapeConfidence = nil
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Unknown", Complete: true, Files: []downloadpkg.File{{RelativePath: "Unknown.mkv", Size: 2 * 1024 * 1024 * 1024}}}
	if err := service.Enqueue(download, manifest); ErrorCode(err) != CodeTransferMediaUnrecognized {
		t.Fatalf("error=%v", err)
	}
	var count int64
	if err := queue.db.Model(&models.TransferTask{}).Where("download_task_id = ?", download.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("transfer task count=%d err=%v", count, err)
	}
}

func TestTransferEnqueueRejectsUnfilteredMoviePackage(t *testing.T) {
	queue, _, download, _, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Movie pack", Complete: true, Files: []downloadpkg.File{
		{RelativePath: "Movie.mkv", Size: 20 * 1024 * 1024 * 1024},
		{RelativePath: "advertisement.mp4", Size: 512 * 1024},
	}}
	if err := service.Enqueue(download, manifest); ErrorCode(err) != CodeTransferMediaUnrecognized {
		t.Fatalf("error=%v", err)
	}
	var count int64
	if err := queue.db.Model(&models.TransferTask{}).Where("download_task_id = ?", download.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("transfer task count=%d err=%v", count, err)
	}
}

func TestTransferEnqueueRejectsTVPackageThatBypassesSharedSelection(t *testing.T) {
	queue, _, download, _, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	tmdbID, confidence := int64(550), .98
	download.ScrapeStatus, download.ScrapeMediaType, download.ScrapeTitle, download.ScrapeCategory = "completed_verified", "tv", "Series", "剧集"
	download.ScrapeTMDBID, download.ScrapeConfidence = &tmdbID, &confidence
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	manifest := downloadpkg.Manifest{Name: "Series", Complete: true, Files: []downloadpkg.File{
		{RelativePath: "Series/Series.S01E01.mkv", Size: 2 * 1024 * 1024 * 1024},
		{RelativePath: "Series/advertisement.S01E99.mp4", Size: 20 * 1024 * 1024},
	}}
	if err := service.Enqueue(download, manifest); ErrorCode(err) != CodeTransferMediaUnrecognized {
		t.Fatalf("error=%v", err)
	}
}

func TestFailedLegacyTransferRetryReclassifiesPackageBeforeMutation(t *testing.T) {
	queue, _, download, _, destination := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	initial := downloadpkg.Manifest{Name: "Movie", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: 2 * 1024 * 1024 * 1024}}}
	if err := service.Enqueue(download, initial); err != nil {
		t.Fatal(err)
	}
	legacy := downloadpkg.Manifest{Name: "Movie with ads", Complete: true, Files: []downloadpkg.File{
		{RelativePath: "Movie.2024.mkv", Size: 2 * 1024 * 1024 * 1024},
		{RelativePath: "advertisement-one.mp4", Size: 622 * 1024},
		{RelativePath: "advertisement-two.mp4", Size: 289 * 1024},
	}}
	raw, _ := json.Marshal(legacy)
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", download.ID).Updates(map[string]any{"scrape_status": "completed_unrecognized", "scrape_title": "", "scrape_media_type": "", "scrape_category": "", "scrape_tmdb_id": nil, "scrape_confidence": nil}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.TransferTask{}).Where("download_task_id = ?", download.ID).Updates(map[string]any{"manifest_json": string(raw), "total_files": len(legacy.Files), "phase": models.TransferTaskStatusFailed, "updated_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	service.SetCompletedManifestVerifier(func(_ context.Context, task *models.DownloadTask, manifest downloadpkg.Manifest) (downloadpkg.Manifest, error) {
		tmdbID, confidence := int64(550), .98
		task.ScrapeStatus = "completed_verified"
		task.ScrapeTitle = "Movie"
		task.ScrapeMediaType = "movie"
		task.ScrapeCategory = "华语电影"
		task.ScrapeTMDBID = &tmdbID
		task.ScrapeConfidence = &confidence
		manifest.Files = manifest.Files[:1]
		return manifest, queue.db.Model(task).Updates(map[string]any{"scrape_status": task.ScrapeStatus, "scrape_title": task.ScrapeTitle, "scrape_media_type": task.ScrapeMediaType, "scrape_category": task.ScrapeCategory, "scrape_tmdb_id": tmdbID, "scrape_confidence": confidence}).Error
	})
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != "" || result.RetryAt != nil || result.Wait != nil {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("primary movie was not imported: %v", err)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	var persisted downloadpkg.Manifest
	if err := json.Unmarshal([]byte(transfer.ManifestJSON), &persisted); err != nil || len(persisted.Files) != 1 {
		t.Fatalf("persisted manifest=%+v err=%v", persisted, err)
	}
}

func TestFailedLegacyTransferRetryClearsStalePlanWhenRecognitionStillFails(t *testing.T) {
	queue, _, download, _, _ := transferFixture(t, models.MediaLibraryTransferMove, models.MediaLibraryConflictOverwrite, false)
	service := NewTransferService(queue.db, queue.audit, queue, zerolog.Nop())
	initial := downloadpkg.Manifest{Name: "Movie", Complete: true, Files: []downloadpkg.File{{RelativePath: "Movie.2024.mkv", Size: 2 * 1024 * 1024 * 1024}}}
	if err := service.Enqueue(download, initial); err != nil {
		t.Fatal(err)
	}
	legacy := downloadpkg.Manifest{Name: "Movie with ads", Complete: true, Files: []downloadpkg.File{
		{RelativePath: "Movie.2024.mkv", Size: 2 * 1024 * 1024 * 1024},
		{RelativePath: "advertisement-one.mp4", Size: 622 * 1024},
		{RelativePath: "advertisement-two.mp4", Size: 289 * 1024},
	}}
	raw, _ := json.Marshal(legacy)
	staleSummary := `{"items":[{"relative_path":"未分类/advertisement-one.mp4","kind":"video","size":636928,"result":"completed"}],"total_files":3,"truncated":false}`
	if err := queue.db.Model(&models.DownloadTask{}).Where("id = ?", download.ID).Updates(map[string]any{"scrape_status": "completed_unrecognized", "scrape_title": "", "scrape_media_type": "", "scrape_category": "", "scrape_tmdb_id": nil, "scrape_confidence": nil}).Error; err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.TransferTask{}).Where("download_task_id = ?", download.ID).Updates(map[string]any{"manifest_json": string(raw), "plan_summary_json": staleSummary, "cloud_state_json": `{"items":{"ad":{"stage":"completed"}}}`, "processed_files": 3, "total_files": 3, "phase": models.TransferTaskStatusFailed}).Error; err != nil {
		t.Fatal(err)
	}
	service.SetCompletedManifestVerifier(func(context.Context, *models.DownloadTask, downloadpkg.Manifest) (downloadpkg.Manifest, error) {
		return downloadpkg.Manifest{}, appError(CodeTransferMediaUnrecognized, "媒体未识别", nil)
	})
	claimed, err := queue.Claim([]string{"transfer"})
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	result := NewTransferWorker(service).Run(context.Background(), workerRuntime{queue: queue, job: *claimed}, *claimed)
	if result.ErrorCode != CodeTransferMediaUnrecognized {
		t.Fatalf("result=%+v", result)
	}
	var transfer models.TransferTask
	if err := queue.db.Where("download_task_id = ?", download.ID).First(&transfer).Error; err != nil {
		t.Fatal(err)
	}
	if transfer.PlanSummaryJSON != "" || transfer.CloudStateJSON != "" || transfer.ProcessedFiles != 0 || transfer.TotalFiles != 0 {
		t.Fatalf("stale projection survived retry: %+v", transfer)
	}
}
