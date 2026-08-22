package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

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
