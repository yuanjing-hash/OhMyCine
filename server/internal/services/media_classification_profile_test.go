package services

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
)

func profileTestService(t *testing.T) (*MediaClassificationProfileService, Actor) {
	db, err := database.Open(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	audit := NewAuditService(db)
	user := models.User{Username: "profile-test", UsernameNormalized: "profile-test", DisplayName: "Profile Test", PasswordHash: "not-used", Status: models.UserStatusActive, AuthzVersion: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	actor := Actor{User: user, Permissions: map[string]struct{}{authz.PermissionMediaClassificationProfilesRead: {}, authz.PermissionMediaClassificationProfilesCreate: {}, authz.PermissionMediaClassificationProfilesUpdate: {}, authz.PermissionMediaClassificationProfilesDelete: {}}}
	return NewMediaClassificationProfileService(db, audit, nil), actor
}

func TestProfileLifecycleCopyRevisionAndAuditSafety(t *testing.T) {
	service, actor := profileTestService(t)
	list, err := service.List(actor)
	if err != nil || len(list) != 1 || list[0].Code == nil || *list[0].Code != "default-v1" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if _, err := service.Update(actor, list[0].ID, UpdateMediaClassificationProfileInput{}, RequestContext{}); ErrorCode(err) != CodeProfileProtected {
		t.Fatalf("protected update=%v", err)
	}
	created, err := service.Create(actor, CreateMediaClassificationProfileInput{Name: " Custom "}, RequestContext{RequestID: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Rules.Groups) != 2 || created.Revision != 1 {
		t.Fatalf("created=%+v", created)
	}
	copied, err := service.Copy(actor, created.ID, CopyMediaClassificationProfileInput{}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if copied.Name != "Custom 副本" || copied.ID == created.ID {
		t.Fatalf("copied=%+v", copied)
	}
	rules := classification.DefaultRules()
	payload, _ := json.Marshal(rules)
	updated, err := service.Update(actor, created.ID, UpdateMediaClassificationProfileInput{Revision: 1, Name: "Custom", Rules: payload}, RequestContext{})
	if err != nil || updated.Revision != 2 {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	if _, err := service.Update(actor, created.ID, UpdateMediaClassificationProfileInput{Revision: 1, Name: "Custom", Rules: payload}, RequestContext{}); ErrorCode(err) != CodeProfileRevisionConflict {
		t.Fatalf("stale=%v", err)
	}
	if err := service.Delete(actor, copied.ID, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	entries, err := service.audit.List(100)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(entries)
	if strings.Contains(string(raw), "rules_json") || strings.Contains(string(raw), "动画电影") {
		t.Fatalf("audit leaked rules: %s", raw)
	}
}

func TestProfilePolicyAndNameConflicts(t *testing.T) {
	service, actor := profileTestService(t)
	denied := actor
	denied.Permissions = map[string]struct{}{}
	if _, err := service.List(denied); ErrorCode(err) != CodePermissionDenied {
		t.Fatal(err)
	}
	if _, err := service.Create(actor, CreateMediaClassificationProfileInput{Name: "Café"}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(actor, CreateMediaClassificationProfileInput{Name: "CAFE\u0301"}, RequestContext{}); ErrorCode(err) != CodeProfileNameConflict {
		t.Fatalf("normalized conflict=%v", err)
	}
	longName := strings.Repeat("长", maxProfileNameLength)
	longProfile, err := service.Create(actor, CreateMediaClassificationProfileInput{Name: longName}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	copy, err := service.Copy(actor, longProfile.ID, CopyMediaClassificationProfileInput{}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(copy.Name)) > maxProfileNameLength || !strings.HasSuffix(copy.Name, " 副本") {
		t.Fatalf("copy name=%q", copy.Name)
	}
}

func TestProfileServiceEnforcesEveryActionPermission(t *testing.T) {
	service, actor := profileTestService(t)
	builtin, err := service.List(actor)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(actor, CreateMediaClassificationProfileInput{Name: "Policy Target"}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(created.Rules)
	denied := actor
	denied.Permissions = map[string]struct{}{}

	checks := []struct {
		name string
		run  func() error
	}{
		{"list", func() error { _, err := service.List(denied); return err }},
		{"get", func() error { _, err := service.Get(denied, builtin[0].ID); return err }},
		{"create", func() error {
			_, err := service.Create(denied, CreateMediaClassificationProfileInput{Name: "Denied"}, RequestContext{})
			return err
		}},
		{"copy", func() error {
			_, err := service.Copy(denied, builtin[0].ID, CopyMediaClassificationProfileInput{}, RequestContext{})
			return err
		}},
		{"update", func() error {
			_, err := service.Update(denied, created.ID, UpdateMediaClassificationProfileInput{Revision: 1, Name: created.Name, Rules: payload}, RequestContext{})
			return err
		}},
		{"delete", func() error { return service.Delete(denied, created.ID, RequestContext{}) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if code := ErrorCode(check.run()); code != CodePermissionDenied {
				t.Fatalf("code=%q", code)
			}
		})
	}
}

type fixedProfileReferences []string

func (references fixedProfileReferences) References(uint) ([]string, error) { return references, nil }

func TestProfileCopyIsDeepAndReferenceCheckerBlocksDelete(t *testing.T) {
	service, actor := profileTestService(t)
	builtin, err := service.List(actor)
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.Get(actor, builtin[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := service.Copy(actor, source.ID, CopyMediaClassificationProfileInput{}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(categoryIDs(source.Rules), categoryIDs(copy.Rules)) {
		t.Fatal("copy retained source category ids")
	}
	sourceWithoutIDs, copyWithoutIDs := source.Rules, copy.Rules
	clearCategoryIDs(&sourceWithoutIDs)
	clearCategoryIDs(&copyWithoutIDs)
	if !reflect.DeepEqual(sourceWithoutIDs, copyWithoutIDs) {
		t.Fatalf("copy payload drifted\nsource=%+v\ncopy=%+v", sourceWithoutIDs, copyWithoutIDs)
	}

	service.references = fixedProfileReferences{"library:1"}
	if err := service.Delete(actor, copy.ID, RequestContext{}); ErrorCode(err) != CodeProfileInUse {
		t.Fatalf("delete code=%q err=%v", ErrorCode(err), err)
	}
}

func TestProfileOwnsRecognitionAndNamingConfiguration(t *testing.T) {
	service, actor := profileTestService(t)
	created, err := service.Create(actor, CreateMediaClassificationProfileInput{Name: "Release Profile"}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	rulesPayload, _ := json.Marshal(created.Rules)
	recognitionPayload := json.RawMessage(`[{"enabled":true,"media_type":"all","pattern":"^【[^】]*发布[^】]*】","replacement":""},{"enabled":true,"media_type":"movie","pattern":"\\.CC\\.","replacement":"."}]`)
	movieDirectory := "电影/{category}/{title} ({year})"
	movieFilename := "{title} ({year})"
	tvDirectory := "剧集/{category}/{title} ({year})/Season {season:02}"
	tvFilename := "{title} - S{season:02}E{episode:02}"
	updated, err := service.Update(actor, created.ID, UpdateMediaClassificationProfileInput{Revision: created.Revision, Name: created.Name, Rules: rulesPayload, RecognitionRules: &recognitionPayload, MovieDirectoryTemplate: &movieDirectory, MovieFilenameTemplate: &movieFilename, TVDirectoryTemplate: &tvDirectory, TVFilenameTemplate: &tvFilename}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.RecognitionRuleCount != 2 || len(updated.RecognitionRules) != 2 || updated.MovieDirectoryTemplate != movieDirectory || updated.TVFilenameTemplate != tvFilename {
		t.Fatalf("updated=%+v", updated)
	}
	copied, err := service.Copy(actor, updated.ID, CopyMediaClassificationProfileInput{}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(copied.RecognitionRules, updated.RecognitionRules) || copied.MovieDirectoryTemplate != updated.MovieDirectoryTemplate || copied.TVFilenameTemplate != updated.TVFilenameTemplate {
		t.Fatalf("copy lost recognition/naming config: %+v", copied)
	}
	invalid := json.RawMessage(`[{"enabled":true,"media_type":"all","pattern":"(","replacement":""}]`)
	if _, err := service.Update(actor, updated.ID, UpdateMediaClassificationProfileInput{Revision: updated.Revision, Name: updated.Name, Rules: rulesPayload, RecognitionRules: &invalid}, RequestContext{}); ErrorCode(err) != CodeProfileValidation {
		t.Fatalf("invalid regex error=%v", err)
	}
}

func TestProfileBuiltinRecognitionPacksDefaultValidateAndCopy(t *testing.T) {
	service, actor := profileTestService(t)
	created, err := service.Create(actor, CreateMediaClassificationProfileInput{Name: "Built-in Pack Profile"}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(created.BuiltinRecognitionPacks, []string{"tv-v1", "anime-v1"}) || created.BuiltinRecognitionPackCount != 2 {
		t.Fatalf("created packs=%v count=%d", created.BuiltinRecognitionPacks, created.BuiltinRecognitionPackCount)
	}
	rulesPayload, _ := json.Marshal(created.Rules)
	empty := []string{}
	updated, err := service.Update(actor, created.ID, UpdateMediaClassificationProfileInput{Revision: created.Revision, Name: created.Name, Rules: rulesPayload, BuiltinRecognitionPacks: &empty}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.BuiltinRecognitionPacks == nil || len(updated.BuiltinRecognitionPacks) != 0 {
		t.Fatalf("explicitly disabled packs=%v", updated.BuiltinRecognitionPacks)
	}
	copied, err := service.Copy(actor, updated.ID, CopyMediaClassificationProfileInput{}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if copied.BuiltinRecognitionPacks == nil || len(copied.BuiltinRecognitionPacks) != 0 {
		t.Fatalf("copy packs=%v", copied.BuiltinRecognitionPacks)
	}

	reordered := []string{"anime-v1", "tv-v1"}
	reorderedProfile, err := service.Update(actor, updated.ID, UpdateMediaClassificationProfileInput{Revision: updated.Revision, Name: updated.Name, Rules: rulesPayload, BuiltinRecognitionPacks: &reordered}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reorderedProfile.BuiltinRecognitionPacks, []string{"tv-v1", "anime-v1"}) {
		t.Fatalf("normalized packs=%v", reorderedProfile.BuiltinRecognitionPacks)
	}
	for name, packs := range map[string][]string{
		"unknown":   {"unknown-v1"},
		"duplicate": {"tv-v1", "tv-v1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Update(actor, reorderedProfile.ID, UpdateMediaClassificationProfileInput{Revision: reorderedProfile.Revision, Name: reorderedProfile.Name, Rules: rulesPayload, BuiltinRecognitionPacks: &packs}, RequestContext{}); ErrorCode(err) != CodeProfileValidation {
				t.Fatalf("packs=%v error=%v", packs, err)
			}
		})
	}
}

func TestRecognitionRulesApplyInOrderAndRespectMediaType(t *testing.T) {
	raw := []byte(`[{"enabled":true,"media_type":"all","pattern":"^【[^】]*】","replacement":""},{"enabled":true,"media_type":"movie","pattern":"^(.*)\\.FIXED$","replacement":"${1}.1954"},{"enabled":true,"media_type":"tv","pattern":"FIXED","replacement":"TV"}]`)
	_, rules, err := canonicalRecognitionRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := applyRecognitionRules("【发布站】Seven.Samurai.FIXED", "movie", rules); got != "Seven.Samurai.1954" {
		t.Fatalf("movie result=%q", got)
	}
	if got := applyRecognitionRules("【发布站】Series.FIXED", "tv", rules); got != "Series.TV" {
		t.Fatalf("tv result=%q", got)
	}
}

func TestRecognitionRulesRejectNullAndMissingMediaType(t *testing.T) {
	for name, raw := range map[string][]byte{
		"null":               []byte(`null`),
		"missing media type": []byte(`[{"enabled":true,"pattern":"x","replacement":""}]`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := canonicalRecognitionRules(raw); err == nil {
				t.Fatalf("raw=%s was accepted", raw)
			}
		})
	}
}

func categoryIDs(rules classification.RulesV1) []string {
	var ids []string
	for _, group := range rules.Groups {
		for _, category := range group.Categories {
			ids = append(ids, category.ID)
		}
	}
	return ids
}
func clearCategoryIDs(rules *classification.RulesV1) {
	for groupIndex := range rules.Groups {
		for categoryIndex := range rules.Groups[groupIndex].Categories {
			rules.Groups[groupIndex].Categories[categoryIndex].ID = ""
		}
	}
}
