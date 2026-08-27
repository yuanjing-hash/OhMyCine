package services

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

func TestEmbyConnectionEncryptsCredentialAndProbesWithoutRedirect(t *testing.T) {
	const serverKey = "server-side-key-must-not-leak"
	var probeKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeKey = r.Header.Get("X-Emby-Token")
		if r.URL.Path != "/emby/System/Info" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"Id":"emby-1","ServerName":"家庭 Emby","Version":"4.8"}`)
	}))
	defer upstream.Close()
	db, store, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	created, err := connections.Create(actor, ConnectionInput{Name: "家庭 Emby", Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL + "/emby/", APIKey: serverKey, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(created)
	if bytes.Contains(encoded, []byte(serverKey)) || !strings.HasSuffix(created.Endpoint, "/emby") {
		t.Fatalf("unsafe or unnormalized Emby summary: %s", encoded)
	}
	var record models.Connection
	if err := db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.CredentialCiphertext == serverKey || strings.Contains(record.CredentialCiphertext, serverKey) {
		t.Fatal("Emby API key was stored in plaintext")
	}
	plaintext, err := store.Decrypt(connectionPurpose(created.ID, models.ConnectionProviderEmby), record.CredentialCiphertext)
	if err != nil || plaintext != serverKey {
		t.Fatalf("decrypt credential=%q err=%v", plaintext, err)
	}
	probed, err := connections.Test(context.Background(), actor, created.ID, RequestContext{})
	if err != nil || probed.Health.Status != "online" || probeKey != serverKey {
		t.Fatalf("probe=%+v key=%q err=%v", probed, probeKey, err)
	}
	var gateway models.EmbyProxyGateway
	if err := db.Where("connection_id = ?", created.ID).First(&gateway).Error; err != nil {
		t.Fatal(err)
	}
	if _, aliasErr := normalizeEmbyGatewayAlias(gateway.PublicID); aliasErr != nil || gateway.Enabled || !gateway.ExternalPlayerEnabled || !gateway.FanartEnabled {
		t.Fatalf("default gateway=%+v err=%v", gateway, err)
	}
}

func TestJoinGatewayPathKeepsConfiguredBaseExactlyOnce(t *testing.T) {
	tests := map[string]string{
		"base already present": "/emby/users/public",
		"base must be added":   "/emby/users/public",
		"prefix collision":     "/emby/emby-other/users/public",
	}
	inputs := map[string]string{
		"base already present": "/emby/users/public",
		"base must be added":   "/users/public",
		"prefix collision":     "/emby-other/users/public",
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := joinGatewayPath("/emby/", inputs[name]); got != want {
				t.Fatalf("joinGatewayPath()=%q want=%q", got, want)
			}
		})
	}
}

func TestEmbyGatewayForwardsApplicationBasePathExactlyOnce(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emby/redirect" {
			w.Header().Set("Location", upstream.URL+"/emby/users/public?from=redirect")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path != "/emby/users/public" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"path":"`+r.URL.Path+`","query":"`+r.URL.RawQuery+`"}`)
	}))
	defer upstream.Close()

	db, store, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	created, err := connections.Create(actor, ConnectionInput{Name: "Emby Base Path", Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL + "/emby/", APIKey: "server-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Connection{}).Where("id = ?", created.ID).Update("last_health_status", "online").Error; err != nil {
		t.Fatal(err)
	}
	signedProxy, err := NewSignedProxyService(db, store, connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewEmbyGatewayService(db, NewAuditService(db), signedProxy, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := gateway.Configure(actor, created.ID, true, 1, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}

	for _, gatewayPath := range []string{"/emby/users/public", "/users/public"} {
		request := httptest.NewRequest(http.MethodGet, gatewayPath+"?api_key=client%20token&x=1&x=2", nil)
		response := httptest.NewRecorder()
		gateway.ServeHTTP(response, request, settings.PublicID, gatewayPath)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"path":"/emby/users/public"`) || !strings.Contains(response.Body.String(), `"query":"api_key=client%20token&x=1&x=2"`) {
			t.Fatalf("path=%q status=%d body=%q", gatewayPath, response.Code, response.Body.String())
		}
	}

	redirectRequest := httptest.NewRequest(http.MethodGet, "/emby/redirect", nil)
	redirectResponse := httptest.NewRecorder()
	gateway.ServeHTTP(redirectResponse, redirectRequest, settings.PublicID, "/emby/redirect")
	if got, want := redirectResponse.Header().Get("Location"), "/emby/"+settings.PublicID+"/users/public?from=redirect"; got != want {
		t.Fatalf("location=%q want=%q", got, want)
	}
}

func TestEmbyGatewayPatchesOnlyKnownWebPlayerAssetsForCrossOriginRedirectPlayback(t *testing.T) {
	const (
		baseAsset   = `function getCrossOriginValue(mediaSource,playMethod){return mediaSource.IsRemote&&"DirectPlay"===playMethod?null:"anonymous"}`
		pluginAsset = `var initialSubtitleStream=getValue();initialSubtitleStream&&(elem.crossOrigin=initialSubtitleStream);startPlayback();`
		indexAsset  = `<!doctype html><html><head><script src="boot.js"></script></head><body></body></html>`
	)
	var baseAcceptEncoding, baseValidator, pluginAcceptEncoding, pluginValidator, indexAcceptEncoding, indexValidator string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/emby/web/index.html":
			indexAcceptEncoding = r.Header.Get("Accept-Encoding")
			indexValidator = r.Header.Get("If-None-Match")
			if indexValidator != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
			w.Header().Set("ETag", `"index-v1"`)
			_, _ = io.WriteString(w, indexAsset)
		case "/emby/web/modules/htmlvideoplayer/basehtmlplayer.js":
			baseAcceptEncoding = r.Header.Get("Accept-Encoding")
			baseValidator = r.Header.Get("If-None-Match")
			if baseValidator != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Header().Set("ETag", `"base-v1"`)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.Header().Set("SourceMap", "basehtmlplayer.js.map")
			_, _ = io.WriteString(w, baseAsset)
		case "/emby/web/modules/htmlvideoplayer/plugin.js":
			pluginAcceptEncoding = r.Header.Get("Accept-Encoding")
			pluginValidator = r.Header.Get("If-Modified-Since")
			if pluginValidator != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("ETag", `"plugin-v1"`)
			_, _ = io.WriteString(w, pluginAsset)
		case "/emby/web/modules/other.js":
			if r.Header.Get("If-None-Match") != "" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = io.WriteString(w, pluginAsset)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	db, store, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	created, err := connections.Create(actor, ConnectionInput{Name: "Emby Web Patch", Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL + "/emby", APIKey: "server-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Connection{}).Where("id = ?", created.ID).Update("last_health_status", "online").Error; err != nil {
		t.Fatal(err)
	}
	signedProxy, err := NewSignedProxyService(db, store, connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewEmbyGatewayService(db, NewAuditService(db), signedProxy, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := gateway.Configure(actor, created.ID, true, 1, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}

	baseRequest := httptest.NewRequest(http.MethodGet, "/web/modules/htmlvideoplayer/basehtmlplayer.js?v=4.9.5.0", nil)
	baseRequest.Header.Set("Accept-Encoding", "br, gzip")
	baseRequest.Header.Set("If-None-Match", `"base-v1"`)
	baseResponse := httptest.NewRecorder()
	gateway.ServeHTTP(baseResponse, baseRequest, settings.PublicID, "/web/modules/htmlvideoplayer/basehtmlplayer.js")
	if baseResponse.Code != http.StatusOK || strings.Contains(baseResponse.Body.String(), `?null:"anonymous"`) || !strings.Contains(baseResponse.Body.String(), "return null") {
		t.Fatalf("base asset status=%d body=%q", baseResponse.Code, baseResponse.Body.String())
	}
	if baseAcceptEncoding != "identity" || baseValidator != "" || baseResponse.Header().Get("ETag") != "" || baseResponse.Header().Get("Last-Modified") != "" || baseResponse.Header().Get("SourceMap") != "" || !strings.Contains(baseResponse.Header().Get("Cache-Control"), "no-store") || baseResponse.Header().Get("Content-Length") != strconv.Itoa(baseResponse.Body.Len()) {
		t.Fatalf("base request encoding=%q validator=%q response_headers=%v", baseAcceptEncoding, baseValidator, baseResponse.Header())
	}

	pluginRequest := httptest.NewRequest(http.MethodGet, "/emby/web/modules/htmlvideoplayer/plugin.js?v=4.9.5.0", nil)
	pluginRequest.Header.Set("Accept-Encoding", "gzip")
	pluginRequest.Header.Set("If-Modified-Since", time.Now().UTC().Format(http.TimeFormat))
	pluginResponse := httptest.NewRecorder()
	gateway.ServeHTTP(pluginResponse, pluginRequest, settings.PublicID, "/emby/web/modules/htmlvideoplayer/plugin.js")
	if pluginResponse.Code != http.StatusOK || strings.Contains(pluginResponse.Body.String(), ".crossOrigin=") || !strings.Contains(pluginResponse.Body.String(), "initialSubtitleStream;startPlayback()") {
		t.Fatalf("plugin asset status=%d body=%q", pluginResponse.Code, pluginResponse.Body.String())
	}
	if pluginAcceptEncoding != "identity" || pluginValidator != "" || pluginResponse.Header().Get("ETag") != "" || !strings.Contains(pluginResponse.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("plugin request encoding=%q validator=%q response_headers=%v", pluginAcceptEncoding, pluginValidator, pluginResponse.Header())
	}

	indexRequest := httptest.NewRequest(http.MethodGet, "/web/index.html", nil)
	indexRequest.Header.Set("Accept-Encoding", "br, gzip")
	indexRequest.Header.Set("If-None-Match", `"index-v1"`)
	indexResponse := httptest.NewRecorder()
	gateway.ServeHTTP(indexResponse, indexRequest, settings.PublicID, "/web/index.html")
	expectedAssetURL := "/emby/" + settings.PublicID + embyWebCompatibilityAssetPath
	indexBody := indexResponse.Body.String()
	if indexResponse.Code != http.StatusOK || !strings.Contains(indexBody, embyWebCompatibilityMarker) || !strings.Contains(indexBody, `src="`+expectedAssetURL+`"`) || strings.Index(indexBody, embyWebCompatibilityMarker) > strings.Index(indexBody, `src="boot.js"`) {
		t.Fatalf("index asset status=%d body=%q", indexResponse.Code, indexBody)
	}
	if indexAcceptEncoding != "identity" || indexValidator != "" || indexResponse.Header().Get("ETag") != "" || !strings.Contains(indexResponse.Header().Get("Cache-Control"), "no-store") || indexResponse.Header().Get("Content-Security-Policy") != "default-src 'self'; script-src 'self'" || indexResponse.Header().Get("Content-Length") != strconv.Itoa(indexResponse.Body.Len()) {
		t.Fatalf("index request encoding=%q validator=%q response_headers=%v", indexAcceptEncoding, indexValidator, indexResponse.Header())
	}

	compatibilityRequest := httptest.NewRequest(http.MethodGet, expectedAssetURL, nil)
	compatibilityResponse := httptest.NewRecorder()
	gateway.ServeHTTP(compatibilityResponse, compatibilityRequest, settings.PublicID, embyWebCompatibilityAssetPath)
	if compatibilityResponse.Code != http.StatusOK || !strings.Contains(compatibilityResponse.Body.String(), "Object.defineProperty") || !strings.Contains(compatibilityResponse.Body.String(), "MutationObserver") || !strings.Contains(compatibilityResponse.Body.String(), "externalPlayer:true") || !strings.Contains(compatibilityResponse.Body.String(), "fanart:true") || !strings.Contains(compatibilityResponse.Body.String(), "omc_ticket") || compatibilityResponse.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(compatibilityResponse.Header().Get("Cache-Control"), "no-store") || compatibilityResponse.Header().Get("Content-Length") != strconv.Itoa(compatibilityResponse.Body.Len()) {
		t.Fatalf("compatibility asset status=%d headers=%v body=%q", compatibilityResponse.Code, compatibilityResponse.Header(), compatibilityResponse.Body.String())
	}
	compatibilityHeadRequest := httptest.NewRequest(http.MethodHead, expectedAssetURL, nil)
	compatibilityHeadResponse := httptest.NewRecorder()
	gateway.ServeHTTP(compatibilityHeadResponse, compatibilityHeadRequest, settings.PublicID, embyWebCompatibilityAssetPath)
	if compatibilityHeadResponse.Code != http.StatusOK || compatibilityHeadResponse.Body.Len() != 0 || compatibilityHeadResponse.Header().Get("Content-Length") != strconv.Itoa(len(buildEmbyWebCompatibilityScript(embyGatewayTarget{ExternalPlayerEnabled: true, FanartEnabled: true}))) {
		t.Fatalf("compatibility HEAD status=%d headers=%v body=%q", compatibilityHeadResponse.Code, compatibilityHeadResponse.Header(), compatibilityHeadResponse.Body.String())
	}

	ordinaryRequest := httptest.NewRequest(http.MethodGet, "/web/modules/other.js", nil)
	ordinaryRequest.Header.Set("If-None-Match", `"ordinary-v1"`)
	ordinaryResponse := httptest.NewRecorder()
	gateway.ServeHTTP(ordinaryResponse, ordinaryRequest, settings.PublicID, "/web/modules/other.js")
	if ordinaryResponse.Code != http.StatusNotModified {
		t.Fatalf("ordinary asset was unexpectedly modified: status=%d body=%q", ordinaryResponse.Code, ordinaryResponse.Body.String())
	}
}

func TestEmbyGatewayAliasValidationUniquenessAndRevision(t *testing.T) {
	db, store, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	first, err := connections.Create(actor, ConnectionInput{Name: "Alias Emby One", Provider: models.ConnectionProviderEmby, Endpoint: "https://one.example.test/emby", APIKey: "server-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := connections.Create(actor, ConnectionInput{Name: "Alias Emby Two", Provider: models.ConnectionProviderEmby, Endpoint: "https://two.example.test/emby", APIKey: "server-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Connection{}).Where("id IN ?", []uint{first.ID, second.ID}).Update("last_health_status", "online").Error; err != nil {
		t.Fatal(err)
	}
	signedProxy, err := NewSignedProxyService(db, store, connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewEmbyGatewayService(db, NewAuditService(db), signedProxy, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	initial, err := gateway.Get(actor, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldAlias := initial.Alias
	requested := " Home-Cinema "
	updated, err := gateway.ConfigureSettings(actor, first.ID, EmbyGatewaySettingsInput{Enabled: true, Alias: &requested, Revision: initial.Revision}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Alias != "home-cinema" || updated.PublicID != updated.Alias || updated.Revision != initial.Revision+1 || updated.BaseURL != "https://media.example.test/emby/home-cinema" {
		t.Fatalf("updated=%+v", updated)
	}
	if !updated.ExternalPlayerEnabled || !updated.FanartEnabled {
		t.Fatalf("default web enhancements were not preserved: %+v", updated)
	}
	disabled := false
	updated, err = gateway.ConfigureSettings(actor, first.ID, EmbyGatewaySettingsInput{Enabled: true, ExternalPlayerEnabled: &disabled, FanartEnabled: &disabled, Revision: updated.Revision}, RequestContext{})
	if err != nil || updated.ExternalPlayerEnabled || updated.FanartEnabled {
		t.Fatalf("disabled web enhancements=%+v err=%v", updated, err)
	}
	disabledScript := buildEmbyWebCompatibilityScript(embyGatewayTarget{ExternalPlayerEnabled: updated.ExternalPlayerEnabled, FanartEnabled: updated.FanartEnabled})
	if !strings.Contains(disabledScript, "externalPlayer:false") || !strings.Contains(disabledScript, "fanart:false") {
		t.Fatalf("disabled script options=%q", disabledScript[:160])
	}
	if _, err := gateway.gatewayByPublicID(oldAlias); ErrorCode(err) != CodeNotFound {
		t.Fatalf("old alias still resolves: code=%q err=%v", ErrorCode(err), err)
	}
	if _, err := gateway.ConfigureSettings(actor, first.ID, EmbyGatewaySettingsInput{Enabled: true, Alias: &requested, Revision: initial.Revision}, RequestContext{}); ErrorCode(err) != CodeConflict {
		t.Fatalf("stale revision code=%q err=%v", ErrorCode(err), err)
	}

	duplicate := "HOME-CINEMA"
	if _, err := gateway.ConfigureSettings(actor, second.ID, EmbyGatewaySettingsInput{Enabled: true, Alias: &duplicate, Revision: 1}, RequestContext{}); ErrorCode(err) != CodeEmbyGatewayAliasConflict {
		t.Fatalf("duplicate alias code=%q err=%v", ErrorCode(err), err)
	}
	for value, wantCode := range map[string]string{
		"api": CodeEmbyGatewayAliasReserved, "ab": CodeEmbyGatewayAliasInvalid, "-bad": CodeEmbyGatewayAliasInvalid,
		"bad-": CodeEmbyGatewayAliasInvalid, "bad--alias": CodeEmbyGatewayAliasInvalid, "bad/path": CodeEmbyGatewayAliasInvalid,
		"bad_alias": CodeEmbyGatewayAliasInvalid, strings.Repeat("a", embyAliasMaxLength+1): CodeEmbyGatewayAliasInvalid,
	} {
		candidate := value
		if _, err := gateway.ConfigureSettings(actor, second.ID, EmbyGatewaySettingsInput{Enabled: false, Alias: &candidate, Revision: 1}, RequestContext{}); ErrorCode(err) != wantCode {
			t.Fatalf("alias=%q code=%q want=%q err=%v", value, ErrorCode(err), wantCode, err)
		}
	}

	legacyID := "AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	if !validOpaqueID(legacyID) {
		t.Fatalf("test legacy ID is not a valid v27 identifier: %q", legacyID)
	}
	if err := db.Model(&models.EmbyProxyGateway{}).Where("connection_id = ?", second.ID).Update("public_id", legacyID).Error; err != nil {
		t.Fatal(err)
	}
	legacy, err := gateway.gatewayByPublicID(legacyID)
	if err != nil || legacy.ConnectionID != second.ID {
		t.Fatalf("legacy gateway=%+v err=%v", legacy, err)
	}
}

func TestEmbyConnectionRejectsHeaderUnsafeAPIKeysOnCreateAndUpdate(t *testing.T) {
	_, _, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	for _, value := range []string{"bad\r\nX-Injected: yes", "bad\x00value", strings.Repeat("x", 4097)} {
		if _, err := connections.Create(actor, ConnectionInput{Name: "Invalid Emby", Provider: models.ConnectionProviderEmby, Endpoint: "https://example.test", APIKey: value, Enabled: true}, RequestContext{}); ErrorCode(err) != CodeEmbyAPIKeyInvalid {
			t.Fatalf("create code=%q err=%v", ErrorCode(err), err)
		}
	}
	created, err := connections.Create(actor, ConnectionInput{Name: "Valid Emby", Provider: models.ConnectionProviderEmby, Endpoint: "https://example.test", APIKey: "safe-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	unsafe := "bad\r\nX-Injected: yes"
	if _, err := connections.Update(actor, created.ID, UpdateConnectionInput{APIKey: &unsafe, Revision: created.Revision}, RequestContext{}); ErrorCode(err) != CodeEmbyAPIKeyInvalid {
		t.Fatalf("update code=%q err=%v", ErrorCode(err), err)
	}
}

func TestEmbyProbeCannotMarkUpdatedEndpointOnline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	oldUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"Id":"old-server","ServerName":"Old"}`)
	}))
	defer oldUpstream.Close()
	newUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"Id":"new-server","ServerName":"New"}`)
	}))
	defer newUpstream.Close()
	db, _, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	created, err := connections.Create(actor, ConnectionInput{Name: "Concurrent Emby", Provider: models.ConnectionProviderEmby, Endpoint: oldUpstream.URL, APIKey: "server-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	probeDone := make(chan error, 1)
	go func() {
		_, probeErr := connections.Test(context.Background(), actor, created.ID, RequestContext{})
		probeDone <- probeErr
	}()
	<-started
	endpoint := newUpstream.URL
	updated, err := connections.Update(actor, created.ID, UpdateConnectionInput{Endpoint: &endpoint, Revision: created.Revision}, RequestContext{})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if probeErr := <-probeDone; ErrorCode(probeErr) != CodeConflict {
		t.Fatalf("stale probe code=%q err=%v", ErrorCode(probeErr), probeErr)
	}
	var record models.Connection
	if err := db.First(&record, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.Endpoint != updated.Endpoint || record.LastHealthStatus != "unknown" || record.Revision != updated.Revision {
		t.Fatalf("stale probe overwrote update: record=%+v updated=%+v", record, updated)
	}
}

func TestEmbyGatewayWebSocketPassthroughPreservesClientCredential(t *testing.T) {
	var upstreamToken atomic.Value
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emby/socket" {
			http.NotFound(w, r)
			return
		}
		upstreamToken.Store(r.Header.Get("X-Emby-Token"))
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		kind, message, err := connection.ReadMessage()
		if err == nil {
			_ = connection.WriteMessage(kind, message)
		}
	}))
	defer upstream.Close()
	db, store, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	embyConnection, err := connections.Create(actor, ConnectionInput{Name: "WebSocket Emby", Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL + "/emby", APIKey: "server-admin-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Connection{}).Where("id = ?", embyConnection.ID).Update("last_health_status", "online").Error; err != nil {
		t.Fatal(err)
	}
	signedProxy, err := NewSignedProxyService(db, store, connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewEmbyGatewayService(db, NewAuditService(db), signedProxy, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := gateway.Configure(actor, embyConnection.ID, true, 1, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	downstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayPath := strings.TrimPrefix(r.URL.Path, "/emby/"+settings.PublicID)
		gateway.ServeHTTP(w, r, settings.PublicID, gatewayPath)
	}))
	downstream.Config.ReadTimeout = 50 * time.Millisecond
	downstream.Config.WriteTimeout = 50 * time.Millisecond
	downstream.Start()
	defer downstream.Close()
	wsURL := "ws" + strings.TrimPrefix(downstream.URL, "http") + "/emby/" + settings.PublicID + "/emby/socket"
	header := http.Header{"X-Emby-Token": {"client-websocket-token"}}
	connection, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			t.Fatalf("dial status=%d err=%v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	time.Sleep(100 * time.Millisecond)
	if err := connection.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, message, err := connection.ReadMessage()
	if err != nil || string(message) != "ping" {
		t.Fatalf("echo=%q err=%v", message, err)
	}
	if value, _ := upstreamToken.Load().(string); value != "client-websocket-token" {
		t.Fatalf("upstream token=%q", value)
	}
}

func TestEmbyGatewaySelectivePlaybackTakeoverTicketBindingAndFallback(t *testing.T) {
	driver := &fakeCloudDriver{signedProxy: true, echoDirectUA: true, directURL: "https://cdn.example.test/private", items: map[string]cloud.Item{
		"video-file": {ID: "video-file", ParentID: "library-root", Name: "Movie.mkv", PickCode: "private-pickcode", Size: 100},
	}}
	db, store, connections, actor := newConnectionTestService(t, driver)
	cloudConnection, err := connections.Create(actor, ConnectionInput{Name: "115 for Emby", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	storage := models.Storage{Name: "Emby cloud", NameNormalized: "emby-cloud", Type: models.StorageTypePan115, RootPath: "storage-root", RootDisplayPath: "/媒体", RootPathNormalized: "pan115:emby", ConnectionID: &cloudConnection.ID, Enabled: true, Capabilities: `{"temporary_direct_url":true,"signed_proxy":true}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&storage).Error; err != nil {
		t.Fatal(err)
	}
	var profile models.MediaClassificationProfile
	if err := db.Where("code = ?", "default-v1").First(&profile).Error; err != nil {
		t.Fatal(err)
	}
	library := models.MediaLibrary{Name: "Emby STRM", NameNormalized: "emby-strm", StorageID: storage.ID, ProfileID: profile.ID, ProfileRevision: profile.Revision, RelativeRoot: "/", ProviderRootID: "library-root", Enabled: true, Recursive: true, VideoExtensionsJSON: `[".mkv"]`, IgnorePatternsJSON: `[]`, STRMEnabled: true, SignedProxyEnabled: true, MetadataArtifactsEnabled: true, STRMLocalRoot: t.TempDir(), Status: models.MediaLibraryStatusListening, ArtifactStatus: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Select("*").Create(&library).Error; err != nil {
		t.Fatal(err)
	}
	run := models.MediaArtifactRun{ID: "33333333-3333-4333-8333-333333333333", LibraryID: library.ID, Generation: 1, PolicyJSON: `{}`, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	opaque := "artifact_CCCCCCCCCCCCCCCCCCCCCC"
	artifact := models.MediaArtifact{OpaqueID: opaque, RunID: run.ID, LibraryID: library.ID, ProviderItemID: "video-file", Kind: models.MediaArtifactKindSTRM, TargetKind: models.MediaArtifactTargetLocalProjection, RelativePath: "/Movie.strm", Managed: true, Active: true, Status: models.MediaArtifactStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := db.Select("*").Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	signedProxy, err := NewSignedProxyService(db, store, connections, "https://media.example.test", zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	signedURL, err := signedProxy.SignArtifact(opaque, library.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	var fallbackCalls atomic.Int32
	var playbackCalls atomic.Int32
	var crossOriginCalls atomic.Int32
	var ordinaryClientToken string
	var noClientToken string
	crossOrigin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		crossOriginCalls.Add(1)
	}))
	defer crossOrigin.Close()
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/base/") {
			http.NotFound(w, r)
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/base") {
		case "/api/plain":
			ordinaryClientToken = r.Header.Get("X-Emby-Token")
			w.Header().Set("Cache-Control", "public, max-age=60")
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
			_, _ = io.WriteString(w, "plain")
		case "/api/no-client-token":
			noClientToken = r.Header.Get("X-Emby-Token")
			_, _ = io.WriteString(w, "plain")
		case "/Items/item-1/PlaybackInfo":
			playbackCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			var output bytes.Buffer
			_ = json.NewEncoder(&output).Encode(map[string]any{"MediaSources": []any{
				map[string]any{"Id": "source-cloud", "Type": "Video", "Path": signedURL, "SupportsTranscoding": true, "TranscodingUrl": "/transcode"},
				map[string]any{"Id": "source-local", "Type": "Video", "Path": "D:\\Media\\Local.mkv", "SupportsTranscoding": true},
			}})
			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				w.Header().Set("Content-Encoding", "gzip")
				compressed := gzip.NewWriter(w)
				_, _ = compressed.Write(output.Bytes())
				_ = compressed.Close()
				return
			}
			_, _ = w.Write(output.Bytes())
		case "/Items/item-large/PlaybackInfo":
			playbackCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"MediaSources":[],"padding":"`+strings.Repeat("x", embyPlaybackBodyLimit)+`"}`)
		case "/videos/item-1/stream", "/videos/item-2/stream":
			fallbackCalls.Add(1)
			_, _ = io.WriteString(w, "upstream-fallback")
		case "/redirect-same":
			w.Header().Set("Location", upstream.URL+"/base/target")
			w.WriteHeader(http.StatusFound)
		case "/redirect-prefix-collision":
			w.Header().Set("Location", upstream.URL+"/base-other/target")
			w.WriteHeader(http.StatusFound)
		case "/redirect-cross-origin":
			w.Header().Set("Location", crossOrigin.URL+"/credential-sink")
			w.WriteHeader(http.StatusFound)
		case "/nested/redirect-relative-outside":
			w.Header().Set("Location", "../../../api/plain")
			w.WriteHeader(http.StatusFound)
		case "/nested/redirect-relative-inside":
			w.Header().Set("Location", "../target")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	embyConnection, err := connections.Create(actor, ConnectionInput{Name: "Gateway Emby", Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL + "/base", APIKey: "server-admin-key", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Connection{}).Where("id = ?", embyConnection.ID).Update("last_health_status", "online").Error; err != nil {
		t.Fatal(err)
	}
	var gatewayLogs bytes.Buffer
	gateway, err := NewEmbyGatewayService(db, NewAuditService(db), signedProxy, "https://media.example.test", zerolog.New(&gatewayLogs))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := gateway.Configure(actor, embyConnection.ID, true, 1, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}

	plainRequest := httptest.NewRequest(http.MethodGet, "/api/plain", nil)
	plainRequest.Header.Set("X-Emby-Token", "client-user-token")
	plainResponse := httptest.NewRecorder()
	gateway.ServeHTTP(plainResponse, plainRequest, settings.PublicID, "/api/plain")
	if plainResponse.Code != http.StatusOK || plainResponse.Body.String() != "plain" || ordinaryClientToken != "client-user-token" || plainResponse.Header().Get("Cache-Control") != "public, max-age=60" || plainResponse.Header().Get("Content-Security-Policy") != "default-src 'none'" {
		t.Fatalf("plain status=%d token=%q headers=%v body=%q", plainResponse.Code, ordinaryClientToken, plainResponse.Header(), plainResponse.Body.String())
	}
	noTokenRequest := httptest.NewRequest(http.MethodGet, "/api/no-client-token", nil)
	noTokenResponse := httptest.NewRecorder()
	gateway.ServeHTTP(noTokenResponse, noTokenRequest, settings.PublicID, "/api/no-client-token")
	if noTokenResponse.Code != http.StatusOK || noClientToken != "" {
		t.Fatalf("Server API key was injected into gateway request: status=%d token=%q", noTokenResponse.Code, noClientToken)
	}

	playbackRequest := httptest.NewRequest(http.MethodPost, "/Items/item-1/PlaybackInfo", strings.NewReader(`{"UserId":"user-1"}`))
	playbackRequest.Header.Set("Accept-Encoding", "br, gzip")
	playbackResponse := httptest.NewRecorder()
	gateway.ServeHTTP(playbackResponse, playbackRequest, settings.PublicID, "/Items/item-1/PlaybackInfo")
	if playbackResponse.Code != http.StatusOK || playbackResponse.Header().Get("Cache-Control") != "no-store" || strings.Contains(playbackResponse.Body.String(), signedURL) || strings.Contains(playbackResponse.Body.String(), "private-pickcode") {
		t.Fatalf("playback status=%d body=%s", playbackResponse.Code, playbackResponse.Body.String())
	}
	if playbackCalls.Load() != 1 {
		t.Fatalf("playback upstream calls=%d", playbackCalls.Load())
	}
	var playback struct {
		MediaSources []map[string]any `json:"MediaSources"`
	}
	if err := json.Unmarshal(playbackResponse.Body.Bytes(), &playback); err != nil || len(playback.MediaSources) != 2 {
		t.Fatalf("playback decode=%+v err=%v", playback, err)
	}
	cloudSource, localSource := playback.MediaSources[0], playback.MediaSources[1]
	streamValue, _ := cloudSource["DirectStreamUrl"].(string)
	if cloudSource["SupportsDirectPlay"] != true || cloudSource["SupportsTranscoding"] != false || cloudSource["Path"] != streamValue || streamValue == "" || localSource["Path"] != "D:\\Media\\Local.mkv" || localSource["SupportsTranscoding"] != true {
		t.Fatalf("cloud=%+v local=%+v", cloudSource, localSource)
	}
	streamURL, err := url.Parse(streamValue)
	if err != nil || streamURL.Query().Get(playbackTicketParam) == "" || strings.Contains(streamValue, opaque) || strings.Contains(streamValue, signedURL) {
		t.Fatalf("unsafe stream URL %q err=%v", streamValue, err)
	}
	if streamURL.Path != "/videos/item-1/stream" || strings.Contains(streamURL.Path, "/emby/") || strings.Contains(streamURL.Path, settings.PublicID) {
		t.Fatalf("stream URL is not Emby API-relative: %q", streamValue)
	}
	streamGatewayPath := streamURL.Path
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		streamRequest := httptest.NewRequest(method, streamURL.RequestURI(), nil)
		streamRequest.Header.Set("User-Agent", "Emby Web/4.9.5.0")
		streamResponse := httptest.NewRecorder()
		gateway.ServeHTTP(streamResponse, streamRequest, settings.PublicID, streamGatewayPath)
		if streamResponse.Code != http.StatusFound || streamResponse.Header().Get("Location") != "https://cdn.example.test/private" || streamResponse.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s stream status=%d location=%q", method, streamResponse.Code, streamResponse.Header().Get("Location"))
		}
	}

	badQuery := streamURL.Query()
	badTicket := badQuery.Get(playbackTicketParam)
	badQuery.Set(playbackTicketParam, badTicket[:len(badTicket)-1]+"x")
	badRequest := httptest.NewRequest(http.MethodGet, streamURL.Path+"?"+badQuery.Encode(), nil)
	badResponse := httptest.NewRecorder()
	gateway.ServeHTTP(badResponse, badRequest, settings.PublicID, streamGatewayPath)
	if badResponse.Code != http.StatusForbidden || fallbackCalls.Load() != 0 {
		t.Fatalf("invalid ticket status=%d fallbacks=%d", badResponse.Code, fallbackCalls.Load())
	}

	wrongSourceQuery := streamURL.Query()
	wrongSourceQuery.Set("MediaSourceId", "source-local")
	wrongSourceRequest := httptest.NewRequest(http.MethodGet, streamURL.Path+"?"+wrongSourceQuery.Encode(), nil)
	wrongSourceResponse := httptest.NewRecorder()
	gateway.ServeHTTP(wrongSourceResponse, wrongSourceRequest, settings.PublicID, streamGatewayPath)
	if wrongSourceResponse.Code != http.StatusForbidden || fallbackCalls.Load() != 0 {
		t.Fatalf("wrong source status=%d fallbacks=%d", wrongSourceResponse.Code, fallbackCalls.Load())
	}

	wrongItemRequest := httptest.NewRequest(http.MethodGet, strings.Replace(streamURL.RequestURI(), "/item-1/", "/item-2/", 1), nil)
	wrongItemResponse := httptest.NewRecorder()
	gateway.ServeHTTP(wrongItemResponse, wrongItemRequest, settings.PublicID, "/videos/item-2/stream")
	if wrongItemResponse.Code != http.StatusForbidden || fallbackCalls.Load() != 0 {
		t.Fatalf("wrong item status=%d fallbacks=%d", wrongItemResponse.Code, fallbackCalls.Load())
	}

	nonStreamQuery := streamURL.Query()
	nonStreamRequest := httptest.NewRequest(http.MethodGet, "/videos/item-1/additionalparts?"+nonStreamQuery.Encode(), nil)
	nonStreamResponse := httptest.NewRecorder()
	gateway.ServeHTTP(nonStreamResponse, nonStreamRequest, settings.PublicID, "/videos/item-1/additionalparts")
	if nonStreamResponse.Code != http.StatusForbidden || fallbackCalls.Load() != 0 {
		t.Fatalf("non-stream ticket status=%d fallbacks=%d", nonStreamResponse.Code, fallbackCalls.Load())
	}

	leakRequest := httptest.NewRequest(http.MethodGet, "/api/plain?"+url.Values{playbackTicketParam: {badTicket}}.Encode(), nil)
	leakResponse := httptest.NewRecorder()
	gateway.ServeHTTP(leakResponse, leakRequest, settings.PublicID, "/api/plain")
	if leakResponse.Code != http.StatusForbidden || ordinaryClientToken != "client-user-token" || leakResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reserved ticket leaked to ordinary upstream: status=%d", leakResponse.Code)
	}
	for _, rawQuery := range []string{"OMC_TICKET=" + url.QueryEscape(badTicket), "omc_ticket=x;broken=y", "omc%255fticket=x", "omc%2525255fticket=x"} {
		malformedRequest := httptest.NewRequest(http.MethodGet, "/api/plain?"+rawQuery, nil)
		malformedResponse := httptest.NewRecorder()
		gateway.ServeHTTP(malformedResponse, malformedRequest, settings.PublicID, "/api/plain")
		if malformedResponse.Code != http.StatusForbidden || malformedResponse.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("reserved query %q status=%d", rawQuery, malformedResponse.Code)
		}
	}
	duplicateTicketQuery := streamURL.Query()
	duplicateTicketQuery["OMC_TICKET"] = []string{badTicket}
	duplicateTicketRequest := httptest.NewRequest(http.MethodGet, streamURL.Path+"?"+duplicateTicketQuery.Encode(), nil)
	duplicateTicketResponse := httptest.NewRecorder()
	gateway.ServeHTTP(duplicateTicketResponse, duplicateTicketRequest, settings.PublicID, streamGatewayPath)
	if duplicateTicketResponse.Code != http.StatusForbidden || fallbackCalls.Load() != 0 {
		t.Fatalf("duplicate ticket status=%d fallbacks=%d", duplicateTicketResponse.Code, fallbackCalls.Load())
	}
	duplicateSourceQuery := streamURL.Query()
	duplicateSourceQuery["MEDIASOURCEID"] = []string{"source-cloud"}
	duplicateSourceRequest := httptest.NewRequest(http.MethodGet, streamURL.Path+"?"+duplicateSourceQuery.Encode(), nil)
	duplicateSourceResponse := httptest.NewRecorder()
	gateway.ServeHTTP(duplicateSourceResponse, duplicateSourceRequest, settings.PublicID, streamGatewayPath)
	if duplicateSourceResponse.Code != http.StatusForbidden || fallbackCalls.Load() != 0 {
		t.Fatalf("duplicate media source status=%d fallbacks=%d", duplicateSourceResponse.Code, fallbackCalls.Load())
	}

	secondConnection, err := connections.Create(actor, ConnectionInput{Name: "Gateway Emby 2", Provider: models.ConnectionProviderEmby, Endpoint: upstream.URL + "/base", APIKey: "server-admin-key-2", Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Connection{}).Where("id = ?", secondConnection.ID).Update("last_health_status", "online").Error; err != nil {
		t.Fatal(err)
	}
	secondSettings, err := gateway.Configure(actor, secondConnection.ID, true, 1, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	crossGatewayRequest := httptest.NewRequest(http.MethodGet, streamURL.RequestURI(), nil)
	crossGatewayResponse := httptest.NewRecorder()
	gateway.ServeHTTP(crossGatewayResponse, crossGatewayRequest, secondSettings.PublicID, streamGatewayPath)
	if crossGatewayResponse.Code != http.StatusForbidden || fallbackCalls.Load() != 0 {
		t.Fatalf("cross gateway status=%d fallbacks=%d", crossGatewayResponse.Code, fallbackCalls.Load())
	}

	if _, err := gateway.verifyTicket(badTicket, settings.PublicID, "item-1", "source-cloud"); err != nil {
		t.Fatalf("current policy ticket rejected before reconfiguration: %v", err)
	}
	reconfigured, err := gateway.Configure(actor, embyConnection.ID, true, settings.Revision, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.verifyTicket(badTicket, settings.PublicID, "item-1", "source-cloud"); err == nil {
		t.Fatal("ticket verifier did not re-read the persisted gateway policy revision")
	}
	staleTicketRequest := httptest.NewRequest(http.MethodGet, streamURL.RequestURI(), nil)
	staleTicketResponse := httptest.NewRecorder()
	gateway.ServeHTTP(staleTicketResponse, staleTicketRequest, settings.PublicID, streamGatewayPath)
	if staleTicketResponse.Code != http.StatusForbidden || fallbackCalls.Load() != 0 {
		t.Fatalf("stale policy ticket status=%d fallbacks=%d", staleTicketResponse.Code, fallbackCalls.Load())
	}
	settings = reconfigured

	fallbackRequest := httptest.NewRequest(http.MethodGet, "/videos/item-1/stream?MediaSourceId=source-local", nil)
	fallbackResponse := httptest.NewRecorder()
	gateway.ServeHTTP(fallbackResponse, fallbackRequest, settings.PublicID, "/videos/item-1/stream")
	if fallbackResponse.Code != http.StatusOK || fallbackResponse.Body.String() != "upstream-fallback" || fallbackCalls.Load() != 1 {
		t.Fatalf("fallback status=%d calls=%d body=%q", fallbackResponse.Code, fallbackCalls.Load(), fallbackResponse.Body.String())
	}

	traversalRequest := httptest.NewRequest(http.MethodGet, "/../System/Info", nil)
	traversalResponse := httptest.NewRecorder()
	gateway.ServeHTTP(traversalResponse, traversalRequest, settings.PublicID, "/../System/Info")
	if traversalResponse.Code != http.StatusBadRequest {
		t.Fatalf("dot traversal status=%d", traversalResponse.Code)
	}
	encodedTraversal := httptest.NewRequest(http.MethodGet, "/safe%2f..%2fSystem/Info", nil)
	encodedTraversal.URL.RawPath = "/safe%2f..%2fSystem/Info"
	encodedResponse := httptest.NewRecorder()
	gateway.ServeHTTP(encodedResponse, encodedTraversal, settings.PublicID, "/safe/../System/Info")
	if encodedResponse.Code != http.StatusBadRequest {
		t.Fatalf("encoded traversal status=%d", encodedResponse.Code)
	}
	for _, encoded := range []string{
		"/safe%252f..%252fSystem/Info",
		"/safe%255c..%255cSystem/Info",
		"/safe%25252f..%25252fSystem/Info",
	} {
		repeatedRequest := httptest.NewRequest(http.MethodGet, encoded, nil)
		repeatedRequest.URL.RawPath = encoded
		repeatedResponse := httptest.NewRecorder()
		gateway.ServeHTTP(repeatedResponse, repeatedRequest, settings.PublicID, "/safe%2f..%2fSystem/Info")
		if repeatedResponse.Code != http.StatusBadRequest {
			t.Fatalf("repeated-encoded traversal %q status=%d", encoded, repeatedResponse.Code)
		}
	}
	decodedRepeatedRequest := httptest.NewRequest(http.MethodGet, "/safe/path", nil)
	decodedRepeatedResponse := httptest.NewRecorder()
	gateway.ServeHTTP(decodedRepeatedResponse, decodedRepeatedRequest, settings.PublicID, "/safe%2f..%2fSystem/Info")
	if decodedRepeatedResponse.Code != http.StatusBadRequest {
		t.Fatalf("decoded repeated-encoding traversal status=%d", decodedRepeatedResponse.Code)
	}

	oversizedRequest := httptest.NewRequest(http.MethodPost, "/Items/item-1/PlaybackInfo", strings.NewReader(strings.Repeat("x", embyPlaybackBodyLimit+1)))
	oversizedResponse := httptest.NewRecorder()
	gateway.ServeHTTP(oversizedResponse, oversizedRequest, settings.PublicID, "/Items/item-1/PlaybackInfo")
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge || playbackCalls.Load() != 1 {
		t.Fatalf("oversized request status=%d upstream_calls=%d", oversizedResponse.Code, playbackCalls.Load())
	}
	largeResponseRequest := httptest.NewRequest(http.MethodGet, "/Items/item-large/PlaybackInfo", nil)
	largeResponse := httptest.NewRecorder()
	gateway.ServeHTTP(largeResponse, largeResponseRequest, settings.PublicID, "/Items/item-large/PlaybackInfo")
	if largeResponse.Code != http.StatusBadGateway || strings.Contains(largeResponse.Body.String(), "padding") {
		t.Fatalf("oversized response status=%d body=%q", largeResponse.Code, largeResponse.Body.String())
	}

	redirectRequest := httptest.NewRequest(http.MethodGet, "/redirect-same", nil)
	redirectResponse := httptest.NewRecorder()
	gateway.ServeHTTP(redirectResponse, redirectRequest, settings.PublicID, "/redirect-same")
	if redirectResponse.Header().Get("Location") != "/emby/"+settings.PublicID+"/target" {
		t.Fatalf("same-origin location=%q", redirectResponse.Header().Get("Location"))
	}
	collisionRequest := httptest.NewRequest(http.MethodGet, "/redirect-prefix-collision", nil)
	collisionResponse := httptest.NewRecorder()
	gateway.ServeHTTP(collisionResponse, collisionRequest, settings.PublicID, "/redirect-prefix-collision")
	if collisionResponse.Header().Get("Location") != upstream.URL+"/base-other/target" {
		t.Fatalf("prefix collision location=%q", collisionResponse.Header().Get("Location"))
	}
	crossOriginRequest := httptest.NewRequest(http.MethodGet, "/redirect-cross-origin", nil)
	crossOriginRequest.Header.Set("X-Emby-Token", "client-user-token")
	crossOriginResponse := httptest.NewRecorder()
	gateway.ServeHTTP(crossOriginResponse, crossOriginRequest, settings.PublicID, "/redirect-cross-origin")
	if crossOriginResponse.Header().Get("Location") != crossOrigin.URL+"/credential-sink" || crossOriginCalls.Load() != 0 {
		t.Fatalf("cross-origin redirect location=%q followed=%d", crossOriginResponse.Header().Get("Location"), crossOriginCalls.Load())
	}
	relativeOutsideRequest := httptest.NewRequest(http.MethodGet, "/nested/redirect-relative-outside", nil)
	relativeOutsideResponse := httptest.NewRecorder()
	gateway.ServeHTTP(relativeOutsideResponse, relativeOutsideRequest, settings.PublicID, "/nested/redirect-relative-outside")
	if relativeOutsideResponse.Header().Get("Location") != upstream.URL+"/api/plain" {
		t.Fatalf("relative outside location=%q", relativeOutsideResponse.Header().Get("Location"))
	}
	relativeInsideRequest := httptest.NewRequest(http.MethodGet, "/nested/redirect-relative-inside", nil)
	relativeInsideResponse := httptest.NewRecorder()
	gateway.ServeHTTP(relativeInsideResponse, relativeInsideRequest, settings.PublicID, "/nested/redirect-relative-inside")
	if relativeInsideResponse.Header().Get("Location") != "/emby/"+settings.PublicID+"/target" {
		t.Fatalf("relative inside location=%q", relativeInsideResponse.Header().Get("Location"))
	}
	changedEndpoint := upstream.URL + "/base-next"
	updatedConnection, err := connections.Update(actor, embyConnection.ID, UpdateConnectionInput{Endpoint: &changedEndpoint, Revision: embyConnection.Revision}, RequestContext{})
	if err != nil || updatedConnection.Health.Status != "unknown" {
		t.Fatalf("endpoint update=%+v err=%v", updatedConnection, err)
	}
	var persistedGateway models.EmbyProxyGateway
	if err := db.Where("connection_id = ?", embyConnection.ID).First(&persistedGateway).Error; err != nil || persistedGateway.Enabled || persistedGateway.PolicyRevision <= settings.Revision {
		t.Fatalf("gateway remained enabled after endpoint update: %+v err=%v", persistedGateway, err)
	}
	blockedRequest := httptest.NewRequest(http.MethodGet, "/api/plain", nil)
	blockedResponse := httptest.NewRecorder()
	gateway.ServeHTTP(blockedResponse, blockedRequest, settings.PublicID, "/api/plain")
	if blockedResponse.Code != http.StatusNotFound {
		t.Fatalf("untested updated endpoint remained reachable: %d", blockedResponse.Code)
	}
	previousGatewayRevision := persistedGateway.PolicyRevision
	replacementAPIKey := "replacement-server-admin-key"
	updatedConnection, err = connections.Update(actor, embyConnection.ID, UpdateConnectionInput{APIKey: &replacementAPIKey, Revision: updatedConnection.Revision}, RequestContext{})
	if err != nil {
		t.Fatalf("API key update failed: %v", err)
	}
	if err := db.Where("connection_id = ?", embyConnection.ID).First(&persistedGateway).Error; err != nil || persistedGateway.Enabled || persistedGateway.PolicyRevision <= previousGatewayRevision {
		t.Fatalf("API key update did not revoke gateway policy: %+v err=%v", persistedGateway, err)
	}
	for _, sensitive := range []string{"server-admin-key", replacementAPIKey, "client-user-token", "private-pickcode", "video-file", opaque, signedURL, badTicket, "https://cdn.example.test/private", `D:\\Media\\Local.mkv`} {
		if strings.Contains(gatewayLogs.String(), sensitive) {
			t.Fatalf("gateway logs exposed sensitive value")
		}
	}
}
