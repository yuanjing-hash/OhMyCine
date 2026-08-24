package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
	"gorm.io/gorm"
)

func TestDownloadRecognitionOverrideSearchesByKeywordAndRetriesExistingProviderTask(t *testing.T) {
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.URL.Path)
		switch request.URL.Path {
		case "/search/tv":
			if request.URL.Query().Get("query") != "Ming Dynasty in 1566" {
				t.Fatalf("query=%q", request.URL.Query().Get("query"))
			}
			_, _ = io.WriteString(writer, `{"results":[{"id":100,"name":"大明王朝1566","original_name":"Ming Dynasty in 1566","original_language":"zh","first_air_date":"2007-01-08"}]}`)
		case "/tv/100":
			_, _ = io.WriteString(writer, `{"id":100,"name":"大明王朝1566","original_name":"Ming Dynasty in 1566","original_language":"zh","first_air_date":"2007-01-08","genres":[{"id":18,"name":"剧情"}],"origin_country":["CN"]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	downloads, _, queue, actor, provider := downloadFixture(t)
	metadata := NewMetadataSettingsService(queue.db, queue.audit, downloads.credentials, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "test-token"})
	metadata.clientFactory = func(tmdb.Credential, string, string) (*tmdb.Client, error) {
		return tmdb.NewForTest("test-token", server.URL, server.Client())
	}
	downloads.SetMetadataSettings(metadata)
	libraryID := uint(99)
	taskID := "download-recognition-recovery"
	job, err := queue.EnqueueWith(EnqueueJobInput{OwnerID: actor.User.ID, JobType: "download", DisplayName: "大明王朝", Provider: models.DownloaderTypePan115Offline, ResourceKey: "provider:115"}, func(tx *gorm.DB, queued models.Job) error {
		return tx.Create(&models.DownloadTask{
			ID: taskID, OwnerID: actor.User.ID, JobID: queued.ID, DownloaderName: "115", ProviderType: models.DownloaderTypePan115Offline,
			ProviderTaskID: "completed-provider-task", SourceCiphertext: "encrypted", DisplayName: "Ming Dynasty in 1566 HQ -BlackTV",
			Phase: models.DownloadTaskStatusFailed, ScrapeStatus: "completed_unrecognized", ScrapeTitle: "Ming Dynasty in 1566", TargetLibraryID: &libraryID,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.db.Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]any{"status": models.JobStatusFailed, "last_error_code": CodeTransferMediaUnrecognized}).Error; err != nil {
		t.Fatal(err)
	}
	deniedReader := Actor{User: models.User{ID: actor.User.ID + 1}, Permissions: map[string]struct{}{authz.PermissionDownloadsReadOwn: {}}}
	if _, err := downloads.RecognitionCandidates(context.Background(), deniedReader, taskID, "Ming Dynasty in 1566", "tv", nil); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("foreign candidate search error=%v code=%s", err, ErrorCode(err))
	}
	deniedController := Actor{User: models.User{ID: actor.User.ID + 1}, Permissions: map[string]struct{}{authz.PermissionJobsControlOwn: {}}}
	if _, err := downloads.OverrideRecognition(context.Background(), deniedController, taskID, DownloadRecognitionOverrideInput{TMDBID: 100, MediaType: "tv"}, RequestContext{}); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("foreign override error=%v code=%s", err, ErrorCode(err))
	}

	candidates, err := downloads.RecognitionCandidates(context.Background(), actor, taskID, "Ming Dynasty in 1566", "tv", nil)
	if err != nil || len(candidates) != 1 || candidates[0].ID != 100 || candidates[0].OriginalTitle != "Ming Dynasty in 1566" {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	result, err := downloads.OverrideRecognition(context.Background(), actor, taskID, DownloadRecognitionOverrideInput{TMDBID: 100, MediaType: "tv"}, RequestContext{})
	if err != nil || result.JobStatus != models.JobStatusQueued {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var persisted models.DownloadTask
	if err := queue.db.First(&persisted, "id = ?", taskID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.RecognitionOverrideTMDBID == nil || *persisted.RecognitionOverrideTMDBID != 100 || persisted.RecognitionOverrideMediaType != "tv" || persisted.ProviderTaskID != "completed-provider-task" {
		t.Fatalf("persisted=%+v", persisted)
	}
	provider.mu.Lock()
	submits := provider.submits
	provider.mu.Unlock()
	if submits != 0 {
		t.Fatalf("manual recovery resubmitted provider task %d time(s)", submits)
	}
	if len(requests) != 2 || requests[0] != "/search/tv" || requests[1] != "/tv/100" {
		t.Fatalf("requests=%v", requests)
	}
	if _, err := downloads.OverrideRecognition(context.Background(), actor, taskID, DownloadRecognitionOverrideInput{TMDBID: 100, MediaType: "tv"}, RequestContext{}); ErrorCode(err) != CodeQueueStateConflict {
		t.Fatalf("duplicate override error=%v code=%s", err, ErrorCode(err))
	}
	if _, err := downloads.RecognitionCandidates(context.Background(), actor, taskID, "Ming Dynasty in 1566", "tv", nil); ErrorCode(err) != CodeQueueStateConflict {
		t.Fatalf("queued recovery candidate search error=%v code=%s", err, ErrorCode(err))
	}
	if len(requests) != 2 {
		t.Fatalf("rejected duplicate recovery called TMDB: requests=%v", requests)
	}
}
