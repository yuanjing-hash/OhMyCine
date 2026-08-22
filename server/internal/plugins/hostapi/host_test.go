package hostapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"gorm.io/gorm"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHostHTTPBindsPluginIdentityPermissionAndEncryptedCredential(t *testing.T) {
	fixture := newHostFixture(t, []contract.Permission{
		{Kind: contract.PermissionNetworkHTTP, Domains: []string{"api.example.test"}},
		{Kind: contract.PermissionCredentialUse, Scopes: []string{"site.session"}},
	})
	var receivedCookie string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		receivedCookie = request.Header.Get("Cookie")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"SESSDATA=leak"}}, Body: io.NopCloser(strings.NewReader(`{"code":0}`)), Request: request}, nil
	})}
	host := New(fixture.db, fixture.credentials, zerolog.Nop(), WithHTTPClient(client), WithResolver(publicResolver))
	payload, _ := json.Marshal(httpRequest{ConnectionID: fixture.connection.ID, Method: http.MethodGet, URL: "https://api.example.test/navigation", Credential: "site.session"})
	response, err := host.Call(context.Background(), fixture.pluginID, OperationHTTP, payload)
	if err != nil {
		t.Fatal(err)
	}
	if receivedCookie != "SESSDATA=encrypted-test" {
		t.Fatalf("controlled request did not receive decrypted credential: %q", receivedCookie)
	}
	if bytes.Contains(response, []byte("SESSDATA")) || bytes.Contains(response, []byte("Set-Cookie")) {
		t.Fatalf("host response leaked credential headers: %s", response)
	}
	var envelope struct {
		Data httpResponse `json:"data"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Data.Status != http.StatusOK {
		t.Fatalf("response=%s err=%v", response, err)
	}

	deniedPayload, _ := json.Marshal(httpRequest{ConnectionID: fixture.connection.ID, Method: http.MethodGet, URL: "https://example.com/private", Credential: "bilibili.session"})
	if _, err := host.Call(context.Background(), fixture.pluginID, OperationHTTP, deniedPayload); err == nil || !err.(*Error).PermissionDenied() {
		t.Fatalf("ungranted domain was not denied: %v", err)
	}
}

func TestHostHTTPCookieCredentialBindingsStayInsideRequest(t *testing.T) {
	fixture := newHostFixture(t, []contract.Permission{
		{Kind: contract.PermissionNetworkHTTP, Domains: []string{"api.example.test"}},
		{Kind: contract.PermissionCredentialUse, Scopes: []string{"site.session"}},
	})
	ciphertext, err := fixture.credentials.Encrypt(CredentialPurpose(fixture.pluginID, fixture.connection.ID, fixture.connection.CredentialScope), "SESSDATA=session-secret; bili_jct=csrf-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.PluginConnection{}).Where("id = ?", fixture.connection.ID).Update("credential_ciphertext", ciphertext).Error; err != nil {
		t.Fatal(err)
	}
	var receivedCookie string
	var receivedBody string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		receivedCookie = request.Header.Get("Cookie")
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			return nil, readErr
		}
		receivedBody = string(body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":0}`)), Request: request}, nil
	})}
	host := New(fixture.db, fixture.credentials, zerolog.Nop(), WithHTTPClient(client), WithResolver(publicResolver))
	payload, _ := json.Marshal(httpRequest{
		ConnectionID: fixture.connection.ID,
		Method:       http.MethodPost,
		URL:          "https://api.example.test/progress",
		Headers:      map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Credential:   "site.session",
		BodyBase64:   base64.StdEncoding.EncodeToString([]byte("aid=7")),
		CredentialBindings: []credentialBinding{
			{Target: "form", Name: "csrf", Source: "cookie", Key: "bili_jct"},
			{Target: "form", Name: "csrf_token", Source: "cookie", Key: "bili_jct"},
		},
	})
	response, err := host.Call(context.Background(), fixture.pluginID, OperationHTTP, payload)
	if err != nil {
		t.Fatal(err)
	}
	if receivedCookie != "SESSDATA=session-secret; bili_jct=csrf-secret" {
		t.Fatalf("credential cookie mismatch: %q", receivedCookie)
	}
	values, err := url.ParseQuery(receivedBody)
	if err != nil || values.Get("aid") != "7" || values.Get("csrf") != "csrf-secret" || values.Get("csrf_token") != "csrf-secret" {
		t.Fatalf("bound body=%q err=%v", receivedBody, err)
	}
	if bytes.Contains(response, []byte("csrf-secret")) || bytes.Contains(response, []byte("session-secret")) {
		t.Fatalf("host response leaked bound credential: %s", response)
	}
	invalidPayload, _ := json.Marshal(httpRequest{
		ConnectionID: fixture.connection.ID,
		Method:       http.MethodPost,
		URL:          "https://api.example.test/progress",
		Headers:      map[string]string{"Content-Type": "application/x-www-form-urlencoded-evil"},
		Credential:   "site.session",
		BodyBase64:   base64.StdEncoding.EncodeToString([]byte("aid=7")),
		CredentialBindings: []credentialBinding{
			{Target: "form", Name: "csrf", Source: "cookie", Key: "bili_jct"},
		},
	})
	if _, err := host.Call(context.Background(), fixture.pluginID, OperationHTTP, invalidPayload); ErrorCode(err) != "plugin_credential_binding_denied" {
		t.Fatalf("ambiguous media type error=%v code=%s", err, ErrorCode(err))
	}
}

func TestHostHTTPCrossOriginRedirectStripsCredentials(t *testing.T) {
	fixture := newHostFixture(t, []contract.Permission{
		{Kind: contract.PermissionNetworkHTTP, Domains: []string{"api.example.test", "cdn.example.test"}},
		{Kind: contract.PermissionCredentialUse, Scopes: []string{"site.session"}},
	})
	requestCount := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		if requestCount == 1 {
			if request.Header.Get("Cookie") == "" {
				t.Fatal("initial request did not receive its credential")
			}
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://cdn.example.test/final"}}, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}
		if request.URL.Hostname() != "cdn.example.test" {
			t.Fatalf("unexpected redirect target: %s", request.URL.Hostname())
		}
		if request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" {
			t.Fatalf("credential crossed redirect origin: %#v", request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: request}, nil
	})}
	host := New(fixture.db, fixture.credentials, zerolog.Nop(), WithHTTPClient(client), WithResolver(publicResolver))
	payload, _ := json.Marshal(httpRequest{ConnectionID: fixture.connection.ID, Method: http.MethodGet, URL: "https://api.example.test/start", Credential: "site.session"})
	if _, err := host.Call(context.Background(), fixture.pluginID, OperationHTTP, payload); err != nil {
		t.Fatal(err)
	}
	if requestCount != 2 {
		t.Fatalf("redirect request count=%d", requestCount)
	}
}

func TestHostHTTPRevalidatesDNSAtDialTime(t *testing.T) {
	fixture := newHostFixture(t, []contract.Permission{{Kind: contract.PermissionNetworkHTTP, Domains: []string{"api.example.test"}}})
	resolveCount := 0
	resolver := func(context.Context, string) ([]net.IPAddr, error) {
		resolveCount++
		if resolveCount == 1 {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	host := New(fixture.db, fixture.credentials, zerolog.Nop(), WithResolver(resolver))
	payload, _ := json.Marshal(httpRequest{Method: http.MethodGet, URL: "https://api.example.test/data"})
	_, err := host.Call(context.Background(), fixture.pluginID, OperationHTTP, payload)
	if ErrorCode(err) != "plugin_http_private_address_denied" {
		t.Fatalf("error=%v code=%s", err, ErrorCode(err))
	}
	if resolveCount < 2 {
		t.Fatalf("DNS was not revalidated at dial time: %d", resolveCount)
	}
}

func TestHostPrivateStorageIsEncryptedQuotaBoundAndConnectionScoped(t *testing.T) {
	quota := int64(8)
	fixture := newHostFixture(t, []contract.Permission{{Kind: contract.PermissionPrivateStorage, MaxBytes: &quota}})
	host := New(fixture.db, fixture.credentials, zerolog.Nop())
	value := base64.StdEncoding.EncodeToString([]byte("private"))
	setPayload, _ := json.Marshal(storageRequest{ConnectionID: fixture.connection.ID, Key: "cursor/recommended", ValueBase64: value})
	if _, err := host.Call(context.Background(), fixture.pluginID, OperationStorageSet, setPayload); err != nil {
		t.Fatal(err)
	}
	var stored models.PluginPrivateKV
	if err := fixture.db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.ValueCiphertext, "private") {
		t.Fatal("plugin private state was persisted in plaintext")
	}
	getPayload, _ := json.Marshal(storageRequest{ConnectionID: fixture.connection.ID, Key: "cursor/recommended"})
	response, err := host.Call(context.Background(), fixture.pluginID, OperationStorageGet, getPayload)
	if err != nil || !bytes.Contains(response, []byte(value)) {
		t.Fatalf("get response=%s err=%v", response, err)
	}
	tooLarge, _ := json.Marshal(storageRequest{ConnectionID: fixture.connection.ID, Key: "overflow", ValueBase64: base64.StdEncoding.EncodeToString([]byte("too-large"))})
	if _, err := host.Call(context.Background(), fixture.pluginID, OperationStorageSet, tooLarge); err == nil || !err.(*Error).PermissionDenied() {
		t.Fatalf("quota overflow was not denied: %v", err)
	}
	second := models.PluginConnection{ID: "22222222-2222-4222-8222-222222222222", PluginID: fixture.pluginID, Name: "second", ConfigJSON: `{}`, CredentialMode: models.PluginCredentialModeNone, Enabled: true, Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := fixture.db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	secondPayload, _ := json.Marshal(storageRequest{ConnectionID: second.ID, Key: "cursor/recommended", ValueBase64: value})
	if _, err := host.Call(context.Background(), fixture.pluginID, OperationStorageSet, secondPayload); err != nil {
		t.Fatalf("another connection must receive an independent quota: %v", err)
	}
}

func TestHostAssetsSupportURLAndInlineContentWithExpiryAndCapacity(t *testing.T) {
	fixture := newHostFixture(t, []contract.Permission{{Kind: contract.PermissionNetworkHTTP, Domains: []string{"cdn.example.test"}}})
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	host := New(fixture.db, fixture.credentials, zerolog.Nop(), WithHTTPClient(&http.Client{}), WithResolver(publicResolver))
	host.now = func() time.Time { return now }

	urlPayload, _ := json.Marshal(assetRegisterRequest{URL: "https://cdn.example.test/video.mp4?temporary=opaque", Headers: map[string]string{"Referer": "https://www.example.test/"}, TTLSeconds: 60})
	urlResponse, err := host.Call(context.Background(), fixture.pluginID, OperationAssetRegister, urlPayload)
	if err != nil {
		t.Fatal(err)
	}
	urlRef := assetReference(t, urlResponse)
	urlAsset, err := host.ResolveAsset(urlRef)
	if err != nil || urlAsset.PluginID != fixture.pluginID || urlAsset.URL != "https://cdn.example.test/video.mp4?temporary=opaque" || urlAsset.Headers.Get("Referer") == "" {
		t.Fatalf("url asset=%+v err=%v", urlAsset, err)
	}
	urlAsset.Headers.Set("Referer", "mutated")
	urlAssetAgain, err := host.ResolveAsset(urlRef)
	if err != nil || urlAssetAgain.Headers.Get("Referer") == "mutated" {
		t.Fatal("resolved asset did not return an isolated header copy")
	}

	inlineBody := []byte(`{"comments":[{"id":"dm:1","time":1,"mode":"scroll","color":"#ffffff","text":"hello"}]}`)
	inlinePayload, _ := json.Marshal(assetRegisterRequest{BodyBase64: base64.StdEncoding.EncodeToString(inlineBody), ContentType: "application/json", TTLSeconds: 30})
	inlineResponse, err := host.Call(context.Background(), fixture.pluginID, OperationAssetRegister, inlinePayload)
	if err != nil {
		t.Fatal(err)
	}
	inlineRef := assetReference(t, inlineResponse)
	inlineAsset, err := host.ResolveAsset(inlineRef)
	if err != nil || inlineAsset.ContentType != "application/json" || !bytes.Equal(inlineAsset.Body, inlineBody) || inlineAsset.URL != "" {
		t.Fatalf("inline asset=%+v err=%v", inlineAsset, err)
	}
	inlineAsset.Body[0] = 'x'
	inlineAssetAgain, err := host.ResolveAsset(inlineRef)
	if err != nil || inlineAssetAgain.Body[0] == 'x' {
		t.Fatal("resolved asset did not return an isolated body copy")
	}
	inlineRange, err := host.OpenAsset(context.Background(), inlineRef, http.MethodGet, "bytes=1-4")
	if err != nil {
		t.Fatal(err)
	}
	rangeBody, _ := io.ReadAll(inlineRange.Body)
	_ = inlineRange.Body.Close()
	if inlineRange.StatusCode != http.StatusPartialContent || inlineRange.Header.Get("Content-Range") != fmt.Sprintf("bytes 1-4/%d", len(inlineBody)) || !bytes.Equal(rangeBody, inlineBody[1:5]) {
		t.Fatalf("inline range status=%d headers=%v body=%q", inlineRange.StatusCode, inlineRange.Header, rangeBody)
	}
	inlineHead, err := host.OpenAsset(context.Background(), inlineRef, http.MethodHead, "")
	if err != nil {
		t.Fatal(err)
	}
	headBody, _ := io.ReadAll(inlineHead.Body)
	_ = inlineHead.Body.Close()
	if inlineHead.StatusCode != http.StatusOK || inlineHead.Header.Get("Content-Length") != strconv.Itoa(len(inlineBody)) || len(headBody) != 0 {
		t.Fatalf("inline HEAD status=%d headers=%v body=%q", inlineHead.StatusCode, inlineHead.Header, headBody)
	}
	unsatisfiable, err := host.OpenAsset(context.Background(), inlineRef, http.MethodGet, "bytes=999999-")
	if err != nil {
		t.Fatal(err)
	}
	_ = unsatisfiable.Body.Close()
	if unsatisfiable.StatusCode != http.StatusRequestedRangeNotSatisfiable || unsatisfiable.Header.Get("Content-Range") != fmt.Sprintf("bytes */%d", len(inlineBody)) {
		t.Fatalf("unsatisfiable status=%d headers=%v", unsatisfiable.StatusCode, unsatisfiable.Header)
	}
	if _, err := host.OpenAsset(context.Background(), inlineRef, http.MethodGet, "bytes=0-1,3-4"); ErrorCode(err) != "plugin_asset_range_invalid" {
		t.Fatalf("multi-range error=%v code=%s", err, ErrorCode(err))
	}

	now = now.Add(31 * time.Second)
	if _, err := host.ResolveAsset(inlineRef); ErrorCode(err) != "plugin_asset_expired" {
		t.Fatalf("expired asset error=%v code=%s", err, ErrorCode(err))
	}
	for len(host.assets) < 4096 {
		reference := uuid.NewString()
		host.assets[reference] = Asset{PluginID: fixture.pluginID, Body: []byte(`{}`), ContentType: "application/json", ExpiresAt: now.Add(time.Minute)}
	}
	if _, err := host.Call(context.Background(), fixture.pluginID, OperationAssetRegister, inlinePayload); ErrorCode(err) != "plugin_asset_capacity_exceeded" {
		t.Fatalf("capacity error=%v code=%s", err, ErrorCode(err))
	}
}

func TestHostOpenAssetStreamsRemoteRangeAndRechecksPluginState(t *testing.T) {
	fixture := newHostFixture(t, []contract.Permission{{Kind: contract.PermissionNetworkHTTP, Domains: []string{"cdn.example.test"}}})
	var receivedMethod string
	var receivedRange string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		receivedMethod = request.Method
		receivedRange = request.Header.Get("Range")
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			Header:        http.Header{"Content-Type": {"video/mp4"}, "Content-Range": {"bytes 0-3/10"}, "Accept-Ranges": {"bytes"}, "Set-Cookie": {"secret=leak"}},
			Body:          io.NopCloser(strings.NewReader("data")),
			ContentLength: 4,
			Request:       request,
		}, nil
	})}
	host := New(fixture.db, fixture.credentials, zerolog.Nop(), WithHTTPClient(client), WithResolver(publicResolver))
	payload, _ := json.Marshal(assetRegisterRequest{URL: "https://cdn.example.test/video.mp4", Headers: map[string]string{"Referer": "https://www.example.test/"}, TTLSeconds: 60})
	response, err := host.Call(context.Background(), fixture.pluginID, OperationAssetRegister, payload)
	if err != nil {
		t.Fatal(err)
	}
	reference := assetReference(t, response)
	stream, err := host.OpenAsset(context.Background(), reference, http.MethodGet, "bytes=0-3")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(stream.Body)
	_ = stream.Body.Close()
	if receivedMethod != http.MethodGet || receivedRange != "bytes=0-3" || stream.StatusCode != http.StatusPartialContent || string(body) != "data" {
		t.Fatalf("method=%s range=%s status=%d body=%q", receivedMethod, receivedRange, stream.StatusCode, body)
	}
	if stream.Header.Get("Content-Range") != "bytes 0-3/10" || stream.Header.Get("Content-Length") != "4" || stream.Header.Get("Set-Cookie") != "" {
		t.Fatalf("unsafe or incomplete asset headers: %#v", stream.Header)
	}
	if err := fixture.db.Model(&models.PluginInstallation{}).Where("plugin_id = ?", fixture.pluginID).Update("status", models.PluginInstallationDisabled).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := host.OpenAsset(context.Background(), reference, http.MethodGet, ""); ErrorCode(err) != "plugin_host_plugin_disabled" {
		t.Fatalf("disabled plugin asset error=%v code=%s", err, ErrorCode(err))
	}
}

func assetReference(t *testing.T, response []byte) string {
	t.Helper()
	var envelope struct {
		Data struct {
			Reference string `json:"ref"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Data.Reference == "" {
		t.Fatalf("asset response=%s err=%v", response, err)
	}
	return envelope.Data.Reference
}

func TestHostLogsCannotOverridePluginIdentityOrSensitiveFields(t *testing.T) {
	fixture := newHostFixture(t, nil)
	var output bytes.Buffer
	host := New(fixture.db, fixture.credentials, zerolog.New(&output))
	payload := []byte(`{"level":"info","operation":"site.feed","message":"done","fields":{"plugin_id":"spoof","token":"secret","count":2}}`)
	if _, err := host.Call(context.Background(), fixture.pluginID, OperationLog, payload); err != nil {
		t.Fatal(err)
	}
	logged := output.String()
	if !strings.Contains(logged, fixture.pluginID) || strings.Contains(logged, "spoof") || strings.Contains(logged, "secret") {
		t.Fatalf("unsafe plugin log: %s", logged)
	}
}

type hostFixture struct {
	db          *gorm.DB
	credentials *credential.Store
	pluginID    string
	connection  models.PluginConnection
}

func newHostFixture(t *testing.T, permissions []contract.Permission) hostFixture {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "host.db"))
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
	pluginID := "org.ohmycine.test-host"
	now := time.Now().UTC()
	pluginPackage := models.PluginPackage{PluginID: pluginID, Version: "0.1.0", RepositoryOwner: "ohmycine", RepositoryRepo: "plugins", RegistryCommit: strings.Repeat("a", 40), RegistryEntryJSON: `{}`, ManifestURL: "https://github.com/ohmycine/plugins/releases/download/v1/manifest.json", PackageURL: "https://github.com/ohmycine/plugins/releases/download/v1/plugin.omcp", PackageSHA256: strings.Repeat("b", 64), ExtractedTreeSHA256: strings.Repeat("c", 64), ManifestJSON: `{}`, PackagePath: filepath.Join(t.TempDir(), "package"), VerifiedAt: now, CreatedAt: now}
	if err := db.Create(&pluginPackage).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PluginInstallation{PluginID: pluginID, ActivePackageID: pluginPackage.ID, Status: models.PluginInstallationEnabled, Revision: 1, RuntimeGeneration: 1, InstalledAt: now, UpdatedAt: now, EnabledAt: &now}).Error; err != nil {
		t.Fatal(err)
	}
	for index, permission := range permissions {
		encoded, _ := json.Marshal(permission)
		if err := db.Create(&models.PluginPermissionGrant{PluginID: pluginID, PluginPackageID: pluginPackage.ID, PermissionKey: string(permission.Kind) + string(rune(index+'a')), PermissionJSON: string(encoded), CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	connection := models.PluginConnection{ID: "11111111-1111-4111-8111-111111111111", PluginID: pluginID, Name: "test", ConfigJSON: `{}`, CredentialScope: "site.session", CredentialMode: models.PluginCredentialModeCookie, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	ciphertext, err := store.Encrypt(CredentialPurpose(pluginID, connection.ID, connection.CredentialScope), "SESSDATA=encrypted-test")
	if err != nil {
		t.Fatal(err)
	}
	connection.CredentialCiphertext = ciphertext
	if err := db.Create(&connection).Error; err != nil {
		t.Fatal(err)
	}
	return hostFixture{db: db, credentials: store, pluginID: pluginID, connection: connection}
}

func publicResolver(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("203.0.113.10")}}, nil
}
