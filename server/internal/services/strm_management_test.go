package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func strmManagementFixture(t *testing.T) (*STRMManagementService, *QueueService, Actor, models.MediaLibrary, string) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "strm-management.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	storage := models.Storage{Name: "Cloud", NameNormalized: "strm-cloud", Type: models.StorageTypePan115, RootPath: "root", RootDisplayPath: "/", RootPathNormalized: "strm:root", Enabled: true, Capabilities: "{}"}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "STRM", NameNormalized: "strm", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", Enabled: true, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, STRMAssetExtraExtensionsJSON: `[]`, IgnorePatternsJSON: `[]`, STRMEnabled: true, SignedProxyEnabled: true, STRMLocalRoot: root, MetadataArtifactsEnabled: true, ArtifactGeneration: 2, ArtifactAppliedGeneration: 2, ArtifactStatus: models.MediaArtifactStatusCompleted, Status: models.MediaLibraryStatusListening, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "strm-admin", UsernameNormalized: "strm-admin", DisplayName: "STRM Admin", PasswordHash: "unused", Status: models.UserStatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: user, Permissions: map[string]struct{}{authz.PermissionSTRMRunsRead: {}, authz.PermissionSTRMRunsCreate: {}, authz.PermissionSTRMCleanup: {}}}
	audit := NewAuditService(db)
	queue := NewQueueService(db, audit)
	libraries := NewMediaLibraryService(db, audit, zerolog.Nop())
	t.Cleanup(libraries.Close)
	service := NewSTRMManagementService(db, audit, queue, libraries, nil)
	return service, queue, actor, library, root
}

func createAutoCleanupScenario(t *testing.T, service *STRMManagementService, library models.MediaLibrary, root, scanKind string, partial bool, runStatus string) (models.MediaArtifactRun, models.MediaArtifact, string) {
	t.Helper()
	_, rootIdentity, err := canonicalProjectionRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	generation := uint64(3)
	ownerPolicy, err := json.Marshal(mediaArtifactPolicy{LibraryID: library.ID, Generation: generation - 1, ProjectionRoot: root, ProjectionRootIdentity: rootIdentity, TargetKind: models.MediaArtifactTargetLocalProjection, STRMEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ownerRun := models.MediaArtifactRun{ID: uuid.NewString(), LibraryID: library.ID, Generation: generation - 1, PolicyJSON: string(ownerPolicy), Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&ownerRun).Error; err != nil {
		t.Fatal(err)
	}
	scan := models.MediaLibraryScanRun{LibraryID: library.ID, Kind: scanKind, Status: "success", Generation: generation, Partial: partial, StartedAt: now, FinishedAt: &now}
	if err := service.db.Create(&scan).Error; err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(mediaArtifactPolicy{LibraryID: library.ID, Generation: generation, StorageID: library.StorageID, ProjectionRoot: root, ProjectionRootIdentity: rootIdentity, TargetKind: models.MediaArtifactTargetLocalProjection, STRMEnabled: true, ScanRunID: scan.ID, ScanKind: scan.Kind, ScanPartial: partial, CleanupEligible: !partial && automaticCleanupScanKind(scanKind)})
	if err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: uuid.NewString(), LibraryID: library.ID, Generation: generation, PolicyJSON: string(policy), Status: runStatus, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Updates(map[string]any{"artifact_generation": generation, "artifact_applied_generation": generation, "artifact_status": models.MediaArtifactStatusCompleted}).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "stale.strm")
	if err := os.WriteFile(path, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := models.MediaArtifact{OpaqueID: uuid.NewString(), RunID: ownerRun.ID, LibraryID: library.ID, Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/stale.strm", ContentFingerprint: strings.Repeat("a", 64), Managed: true, Active: false, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	artifact.Active = false
	return run, artifact, path
}

func relocateCleanupArtifact(t *testing.T, service *STRMManagementService, artifact models.MediaArtifact, source, root, relativePath string) string {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(relativePath, "/")))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Update("relative_path", relativePath).Error; err != nil {
		t.Fatal(err)
	}
	return target
}

func TestSTRMManagementReconcileUsesDurableQueue(t *testing.T) {
	service, queue, actor, library, _ := strmManagementFixture(t)
	job, err := service.RequestReconcile(actor, library.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	if job.JobType != JobTypeSTRMReconcile || job.Status != models.JobStatusQueued {
		t.Fatalf("job=%+v", job)
	}
	duplicate, err := service.RequestReconcile(actor, library.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != job.ID {
		t.Fatalf("duplicate job=%s, want coalesced %s", duplicate.ID, job.ID)
	}
	var jobCount int64
	if err := service.db.Model(&models.Job{}).Where("job_type = ? AND resource_key = ?", JobTypeSTRMReconcile, "library:"+strconv.FormatUint(uint64(library.ID), 10)).Count(&jobCount).Error; err != nil || jobCount != 1 {
		t.Fatalf("jobs=%d err=%v", jobCount, err)
	}
	claimed, err := queue.Claim([]string{JobTypeSTRMReconcile})
	if err != nil || claimed == nil {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	var payload strmReconcilePayload
	if err := json.Unmarshal([]byte(claimed.Job.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.LibraryID != library.ID || payload.Mode != "full" {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestSTRMManagementRetryWithoutArtifactServiceKeepsFailedState(t *testing.T) {
	service, _, actor, library, _ := strmManagementFixture(t)
	run := models.MediaArtifactRun{ID: "00000000-0000-0000-0000-000000000010", LibraryID: library.ID, Generation: library.ArtifactGeneration, PolicyJSON: "{}", Status: models.MediaArtifactStatusFailed, ErrorCode: "previous_failure", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := service.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.RetryRun(actor, run.ID); ErrorCode(err) != CodeConflict {
		t.Fatalf("err=%v", err)
	}
	if err := service.db.First(&run, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != models.MediaArtifactStatusFailed || run.RetryCount != 0 || run.ErrorCode != "previous_failure" {
		t.Fatalf("run=%+v", run)
	}
}

func TestSTRMManagementRetryNoLongerApplicableRestoresFailedState(t *testing.T) {
	service, queue, actor, library, _ := strmManagementFixture(t)
	service.artifacts = NewMediaArtifactService(service.db, queue, nil, zerolog.Nop())
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Updates(map[string]any{"artifact_applied_generation": library.ArtifactGeneration - 1, "artifact_status": models.MediaArtifactStatusFailed}).Error; err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: "00000000-0000-0000-0000-000000000011", LibraryID: library.ID, Generation: library.ArtifactGeneration, PolicyJSON: "{}", Status: models.MediaArtifactStatusFailed, ErrorCode: "previous_failure", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := service.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.RetryRun(actor, run.ID); ErrorCode(err) != CodeConflict {
		t.Fatalf("err=%v", err)
	}
	if err := service.db.First(&run, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != models.MediaArtifactStatusFailed || run.RetryCount != 1 || run.ErrorCode != "artifact_retry_not_applicable" {
		t.Fatalf("run=%+v", run)
	}
	var jobCount int64
	if err := service.db.Model(&models.Job{}).Where("job_type = ?", JobTypeMediaArtifact).Count(&jobCount).Error; err != nil || jobCount != 0 {
		t.Fatalf("jobs=%d err=%v", jobCount, err)
	}
}

func TestSTRMCleanupRequiresPreviewAndDeletesOnlyInactiveManagedFiles(t *testing.T) {
	service, _, actor, library, root := strmManagementFixture(t)
	_, rootIdentity, err := canonicalProjectionRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	policyJSON, err := json.Marshal(mediaArtifactPolicy{LibraryID: library.ID, Generation: 1, ProjectionRoot: root, ProjectionRootIdentity: rootIdentity, TargetKind: models.MediaArtifactTargetLocalProjection, STRMEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: "00000000-0000-0000-0000-000000000001", LibraryID: library.ID, Generation: 1, PolicyJSON: string(policyJSON), Status: models.MediaArtifactStatusCompleted, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := service.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "stale.strm")
	active := filepath.Join(root, "active.strm")
	unmanaged := filepath.Join(root, "user.strm")
	for _, path := range []string{stale, active, unmanaged} {
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rows := []models.MediaArtifact{
		{OpaqueID: "stale", RunID: run.ID, LibraryID: library.ID, Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/stale.strm", Managed: true, Active: false, Status: models.MediaArtifactStatusCompleted},
		{OpaqueID: "active", RunID: run.ID, LibraryID: library.ID, Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/active.strm", Managed: true, Active: true, Status: models.MediaArtifactStatusCompleted},
		{OpaqueID: "user", RunID: run.ID, LibraryID: library.ID, Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/user.strm", Managed: false, Active: false, Status: models.MediaArtifactStatusCompleted},
	}
	for index := range rows {
		if err := service.db.Create(&rows[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := service.db.Model(&models.MediaArtifact{}).Where("opaque_id IN ?", []string{"stale", "user"}).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewCleanup(actor, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Count != 1 || len(preview.Paths) != 1 || preview.Paths[0] != "/stale.strm" {
		t.Fatalf("preview=%+v", preview)
	}
	other := actor
	other.User.ID++
	if _, err := service.ExecuteCleanup(other, library.ID, preview.ConfirmationToken, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("cross actor err=%v", err)
	}
	removed, err := service.ExecuteCleanup(actor, library.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale remains: %v", err)
	}
	for _, path := range []string{active, unmanaged} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected file %s: %v", path, err)
		}
	}
	var count int64
	if err := service.db.Model(&models.MediaArtifact{}).Where("library_id = ?", library.ID).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var audits []models.AuditLog
	if err := service.db.Where("action = ?", "strm.cleanup").Find(&audits).Error; err != nil || len(audits) != 1 || !strings.Contains(audits[0].Metadata, "\"count\":1") {
		t.Fatalf("audits=%+v err=%v", audits, err)
	}
}

func TestSTRMCleanupConfirmationTokenRejectsMalformedClaims(t *testing.T) {
	service, _, actor, library, root := strmManagementFixture(t)
	_, rootIdentity, err := canonicalProjectionRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	policyJSON, err := json.Marshal(mediaArtifactPolicy{LibraryID: library.ID, Generation: 1, ProjectionRoot: root, ProjectionRootIdentity: rootIdentity, TargetKind: models.MediaArtifactTargetLocalProjection, STRMEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: uuid.NewString(), LibraryID: library.ID, Generation: 1, PolicyJSON: string(policyJSON), Status: models.MediaArtifactStatusCompleted, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := service.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewCleanup(actor, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(preview.ConfirmationToken, ".")
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), root) {
		t.Fatalf("confirmation token leaked projection root: %s", body)
	}

	signRaw := func(body []byte) string {
		mac := hmac.New(sha256.New, service.cleanupKey)
		_, _ = mac.Write(body)
		return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	tamperedSuffix := "A"
	if strings.HasSuffix(preview.ConfirmationToken, tamperedSuffix) {
		tamperedSuffix = "B"
	}
	tampered := preview.ConfirmationToken[:len(preview.ConfirmationToken)-1] + tamperedSuffix
	tests := []struct {
		name  string
		token string
	}{
		{name: "tampered signature", token: tampered},
		{name: "oversized", token: strings.Repeat("a", 2049)},
		{name: "unknown field", token: signRaw(append(body[:len(body)-1], []byte(`,"unexpected":true}`)...))},
		{name: "trailing json", token: signRaw(append(append([]byte(nil), body...), []byte(` {}`)...))},
		{name: "wrong operation", token: signRaw([]byte(strings.Replace(string(body), `"operation":"artifact_cleanup"`, `"operation":"other"`, 1)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.ExecuteCleanup(actor, library.ID, test.token, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestSTRMManualCleanupCanConfirmArtifactsFromPreviousProjectionRoot(t *testing.T) {
	service, _, actor, library, oldRoot := strmManagementFixture(t)
	_, oldIdentity, err := canonicalProjectionRoot(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	policyJSON, err := json.Marshal(mediaArtifactPolicy{LibraryID: library.ID, Generation: 1, ProjectionRoot: oldRoot, ProjectionRootIdentity: oldIdentity, TargetKind: models.MediaArtifactTargetLocalProjection, STRMEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: uuid.NewString(), LibraryID: library.ID, Generation: 1, PolicyJSON: string(policyJSON), Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	oldWorkDirectory := filepath.Join(oldRoot, "电影", "外语电影", "七武士 (1954)")
	if err := os.MkdirAll(oldWorkDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	stalePaths := []string{filepath.Join(oldWorkDirectory, "七武士 (1954).strm"), filepath.Join(oldWorkDirectory, "七武士 (1954).nfo")}
	for index, stalePath := range stalePaths {
		if err := os.WriteFile(stalePath, []byte("stale\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		kind := models.MediaArtifactKindSTRM
		if index == 1 {
			kind = models.MediaArtifactKindNFO
		}
		relativePath, err := filepath.Rel(oldRoot, stalePath)
		if err != nil {
			t.Fatal(err)
		}
		artifact := models.MediaArtifact{OpaqueID: uuid.NewString(), RunID: run.ID, LibraryID: library.ID, Kind: kind, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/" + filepath.ToSlash(relativePath), ContentFingerprint: strings.Repeat("a", 64), Managed: true, Active: false, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
		if err := service.db.Create(&artifact).Error; err != nil {
			t.Fatal(err)
		}
		if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Update("active", false).Error; err != nil {
			t.Fatal(err)
		}
	}
	newRoot := t.TempDir()
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("strm_local_root", newRoot).Error; err != nil {
		t.Fatal(err)
	}
	_, newIdentity, err := canonicalProjectionRoot(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	currentPolicyJSON, err := json.Marshal(mediaArtifactPolicy{LibraryID: library.ID, Generation: library.ArtifactGeneration, ProjectionRoot: newRoot, ProjectionRootIdentity: newIdentity, TargetKind: models.MediaArtifactTargetLocalProjection, STRMEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	currentRun := models.MediaArtifactRun{ID: uuid.NewString(), LibraryID: library.ID, Generation: library.ArtifactGeneration, PolicyJSON: string(currentPolicyJSON), Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&currentRun).Error; err != nil {
		t.Fatal(err)
	}
	currentWorkDirectory := filepath.Join(newRoot, "电视剧", "剧情", "当前剧", "Season 01")
	if err := os.MkdirAll(currentWorkDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	currentStalePath := filepath.Join(currentWorkDirectory, "当前剧 S01E01.strm")
	if err := os.WriteFile(currentStalePath, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	currentRelativePath, err := filepath.Rel(newRoot, currentStalePath)
	if err != nil {
		t.Fatal(err)
	}
	currentArtifact := models.MediaArtifact{OpaqueID: uuid.NewString(), RunID: currentRun.ID, LibraryID: library.ID, Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/" + filepath.ToSlash(currentRelativePath), ContentFingerprint: strings.Repeat("b", 64), Managed: true, Active: false, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := service.db.Create(&currentArtifact).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", currentArtifact.ID).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	stalePaths = append(stalePaths, currentStalePath)
	preview, err := service.PreviewCleanup(actor, library.ID)
	if err != nil || preview.Count != 3 || strings.Contains(preview.ConfirmationToken, oldRoot) || strings.Contains(preview.ConfirmationToken, newRoot) {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	removed, err := service.ExecuteCleanup(actor, library.ID, preview.ConfirmationToken, RequestContext{})
	if err != nil || removed != 3 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	for _, stalePath := range stalePaths {
		if _, err := os.Stat(stalePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old-root artifact remains: %v", err)
		}
	}
	for _, directory := range []string{oldWorkDirectory, filepath.Join(oldRoot, "电影"), currentWorkDirectory, filepath.Join(newRoot, "电视剧")} {
		if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("empty artifact directory remains: %s err=%v", directory, err)
		}
	}
	for _, projectionRoot := range []string{oldRoot, newRoot} {
		if info, err := os.Stat(projectionRoot); err != nil || !info.IsDir() {
			t.Fatalf("projection root removed: %s err=%v", projectionRoot, err)
		}
	}
}

func TestAutomaticSTRMCleanupAfterCompleteFullAndIncrementalScans(t *testing.T) {
	for _, scanKind := range []string{"full", "incremental"} {
		t.Run(scanKind, func(t *testing.T) {
			service, _, _, library, root := strmManagementFixture(t)
			run, artifact, path := createAutoCleanupScenario(t, service, library, root, scanKind, false, models.MediaArtifactStatusCompleted)
			workDirectory := filepath.Join(root, "电视剧", "剧情", "七武士", "Season 01")
			if err := os.MkdirAll(workDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			nestedPath := filepath.Join(workDirectory, "七武士 S01E01.strm")
			if err := os.Rename(path, nestedPath); err != nil {
				t.Fatal(err)
			}
			if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Update("relative_path", "/电视剧/剧情/七武士/Season 01/七武士 S01E01.strm").Error; err != nil {
				t.Fatal(err)
			}
			path = nestedPath
			result := service.AutoCleanup(context.Background(), run.ID)
			if result.ErrorCode != "" || result.Skipped || result.Removed != 1 {
				t.Fatalf("result=%+v", result)
			}
			if repeated := service.AutoCleanup(context.Background(), run.ID); repeated.Removed != 0 || repeated.ErrorCode != "" || repeated.Skipped {
				t.Fatalf("repeated=%+v", repeated)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale file remains: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "电视剧")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("empty classification tree remains: %v", err)
			}
			if info, err := os.Stat(root); err != nil || !info.IsDir() {
				t.Fatalf("projection root removed: %v", err)
			}
			var count int64
			if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("manifest count=%d err=%v", count, err)
			}
			if err := service.db.First(&run, "id = ?", run.ID).Error; err != nil || run.RemovedCount != 1 || run.CleanupStatus != models.MediaArtifactCleanupCompleted || run.CleanupErrorCode != "" || run.CleanupAt == nil {
				t.Fatalf("run=%+v err=%v", run, err)
			}
			var audit models.AuditLog
			if err := service.db.Where("action = ?", "strm.cleanup.auto").Order("id DESC").First(&audit).Error; err != nil || audit.ActorID != nil || strings.Contains(audit.Metadata, root) || !strings.Contains(audit.Metadata, `"directory_count":4`) {
				t.Fatalf("audit=%+v err=%v", audit, err)
			}
		})
	}
}

func TestAutomaticCleanupScanKindPolicy(t *testing.T) {
	for _, kind := range []string{"initial", "catch_up", "manual", "event", "incremental", "full", "strm_incremental_manual", "strm_full_manual"} {
		if !automaticCleanupScanKind(kind) {
			t.Errorf("expected %q to be cleanup eligible", kind)
		}
	}
	for _, kind := range []string{"", "failed", "partial", "retry", "unknown"} {
		if automaticCleanupScanKind(kind) {
			t.Errorf("expected %q to be cleanup ineligible", kind)
		}
	}
}

func TestAutomaticSTRMCleanupSkipsPartialFailedAndSupersededRuns(t *testing.T) {
	tests := []struct {
		name      string
		partial   bool
		runStatus string
	}{
		{name: "partial", partial: true, runStatus: models.MediaArtifactStatusCompleted},
		{name: "failed", runStatus: models.MediaArtifactStatusFailed},
		{name: "superseded", runStatus: models.MediaArtifactStatusSuperseded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _, _, library, root := strmManagementFixture(t)
			run, artifact, path := createAutoCleanupScenario(t, service, library, root, "incremental", test.partial, test.runStatus)
			result := service.AutoCleanup(context.Background(), run.ID)
			if result.Removed != 0 || !result.Skipped || result.ErrorCode != "" {
				t.Fatalf("result=%+v", result)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("protected file missing: %v", err)
			}
			var count int64
			if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("manifest count=%d err=%v", count, err)
			}
			if err := service.db.First(&run, "id = ?", run.ID).Error; err != nil || run.CleanupStatus != models.MediaArtifactCleanupSkipped {
				t.Fatalf("run=%+v err=%v", run, err)
			}
		})
	}
}

func TestAutomaticSTRMCleanupRejectsProjectionRootChange(t *testing.T) {
	service, _, _, library, root := strmManagementFixture(t)
	run, artifact, path := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	newRoot := t.TempDir()
	if err := service.db.Model(&models.MediaLibrary{}).Where("id = ?", library.ID).Update("strm_local_root", newRoot).Error; err != nil {
		t.Fatal(err)
	}
	result := service.AutoCleanup(context.Background(), run.ID)
	if result.Removed != 0 || result.ErrorCode != "artifact_cleanup_root_changed" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("old-root file changed: %v", err)
	}
	var count int64
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("manifest count=%d err=%v", count, err)
	}
}

func TestAutomaticSTRMCleanupIgnoresUnmanagedAndIsIdempotent(t *testing.T) {
	service, _, _, library, root := strmManagementFixture(t)
	run, artifact, path := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Update("managed", false).Error; err != nil {
		t.Fatal(err)
	}
	first := service.AutoCleanup(context.Background(), run.ID)
	if first.Removed != 0 || first.ErrorCode != "" {
		t.Fatalf("unmanaged result=%+v", first)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unmanaged file changed: %v", err)
	}
	second := service.AutoCleanup(context.Background(), run.ID)
	if second.Removed != 0 || second.ErrorCode != "" {
		t.Fatalf("second=%+v", second)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unmanaged file changed after repeat: %v", err)
	}
	if err := service.db.First(&run, "id = ?", run.ID).Error; err != nil || run.RemovedCount != 0 || run.CleanupStatus != models.MediaArtifactCleanupCompleted {
		t.Fatalf("run=%+v err=%v", run, err)
	}
}

func TestAutomaticSTRMCleanupPreservesNonEmptyArtifactAncestors(t *testing.T) {
	service, _, _, library, root := strmManagementFixture(t)
	run, artifact, original := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	workDirectory := filepath.Join(root, "电影", "外语电影", "七武士 (1954)")
	path := relocateCleanupArtifact(t, service, artifact, original, root, "/电影/外语电影/七武士 (1954)/七武士 (1954).strm")
	userFile := filepath.Join(workDirectory, "用户说明.txt")
	if err := os.WriteFile(userFile, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := service.AutoCleanup(context.Background(), run.ID)
	if result.ErrorCode != "" || result.Removed != 1 {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed artifact remains: %v", err)
	}
	if content, err := os.ReadFile(userFile); err != nil || string(content) != "keep\n" {
		t.Fatalf("user file content=%q err=%v", content, err)
	}
	if info, err := os.Stat(workDirectory); err != nil || !info.IsDir() {
		t.Fatalf("non-empty work directory removed: %v", err)
	}
}

func TestAutomaticSTRMCleanupRetriesDirectoryFailureAfterFileDeletion(t *testing.T) {
	service, _, _, library, root := strmManagementFixture(t)
	run, artifact, original := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	path := relocateCleanupArtifact(t, service, artifact, original, root, "/电影/外语电影/七武士 (1954)/七武士 (1954).strm")
	workDirectory := filepath.Dir(path)
	service.removeDir = func(string) error { return errors.New("injected directory delete failure") }

	first := service.AutoCleanup(context.Background(), run.ID)
	if first.Removed != 0 || first.ErrorCode != "artifact_cleanup_directory_delete_failed" {
		t.Fatalf("first=%+v", first)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact file was not converged: %v", err)
	}
	if info, err := os.Stat(workDirectory); err != nil || !info.IsDir() {
		t.Fatalf("failed directory unexpectedly changed: %v", err)
	}
	var persisted models.MediaArtifact
	if err := service.db.First(&persisted, "id = ?", artifact.ID).Error; err != nil || persisted.Status != models.MediaArtifactStatusCompleted {
		t.Fatalf("retry manifest=%+v err=%v", persisted, err)
	}
	if err := service.db.First(&run, "id = ?", run.ID).Error; err != nil || run.CleanupStatus != models.MediaArtifactCleanupFailed || run.CleanupErrorCode != "artifact_cleanup_directory_delete_failed" || run.RemovedCount != 0 {
		t.Fatalf("failed run=%+v err=%v", run, err)
	}

	service.removeDir = os.Remove
	second := service.AutoCleanup(context.Background(), run.ID)
	if second.ErrorCode != "" || second.Removed != 1 {
		t.Fatalf("second=%+v", second)
	}
	if _, err := os.Stat(filepath.Join(root, "电影")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty tree remains after retry: %v", err)
	}
	var count int64
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("manifest count=%d err=%v", count, err)
	}
}

func TestAutomaticSTRMCleanupRejectsAncestorReparseCreatedAfterFileDelete(t *testing.T) {
	service, _, _, library, root := strmManagementFixture(t)
	run, artifact, original := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	path := relocateCleanupArtifact(t, service, artifact, original, root, "/电影/七武士/七武士.strm")
	workDirectory := filepath.Dir(path)
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probeLink := filepath.Join(t.TempDir(), "probe-link")
	if err := os.Symlink(outside, probeLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Remove(probeLink); err != nil {
		t.Fatal(err)
	}
	service.removeFile = func(target string) error {
		if err := os.Remove(target); err != nil {
			return err
		}
		if err := os.Remove(workDirectory); err != nil {
			return err
		}
		return os.Symlink(outside, workDirectory)
	}

	result := service.AutoCleanup(context.Background(), run.ID)
	if result.Removed != 0 || result.ErrorCode != "artifact_cleanup_reparse_boundary" {
		t.Fatalf("result=%+v", result)
	}
	if content, err := os.ReadFile(outsideFile); err != nil || string(content) != "outside\n" {
		t.Fatalf("outside content=%q err=%v", content, err)
	}
	var count int64
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("manifest count=%d err=%v", count, err)
	}
}

func TestAutomaticSTRMCleanupStopsAtSymlinkBoundary(t *testing.T) {
	service, _, _, library, root := strmManagementFixture(t)
	run, artifact, original := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "stale.strm")
	if err := os.WriteFile(outsideFile, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Update("relative_path", "/linked/stale.strm").Error; err != nil {
		t.Fatal(err)
	}
	result := service.AutoCleanup(context.Background(), run.ID)
	if result.Removed != 0 || result.ErrorCode != "artifact_cleanup_reparse_boundary" {
		t.Fatalf("result=%+v", result)
	}
	if content, err := os.ReadFile(outsideFile); err != nil || string(content) != "outside\n" {
		t.Fatalf("outside content=%q err=%v", content, err)
	}
}

func TestAutomaticSTRMCleanupRecordsDeleteFailureWithoutRollingBackArtifacts(t *testing.T) {
	service, _, _, library, root := strmManagementFixture(t)
	run, artifact, path := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	service.removeFile = func(string) error { return errors.New("injected delete failure") }
	result := service.AutoCleanup(context.Background(), run.ID)
	if result.Removed != 0 || result.ErrorCode != "artifact_cleanup_delete_failed" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file unexpectedly removed: %v", err)
	}
	var count int64
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("manifest count=%d err=%v", count, err)
	}
	if err := service.db.First(&run, "id = ?", run.ID).Error; err != nil || run.Status != models.MediaArtifactStatusCompleted || run.CleanupStatus != models.MediaArtifactCleanupFailed || run.CleanupErrorCode != "artifact_cleanup_delete_failed" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
}

func TestAutomaticSTRMCleanupRejectsKindExtensionMismatch(t *testing.T) {
	service, _, _, library, root := strmManagementFixture(t)
	run, artifact, path := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	if err := service.db.Model(&models.MediaArtifact{}).Where("id = ?", artifact.ID).Update("kind", models.MediaArtifactKindNFO).Error; err != nil {
		t.Fatal(err)
	}
	result := service.AutoCleanup(context.Background(), run.ID)
	if result.Removed != 0 || result.ErrorCode != "artifact_cleanup_path_kind_invalid" {
		t.Fatalf("result=%+v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file changed: %v", err)
	}
}

func TestAutomaticSTRMCleanupClaimBlocksArtifactReactivation(t *testing.T) {
	service, queue, _, library, root := strmManagementFixture(t)
	run, _, path := createAutoCleanupScenario(t, service, library, root, "full", false, models.MediaArtifactStatusCompleted)
	writer := NewMediaArtifactService(service.db, queue, nil, zerolog.Nop())
	var writerErr error
	service.removeFile = func(target string) error {
		_, writerErr = writer.writeLocalArtifact(root, models.MediaArtifactRun{ID: "new-generation-writer", LibraryID: library.ID}, localArtifactSpec{SourceIdentity: "entry:new", Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/stale.strm", Content: func(models.MediaArtifact) ([]byte, error) { return []byte("new\n"), nil }})
		return os.Remove(target)
	}
	result := service.AutoCleanup(context.Background(), run.ID)
	if result.ErrorCode != "" || result.Removed != 1 || writerErr == nil {
		t.Fatalf("result=%+v writerErr=%v", result, writerErr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path remains: %v", err)
	}
}
