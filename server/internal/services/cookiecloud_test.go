package services

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	sitepkg "github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

func TestCookieCloudLocalReceiveAndSyncUpdatesMatchingSite(t *testing.T) {
	sites, _, actor, store, _, _ := siteFixture(t)
	created, err := sites.Create(context.Background(), actor, validSiteInput("PTTime", "https://one.example.test"), RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewCookieCloudService(sites.db, sites.audit, store, sites, zerolog.Nop())
	settings, err := service.Update(context.Background(), actor, CookieCloudSettingsInput{
		Mode: "local", UUID: "fixture-user", Password: "fixture-password",
		AuthHeader: "fixture-shared-auth", Revision: 1,
	}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !settings.CredentialConfigured || settings.LocalUploadPath != "/cookiecloud" {
		t.Fatalf("settings=%+v", settings)
	}
	payload := cryptoJSCookieCloudFixture(t, "fixture-user", "fixture-password", map[string]any{
		"cookie_data": map[string]any{"pttime": []map[string]string{
			{"domain": ".example.test", "name": "uid", "value": "2"},
			{"domain": ".example.test", "name": "token", "value": "synchronized"},
		}},
	})
	if err := service.Receive("fixture-user", payload, "legacy", "wrong-auth"); ErrorCode(err) != CodeCookieCloudAuthentication {
		t.Fatalf("wrong auth err=%v", err)
	}
	if err := service.Receive("fixture-user", payload, "legacy", "fixture-shared-auth"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Sync(context.Background(), actor, RequestContext{})
	if err != nil || result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var record models.Site
	if err := sites.db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	credential, err := sites.decryptCredential(record)
	if err != nil {
		t.Fatal(err)
	}
	if credential.Cookie != "token=synchronized; uid=2" {
		t.Fatalf("cookie=%q", credential.Cookie)
	}
	encoded, _ := json.Marshal(settings)
	for _, forbidden := range []string{"fixture-user", "fixture-password", "fixture-shared-auth", "synchronized"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("summary leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestCookieCloudSyncDiscoversAndCreatesSupportedSite(t *testing.T) {
	sites, _, actor, store, _, _ := siteFixture(t)
	service := NewCookieCloudService(sites.db, sites.audit, store, sites, zerolog.Nop())
	if _, err := service.Update(context.Background(), actor, CookieCloudSettingsInput{
		Mode: "local", UUID: "fixture-user", Password: "fixture-password",
		AuthHeader: "fixture-shared-auth", Revision: 1,
	}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	payload := cryptoJSCookieCloudFixture(t, "fixture-user", "fixture-password", map[string]any{
		"cookie_data": map[string]any{"pttime": []map[string]string{
			{"domain": ".pttime.org", "name": "uid", "value": "2"},
			{"domain": ".pttime.org", "name": "token", "value": "discovered-secret"},
		}},
	})
	if err := service.Receive("fixture-user", payload, "legacy", "fixture-shared-auth"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Sync(context.Background(), actor, RequestContext{})
	if err != nil || result.Created != 1 || result.Updated != 0 || result.Failed != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var record models.Site
	if err := sites.db.Where("kind = ?", "pttime").First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.BaseURL != "https://www.pttime.org" || record.Name != "PTTime" {
		t.Fatalf("record=%+v", record)
	}
	credential, err := sites.decryptCredential(record)
	if err != nil || credential.Cookie != "token=discovered-secret; uid=2" {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}

func TestCookieCloudSyncDiscoversMultipleCatalogSites(t *testing.T) {
	sites, _, actor, store, _, _ := siteFixture(t)
	for _, kind := range []string{"sewerpt", "panda"} {
		sites.adapters[kind] = &stubSiteAdapter{kind: kind, testErr: map[string]error{}, searchErr: map[string]error{}}
	}
	service := NewCookieCloudService(sites.db, sites.audit, store, sites, zerolog.Nop())
	if _, err := service.Update(context.Background(), actor, CookieCloudSettingsInput{
		Mode: "local", UUID: "fixture-user", Password: "fixture-password",
		AuthHeader: "fixture-shared-auth", Revision: 1,
	}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	payload := cryptoJSCookieCloudFixture(t, "fixture-user", "fixture-password", map[string]any{
		"cookie_data": map[string]any{
			"pttime":  []map[string]string{{"domain": ".pttime.org", "name": "uid", "value": "1"}, {"domain": ".pttime.org", "name": "token", "value": "one"}},
			"sewerpt": []map[string]string{{"domain": ".sewerpt.com", "name": "uid", "value": "2"}, {"domain": ".sewerpt.com", "name": "token", "value": "two"}},
			"panda":   []map[string]string{{"domain": ".pandapt.net", "name": "uid", "value": "3"}, {"domain": ".pandapt.net", "name": "token", "value": "three"}},
		},
	})
	if err := service.Receive("fixture-user", payload, "legacy", "fixture-shared-auth"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Sync(context.Background(), actor, RequestContext{})
	if err != nil || result.Created != 3 || result.Updated != 0 || result.Failed != 0 || result.SkippedUnsupportedDomains != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var records []models.Site
	if err := sites.db.Order("kind").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].Kind != "panda" || records[0].Name != "熊猫高清 · PandaPT" || records[1].Kind != "pttime" || records[1].Name != "PTTime" || records[2].Kind != "sewerpt" || records[2].Name != "下水道 · SewerPT" {
		t.Fatalf("records=%+v", records)
	}
}

func TestCookieCloudSyncIgnoresPublicBTConnections(t *testing.T) {
	sites, _, actor, store, _, _ := siteFixture(t)
	sites.adapters["nyaa"] = &stubSiteAdapter{kind: "nyaa", testErr: map[string]error{}, searchErr: map[string]error{}}
	created, err := sites.Create(context.Background(), actor, SiteInput{Name: "Nyaa", Kind: "nyaa", BaseURL: "https://nyaa.si", Enabled: true, Priority: 100, TimeoutSeconds: 12, RateLimitPerMinute: 12}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewCookieCloudService(sites.db, sites.audit, store, sites, zerolog.Nop())
	if _, err := service.Update(context.Background(), actor, CookieCloudSettingsInput{Mode: "local", UUID: "fixture-user", Password: "fixture-password", AuthHeader: "fixture-shared-auth", Revision: 1}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	payload := cryptoJSCookieCloudFixture(t, "fixture-user", "fixture-password", map[string]any{"cookie_data": map[string]any{"nyaa": []map[string]string{{"domain": ".nyaa.si", "name": "session", "value": "must-not-be-imported"}}}})
	if err := service.Receive("fixture-user", payload, "legacy", "fixture-shared-auth"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Sync(context.Background(), actor, RequestContext{})
	if err != nil || result.Updated != 0 || result.Failed != 0 || result.SkippedUnsupportedDomains != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var record models.Site
	if err := sites.db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.Revision != created.Revision {
		t.Fatalf("public BT connection was unexpectedly updated: %+v", record)
	}
	plain, err := sites.decryptCredential(record)
	if err != nil || plain.Cookie != "" || plain.APIKey != "" {
		t.Fatalf("public BT credential was polluted: %+v err=%v", plain, err)
	}
}

func TestCookieCloudSyncReportsUnsupportedDomainsAndMissingLoginCookiesWithoutNames(t *testing.T) {
	sites, _, actor, store, _, _ := siteFixture(t)
	service := NewCookieCloudService(sites.db, sites.audit, store, sites, zerolog.Nop())
	if _, err := service.Update(context.Background(), actor, CookieCloudSettingsInput{
		Mode: "local", UUID: "fixture-user", Password: "fixture-password",
		AuthHeader: "fixture-shared-auth", Revision: 1,
	}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	payload := cryptoJSCookieCloudFixture(t, "fixture-user", "fixture-password", map[string]any{
		"cookie_data": map[string]any{
			"pttime": []map[string]string{{"domain": ".pttime.org", "name": "cf_clearance", "value": "not-a-login"}},
			"other": []map[string]string{
				{"domain": ".unsupported-one.example", "name": "session", "value": "secret-one"},
				{"domain": ".unsupported-two.example", "name": "session", "value": "secret-two"},
			},
		},
	})
	if err := service.Receive("fixture-user", payload, "legacy", "fixture-shared-auth"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Sync(context.Background(), actor, RequestContext{})
	if err != nil || result.Created != 0 || result.Updated != 0 || result.Failed != 0 || result.SkippedUnsupportedDomains != 2 || result.SkippedMissingLoginCookies != 1 || result.Skipped != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"unsupported-one", "unsupported-two", "pttime.org", "not-a-login", "secret-one", "secret-two"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("sync summary leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestCookieCloudDecryptRejectsWrongPasswordAndFiltersUnsafeEntries(t *testing.T) {
	payload := cryptoJSCookieCloudFixture(t, "fixture-user", "correct-password", map[string]any{
		"cookie_data": map[string]any{"group": []map[string]string{
			{"domain": ".pt.example.test", "name": "uid", "value": "7"},
			{"domain": ".example.test", "name": "parent", "value": "included"},
			{"domain": ".example.test", "name": "uid", "value": "parent-loses"},
			{"domain": ".pt.example.test", "name": "cf_clearance", "value": "clear"},
			{"domain": ".only-cf.example", "name": "cf_clearance", "value": "ignored"},
			{"domain": ".pt.example", "name": "CookieAutoDeleteCleaningDiscarded", "value": "ignored"},
		}},
	})
	if _, err := decryptCookieCloud(payload, "fixture-user", "wrong-password"); err == nil {
		t.Fatal("wrong password unexpectedly decrypted")
	}
	entries, err := decryptCookieCloud(payload, "fixture-user", "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	cookies := cookiesByDomain(entries)
	if got := cookieForHost(cookies, "tracker.pt.example.test"); got != "cf_clearance=clear; parent=included; uid=7" {
		t.Fatalf("matching cookie=%q", got)
	}
	if got := cookieForHost(cookies, "only-cf.example"); got != "" {
		t.Fatalf("cf-only domain should be ignored: %q", got)
	}
	if got := cookieForHost(cookies, "notpt.example"); got != "" {
		t.Fatalf("unrelated domain matched: %q", got)
	}
}

func TestCookieCloudSyncReportsSafeDiscoveryFailure(t *testing.T) {
	sites, adapter, actor, store, _, _ := siteFixture(t)
	adapter.testErr["https://www.pttime.org"] = sitepkg.ErrAuthentication
	service := NewCookieCloudService(sites.db, sites.audit, store, sites, zerolog.Nop())
	if _, err := service.Update(context.Background(), actor, CookieCloudSettingsInput{
		Mode: "local", UUID: "fixture-user", Password: "fixture-password",
		AuthHeader: "fixture-shared-auth", Revision: 1,
	}, RequestContext{}); err != nil {
		t.Fatal(err)
	}
	payload := cryptoJSCookieCloudFixture(t, "fixture-user", "fixture-password", map[string]any{
		"cookie_data": map[string]any{"pttime": []map[string]string{
			{"domain": ".pttime.org", "name": "uid", "value": "2"},
			{"domain": ".pttime.org", "name": "token", "value": "server-only-secret"},
		}},
	})
	if err := service.Receive("fixture-user", payload, "legacy", "fixture-shared-auth"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Sync(context.Background(), actor, RequestContext{})
	if err != nil || result.Status != "partial" || result.Failed != 1 || len(result.Issues) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	issue := result.Issues[0]
	if issue.Action != "create" || issue.SiteID != 0 || issue.Kind != "pttime" || issue.ErrorCode != CodeSiteAuthentication {
		t.Fatalf("issue=%+v", issue)
	}
	settings, err := service.Get(actor)
	if err != nil || settings.LastSyncStatus != "partial" || settings.LastSyncErrorCode != CodeSiteAuthentication {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	var audit models.AuditLog
	if err := sites.db.Where("action = ?", "cookiecloud.sync").Order("id DESC").First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if audit.Outcome != "partial" || !strings.Contains(audit.Metadata, CodeSiteAuthentication) || strings.Contains(audit.Metadata, "server-only-secret") {
		t.Fatalf("audit=%+v", audit)
	}
}

func TestCookieCloudDecryptsCurrentFixedIVFormat(t *testing.T) {
	plain := map[string]any{"cookie_data": map[string]any{"group": []map[string]string{{"domain": ".pt.example", "name": "uid", "value": "8"}}}}
	payload := cryptoJSCookieCloudFixedFixture(t, "fixture-user", "fixture-password", plain)
	entries, err := decryptCookieCloudPayload(cookieCloudEncryptedPayload{Encrypted: payload, CryptoType: "aes-128-cbc-fixed"}, "fixture-user", "fixture-password")
	if err != nil || cookieForHost(cookiesByDomain(entries), "pt.example") != "uid=8" {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	if _, err := decryptCookieCloudPayload(cookieCloudEncryptedPayload{Encrypted: payload, CryptoType: "future-format"}, "fixture-user", "fixture-password"); ErrorCode(err) != CodeCookieCloudResponseInvalid {
		t.Fatalf("unsupported crypto err=%v", err)
	}
}

func cryptoJSCookieCloudFixture(t *testing.T, uuid, password string, value any) string {
	t.Helper()
	plain, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum([]byte(uuid + "-" + password))
	passphrase := []byte(hex.EncodeToString(digest[:])[:16])
	salt := []byte("12345678")
	keyIV := evpBytesToKey(passphrase, salt, 48)
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	for index := 0; index < padding; index++ {
		plain = append(plain, byte(padding))
	}
	block, err := aes.NewCipher(keyIV[:32])
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, keyIV[32:48]).CryptBlocks(ciphertext, plain)
	return base64.StdEncoding.EncodeToString(append(append([]byte("Salted__"), salt...), ciphertext...))
}

func cryptoJSCookieCloudFixedFixture(t *testing.T, uuid, password string, value any) string {
	t.Helper()
	plain, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	for index := 0; index < padding; index++ {
		plain = append(plain, byte(padding))
	}
	digest := md5.Sum([]byte(uuid + "-" + password))
	key := []byte(hex.EncodeToString(digest[:])[:16])
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(ciphertext, plain)
	return base64.StdEncoding.EncodeToString(ciphertext)
}
