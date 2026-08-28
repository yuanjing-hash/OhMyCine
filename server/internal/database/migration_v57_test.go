package database

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func TestMigrationV57NormalizesFuturePolicyWithoutChangingFrozenTasks(t *testing.T) {
	db, err := Open(t.TempDir() + "/migration-v57.db")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrationsThrough(t, db, 56)
	if err := seedMediaClassificationProfiles(db); err != nil {
		t.Fatal(err)
	}

	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&profile).Updates(map[string]any{
		"movie_directory_template": "{category}/{title}",
		"tv_directory_template":    "电视剧/{category}/{title}/Season {season:02}",
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "v57", NameNormalized: "v57-" + uuid.NewString(), Type: models.StorageTypeLocal, RootPath: t.TempDir(), RootPathNormalized: "v57-root-" + uuid.NewString(), Enabled: true, Capabilities: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "v57", NameNormalized: "v57-library-" + uuid.NewString(), StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", TransferMode: models.MediaLibraryTransferMove, ConflictPolicy: models.MediaLibraryConflictAsk, MovieDirectoryTemplate: "{category}/{title}", MovieFilenameTemplate: "{title}", TVDirectoryTemplate: "电影/{category}/{title}/Season {season:02}", TVFilenameTemplate: "{title} - S{season:02}E{episode:02}", Enabled: false, Recursive: true, VideoExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, Status: models.MediaLibraryStatusDisabled, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	owner := models.User{Username: "v57", UsernameNormalized: "v57-" + uuid.NewString(), DisplayName: "v57", PasswordHash: "x", Status: models.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	job := models.Job{ID: uuid.NewString(), OwnerID: &ownerID, JobType: "download", Status: models.JobStatusQueued, DisplayName: "v57", Revision: 1, Generation: 1, PayloadJSON: `{}`, CheckpointJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	task := models.DownloadTask{ID: uuid.NewString(), OwnerID: owner.ID, JobID: job.ID, DownloaderName: "legacy", ProviderType: models.DownloaderTypeFake, SourceCiphertext: "encrypted", DisplayName: "legacy", Phase: models.DownloadTaskStatusQueued, MovieDirectoryTemplate: "{category}/{title}", TVDirectoryTemplate: "{category}/{title}/Season {season:02}", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&profile, profile.ID).Error; err != nil {
		t.Fatal(err)
	}
	if profile.MovieDirectoryTemplate != "电影/{category}/{title}" || profile.TVDirectoryTemplate != "电视剧/{category}/{title}/Season {season:02}" {
		t.Fatalf("profile templates=%q %q", profile.MovieDirectoryTemplate, profile.TVDirectoryTemplate)
	}
	if err := db.First(&library, library.ID).Error; err != nil {
		t.Fatal(err)
	}
	if library.MovieDirectoryTemplate != "电影/{category}/{title}" || library.TVDirectoryTemplate != "电视剧/{category}/{title}/Season {season:02}" {
		t.Fatalf("library templates=%q %q", library.MovieDirectoryTemplate, library.TVDirectoryTemplate)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.MovieDirectoryTemplate != "{category}/{title}" || task.TVDirectoryTemplate != "{category}/{title}/Season {season:02}" {
		t.Fatalf("frozen task templates changed=%q %q", task.MovieDirectoryTemplate, task.TVDirectoryTemplate)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("schema_migrations").Where("version = ?", 57).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("v57 migration count=%d err=%v", count, err)
	}
}
