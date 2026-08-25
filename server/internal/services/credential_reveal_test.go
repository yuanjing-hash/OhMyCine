package services

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/hostapi"
)

func TestCredentialRevealAllowlistAuditAndCustomMetadataOnly(t *testing.T) {
	// Keep this fixture on the real migrated SQLite schema so model/tag changes
	// cannot silently make a secret serializable through the normal API path.
	db, err := database.Open(filepath.Join(t.TempDir(), "reveal.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	store, err := credential.Open(filepath.Join(t.TempDir(), "credential.key"), "")
	if err != nil {
		t.Fatal(err)
	}
	user := models.User{Username: "reveal-admin", UsernameNormalized: "reveal-admin", DisplayName: "Reveal Admin", PasswordHash: "unused", Status: models.UserStatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	audit := NewAuditService(db)
	service := NewCredentialRevealService(db, audit, store)
	actor := Actor{User: user, Permissions: map[string]struct{}{authz.PermissionConnectionsSecretsExport: {}}}
	now := time.Now().UTC()

	connectionSecret := "UID=100_A1; CID=cid; SEID=secret"
	connectionCipher, _ := store.Encrypt(connectionPurpose(7, models.ConnectionProviderPan115), connectionSecret)
	recycleSecret := "recycle-code"
	recycleCipher, _ := store.Encrypt(connectionRecyclePurpose(7), recycleSecret)
	connection := models.Connection{ID: 7, Name: "115", NameNormalized: "reveal-115", Provider: models.ConnectionProviderPan115, CredentialCiphertext: connectionCipher, RecycleCredentialCiphertext: recycleCipher, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	downloaderSecret := "qbit-password"
	downloaderUsername := "qbit-admin"
	downloaderUsernameCipher, _ := store.Encrypt(downloaderPurpose("downloader-1", "username"), downloaderUsername)
	downloaderCipher, _ := store.Encrypt(downloaderPurpose("downloader-1", "password"), downloaderSecret)
	downloader := models.Downloader{ID: "downloader-1", Name: "qBit", NameNormalized: "reveal-qbit", Type: models.DownloaderTypeQBittorrent, UsernameCiphertext: downloaderUsernameCipher, PasswordCiphertext: downloaderCipher, Enabled: true, CapabilitiesJSON: `{}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&downloader).Error; err != nil {
		t.Fatal(err)
	}
	metadataSecret := "custom-tmdb-key"
	metadataCipher, _ := store.Encrypt(tmdbTokenPurpose, metadataSecret)
	if err := db.Model(&models.MetadataSettings{}).Where("id = ?", 1).Update("tmdb_token_ciphertext", metadataCipher).Error; err != nil {
		t.Fatal(err)
	}
	siteSecrets := siteCredentialEnvelope{Cookie: "site-cookie", Passkey: "site-passkey", APIKey: "site-api-key"}
	siteJSON, _ := json.Marshal(siteSecrets)
	siteCipher, _ := store.Encrypt(siteCredentialPurpose(9, "pttime"), string(siteJSON))
	site := models.Site{ID: 9, Name: "PT", NameNormalized: "reveal-pt", Kind: "pttime", BaseURL: "https://pt.example.test", CredentialCiphertext: siteCipher, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	cookieCloudSecrets := cookieCloudCredential{UUID: "cookiecloud-uuid", Password: "cookiecloud-password", AuthHeader: "cookiecloud-auth-header"}
	cookieCloudJSON, _ := json.Marshal(cookieCloudSecrets)
	cookieCloudCipher, _ := store.Encrypt(cookieCloudCredentialPurpose, string(cookieCloudJSON))
	if err := db.Model(&models.CookieCloudSettings{}).Where("id = ?", 1).Update("credential_ciphertext", cookieCloudCipher).Error; err != nil {
		t.Fatal(err)
	}
	pluginCookie := "secret-plugin-cookie-value"
	pluginBearer := "secret-plugin-bearer-value"
	for index, pluginID := range []string{"plugin-reveal-cookie", "plugin-reveal-bearer"} {
		pkg := models.PluginPackage{PluginID: pluginID, Version: "1.0.0", RepositoryOwner: "test", RepositoryRepo: "plugins", RegistryCommit: strings.Repeat("a", 40), RegistryEntryJSON: `{}`, ManifestURL: "https://example.test/manifest.json", PackageURL: "https://example.test/plugin.zip", PackageSHA256: strings.Repeat("c", 63) + strconv.Itoa(index+1), ExtractedTreeSHA256: strings.Repeat("b", 63) + strconv.Itoa(index+1), ManifestJSON: `{}`, PackagePath: "fixture", VerifiedAt: now, CreatedAt: now}
		if err := db.Create(&pkg).Error; err != nil {
			t.Fatal(err)
		}
		installation := models.PluginInstallation{PluginID: pluginID, ActivePackageID: pkg.ID, Status: models.PluginInstallationEnabled, Revision: 1, InstalledAt: now, UpdatedAt: now, EnabledAt: &now}
		if err := db.Create(&installation).Error; err != nil {
			t.Fatal(err)
		}
	}
	for index, pluginID := range []string{"bilibili", "example"} {
		pkg := models.PluginPackage{PluginID: pluginID, Version: "1.0.0", RepositoryOwner: "test", RepositoryRepo: "plugins", RegistryCommit: strings.Repeat("a", 40), RegistryEntryJSON: `{}`, ManifestURL: "https://example.test/manifest.json", PackageURL: "https://example.test/plugin.zip", PackageSHA256: strings.Repeat(strconv.Itoa(index+1), 64), ExtractedTreeSHA256: strings.Repeat("b", 64), ManifestJSON: `{}`, PackagePath: "fixture", VerifiedAt: now, CreatedAt: now}
		if err := db.Create(&pkg).Error; err != nil {
			t.Fatal(err)
		}
		installation := models.PluginInstallation{PluginID: pluginID, ActivePackageID: pkg.ID, Status: models.PluginInstallationEnabled, Revision: 1, InstalledAt: now, UpdatedAt: now, EnabledAt: &now}
		if err := db.Create(&installation).Error; err != nil {
			t.Fatal(err)
		}
	}
	pluginCookieCipher, _ := store.Encrypt(hostapi.CredentialPurpose("plugin-reveal-cookie", "plugin-cookie", "site.session"), pluginCookie)
	pluginBearerCipher, _ := store.Encrypt(hostapi.CredentialPurpose("plugin-reveal-bearer", "plugin-bearer", "api.token"), pluginBearer)
	for _, record := range []models.PluginConnection{
		{ID: "plugin-cookie", PluginID: "plugin-reveal-cookie", Name: "Cookie", ConfigJSON: `{}`, CredentialScope: "site.session", CredentialMode: models.PluginCredentialModeCookie, CredentialCiphertext: pluginCookieCipher, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "plugin-bearer", PluginID: "plugin-reveal-bearer", Name: "Bearer", ConfigJSON: `{}`, CredentialScope: "api.token", CredentialMode: models.PluginCredentialModeBearer, CredentialCiphertext: pluginBearerCipher, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}

	for _, test := range []struct {
		name  string
		input CredentialRevealInput
		want  string
	}{
		{name: "connection", input: CredentialRevealInput{ResourceType: CredentialResourceConnection, ResourceID: "7", Field: "credential"}, want: connectionSecret},
		{name: "connection recycle password", input: CredentialRevealInput{ResourceType: CredentialResourceConnection, ResourceID: "7", Field: "recycle_password"}, want: recycleSecret},
		{name: "downloader username", input: CredentialRevealInput{ResourceType: CredentialResourceDownloader, ResourceID: downloader.ID, Field: "username"}, want: downloaderUsername},
		{name: "downloader password", input: CredentialRevealInput{ResourceType: CredentialResourceDownloader, ResourceID: downloader.ID, Field: "password"}, want: downloaderSecret},
		{name: "site cookie", input: CredentialRevealInput{ResourceType: CredentialResourceSite, ResourceID: "9", Field: "cookie"}, want: siteSecrets.Cookie},
		{name: "site passkey", input: CredentialRevealInput{ResourceType: CredentialResourceSite, ResourceID: "9", Field: "passkey"}, want: siteSecrets.Passkey},
		{name: "site api key", input: CredentialRevealInput{ResourceType: CredentialResourceSite, ResourceID: "9", Field: "api_key"}, want: siteSecrets.APIKey},
		{name: "cookiecloud uuid", input: CredentialRevealInput{ResourceType: CredentialResourceCookieCloud, ResourceID: "1", Field: "uuid"}, want: cookieCloudSecrets.UUID},
		{name: "cookiecloud password", input: CredentialRevealInput{ResourceType: CredentialResourceCookieCloud, ResourceID: "1", Field: "password"}, want: cookieCloudSecrets.Password},
		{name: "cookiecloud auth header", input: CredentialRevealInput{ResourceType: CredentialResourceCookieCloud, ResourceID: "1", Field: "auth_header"}, want: cookieCloudSecrets.AuthHeader},
		{name: "custom metadata", input: CredentialRevealInput{ResourceType: CredentialResourceMetadata, ResourceID: "1", Field: "tmdb_credential"}, want: metadataSecret},
		{name: "plugin cookie", input: CredentialRevealInput{ResourceType: CredentialResourcePluginConnection, ResourceID: "plugin-cookie", Field: "credential"}, want: pluginCookie},
		{name: "plugin bearer", input: CredentialRevealInput{ResourceType: CredentialResourcePluginConnection, ResourceID: "plugin-bearer", Field: "credential"}, want: pluginBearer},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.Reveal(actor, test.input, RequestContext{RequestID: "reveal-request"})
			if err != nil || result.Value != test.want {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}

	if _, err := service.Reveal(actor, CredentialRevealInput{ResourceType: "user", ResourceID: "1", Field: "password"}, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("login password reveal error=%v", err)
	}
	if _, err := service.Reveal(actor, CredentialRevealInput{ResourceType: CredentialResourceConnection, ResourceID: "7", Field: "unknown"}, RequestContext{}); ErrorCode(err) != CodeInvalidRequest {
		t.Fatalf("unknown field reveal error=%v", err)
	}
	if _, err := service.Reveal(Actor{User: user, Permissions: map[string]struct{}{}}, CredentialRevealInput{ResourceType: CredentialResourceConnection, ResourceID: "7", Field: "credential"}, RequestContext{}); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("permission error=%v", err)
	}

	var records []models.AuditLog
	if err := db.Where("action = ?", "credential.reveal").Find(&records).Error; err != nil || len(records) < 4 {
		t.Fatalf("audits=%d err=%v", len(records), err)
	}
	encoded, _ := json.Marshal(records)
	for _, secret := range []string{connectionSecret, recycleSecret, downloaderUsername, downloaderSecret, siteSecrets.Cookie, siteSecrets.Passkey, siteSecrets.APIKey, cookieCloudSecrets.UUID, cookieCloudSecrets.Password, cookieCloudSecrets.AuthHeader, metadataSecret, pluginCookie, pluginBearer} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("audit leaked secret %q", secret)
		}
	}
}

func TestCredentialRevealRejectsBuiltinOrDeploymentMetadata(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "builtin.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	store, _ := credential.Open(filepath.Join(t.TempDir(), "credential.key"), "")
	user := models.User{Username: "builtin-admin", UsernameNormalized: "builtin-admin", DisplayName: "Builtin Admin", PasswordHash: "unused", Status: models.UserStatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	service := NewCredentialRevealService(db, NewAuditService(db), store)
	actor := Actor{User: user, Permissions: map[string]struct{}{authz.PermissionConnectionsSecretsExport: {}}}
	_, err = service.Reveal(actor, CredentialRevealInput{ResourceType: CredentialResourceMetadata, ResourceID: "1", Field: "tmdb_credential"}, RequestContext{})
	if ErrorCode(err) != CodeNotFound {
		t.Fatalf("empty custom credential error=%v", err)
	}
}
