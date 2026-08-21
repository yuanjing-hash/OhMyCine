package services

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

func TestMetadataSettingsEncryptAndRedactTMDBToken(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsRead] = struct{}{}
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "metadata.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewMetadataSettingsService(queue.db, queue.audit, store)
	service.apiTester = func(context.Context, tmdb.Credential, string, string) error { return nil }
	const token = "eyJhbGciOiJIUzI1NiJ9.safe-test-token"
	updated, err := service.TestAndSetToken(context.Background(), actor, UpdateMetadataSettingsInput{TMDBToken: token, Revision: 1}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.TMDBConfigured || updated.Revision != 2 {
		t.Fatalf("updated=%+v", updated)
	}
	var record models.MetadataSettings
	if err := queue.db.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.TMDBTokenCiphertext == token || strings.Contains(record.TMDBTokenCiphertext, "safe-test-token") {
		t.Fatal("TMDB token was stored in plaintext")
	}
	listed, err := service.Get(actor)
	if err != nil || !listed.TMDBConfigured {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	cleared, err := service.Update(actor, UpdateMetadataSettingsInput{ClearTMDB: true, Revision: updated.Revision}, RequestContext{})
	if err != nil || cleared.TMDBConfigured {
		t.Fatalf("cleared=%+v err=%v", cleared, err)
	}
}

func TestMetadataCredentialPriorityAndClearFallback(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsRead] = struct{}{}
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "priority.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	previousBuiltin := tmdb.BuiltinReadAccessToken
	tmdb.BuiltinReadAccessToken = "builtin-token"
	t.Cleanup(func() { tmdb.BuiltinReadAccessToken = previousBuiltin })
	service := NewMetadataSettingsService(queue.db, queue.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindAPIKey, Value: "deployment-api-key"})
	service.apiTester = func(context.Context, tmdb.Credential, string, string) error { return nil }
	initial, err := service.Get(actor)
	if err != nil || initial.CredentialSource != "deployment" || initial.CredentialKind != string(tmdb.CredentialKindAPIKey) {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	custom, err := service.TestAndSetToken(context.Background(), actor, UpdateMetadataSettingsInput{TMDBToken: "custom-token", CredentialKind: string(tmdb.CredentialKindReadAccessToken), Revision: initial.Revision}, RequestContext{})
	if err != nil || custom.CredentialSource != "custom" || custom.CredentialKind != string(tmdb.CredentialKindReadAccessToken) || !custom.CustomConfigured {
		t.Fatalf("custom=%+v err=%v", custom, err)
	}
	fallback, err := service.Update(actor, UpdateMetadataSettingsInput{ClearTMDB: true, Revision: custom.Revision}, RequestContext{})
	if err != nil || fallback.CredentialSource != "deployment" || fallback.CredentialKind != string(tmdb.CredentialKindAPIKey) || fallback.CustomConfigured {
		t.Fatalf("fallback=%+v err=%v", fallback, err)
	}
	builtinService := NewMetadataSettingsService(queue.db, queue.audit, store)
	builtin, err := builtinService.Get(actor)
	if err != nil || builtin.CredentialSource != "builtin" || builtin.CredentialKind != string(tmdb.CredentialKindReadAccessToken) {
		t.Fatalf("builtin=%+v err=%v", builtin, err)
	}
}

func TestMetadataAPIKeyCandidateUsesExplicitKindAndIsEncrypted(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsRead] = struct{}{}
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "api-key.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewMetadataSettingsService(queue.db, queue.audit, store)
	const apiKey = "0123456789abcdef0123456789abcdef"
	service.apiTester = func(_ context.Context, credential tmdb.Credential, _, _ string) error {
		if credential.Kind != tmdb.CredentialKindAPIKey || credential.Value != apiKey {
			t.Fatalf("candidate=%+v", credential)
		}
		return nil
	}
	saved, err := service.TestAndSetToken(context.Background(), actor, UpdateMetadataSettingsInput{TMDBToken: apiKey, CredentialKind: string(tmdb.CredentialKindAPIKey), Revision: 1}, RequestContext{})
	if err != nil || saved.CredentialKind != string(tmdb.CredentialKindAPIKey) || saved.CredentialSource != "custom" {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	var record models.MetadataSettings
	if err := queue.db.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.TMDBCredentialKind != string(tmdb.CredentialKindAPIKey) || record.TMDBTokenCiphertext == apiKey || strings.Contains(record.TMDBTokenCiphertext, apiKey) {
		t.Fatalf("unsafe record=%+v", record)
	}
	if _, err := service.TestAndSetToken(context.Background(), actor, UpdateMetadataSettingsInput{TMDBToken: "secret", CredentialKind: "automatic", Revision: saved.Revision}, RequestContext{}); ErrorCode(err) != CodeTMDBTokenInvalid {
		t.Fatalf("invalid kind error=%v", err)
	}
}

func TestMetadataBuiltinAPIKeyReportsExplicitKind(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsRead] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "builtin-api-key.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	previousToken, previousAPIKey := tmdb.BuiltinReadAccessToken, tmdb.BuiltinAPIKey
	tmdb.BuiltinReadAccessToken, tmdb.BuiltinAPIKey = "", "builtin-api-key"
	t.Cleanup(func() { tmdb.BuiltinReadAccessToken, tmdb.BuiltinAPIKey = previousToken, previousAPIKey })
	summary, err := NewMetadataSettingsService(queue.db, queue.audit, store).Get(actor)
	if err != nil || summary.CredentialSource != "builtin" || summary.CredentialKind != string(tmdb.CredentialKindAPIKey) {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestMetadataClientFactoryReceivesEffectiveCredentialAndPersistedWorkerRoutes(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "worker-route.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewMetadataSettingsService(queue.db, queue.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "deployment-token"})
	service.apiTester = func(context.Context, tmdb.Credential, string, string) error { return nil }
	if _, err := service.TestAndSetToken(context.Background(), actor, UpdateMetadataSettingsInput{TMDBToken: "custom-token", Revision: 1}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	const apiRoute = "https://api.worker.test/3"
	const imageRoute = "https://images.worker.test/t/p"
	if err := queue.db.Model(&models.MetadataSettings{}).Where("id = ?", 1).Updates(map[string]any{"api_base_url": apiRoute, "image_base_url": imageRoute}).Error; err != nil {
		t.Fatal(err)
	}
	var gotToken, gotAPI, gotImage string
	var gotKind tmdb.CredentialKind
	service.clientFactory = func(credential tmdb.Credential, apiBase, imageBase string) (*tmdb.Client, error) {
		gotToken, gotKind, gotAPI, gotImage = credential.Value, credential.Kind, apiBase, imageBase
		return &tmdb.Client{}, nil
	}
	if _, err := service.Client(); err != nil {
		t.Fatal(err)
	}
	if gotToken != "custom-token" || gotKind != tmdb.CredentialKindReadAccessToken || gotAPI != apiRoute || gotImage != imageRoute {
		t.Fatalf("worker client inputs kind=%q token=%q api=%q image=%q", gotKind, gotToken, gotAPI, gotImage)
	}
}

func TestMetadataCandidateTokenPersistsOnlyAfterProbeAndRevisionCAS(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	actor.Permissions[authz.PermissionSettingsRead] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "candidate-token.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewMetadataSettingsService(queue.db, queue.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "deployment-token"})
	probeCount := 0
	service.apiTester = func(_ context.Context, credential tmdb.Credential, _, _ string) error {
		probeCount++
		if credential.Value == "valid-candidate" {
			return nil
		}
		return errors.New("authentication rejected")
	}
	initial, err := service.Get(actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.TestAndSetToken(context.Background(), actor, UpdateMetadataSettingsInput{TMDBToken: "bad-candidate", Revision: initial.Revision}, RequestContext{}); ErrorCode(err) != CodeTMDBUnavailable {
		t.Fatalf("bad candidate error=%v", err)
	}
	unchanged, err := service.Get(actor)
	if err != nil || unchanged.CredentialSource != "deployment" || unchanged.CustomConfigured || unchanged.Revision != initial.Revision {
		t.Fatalf("failed candidate mutated settings=%+v err=%v", unchanged, err)
	}
	if err := queue.db.Model(&models.MetadataSettings{}).Where("id = ?", 1).Update("revision", initial.Revision+1).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.TestAndSetToken(context.Background(), actor, UpdateMetadataSettingsInput{TMDBToken: "valid-candidate", Revision: initial.Revision}, RequestContext{}); ErrorCode(err) != CodeConflict {
		t.Fatalf("stale candidate error=%v", err)
	}
	if probeCount != 1 {
		t.Fatalf("stale candidate was sent to a metadata route: probes=%d", probeCount)
	}
	current, err := service.Get(actor)
	if err != nil || current.CredentialSource != "deployment" || current.CustomConfigured || current.Revision != initial.Revision+1 {
		t.Fatalf("stale candidate changed credential=%+v err=%v", current, err)
	}
	saved, err := service.TestAndSetToken(context.Background(), actor, UpdateMetadataSettingsInput{TMDBToken: "valid-candidate", Revision: current.Revision}, RequestContext{})
	if err != nil || saved.CredentialSource != "custom" || !saved.CustomConfigured || saved.Revision != current.Revision+1 {
		t.Fatalf("saved candidate=%+v err=%v", saved, err)
	}
}

func TestMetadataRoutesPersistIndependentlyOnlyAfterSuccessfulTest(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	actor.Permissions[authz.PermissionSettingsRead] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "routes.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewMetadataSettingsService(queue.db, queue.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "deployment-token"})
	service.apiTester = func(_ context.Context, _ tmdb.Credential, apiBase, _ string) error {
		if apiBase == "https://api.good.test/3" {
			return nil
		}
		return errors.New("rejected")
	}
	service.imageTester = func(_ context.Context, imageBase string) error {
		if imageBase == "https://images.good.test/t/p" {
			return nil
		}
		return errors.New("rejected")
	}
	apiSaved, err := service.TestAndSetAPI(context.Background(), actor, UpdateTMDBRouteInput{BaseURL: "https://api.good.test/3", Revision: 1}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if apiSaved.APIBaseURL != "https://api.good.test/3" || apiSaved.ImageBaseURL != tmdb.DefaultImageBaseURL {
		t.Fatalf("api=%+v", apiSaved)
	}
	if _, err := service.TestAndSetImage(context.Background(), actor, UpdateTMDBRouteInput{BaseURL: "https://images.bad.test/t/p", Revision: apiSaved.Revision}, RequestContext{}); ErrorCode(err) != CodeTMDBUnavailable {
		t.Fatalf("bad image err=%v", err)
	}
	unchanged, _ := service.Get(actor)
	if unchanged.APIBaseURL != apiSaved.APIBaseURL || unchanged.ImageBaseURL != tmdb.DefaultImageBaseURL || unchanged.Revision != apiSaved.Revision {
		t.Fatalf("failed test mutated route: %+v", unchanged)
	}
	imageSaved, err := service.TestAndSetImage(context.Background(), actor, UpdateTMDBRouteInput{BaseURL: "https://images.good.test/t/p", Revision: unchanged.Revision}, RequestContext{})
	if err != nil || imageSaved.ImageBaseURL != "https://images.good.test/t/p" || imageSaved.APIBaseURL != apiSaved.APIBaseURL {
		t.Fatalf("image=%+v err=%v", imageSaved, err)
	}
}

func TestMetadataRouteCASConflictPreservesConcurrentRouteAndOtherField(t *testing.T) {
	for _, kind := range []string{"api", "image"} {
		t.Run(kind, func(t *testing.T) {
			queue, actor, _ := queueFixture(t)
			actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
			actor.Permissions[authz.PermissionSettingsRead] = struct{}{}
			store, err := credential.Open(filepath.Join(t.TempDir(), "route-cas.key"), "")
			if err != nil {
				t.Fatal(err)
			}
			service := NewMetadataSettingsService(queue.db, queue.audit, store, tmdb.Credential{Kind: tmdb.CredentialKindReadAccessToken, Value: "deployment-token"})
			probeCount := 0
			service.apiTester = func(context.Context, tmdb.Credential, string, string) error { probeCount++; return nil }
			service.imageTester = func(context.Context, string) error { probeCount++; return nil }
			initial, err := service.Get(actor)
			if err != nil {
				t.Fatal(err)
			}
			concurrentAPI, concurrentImage := "https://api.concurrent.test/3", "https://images.concurrent.test/t/p"
			if err := queue.db.Model(&models.MetadataSettings{}).Where("id = ?", 1).Updates(map[string]any{"api_base_url": concurrentAPI, "image_base_url": concurrentImage, "revision": initial.Revision + 1}).Error; err != nil {
				t.Fatal(err)
			}
			input := UpdateTMDBRouteInput{Revision: initial.Revision}
			if kind == "api" {
				input.BaseURL = "https://api.candidate.test/3"
				_, err = service.TestAndSetAPI(context.Background(), actor, input, RequestContext{})
			} else {
				input.BaseURL = "https://images.candidate.test/t/p"
				_, err = service.TestAndSetImage(context.Background(), actor, input, RequestContext{})
			}
			if ErrorCode(err) != CodeConflict {
				t.Fatalf("stale %s route error=%v", kind, err)
			}
			if probeCount != 0 {
				t.Fatalf("stale %s route triggered an external probe", kind)
			}
			current, getErr := service.Get(actor)
			if getErr != nil || current.APIBaseURL != concurrentAPI || current.ImageBaseURL != concurrentImage || current.Revision != initial.Revision+1 {
				t.Fatalf("conflict changed settings: current=%+v err=%v", current, getErr)
			}
		})
	}
}

func TestMetadataSettingsRejectsAmbiguousMutationAndRevisionOverflow(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "metadata.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewMetadataSettingsService(queue.db, queue.audit, store)
	if _, err := service.Update(actor, UpdateMetadataSettingsInput{TMDBToken: "token", ClearTMDB: true, Revision: 1}, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("clear+token error=%v", err)
	}
	if _, err := service.Update(actor, UpdateMetadataSettingsInput{TMDBToken: "token", Revision: math.MaxInt64}, RequestContext{}); ErrorCode(err) != CodeConflict {
		t.Fatalf("overflow error=%v", err)
	}
	var record models.MetadataSettings
	if err := queue.db.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.Revision != 1 || record.TMDBTokenCiphertext != "" {
		t.Fatalf("rejected mutation changed record: %+v", record)
	}
}
