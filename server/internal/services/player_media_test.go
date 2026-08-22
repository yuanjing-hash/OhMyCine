package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

func TestPlayerLocalEntryOpensOnlySafeRegularFiles(t *testing.T) {
	service, library, _ := createCatalogTestLibrary(t)
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	var storage models.Storage
	if err := service.db.First(&storage, library.StorageID).Error; err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(storage.RootPath, "Movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	wanted := []byte("safe-local-media")
	if err := os.WriteFile(filepath.Join(storage.RootPath, "Movies", "Movie.mkv"), wanted, 0o600); err != nil {
		t.Fatal(err)
	}
	entry := models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/Movies/Movie.mkv"}
	file, info, err := openLocalPlayerEntry(service.db, entry)
	if err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) {
			t.Fatalf("open local entry: %v (cause=%v)", err, appErr.Cause)
		}
		t.Fatal(err)
	}
	content := make([]byte, len(wanted))
	if _, err := file.Read(content); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if string(content) != string(wanted) || !info.Mode().IsRegular() {
		t.Fatalf("content=%q info=%v", content, info.Mode())
	}

	unsafeEntries := []models.MediaLibraryEntry{
		{LibraryID: library.ID, RelativePath: "/../outside.mkv"},
		{LibraryID: library.ID, RelativePath: "/Movies"},
		{LibraryID: library.ID, RelativePath: "/Movies/missing.mkv"},
	}
	for _, unsafe := range unsafeEntries {
		if _, _, err := openLocalPlayerEntry(service.db, unsafe); ErrorCode(err) != CodeProxyTargetUnavailable || strings.Contains(ErrorMessage(err), storage.RootPath) {
			t.Fatalf("unsafe path=%q code=%q message=%q", unsafe.RelativePath, ErrorCode(err), ErrorMessage(err))
		}
	}

	link := filepath.Join(storage.RootPath, "Movies", "linked.mkv")
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symlink boundary test skipped by host policy: %v", err)
	} else if _, _, err := openLocalPlayerEntry(service.db, models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/Movies/linked.mkv"}); ErrorCode(err) != CodeProxyTargetUnavailable {
		t.Fatalf("symlink accepted: %v", err)
	}
	if runtime.GOOS == "windows" {
		junctionTarget := filepath.Join(storage.RootPath, "JunctionTarget")
		junction := filepath.Join(storage.RootPath, "Movies", "Junction")
		if err := os.MkdirAll(junctionTarget, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(junctionTarget, "junction.mkv"), []byte("junction"), 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, junctionTarget).CombinedOutput(); err != nil {
			t.Logf("junction boundary test skipped by host policy: %v output=%q", err, output)
		} else if _, _, err := openLocalPlayerEntry(service.db, models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/Movies/Junction/junction.mkv"}); ErrorCode(err) != CodeProxyTargetUnavailable {
			t.Fatalf("junction accepted: %v", err)
		}
	}

	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := openLocalPlayerEntry(service.db, entry); ErrorCode(err) != CodeProxyTargetUnavailable {
		t.Fatalf("disabled library accepted: %v", err)
	}
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.Storage{}).Where("id = ?", storage.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := openLocalPlayerEntry(service.db, entry); ErrorCode(err) != CodeProxyTargetUnavailable {
		t.Fatalf("disabled storage accepted: %v", err)
	}
}

func TestPlayerLocalSeriesProjectsPlayableSeasonsAndEpisodes(t *testing.T) {
	service, library, actor := createCatalogTestLibrary(t)
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	var storage models.Storage
	if err := service.db.First(&storage, library.StorageID).Error; err != nil {
		t.Fatal(err)
	}
	season, episodeOne, episodeTwo := 1, 1, 2
	for _, relative := range []string{"Series/Season 01/E01.mkv", "Series/Season 01/E02.mkv"} {
		target := filepath.Join(storage.RootPath, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(relative), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	entries := []models.MediaLibraryEntry{
		{LibraryID: library.ID, RelativePath: "/Series/Season 01/E01.mkv", ProviderID: "series-e01", Size: 1, ModifiedAt: now, MediaType: "tv", Title: "示例剧", SeriesTitle: "示例剧", WorkKey: "series:local-test", Season: &season, Episode: &episodeOne, MatchStatus: "matched", CategoryName: "电视剧", LastGeneration: 1},
		{LibraryID: library.ID, RelativePath: "/Series/Season 01/E02.mkv", ProviderID: "series-e02", Size: 1, ModifiedAt: now, MediaType: "tv", Title: "示例剧", SeriesTitle: "示例剧", WorkKey: "series:local-test", Season: &season, Episode: &episodeTwo, MatchStatus: "matched", CategoryName: "电视剧", LastGeneration: 1},
	}
	if err := service.db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}
	page, err := service.PlayerCatalog(actor, library.ID, MediaPageQuery{Page: 1, PageSize: 20, MediaType: "series"})
	if err != nil || len(page.List) != 1 || page.List[0].SeasonCount != 1 || page.List[0].EpisodeCount != 2 {
		t.Fatalf("series page=%+v err=%v", page, err)
	}
	detail, err := service.PlayerCatalogDetail(actor, library.ID, page.List[0].ID)
	if err != nil || len(detail.Versions) != 2 {
		t.Fatalf("series detail=%+v err=%v", detail, err)
	}
	for _, version := range detail.Versions {
		if !version.Playable || version.Season == nil || *version.Season != 1 || version.Episode == nil || version.StreamPath == "" || !strings.HasPrefix(version.ExactIdentity, "server:entry:") {
			t.Fatalf("unplayable local episode=%+v", version)
		}
	}
}

func TestSnapshotStillPathsAreSafeUniqueAndBounded(t *testing.T) {
	snapshot := tmdb.Snapshot{BackdropPath: "/primary.jpg", BackdropPaths: []string{
		"/primary.jpg", "https://unsafe.example/still.jpg", "/../escape.jpg", "/one.jpg", "/two.jpg", "/three.jpg", "/four.jpg", "/five.jpg", "/six.jpg", "/seven.jpg", "/eight.jpg",
	}}
	paths := snapshotStillPaths(snapshot)
	if len(paths) != 8 || paths[0] != "/primary.jpg" || paths[7] != "/seven.jpg" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestPlayerMediaItemProjectsSafeSnapshotMetadataAndLegacyStill(t *testing.T) {
	service, library, actor := createCatalogTestLibrary(t)
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	snapshot := tmdb.Snapshot{
		Version: 1, TMDBID: 346, IMDbID: "tt0047478", MediaType: "movie", Title: "七武士", OriginalTitle: "七人の侍",
		Overview: "简介", Tagline: "标语", VoteAverage: 8.5, RuntimeMinutes: 207,
		Genres: []tmdb.Genre{{Name: "剧情"}}, Directors: []tmdb.Person{{Name: "黑泽明"}},
		Writers: []tmdb.Person{{Name: "桥本忍"}}, Cast: []tmdb.Person{{Name: "三船敏郎"}},
		PosterPath: "/poster.jpg", BackdropPath: "/legacy-backdrop.jpg",
	}
	metadata, err := json.Marshal(recognitionMetadataEnvelope{Version: 1, Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	recognition := models.MediaLibraryRecognition{LibraryID: library.ID, SourceKey: "player-metadata", InputFingerprint: "fingerprint", ProfileID: library.ProfileID, ProfileRevision: library.ProfileRevision, Status: "matched", MediaType: "movie", Title: "七武士", TMDBID: &snapshot.TMDBID, MetadataJSON: string(metadata), LastGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&recognition).Error; err != nil {
		t.Fatal(err)
	}
	entry := models.MediaLibraryEntry{LibraryID: library.ID, RelativePath: "/Seven.Samurai.mkv", ProviderID: "safe-provider", RecognitionID: &recognition.ID, Size: 1, ModifiedAt: now, MediaType: "movie", Title: "七武士", WorkKey: "movie:tmdb:346", MatchStatus: "matched", TMDBID: &snapshot.TMDBID, CategoryName: "电影", LastGeneration: 1, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	page, err := service.PlayerCatalog(actor, library.ID, MediaPageQuery{Page: 1, PageSize: 20})
	if err != nil || len(page.List) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	item := page.List[0]
	if item.OriginalTitle != "七人の侍" || item.Rating != 8.5 || item.RuntimeMinutes != 207 || item.TMDBID != 346 || item.IMDbID != "tt0047478" || len(item.Genres) != 1 || len(item.Directors) != 1 || len(item.Writers) != 1 || len(item.Cast) != 1 || len(item.StillPaths) != 1 || item.StillPaths[0] != "/legacy-backdrop.jpg" {
		t.Fatalf("item=%+v", item)
	}
	payload, err := json.Marshal(item)
	var storage models.Storage
	if loadErr := service.db.First(&storage, library.StorageID).Error; loadErr != nil {
		t.Fatal(loadErr)
	}
	if err != nil || strings.Contains(string(payload), storage.RootPath) || strings.Contains(string(payload), entry.RelativePath) || strings.Contains(string(payload), entry.ProviderID) {
		t.Fatalf("unsafe payload=%s err=%v", payload, err)
	}
}

func TestPlayerCatalogRejectsDisabledLibraryAndStorage(t *testing.T) {
	service, library, actor := createCatalogTestLibrary(t)
	workToken := encodeCatalogToken("movie:disabled")

	if _, err := service.PlayerCatalog(actor, library.ID, MediaPageQuery{Page: 1, PageSize: 20}); ErrorCode(err) != CodeNotFound {
		t.Fatalf("disabled library code=%q err=%v", ErrorCode(err), err)
	}
	if _, err := service.PlayerCatalogDetail(actor, library.ID, workToken); ErrorCode(err) != CodeNotFound {
		t.Fatalf("disabled library detail code=%q err=%v", ErrorCode(err), err)
	}
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.Storage{}).Where("id = ?", library.StorageID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.PlayerCatalog(actor, library.ID, MediaPageQuery{Page: 1, PageSize: 20}); ErrorCode(err) != CodeNotFound {
		t.Fatalf("disabled storage code=%q err=%v", ErrorCode(err), err)
	}
	if _, err := service.PlayerCatalogDetail(actor, library.ID, workToken); ErrorCode(err) != CodeNotFound {
		t.Fatalf("disabled storage detail code=%q err=%v", ErrorCode(err), err)
	}
}

func TestPlayerSearchPagesThroughEveryEnabledLibrary(t *testing.T) {
	service, library, actor := createCatalogTestLibrary(t)
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entries := make([]models.MediaLibraryEntry, 0, 205)
	for index := 0; index < 205; index++ {
		entries = append(entries, models.MediaLibraryEntry{
			LibraryID: library.ID, RelativePath: fmt.Sprintf("/movie/%03d.mkv", index), ProviderID: fmt.Sprintf("provider-%03d", index),
			Size: int64(index + 1), ModifiedAt: now, MediaType: "movie", Title: fmt.Sprintf("Player Movie %03d", index),
			WorkKey: fmt.Sprintf("movie:player-%03d", index), MatchStatus: "unmatched", CategoryName: "电影", LastGeneration: 1,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := service.db.CreateInBatches(entries, 100).Error; err != nil {
		t.Fatal(err)
	}

	page, err := service.PlayerSearch(actor, MediaPageQuery{Page: 3, PageSize: 100, Query: "Player Movie"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 205 || page.Page != 3 || len(page.List) != 5 || page.List[0].Title != "Player Movie 200" {
		t.Fatalf("player search page=%+v", page)
	}

	page, err = service.PlayerSearch(actor, MediaPageQuery{Page: int(^uint(0) >> 1), PageSize: 100, Query: "Player Movie"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 205 || len(page.List) != 0 {
		t.Fatalf("huge player search page=%+v", page)
	}
}
