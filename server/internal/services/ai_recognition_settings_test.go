package services

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/aiprovider"
)

type fakeAIProvider struct {
	tests     int
	lists     int
	generates int
	response  []byte
}

func (p *fakeAIProvider) Test(context.Context) error { p.tests++; return nil }
func (p *fakeAIProvider) ListModels(context.Context) ([]aiprovider.Model, error) {
	p.lists++
	return []aiprovider.Model{{ID: "fixture-model", DisplayName: "Fixture"}}, nil
}
func (p *fakeAIProvider) GenerateStructured(context.Context, aiprovider.StructuredRequest) ([]byte, error) {
	p.generates++
	return p.response, nil
}

func TestAIRecognitionSettingsAreOptInEncryptedAndRevisionBound(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsRead] = struct{}{}
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "ai.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewAIRecognitionSettingsService(queue.db, queue.audit, store)
	initial, err := service.Get(actor)
	if err != nil || initial.Enabled || initial.APIKeyConfigured || initial.ProviderType != aiprovider.ProviderOpenAICompatible || initial.Revision != 1 {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	if _, enabled, err := service.RuntimeConfig(); err != nil || enabled {
		t.Fatalf("disabled runtime config enabled=%v err=%v", enabled, err)
	}
	const secret = "sk-secret-provider-key"
	saved, err := service.Update(actor, UpdateAIRecognitionSettingsInput{ProviderType: aiprovider.ProviderOpenAICompatible, BaseURL: "https://api.example.com/v1", APIKey: secret, Model: "fixture-model", Revision: initial.Revision}, RequestContext{})
	if err != nil || saved.Enabled || !saved.APIKeyConfigured || saved.Revision != 2 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	var record models.AIRecognitionSettings
	if err := queue.db.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.APIKeyCiphertext == secret || strings.Contains(record.APIKeyCiphertext, secret) {
		t.Fatal("AI API key stored in plaintext")
	}
	if _, err := service.Update(actor, UpdateAIRecognitionSettingsInput{Enabled: true, ProviderType: saved.ProviderType, BaseURL: saved.BaseURL, Model: saved.Model, Revision: initial.Revision}, RequestContext{}); ErrorCode(err) != CodeConflict {
		t.Fatalf("stale update err=%v", err)
	}
	enabled, err := service.Update(actor, UpdateAIRecognitionSettingsInput{Enabled: true, ProviderType: saved.ProviderType, BaseURL: saved.BaseURL, Model: saved.Model, Revision: saved.Revision}, RequestContext{})
	if err != nil || !enabled.Enabled {
		t.Fatalf("enabled=%+v err=%v", enabled, err)
	}
	config, active, err := service.RuntimeConfig()
	if err != nil || !active || config.APIKey != secret || config.Model != "fixture-model" {
		t.Fatalf("runtime=%+v active=%v err=%v", config, active, err)
	}
}

func TestAIRecognitionRuntimeGatePreventsProviderCreationWhenDisabled(t *testing.T) {
	queue, _, _ := queueFixture(t)
	service := NewAIRecognitionSettingsService(queue.db, queue.audit, nil)
	created := 0
	service.providerFactory = func(aiprovider.Config) (aiprovider.Provider, error) { created++; return &fakeAIProvider{}, nil }
	payload := aiprovider.CandidateArbitrationPayload{Release: aiprovider.ArbitrationRelease{Title: "Fixture"}, Candidates: []aiprovider.ArbitrationCandidate{{CandidateRef: "c1", Title: "Fixture", MediaType: "movie"}}}
	if _, err := service.GenerateCandidateArbitration(context.Background(), payload); err != aiprovider.ErrDisabled {
		t.Fatalf("disabled generation err=%v", err)
	}
	if created != 0 {
		t.Fatalf("disabled settings created %d providers", created)
	}
}

func TestAIRecognitionExplicitProbeWorksWhileRuntimeDisabled(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "probe.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewAIRecognitionSettingsService(queue.db, queue.audit, store)
	provider := &fakeAIProvider{}
	var got aiprovider.Config
	service.providerFactory = func(config aiprovider.Config) (aiprovider.Provider, error) { got = config; return provider, nil }
	input := AIProviderProbeInput{ProviderType: aiprovider.ProviderGoogleAIStudio, APIKey: "google-key", Model: "gemini-flash", Revision: 1}
	if err := service.TestConnection(context.Background(), actor, input, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	modelList, err := service.ListModels(context.Background(), actor, input, RequestContext{})
	if err != nil || len(modelList) != 1 || provider.tests != 1 || provider.lists != 1 {
		t.Fatalf("models=%+v provider=%+v err=%v", modelList, provider, err)
	}
	if got.ProviderType != aiprovider.ProviderGoogleAIStudio || got.BaseURL != "" || got.APIKey != "google-key" {
		t.Fatalf("probe config=%+v", got)
	}
	var record models.AIRecognitionSettings
	if err := queue.db.First(&record, 1).Error; err != nil {
		t.Fatal(err)
	}
	if record.Enabled || record.APIKeyCiphertext != "" || record.Revision != 1 {
		t.Fatalf("probe persisted settings: %+v", record)
	}
}

func TestAIRecognitionRejectsProviderSwitchWithoutReplacementKey(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "switch.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewAIRecognitionSettingsService(queue.db, queue.audit, store)
	saved, err := service.Update(actor, UpdateAIRecognitionSettingsInput{ProviderType: aiprovider.ProviderOpenAICompatible, BaseURL: "https://api.example.com", APIKey: "openai-key", Model: "gpt", Revision: 1}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(actor, UpdateAIRecognitionSettingsInput{ProviderType: aiprovider.ProviderGoogleAIStudio, Model: "gemini", Revision: saved.Revision}, RequestContext{}); ErrorCode(err) != CodeAIConfigurationInvalid {
		t.Fatalf("provider switch err=%v", err)
	}
}

func TestAIRecognitionAPIKeyUsesDedicatedCredentialRevealAllowlist(t *testing.T) {
	queue, actor, _ := queueFixture(t)
	actor.Permissions[authz.PermissionSettingsUpdate] = struct{}{}
	actor.Permissions[authz.PermissionConnectionsSecretsExport] = struct{}{}
	store, err := credential.Open(filepath.Join(t.TempDir(), "reveal-ai.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	settings := NewAIRecognitionSettingsService(queue.db, queue.audit, store)
	if _, err := settings.Update(actor, UpdateAIRecognitionSettingsInput{ProviderType: aiprovider.ProviderOpenAICompatible, BaseURL: "https://api.example.com", APIKey: "reveal-ai-secret", Model: "gpt", Revision: 1}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	reveal := NewCredentialRevealService(queue.db, queue.audit, store)
	result, err := reveal.Reveal(actor, CredentialRevealInput{ResourceType: CredentialResourceAIRecognition, ResourceID: "1", Field: "api_key"}, RequestContext{})
	if err != nil || result.Value != "reveal-ai-secret" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := reveal.Reveal(actor, CredentialRevealInput{ResourceType: CredentialResourceAIRecognition, ResourceID: "1", Field: "base_url"}, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("unexpected reveal field err=%v", err)
	}
}
