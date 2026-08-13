package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/handlers"
	"github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type testClient struct {
	router http.Handler
	cookie *http.Cookie
	csrf   string
}
type testEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func newTestClient(t *testing.T) *testClient {
	t.Helper()
	testRoot := t.TempDir()
	cfg := config.Config{Host: "127.0.0.1", Port: 3000, DatabasePath: filepath.Join(testRoot, "server.db"), LogDirectory: filepath.Join(testRoot, "logs"), Environment: "test", PublicOrigin: "http://localhost:3000", SessionIdleTTL: 2 * time.Hour, SessionMaxTTL: 7 * 24 * time.Hour}
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	log := NewLogger("test")
	audit := services.NewAuditService(db)
	authorization := services.NewAuthorizationService(db)
	auth, err := services.NewAuthService(db, cfg, authorization, audit)
	if err != nil {
		t.Fatal(err)
	}
	admin := services.NewAdminService(db, authorization, auth, audit)
	storages := services.NewStorageService(db, audit)
	directories, err := services.NewDirectoryBrowserService(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	profiles := services.NewMediaClassificationProfileService(db, audit, nil)
	api := handlers.NewAPI(cfg, auth, admin, audit, storages, directories, profiles, log)
	libraries := services.NewMediaLibraryService(db, audit, log)
	profiles.SetReferences(libraries)
	profiles.SetRevisionNotifier(libraries)
	storages.SetReferenceChecker(libraries)
	api.SetMediaLibraryService(libraries)
	t.Cleanup(libraries.Close)
	logManager, err := logging.NewManager(cfg.LogDirectory, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logManager.Close() })
	runtimeLogs, err := services.NewRuntimeLogService(db, logManager, audit)
	if err != nil {
		t.Fatal(err)
	}
	api.SetRuntimeLogService(runtimeLogs)
	return &testClient{router: New(cfg, api, auth, log)}
}

func (c *testClient) request(t *testing.T, method, path string, body any, csrf bool) (int, testEnvelope) {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Origin", "http://localhost:3000")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	if csrf {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	response := httptest.NewRecorder()
	c.router.ServeHTTP(response, req)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "omc_session" {
			c.cookie = cookie
		}
	}
	var envelope testEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response %d %s: %v", response.Code, response.Body.String(), err)
	}
	return response.Code, envelope
}

func (c *testClient) setup(t *testing.T) map[string]any {
	status, envelope := c.request(t, http.MethodPost, "/api/v1/setup/owner", map[string]any{"username": "owner", "display_name": "Owner", "password": "strong-owner-password"}, false)
	if status != http.StatusCreated {
		t.Fatalf("setup status=%d body=%s", status, envelope.Message)
	}
	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	c.csrf, _ = data["csrf_token"].(string)
	if c.csrf == "" || c.cookie == nil {
		t.Fatal("setup did not issue csrf and session cookie")
	}
	return data
}

func (c *testClient) login(t *testing.T, username, password string) {
	status, envelope := c.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": username, "password": password}, false)
	if status != http.StatusOK {
		t.Fatalf("login status=%d message=%s", status, envelope.Message)
	}
	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	c.csrf, _ = data["csrf_token"].(string)
}

func TestSetupSessionAndViewerPermissionBoundary(t *testing.T) {
	client := newTestClient(t)
	setup := client.setup(t)
	status, _ := client.request(t, http.MethodPost, "/api/v1/setup/owner", map[string]any{"username": "other", "password": "another-strong-password"}, false)
	if status != http.StatusConflict {
		t.Fatalf("second setup status=%d", status)
	}

	status, rolesEnvelope := client.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var rolesData struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(rolesEnvelope.Data, &rolesData); err != nil {
		t.Fatal(err)
	}
	var viewerID uint
	for _, role := range rolesData.List {
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	if viewerID == 0 {
		t.Fatal("viewer role missing")
	}
	status, _ = client.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "viewer", "display_name": "Viewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create viewer status=%d", status)
	}

	viewer := newTestClientWithRouter(client.router)
	viewer.login(t, "viewer", "viewer-strong-password")
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/dashboard", nil, false)
	if status != http.StatusOK {
		t.Fatalf("viewer dashboard status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/users", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer users status=%d", status)
	}

	user := setup["user"].(map[string]any)
	ownerID := uint(user["id"].(float64))
	status, _ = client.request(t, http.MethodPost, "/api/v1/users/"+uintString(ownerID)+"/disable", map[string]any{}, true)
	if status != http.StatusForbidden {
		t.Fatalf("owner self-disable status=%d", status)
	}
	status, _ = client.request(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("logout status=%d", status)
	}
	status, _ = client.request(t, http.MethodGet, "/api/v1/auth/me", nil, false)
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked session status=%d", status)
	}
}

func TestDelegatedRoleCannotEscalatePrivileges(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	status, roleEnvelope := owner.request(t, http.MethodPost, "/api/v1/roles", map[string]any{"code": "role_builder", "name": "Role Builder", "permissions": []string{"roles.read", "roles.create"}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create role status=%d message=%s", status, roleEnvelope.Message)
	}
	var role struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(roleEnvelope.Data, &role); err != nil {
		t.Fatal(err)
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "delegate", "display_name": "Delegate", "password": "delegate-strong-password", "role_ids": []uint{role.ID}}, true)
	if status != http.StatusCreated {
		t.Fatalf("create delegate status=%d", status)
	}
	delegate := newTestClientWithRouter(owner.router)
	delegate.login(t, "delegate", "delegate-strong-password")
	status, _ = delegate.request(t, http.MethodPost, "/api/v1/roles", map[string]any{"code": "escalated", "name": "Escalated", "permissions": []string{"system.admin"}}, true)
	if status != http.StatusForbidden {
		t.Fatalf("privilege escalation status=%d", status)
	}
}

func TestStorageCRUDRBACAndDeleteLeavesFilesUntouched(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	root := t.TempDir()
	marker := filepath.Join(root, "nested", "movie.mp4")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, envelope := owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": " Local  Media ", "type": "local", "root_path": root, "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create storage status=%d message=%s", status, envelope.Message)
	}
	var created struct {
		ID       uint   `json:"id"`
		RootPath string `json:"root_path"`
		Probe    struct {
			Readable  bool    `json:"readable"`
			FreeBytes *uint64 `json:"free_bytes"`
		} `json:"probe"`
	}
	if err := json.Unmarshal(envelope.Data, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.RootPath != filepath.Clean(root) || !created.Probe.Readable || created.Probe.FreeBytes == nil {
		t.Fatalf("unexpected storage: %+v", created)
	}

	status, envelope = owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "local media", "type": "local", "root_path": t.TempDir(), "enabled": true}, true)
	if status != http.StatusConflict {
		t.Fatalf("duplicate name status=%d code=%s", status, envelope.Data)
	}
	status, envelope = owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "Relative", "type": "local", "root_path": "relative", "enabled": true}, true)
	if status != http.StatusBadRequest || !bytes.Contains(envelope.Data, []byte("storage_path_not_absolute")) {
		t.Fatalf("relative path status=%d data=%s", status, envelope.Data)
	}

	status, _ = owner.request(t, http.MethodPost, "/api/v1/storages/"+uintString(created.ID)+"/test", map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("test storage status=%d", status)
	}
	status, _ = owner.request(t, http.MethodDelete, "/api/v1/storages/"+uintString(created.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("delete storage status=%d", status)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "media" {
		t.Fatalf("media changed after config deletion: %q %v", content, err)
	}
	status, auditEnvelope := owner.request(t, http.MethodGet, "/api/v1/audit?limit=20", nil, false)
	if status != http.StatusOK {
		t.Fatalf("audit status=%d", status)
	}
	if bytes.Contains(auditEnvelope.Data, []byte(root)) || bytes.Contains(auditEnvelope.Data, []byte("movie.mp4")) {
		t.Fatalf("audit response leaked local path or child filename: %s", auditEnvelope.Data)
	}

	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var rolesData struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(rolesEnvelope.Data, &rolesData); err != nil {
		t.Fatal(err)
	}
	var viewerID uint
	for _, role := range rolesData.List {
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "storageviewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "storageviewer", "viewer-strong-password")
	deniedRequests := []struct {
		method string
		path   string
		body   any
		csrf   bool
	}{
		{http.MethodGet, "/api/v1/storages", nil, false},
		{http.MethodPost, "/api/v1/storages", map[string]any{"name": "Denied", "root_path": root}, true},
		{http.MethodPatch, "/api/v1/storages/1", map[string]any{"enabled": false}, true},
		{http.MethodDelete, "/api/v1/storages/1", map[string]any{}, true},
		{http.MethodPost, "/api/v1/storages/1/test", map[string]any{}, true},
	}
	for _, request := range deniedRequests {
		status, envelope = viewer.request(t, request.method, request.path, request.body, request.csrf)
		if status != http.StatusForbidden || !bytes.Contains(envelope.Data, []byte(services.CodePermissionDenied)) {
			t.Fatalf("viewer %s %s status=%d data=%s", request.method, request.path, status, envelope.Data)
		}
	}
}

func TestMediaLibraryAPICRUDRBACAndAutomaticInitialization(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "API.Movie.mp4"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, storageEnvelope := owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "Library source", "type": "local", "root_path": root, "enabled": true}, true)
	if status != http.StatusCreated {
		t.Fatalf("create storage status=%d message=%s", status, storageEnvelope.Message)
	}
	var storage struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(storageEnvelope.Data, &storage); err != nil {
		t.Fatal(err)
	}
	status, profilesEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var profiles struct {
		List []struct {
			ID uint `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(profilesEnvelope.Data, &profiles); err != nil || len(profiles.List) == 0 {
		t.Fatalf("profiles=%+v err=%v", profiles, err)
	}
	status, directoryEnvelope := owner.request(t, http.MethodGet, "/api/v1/storages/"+uintString(storage.ID)+"/directory", nil, false)
	if status != http.StatusOK {
		t.Fatalf("storage directory status=%d", status)
	}
	var directory struct {
		CurrentSelectionToken string `json:"current_selection_token"`
	}
	if err := json.Unmarshal(directoryEnvelope.Data, &directory); err != nil || directory.CurrentSelectionToken == "" {
		t.Fatalf("directory=%+v err=%v", directory, err)
	}
	payload := map[string]any{"name": "API library", "storage_id": storage.ID, "profile_id": profiles.List[0].ID, "relative_root_token": directory.CurrentSelectionToken, "enabled": true, "recursive": true}
	status, rejected := owner.request(t, http.MethodPost, "/api/v1/media-libraries", map[string]any{"name": "Raw path", "storage_id": storage.ID, "profile_id": profiles.List[0].ID, "relative_root": "/", "enabled": false}, true)
	if status != http.StatusBadRequest || !bytes.Contains(rejected.Data, []byte(services.CodeInvalidRequest)) {
		t.Fatalf("raw relative root status=%d data=%s", status, rejected.Data)
	}
	status, libraryEnvelope := owner.request(t, http.MethodPost, "/api/v1/media-libraries", payload, true)
	if status != http.StatusCreated {
		t.Fatalf("create library status=%d message=%s", status, libraryEnvelope.Message)
	}
	var library struct {
		ID           uint   `json:"id"`
		RelativeRoot string `json:"relative_root"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(libraryEnvelope.Data, &library); err != nil {
		t.Fatal(err)
	}
	if library.RelativeRoot != "/" || strings.Contains(string(libraryEnvelope.Data), root) {
		t.Fatalf("unsafe library response: %s", libraryEnvelope.Data)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		status, libraryEnvelope = owner.request(t, http.MethodGet, "/api/v1/media-libraries/"+uintString(library.ID), nil, false)
		if status != http.StatusOK {
			t.Fatal(status)
		}
		if err := json.Unmarshal(libraryEnvelope.Data, &library); err != nil {
			t.Fatal(err)
		}
		if library.Status == "listening" {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if library.Status != "listening" {
		t.Fatalf("library status=%q", library.Status)
	}
	status, entriesEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-libraries/"+uintString(library.ID)+"/entries", nil, false)
	if status != http.StatusOK || !strings.Contains(string(entriesEnvelope.Data), "/API.Movie.mp4") || strings.Contains(string(entriesEnvelope.Data), root) {
		t.Fatalf("entries status=%d data=%s", status, entriesEnvelope.Data)
	}
	status, _ = owner.request(t, http.MethodDelete, "/api/v1/storages/"+uintString(storage.ID), map[string]any{}, true)
	if status != http.StatusConflict {
		t.Fatalf("referenced storage delete status=%d", status)
	}
	status, _ = owner.request(t, http.MethodDelete, "/api/v1/media-libraries/"+uintString(library.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("delete media library status=%d", status)
	}
}

func TestDirectoryPickerRBACNoStoreAndStorageRoundTrip(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/filesystem/roots", nil)
	req.AddCookie(owner.cookie)
	response := httptest.NewRecorder()
	owner.router.ServeHTTP(response, req)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("roots status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
	var roots testEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &roots); err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Items []struct {
			SelectionToken string `json:"selection_token"`
		} `json:"items"`
	}
	if err := json.Unmarshal(roots.Data, &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) == 0 {
		t.Skip("Server process has no selectable filesystem roots")
	}
	status, envelope := owner.request(t, http.MethodPost, "/api/v1/storages", map[string]any{"name": "Picker root", "type": "local", "picker_token": listing.Items[0].SelectionToken}, true)
	if status != http.StatusCreated {
		t.Fatalf("picker create status=%d message=%s data=%s", status, envelope.Message, envelope.Data)
	}

	// Viewer has neither middleware nor service authorization for filesystem disclosure.
	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var rolesData struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(rolesEnvelope.Data, &rolesData); err != nil {
		t.Fatal(err)
	}
	var viewerID uint
	for _, role := range rolesData.List {
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "picker-viewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "picker-viewer", "viewer-strong-password")
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/filesystem/roots", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer roots status=%d", status)
	}
	viewerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/filesystem/roots", nil)
	viewerRequest.AddCookie(viewer.cookie)
	viewerResponse := httptest.NewRecorder()
	viewer.router.ServeHTTP(viewerResponse, viewerRequest)
	if viewerResponse.Code != http.StatusForbidden || viewerResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("viewer roots cache status=%d cache=%q", viewerResponse.Code, viewerResponse.Header().Get("Cache-Control"))
	}
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/filesystem/directories?token=invalid", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer directories status=%d", status)
	}
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/storages/1/directory", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer storage directory status=%d", status)
	}
}

func TestMediaClassificationProfileLifecycleAndPermissionBoundary(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	status, listEnvelope := owner.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatalf("list status=%d", status)
	}
	var listData struct {
		List []struct {
			ID   uint    `json:"id"`
			Code *string `json:"code"`
		} `json:"list"`
	}
	if err := json.Unmarshal(listEnvelope.Data, &listData); err != nil || len(listData.List) != 1 {
		t.Fatalf("list=%s err=%v", listEnvelope.Data, err)
	}
	builtinID := listData.List[0].ID
	status, _ = owner.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(builtinID), map[string]any{"revision": 1, "name": "x", "rules": map[string]any{}}, true)
	if status != http.StatusForbidden {
		t.Fatalf("protected update status=%d", status)
	}
	status, createEnvelope := owner.request(t, http.MethodPost, "/api/v1/media-classification-profiles", map[string]any{"name": "API Custom"}, true)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d message=%s", status, createEnvelope.Message)
	}
	var created struct {
		ID       uint            `json:"id"`
		Revision uint64          `json:"revision"`
		Rules    json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(createEnvelope.Data, &created); err != nil {
		t.Fatal(err)
	}
	status, _ = owner.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(created.ID), map[string]any{"revision": created.Revision, "name": "API Custom", "rules": json.RawMessage(created.Rules)}, true)
	if status != http.StatusOK {
		t.Fatalf("update status=%d", status)
	}
	status, stale := owner.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(created.ID), map[string]any{"revision": created.Revision, "name": "API Custom", "rules": json.RawMessage(created.Rules)}, true)
	if status != http.StatusConflict || !bytes.Contains(stale.Data, []byte(services.CodeProfileRevisionConflict)) {
		t.Fatalf("stale status=%d data=%s", status, stale.Data)
	}
	status, copyEnvelope := owner.request(t, http.MethodPost, "/api/v1/media-classification-profiles/"+uintString(builtinID)+"/copy", map[string]any{}, true)
	if status != http.StatusCreated {
		t.Fatalf("copy status=%d", status)
	}
	if bytes.Contains(copyEnvelope.Data, []byte("default-movie-animation")) {
		t.Fatal("copy retained built-in category ids")
	}
	status, auditEnvelope := owner.request(t, http.MethodGet, "/api/v1/audit?limit=50", nil, false)
	if status != http.StatusOK || bytes.Contains(auditEnvelope.Data, []byte("rules_json")) || bytes.Contains(auditEnvelope.Data, []byte("动画电影")) {
		t.Fatalf("unsafe audit=%s", auditEnvelope.Data)
	}

	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var roles struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	_ = json.Unmarshal(rolesEnvelope.Data, &roles)
	var operatorID, viewerID uint
	for _, role := range roles.List {
		if role.Code == "operator" {
			operatorID = role.ID
		}
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "profileoperator", "password": "operator-strong-password", "role_ids": []uint{operatorID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	operator := newTestClientWithRouter(owner.router)
	operator.login(t, "profileoperator", "operator-strong-password")
	status, operatorCreate := operator.request(t, http.MethodPost, "/api/v1/media-classification-profiles", map[string]any{"name": "Operator Profile"}, true)
	if status != http.StatusCreated {
		t.Fatalf("operator create=%d message=%s", status, operatorCreate.Message)
	}
	var operatorProfile struct {
		ID       uint            `json:"id"`
		Revision uint64          `json:"revision"`
		Rules    json.RawMessage `json:"rules"`
	}
	if err := json.Unmarshal(operatorCreate.Data, &operatorProfile); err != nil {
		t.Fatal(err)
	}
	status, _ = operator.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatalf("operator list=%d", status)
	}
	status, _ = operator.request(t, http.MethodPost, "/api/v1/media-classification-profiles/"+uintString(operatorProfile.ID)+"/copy", map[string]any{"name": "Operator Copy"}, true)
	if status != http.StatusCreated {
		t.Fatalf("operator copy=%d", status)
	}
	status, _ = operator.request(t, http.MethodPatch, "/api/v1/media-classification-profiles/"+uintString(operatorProfile.ID), map[string]any{"revision": operatorProfile.Revision, "name": "Operator Updated", "rules": operatorProfile.Rules}, true)
	if status != http.StatusOK {
		t.Fatalf("operator update=%d", status)
	}
	status, _ = operator.request(t, http.MethodDelete, "/api/v1/media-classification-profiles/"+uintString(operatorProfile.ID), map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("operator delete=%d", status)
	}

	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "profileviewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "profileviewer", "viewer-strong-password")
	status, _ = viewer.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer list=%d", status)
	}
	status, _ = viewer.request(t, http.MethodPost, "/api/v1/media-classification-profiles", map[string]any{"name": "Denied"}, true)
	if status != http.StatusForbidden {
		t.Fatalf("viewer create=%d", status)
	}
}

func TestMediaClassificationProfileRequestsRejectUnknownFields(t *testing.T) {
	client := newTestClient(t)
	client.setup(t)
	status, _ := client.request(t, http.MethodPost, "/api/v1/media-classification-profiles", map[string]any{"name": "Unknown", "mystery": true}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("create status=%d", status)
	}
	status, listEnvelope := client.request(t, http.MethodGet, "/api/v1/media-classification-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var listData struct {
		List []struct {
			ID uint `json:"id"`
		} `json:"list"`
	}
	if err := json.Unmarshal(listEnvelope.Data, &listData); err != nil {
		t.Fatal(err)
	}
	status, _ = client.request(t, http.MethodPost, "/api/v1/media-classification-profiles/"+uintString(listData.List[0].ID)+"/copy", map[string]any{"mystery": true}, true)
	if status != http.StatusBadRequest {
		t.Fatalf("copy status=%d", status)
	}
}

func TestRuntimeLogsRBACNoStoreQuerySettingsAndExport(t *testing.T) {
	owner := newTestClient(t)
	owner.setup(t)
	for _, path := range []string{"/api/v1/runtime-logs", "/api/v1/runtime-logs/facets", "/api/v1/runtime-logs/settings"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(owner.cookie)
		response := httptest.NewRecorder()
		owner.router.ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s status=%d cache=%q", path, response.Code, response.Header().Get("Cache-Control"))
		}
	}
	status, settingsEnvelope := owner.request(t, http.MethodGet, "/api/v1/runtime-logs/settings", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var settings map[string]any
	if json.Unmarshal(settingsEnvelope.Data, &settings) != nil {
		t.Fatal("decode settings")
	}
	settings["max_file_mib"] = 21
	status, _ = owner.request(t, http.MethodPatch, "/api/v1/runtime-logs/settings", settings, true)
	if status != http.StatusOK {
		t.Fatalf("update settings status=%d", status)
	}
	status, rolesEnvelope := owner.request(t, http.MethodGet, "/api/v1/roles", nil, false)
	if status != http.StatusOK {
		t.Fatal(status)
	}
	var roles struct {
		List []struct {
			ID   uint   `json:"id"`
			Code string `json:"code"`
		} `json:"list"`
	}
	_ = json.Unmarshal(rolesEnvelope.Data, &roles)
	var operatorID, viewerID uint
	for _, role := range roles.List {
		if role.Code == "operator" {
			operatorID = role.ID
		}
		if role.Code == "viewer" {
			viewerID = role.ID
		}
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "logoperator", "password": "operator-strong-password", "role_ids": []uint{operatorID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	operator := newTestClientWithRouter(owner.router)
	operator.login(t, "logoperator", "operator-strong-password")
	status, _ = operator.request(t, http.MethodGet, "/api/v1/runtime-logs", nil, false)
	if status != http.StatusOK {
		t.Fatalf("operator read=%d", status)
	}
	status, _ = operator.request(t, http.MethodPost, "/api/v1/runtime-logs/export", map[string]any{}, true)
	if status != http.StatusForbidden {
		t.Fatalf("operator export=%d", status)
	}
	status, _ = owner.request(t, http.MethodPost, "/api/v1/users", map[string]any{"username": "logviewer", "password": "viewer-strong-password", "role_ids": []uint{viewerID}}, true)
	if status != http.StatusCreated {
		t.Fatal(status)
	}
	viewer := newTestClientWithRouter(owner.router)
	viewer.login(t, "logviewer", "viewer-strong-password")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-logs", nil)
	req.AddCookie(viewer.cookie)
	response := httptest.NewRecorder()
	viewer.router.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("viewer status=%d cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
}

func newTestClientWithRouter(router http.Handler) *testClient { return &testClient{router: router} }
func uintString(value uint) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	result := ""
	for value > 0 {
		result = string(digits[value%10]) + result
		value /= 10
	}
	return result
}
