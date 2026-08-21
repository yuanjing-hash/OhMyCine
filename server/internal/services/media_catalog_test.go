package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func createCatalogTestLibrary(t *testing.T) (*MediaLibraryService, models.MediaLibrary, Actor) {
	t.Helper()
	service, _, actor, storage, profile := mediaLibraryTestService(t)
	detail, err := service.Create(context.Background(), actor, testLibraryInput("Catalog library", storage, profile, false), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	return service, detail.MediaLibrary, actor
}

func TestMediaLibraryEntryPageReturnsDatabaseTotalAndEmptyOutOfRangePage(t *testing.T) {
	service, library, actor := createCatalogTestLibrary(t)
	now := time.Now().UTC()
	entries := make([]models.MediaLibraryEntry, 0, 12099)
	for index := 0; index < 12099; index++ {
		entries = append(entries, models.MediaLibraryEntry{
			LibraryID: library.ID, RelativePath: fmt.Sprintf("/movie/%05d.mkv", index), ProviderID: fmt.Sprintf("p-%d", index),
			Size: int64(index), ModifiedAt: now, MediaType: "movie", Title: fmt.Sprintf("Movie %05d", index),
			WorkKey: fmt.Sprintf("movie:%032x", index), MatchStatus: "unmatched", CategoryName: "电影", LastGeneration: 1,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := service.db.CreateInBatches(entries, 250).Error; err != nil {
		t.Fatal(err)
	}

	page, err := service.EntryPage(actor, library.ID, MediaPageQuery{Page: 2, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 12099 || len(page.List) != 50 || page.Page != 2 || page.List[0].RelativePath != "/movie/00050.mkv" {
		t.Fatalf("entry page=%+v first=%+v", page, page.List[0])
	}
	outOfRange, err := service.EntryPage(actor, library.ID, MediaPageQuery{Page: 999, PageSize: 50})
	if err != nil || outOfRange.Total != 12099 || len(outOfRange.List) != 0 {
		t.Fatalf("out-of-range page=%+v err=%v", outOfRange, err)
	}
}

func TestMediaCatalogGroupsSeriesBeforePagingAndBuildsSeasonDetail(t *testing.T) {
	service, library, actor := createCatalogTestLibrary(t)
	now := time.Now().UTC()
	season1, season2, episode1, episode2 := 1, 2, 1, 2
	entries := []models.MediaLibraryEntry{
		{LibraryID: library.ID, RelativePath: "/莉可丽丝/Season 02/E02.mkv", ProviderID: "s2e2", Size: 22, ModifiedAt: now, MediaType: "tv", Title: "莉可丽丝", SeriesTitle: "莉可丽丝", WorkKey: "series:lycoris", Season: &season2, Episode: &episode2, MatchStatus: "matched", CategoryName: "动画", LastGeneration: 1},
		{LibraryID: library.ID, RelativePath: "/莉可丽丝/Season 01/E02.mkv", ProviderID: "s1e2", Size: 12, ModifiedAt: now, MediaType: "tv", Title: "莉可丽丝", SeriesTitle: "莉可丽丝", WorkKey: "series:lycoris", Season: &season1, Episode: &episode2, MatchStatus: "matched", CategoryName: "动画", LastGeneration: 1},
		{LibraryID: library.ID, RelativePath: "/莉可丽丝/Season 01/E01.mkv", ProviderID: "s1e1", Size: 11, ModifiedAt: now, MediaType: "tv", Title: "莉可丽丝", SeriesTitle: "莉可丽丝", WorkKey: "series:lycoris", Season: &season1, Episode: &episode1, MatchStatus: "matched", CategoryName: "动画", LastGeneration: 1},
		{LibraryID: library.ID, RelativePath: "/Movie.mkv", ProviderID: "movie", Size: 50, ModifiedAt: now, MediaType: "movie", Title: "Movie", WorkKey: "movie:movie", MatchStatus: "unmatched", CategoryName: "电影", LastGeneration: 1},
	}
	if err := service.db.Create(&entries).Error; err != nil {
		t.Fatal(err)
	}

	page, err := service.Catalog(actor, library.ID, MediaPageQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.List) != 2 {
		t.Fatalf("catalog page=%+v", page)
	}
	seriesPage, err := service.Catalog(actor, library.ID, MediaPageQuery{Page: 1, PageSize: 20, Query: "莉可", MediaType: "series"})
	if err != nil || seriesPage.Total != 1 || len(seriesPage.List) != 1 || seriesPage.List[0].FileCount != 3 || seriesPage.List[0].SeasonCount != 2 || seriesPage.List[0].ModifiedAt.IsZero() {
		t.Fatalf("series page=%+v err=%v", seriesPage, err)
	}
	detail, err := service.CatalogDetail(actor, library.ID, seriesPage.List[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Seasons) != 2 || detail.Seasons[0].Number != 1 || len(detail.Seasons[0].Episodes) != 2 || *detail.Seasons[0].Episodes[0].Episode != 1 || detail.Seasons[1].Number != 2 {
		t.Fatalf("catalog detail=%+v", detail)
	}
	if _, err := service.Catalog(actor, library.ID, MediaPageQuery{Page: -1, PageSize: 50}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("invalid page error=%v", err)
	}
}
