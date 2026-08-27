package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
	"modernc.org/sqlite"
)

const maxProfileNameLength = 128

type ProfileReferenceChecker interface {
	References(profileID uint) ([]string, error)
}

type NoMediaLibraryProfileReferences struct{}

func (NoMediaLibraryProfileReferences) References(uint) ([]string, error) { return nil, nil }

type MediaClassificationProfileService struct {
	db               *gorm.DB
	audit            *AuditService
	references       ProfileReferenceChecker
	revisionNotifier ProfileRevisionNotifier
}

type ProfileRevisionNotifier interface {
	ProfileRevisionChanged(profileID uint, revision uint64) error
}

func NewMediaClassificationProfileService(db *gorm.DB, audit *AuditService, references ProfileReferenceChecker) *MediaClassificationProfileService {
	if references == nil {
		references = NoMediaLibraryProfileReferences{}
	}
	return &MediaClassificationProfileService{db: db, audit: audit, references: references}
}
func (s *MediaClassificationProfileService) SetReferences(references ProfileReferenceChecker) {
	if references != nil {
		s.references = references
	}
}
func (s *MediaClassificationProfileService) SetRevisionNotifier(notifier ProfileRevisionNotifier) {
	s.revisionNotifier = notifier
}

type MediaClassificationProfileSummary struct {
	ID                          uint      `json:"id"`
	Code                        *string   `json:"code"`
	Name                        string    `json:"name"`
	Kind                        string    `json:"kind"`
	Protected                   bool      `json:"protected"`
	SchemaVersion               int       `json:"schema_version"`
	Revision                    uint64    `json:"revision"`
	MovieCategories             int       `json:"movie_category_count"`
	TVCategories                int       `json:"tv_category_count"`
	RecognitionRuleCount        int       `json:"recognition_rule_count"`
	BuiltinRecognitionPackCount int       `json:"builtin_recognition_pack_count"`
	CreatedAt                   time.Time `json:"created_at"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

type MediaClassificationProfileDetail struct {
	MediaClassificationProfileSummary
	Rules                   classification.RulesV1 `json:"rules"`
	BuiltinRecognitionPacks []string               `json:"builtin_recognition_packs"`
	RecognitionRules        []RecognitionRule      `json:"recognition_rules"`
	MovieDirectoryTemplate  string                 `json:"movie_directory_template"`
	MovieFilenameTemplate   string                 `json:"movie_filename_template"`
	TVDirectoryTemplate     string                 `json:"tv_directory_template"`
	TVFilenameTemplate      string                 `json:"tv_filename_template"`
}

type CreateMediaClassificationProfileInput struct {
	Name                    string
	Rules                   json.RawMessage
	BuiltinRecognitionPacks *[]string
	RecognitionRules        *json.RawMessage
	MovieDirectoryTemplate  *string
	MovieFilenameTemplate   *string
	TVDirectoryTemplate     *string
	TVFilenameTemplate      *string
}
type CopyMediaClassificationProfileInput struct{ Name *string }
type UpdateMediaClassificationProfileInput struct {
	Revision                uint64
	Name                    string
	Rules                   json.RawMessage
	BuiltinRecognitionPacks *[]string
	RecognitionRules        *json.RawMessage
	MovieDirectoryTemplate  *string
	MovieFilenameTemplate   *string
	TVDirectoryTemplate     *string
	TVFilenameTemplate      *string
}

func (s *MediaClassificationProfileService) List(actor Actor) ([]MediaClassificationProfileSummary, error) {
	if !actor.Can(authz.PermissionMediaClassificationProfilesRead) {
		return nil, appError(CodePermissionDenied, "无权查看媒体分类规则", nil)
	}
	var records []models.MediaClassificationProfile
	if err := s.db.Order("kind DESC, name_normalized, id").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]MediaClassificationProfileSummary, 0, len(records))
	for _, record := range records {
		detail, err := profileDetail(record)
		if err != nil {
			return nil, err
		}
		items = append(items, detail.MediaClassificationProfileSummary)
	}
	return items, nil
}

func (s *MediaClassificationProfileService) Get(actor Actor, id uint) (MediaClassificationProfileDetail, error) {
	if !actor.Can(authz.PermissionMediaClassificationProfilesRead) {
		return MediaClassificationProfileDetail{}, appError(CodePermissionDenied, "无权查看媒体分类规则", nil)
	}
	var record models.MediaClassificationProfile
	if err := s.db.First(&record, id).Error; err != nil {
		return MediaClassificationProfileDetail{}, profileNotFound(err)
	}
	return profileDetail(record)
}

func (s *MediaClassificationProfileService) Create(actor Actor, input CreateMediaClassificationProfileInput, request RequestContext) (MediaClassificationProfileDetail, error) {
	if !actor.Can(authz.PermissionMediaClassificationProfilesCreate) {
		return MediaClassificationProfileDetail{}, appError(CodePermissionDenied, "无权创建媒体分类规则", nil)
	}
	name, normalized, err := normalizeProfileName(input.Name)
	if err != nil {
		s.auditFailure(actor, "media_classification_profile.create", "", nil, request)
		return MediaClassificationProfileDetail{}, err
	}
	rules := classification.EmptyRules()
	if len(input.Rules) > 0 && string(input.Rules) != "null" {
		rules, err = classification.DecodeStrict(input.Rules)
		if err != nil {
			s.auditFailure(actor, "media_classification_profile.create", "", nil, request)
			return MediaClassificationProfileDetail{}, profileValidation(err)
		}
	}
	rulesJSON, err := classification.CanonicalJSON(rules)
	if err != nil {
		return MediaClassificationProfileDetail{}, profileValidation(err)
	}
	organization, err := resolveProfileOrganizationConfig(models.MediaClassificationProfile{}, input.BuiltinRecognitionPacks, input.RecognitionRules, input.MovieDirectoryTemplate, input.MovieFilenameTemplate, input.TVDirectoryTemplate, input.TVFilenameTemplate)
	if err != nil {
		return MediaClassificationProfileDetail{}, profileValidation(err)
	}
	now := time.Now().UTC()
	record := models.MediaClassificationProfile{Name: name, NameNormalized: normalized, Kind: models.MediaClassificationProfileKindCustom, SchemaVersion: 1, RulesJSON: rulesJSON, BuiltinRecognitionPacksJSON: organization.BuiltinRecognitionPacksJSON, RecognitionRulesJSON: organization.RecognitionRulesJSON, MovieDirectoryTemplate: organization.MovieDirectoryTemplate, MovieFilenameTemplate: organization.MovieFilenameTemplate, TVDirectoryTemplate: organization.TVDirectoryTemplate, TVFilenameTemplate: organization.TVFilenameTemplate, Revision: 1, CreatedAt: now, UpdatedAt: now}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &actor.User.ID, "media_classification_profile.create", "media_classification_profile", uintID(record.ID), "success", auditProfileMetadata(record, rules), request)
	})
	if err != nil {
		s.auditFailure(actor, "media_classification_profile.create", "", nil, request)
		if conflict := profileConstraintError(err); conflict != nil {
			return MediaClassificationProfileDetail{}, conflict
		}
		return MediaClassificationProfileDetail{}, err
	}
	return profileDetail(record)
}

func (s *MediaClassificationProfileService) Copy(actor Actor, id uint, input CopyMediaClassificationProfileInput, request RequestContext) (MediaClassificationProfileDetail, error) {
	if !actor.Can(authz.PermissionMediaClassificationProfilesCreate) {
		return MediaClassificationProfileDetail{}, appError(CodePermissionDenied, "无权复制媒体分类规则", nil)
	}
	var source models.MediaClassificationProfile
	if err := s.db.First(&source, id).Error; err != nil {
		s.auditFailure(actor, "media_classification_profile.copy", uintID(id), nil, request)
		return MediaClassificationProfileDetail{}, profileNotFound(err)
	}
	sourceRules, err := classification.DecodeStrict([]byte(source.RulesJSON))
	if err != nil {
		return MediaClassificationProfileDetail{}, err
	}
	cloned, err := classification.Clone(sourceRules, true)
	if err != nil {
		return MediaClassificationProfileDetail{}, err
	}
	var name, normalized string
	if input.Name != nil {
		name, normalized, err = normalizeProfileName(*input.Name)
		if err != nil {
			s.auditFailure(actor, "media_classification_profile.copy", uintID(id), &source.Revision, request)
			return MediaClassificationProfileDetail{}, err
		}
		detail, createErr := s.createCopyRecord(actor, source, cloned, name, normalized, request)
		if createErr != nil {
			s.auditFailure(actor, "media_classification_profile.copy", uintID(id), &source.Revision, request)
		}
		return detail, createErr
	}

	// Name allocation is optimistic. If another request claims the candidate
	// between lookup and INSERT, retry from the next available suffix.
	for attempt := 0; attempt < 32; attempt++ {
		name, normalized, err = s.nextCopyName(source.Name)
		if err != nil {
			break
		}
		var detail MediaClassificationProfileDetail
		detail, err = s.createCopyRecord(actor, source, cloned, name, normalized, request)
		if ErrorCode(err) != CodeProfileNameConflict {
			return detail, err
		}
	}
	s.auditFailure(actor, "media_classification_profile.copy", uintID(id), &source.Revision, request)
	if err != nil {
		return MediaClassificationProfileDetail{}, err
	}
	return MediaClassificationProfileDetail{}, appError(CodeProfileNameConflict, "无法分配不冲突的副本名称", nil)
}

func (s *MediaClassificationProfileService) createCopyRecord(actor Actor, source models.MediaClassificationProfile, rules classification.RulesV1, name, normalized string, request RequestContext) (MediaClassificationProfileDetail, error) {
	rulesJSON, err := classification.CanonicalJSON(rules)
	if err != nil {
		return MediaClassificationProfileDetail{}, err
	}
	organization, err := storedProfileOrganizationConfig(source)
	if err != nil {
		return MediaClassificationProfileDetail{}, profileValidation(err)
	}
	now := time.Now().UTC()
	record := models.MediaClassificationProfile{Name: name, NameNormalized: normalized, Kind: models.MediaClassificationProfileKindCustom, SchemaVersion: 1, RulesJSON: rulesJSON, BuiltinRecognitionPacksJSON: organization.BuiltinRecognitionPacksJSON, RecognitionRulesJSON: organization.RecognitionRulesJSON, MovieDirectoryTemplate: organization.MovieDirectoryTemplate, MovieFilenameTemplate: organization.MovieFilenameTemplate, TVDirectoryTemplate: organization.TVDirectoryTemplate, TVFilenameTemplate: organization.TVFilenameTemplate, Revision: 1, CreatedAt: now, UpdatedAt: now}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		metadata := auditProfileMetadata(record, rules)
		metadata["source_profile_id"] = source.ID
		return s.audit.Record(tx, &actor.User.ID, "media_classification_profile.copy", "media_classification_profile", uintID(record.ID), "success", metadata, request)
	})
	if err != nil {
		if conflict := profileConstraintError(err); conflict != nil {
			return MediaClassificationProfileDetail{}, conflict
		}
		return MediaClassificationProfileDetail{}, err
	}
	return profileDetail(record)
}

func (s *MediaClassificationProfileService) Update(actor Actor, id uint, input UpdateMediaClassificationProfileInput, request RequestContext) (MediaClassificationProfileDetail, error) {
	if !actor.Can(authz.PermissionMediaClassificationProfilesUpdate) {
		return MediaClassificationProfileDetail{}, appError(CodePermissionDenied, "无权编辑媒体分类规则", nil)
	}
	var existing models.MediaClassificationProfile
	if err := s.db.First(&existing, id).Error; err != nil {
		s.auditFailure(actor, "media_classification_profile.update", uintID(id), nil, request)
		return MediaClassificationProfileDetail{}, profileNotFound(err)
	}
	if existing.Protected || existing.Kind == models.MediaClassificationProfileKindSystem {
		s.auditFailure(actor, "media_classification_profile.update", uintID(id), &existing.Revision, request)
		return MediaClassificationProfileDetail{}, appError(CodeProfileProtected, "内置媒体分类规则不可修改", nil)
	}
	name, normalized, err := normalizeProfileName(input.Name)
	if err != nil {
		s.auditFailure(actor, "media_classification_profile.update", uintID(id), &existing.Revision, request)
		return MediaClassificationProfileDetail{}, err
	}
	if input.Revision == 0 || input.Revision == math.MaxUint64 {
		s.auditFailure(actor, "media_classification_profile.update", uintID(id), &existing.Revision, request)
		return MediaClassificationProfileDetail{}, profileValidation(fmt.Errorf("revision 必须处于可递增范围"))
	}
	rules, err := classification.DecodeStrict(input.Rules)
	if err != nil {
		s.auditFailure(actor, "media_classification_profile.update", uintID(id), &existing.Revision, request)
		return MediaClassificationProfileDetail{}, profileValidation(err)
	}
	rulesJSON, _ := classification.CanonicalJSON(rules)
	organization, err := resolveProfileOrganizationConfig(existing, input.BuiltinRecognitionPacks, input.RecognitionRules, input.MovieDirectoryTemplate, input.MovieFilenameTemplate, input.TVDirectoryTemplate, input.TVFilenameTemplate)
	if err != nil {
		s.auditFailure(actor, "media_classification_profile.update", uintID(id), &existing.Revision, request)
		return MediaClassificationProfileDetail{}, profileValidation(err)
	}
	nextRevision := input.Revision + 1
	now := time.Now().UTC()
	err = s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.MediaClassificationProfile{}).Where("id = ? AND revision = ? AND protected = 0", id, input.Revision).Updates(map[string]any{"name": name, "name_normalized": normalized, "rules_json": rulesJSON, "builtin_recognition_packs_json": organization.BuiltinRecognitionPacksJSON, "recognition_rules_json": organization.RecognitionRulesJSON, "movie_directory_template": organization.MovieDirectoryTemplate, "movie_filename_template": organization.MovieFilenameTemplate, "tv_directory_template": organization.TVDirectoryTemplate, "tv_filename_template": organization.TVFilenameTemplate, "schema_version": 1, "revision": nextRevision, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError(CodeProfileRevisionConflict, "规则已被其他会话更新，请刷新后重试", nil)
		}
		updated := existing
		updated.Name, updated.NameNormalized, updated.RulesJSON, updated.Revision, updated.UpdatedAt = name, normalized, rulesJSON, nextRevision, now
		updated.BuiltinRecognitionPacksJSON = organization.BuiltinRecognitionPacksJSON
		updated.RecognitionRulesJSON = organization.RecognitionRulesJSON
		updated.MovieDirectoryTemplate = organization.MovieDirectoryTemplate
		updated.MovieFilenameTemplate = organization.MovieFilenameTemplate
		updated.TVDirectoryTemplate = organization.TVDirectoryTemplate
		updated.TVFilenameTemplate = organization.TVFilenameTemplate
		return s.audit.Record(tx, &actor.User.ID, "media_classification_profile.update", "media_classification_profile", uintID(id), "success", auditProfileMetadata(updated, rules), request)
	})
	if err != nil {
		s.auditFailure(actor, "media_classification_profile.update", uintID(id), &existing.Revision, request)
		if conflict := profileConstraintError(err); conflict != nil {
			return MediaClassificationProfileDetail{}, conflict
		}
		return MediaClassificationProfileDetail{}, err
	}
	var updated models.MediaClassificationProfile
	if err := s.db.First(&updated, id).Error; err != nil {
		return MediaClassificationProfileDetail{}, profileNotFound(err)
	}
	if s.revisionNotifier != nil {
		if err := s.revisionNotifier.ProfileRevisionChanged(id, updated.Revision); err != nil {
			return MediaClassificationProfileDetail{}, err
		}
	}
	return profileDetail(updated)
}

func (s *MediaClassificationProfileService) Delete(actor Actor, id uint, request RequestContext) error {
	if !actor.Can(authz.PermissionMediaClassificationProfilesDelete) {
		return appError(CodePermissionDenied, "无权删除媒体分类规则", nil)
	}
	var record models.MediaClassificationProfile
	if err := s.db.First(&record, id).Error; err != nil {
		s.auditFailure(actor, "media_classification_profile.delete", uintID(id), nil, request)
		return profileNotFound(err)
	}
	if record.Protected || record.Kind == models.MediaClassificationProfileKindSystem {
		s.auditFailure(actor, "media_classification_profile.delete", uintID(id), &record.Revision, request)
		return appError(CodeProfileProtected, "内置媒体分类规则不可删除", nil)
	}
	references, err := s.references.References(id)
	if err != nil {
		s.auditFailure(actor, "media_classification_profile.delete", uintID(id), &record.Revision, request)
		return err
	}
	if len(references) > 0 {
		s.auditFailure(actor, "media_classification_profile.delete", uintID(id), &record.Revision, request)
		return appError(CodeProfileInUse, "媒体分类规则正在被媒体库使用", nil)
	}
	rules, err := classification.DecodeStrict([]byte(record.RulesJSON))
	if err != nil {
		return err
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&models.MediaClassificationProfile{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return appError(CodeNotFound, "媒体分类规则不存在", nil)
		}
		return s.audit.Record(tx, &actor.User.ID, "media_classification_profile.delete", "media_classification_profile", uintID(id), "success", auditProfileMetadata(record, rules), request)
	})
}

func profileDetail(record models.MediaClassificationProfile) (MediaClassificationProfileDetail, error) {
	rules, err := classification.DecodeStrict([]byte(record.RulesJSON))
	if err != nil {
		return MediaClassificationProfileDetail{}, fmt.Errorf("decode stored classification profile %d: %w", record.ID, err)
	}
	organization, err := storedProfileOrganizationConfig(record)
	if err != nil {
		return MediaClassificationProfileDetail{}, fmt.Errorf("decode stored profile organization %d: %w", record.ID, err)
	}
	summary := MediaClassificationProfileSummary{ID: record.ID, Code: record.Code, Name: record.Name, Kind: record.Kind, Protected: record.Protected, SchemaVersion: record.SchemaVersion, Revision: record.Revision, RecognitionRuleCount: len(organization.RecognitionRules), BuiltinRecognitionPackCount: len(organization.BuiltinRecognitionPacks), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	for _, group := range rules.Groups {
		switch group.MediaType {
		case classification.MediaTypeMovie:
			summary.MovieCategories = len(group.Categories)
		case classification.MediaTypeTV:
			summary.TVCategories = len(group.Categories)
		}
	}
	return MediaClassificationProfileDetail{MediaClassificationProfileSummary: summary, Rules: rules, BuiltinRecognitionPacks: organization.BuiltinRecognitionPacks, RecognitionRules: organization.RecognitionRules, MovieDirectoryTemplate: organization.MovieDirectoryTemplate, MovieFilenameTemplate: organization.MovieFilenameTemplate, TVDirectoryTemplate: organization.TVDirectoryTemplate, TVFilenameTemplate: organization.TVFilenameTemplate}, nil
}

func storedProfileOrganizationConfig(record models.MediaClassificationProfile) (profileOrganizationConfig, error) {
	defaults := defaultProfileOrganizationConfig()
	builtinRaw := []byte(record.BuiltinRecognitionPacksJSON)
	if len(bytes.TrimSpace(builtinRaw)) == 0 {
		builtinRaw = []byte(defaults.BuiltinRecognitionPacksJSON)
	}
	builtinCanonical, builtinPacks, err := canonicalBuiltinRecognitionPacks(builtinRaw)
	if err != nil {
		return profileOrganizationConfig{}, err
	}
	raw := []byte(record.RecognitionRulesJSON)
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("[]")
	}
	canonical, rules, err := canonicalRecognitionRules(raw)
	if err != nil {
		return profileOrganizationConfig{}, err
	}
	config := profileOrganizationConfig{
		BuiltinRecognitionPacksJSON: builtinCanonical,
		BuiltinRecognitionPacks:     builtinPacks,
		RecognitionRulesJSON:        canonical,
		RecognitionRules:            rules,
		MovieDirectoryTemplate:      firstNonEmpty(record.MovieDirectoryTemplate, defaults.MovieDirectoryTemplate),
		MovieFilenameTemplate:       firstNonEmpty(record.MovieFilenameTemplate, defaults.MovieFilenameTemplate),
		TVDirectoryTemplate:         firstNonEmpty(record.TVDirectoryTemplate, defaults.TVDirectoryTemplate),
		TVFilenameTemplate:          firstNonEmpty(record.TVFilenameTemplate, defaults.TVFilenameTemplate),
	}
	if err := validateProfileTemplates(config); err != nil {
		return profileOrganizationConfig{}, err
	}
	return config, nil
}

func resolveProfileOrganizationConfig(existing models.MediaClassificationProfile, builtinRecognitionPacks *[]string, recognitionRules *json.RawMessage, movieDirectory, movieFilename, tvDirectory, tvFilename *string) (profileOrganizationConfig, error) {
	config := defaultProfileOrganizationConfig()
	if existing.ID != 0 {
		stored, err := storedProfileOrganizationConfig(existing)
		if err != nil {
			return profileOrganizationConfig{}, err
		}
		config = stored
	}
	if builtinRecognitionPacks != nil {
		raw, err := json.Marshal(*builtinRecognitionPacks)
		if err != nil {
			return profileOrganizationConfig{}, err
		}
		canonical, packs, err := canonicalBuiltinRecognitionPacks(raw)
		if err != nil {
			return profileOrganizationConfig{}, err
		}
		config.BuiltinRecognitionPacksJSON, config.BuiltinRecognitionPacks = canonical, packs
	}
	if recognitionRules != nil {
		canonical, rules, err := canonicalRecognitionRules(*recognitionRules)
		if err != nil {
			return profileOrganizationConfig{}, err
		}
		config.RecognitionRulesJSON, config.RecognitionRules = canonical, rules
	}
	if movieDirectory != nil {
		config.MovieDirectoryTemplate = strings.TrimSpace(*movieDirectory)
	}
	if movieFilename != nil {
		config.MovieFilenameTemplate = strings.TrimSpace(*movieFilename)
	}
	if tvDirectory != nil {
		config.TVDirectoryTemplate = strings.TrimSpace(*tvDirectory)
	}
	if tvFilename != nil {
		config.TVFilenameTemplate = strings.TrimSpace(*tvFilename)
	}
	if err := validateProfileTemplates(config); err != nil {
		return profileOrganizationConfig{}, err
	}
	return config, nil
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeProfileName(value string) (string, string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", "", appError(CodeProfileNameRequired, "规则名称不能为空", nil)
	}
	if len([]rune(name)) > maxProfileNameLength {
		return "", "", profileValidation(fmt.Errorf("规则名称不能超过 %d 个字符", maxProfileNameLength))
	}
	normalized := cases.Fold().String(norm.NFKC.String(name))
	return name, normalized, nil
}

func (s *MediaClassificationProfileService) nextCopyName(sourceName string) (string, string, error) {
	for index := 1; index < 10000; index++ {
		suffix := " 副本"
		if index > 1 {
			suffix = fmt.Sprintf(" 副本 %d", index)
		}
		baseRunes, suffixRunes := []rune(sourceName), []rune(suffix)
		if available := maxProfileNameLength - len(suffixRunes); len(baseRunes) > available {
			baseRunes = baseRunes[:available]
		}
		candidate := strings.TrimSpace(string(baseRunes)) + suffix
		name, normalized, err := normalizeProfileName(candidate)
		if err != nil {
			return "", "", err
		}
		var count int64
		if err := s.db.Model(&models.MediaClassificationProfile{}).Where("name_normalized = ?", normalized).Count(&count).Error; err != nil {
			return "", "", err
		}
		if count == 0 {
			return name, normalized, nil
		}
	}
	return "", "", appError(CodeProfileNameConflict, "无法分配不冲突的副本名称", nil)
}

func auditProfileMetadata(record models.MediaClassificationProfile, rules classification.RulesV1) map[string]any {
	movie, tv := 0, 0
	for _, group := range rules.Groups {
		if group.MediaType == classification.MediaTypeMovie {
			movie = len(group.Categories)
		} else {
			tv = len(group.Categories)
		}
	}
	organization, _ := storedProfileOrganizationConfig(record)
	return map[string]any{"kind": record.Kind, "revision": record.Revision, "movie_category_count": movie, "tv_category_count": tv, "builtin_recognition_pack_count": len(organization.BuiltinRecognitionPacks), "recognition_rule_count": len(organization.RecognitionRules)}
}
func (s *MediaClassificationProfileService) auditFailure(actor Actor, action, targetID string, revision *uint64, request RequestContext) {
	metadata := map[string]any{}
	if revision != nil {
		metadata["revision"] = *revision
	}
	_ = s.audit.Record(nil, &actor.User.ID, action, "media_classification_profile", targetID, "failure", metadata, request)
}
func profileValidation(err error) error { return appError(CodeProfileValidation, err.Error(), err) }
func profileNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return appError(CodeNotFound, "媒体分类规则不存在", err)
	}
	return err
}
func profileConstraintError(err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == 2067 {
		return appError(CodeProfileNameConflict, "规则名称已存在", err)
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "name_normalized") || strings.Contains(message, "unique constraint") {
		return appError(CodeProfileNameConflict, "规则名称已存在", err)
	}
	return nil
}
