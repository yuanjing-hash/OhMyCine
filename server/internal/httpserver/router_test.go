package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/internal/handlers"
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
	cfg := config.Config{Host: "127.0.0.1", Port: 3000, DatabasePath: filepath.Join(t.TempDir(), "server.db"), Environment: "test", PublicOrigin: "http://localhost:3000", SessionIdleTTL: 2 * time.Hour, SessionMaxTTL: 7 * 24 * time.Hour}
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
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
	api := handlers.NewAPI(cfg, auth, admin, audit, log)
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
