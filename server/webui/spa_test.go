package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestRegisterServesSPADeepLinks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assets := fstest.MapFS{
		"index.html":            &fstest.MapFile{Data: []byte("<html>app shell</html>")},
		"assets/application.js": &fstest.MapFile{Data: []byte("console.log('app')")},
	}

	tests := []struct {
		name   string
		path   string
		accept string
	}{
		{name: "root", path: "/"},
		{name: "browser navigation", path: "/system/users/accounts", accept: "text/html,application/xhtml+xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := requestSPA(t, assets, http.MethodGet, tt.path, tt.accept)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if contentType := response.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
				t.Fatalf("Content-Type = %q, want HTML", contentType)
			}
			if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-cache" {
				t.Fatalf("Cache-Control = %q, want no-cache", cacheControl)
			}
		})
	}
}

func TestRegisterDoesNotFallbackForAPIsOrMissingAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>app shell</html>")}}

	tests := []struct {
		name   string
		method string
		path   string
		accept string
	}{
		{name: "api root", method: http.MethodGet, path: "/api", accept: "text/html"},
		{name: "api route", method: http.MethodGet, path: "/api/v1/missing", accept: "text/html"},
		{name: "websocket root", method: http.MethodGet, path: "/ws", accept: "text/html"},
		{name: "websocket route", method: http.MethodGet, path: "/ws/events", accept: "text/html"},
		{name: "proxy root", method: http.MethodGet, path: "/proxy", accept: "text/html"},
		{name: "proxy route", method: http.MethodGet, path: "/proxy/files/movie", accept: "text/html"},
		{name: "asset directory", method: http.MethodGet, path: "/assets/missing", accept: "text/html"},
		{name: "asset file", method: http.MethodGet, path: "/missing.js", accept: "text/html"},
		{name: "json client", method: http.MethodGet, path: "/system/users/accounts", accept: "application/json"},
		{name: "generic client", method: http.MethodGet, path: "/system/users/accounts", accept: "*/*"},
		{name: "omitted accept", method: http.MethodGet, path: "/system/users/accounts"},
		{name: "state changing request", method: http.MethodPost, path: "/system/users/accounts", accept: "text/html"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := requestSPA(t, assets, tt.method, tt.path, tt.accept)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
			}
		})
	}
}

func TestRegisterServesExistingImmutableAsset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assets := fstest.MapFS{
		"index.html":            &fstest.MapFile{Data: []byte("<html>app shell</html>")},
		"assets/application.js": &fstest.MapFile{Data: []byte("console.log('app')")},
	}

	response := requestSPA(t, assets, http.MethodGet, "/assets/application.js", "*/*")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q, want immutable asset caching", cacheControl)
	}
}

func TestRegisterReturnsNotFoundWhenIndexIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := requestSPA(t, fstest.MapFS{}, http.MethodGet, "/system/users/accounts", "text/html")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func requestSPA(t *testing.T, assets fs.FS, method, target, accept string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	Register(router, assets)
	request := httptest.NewRequest(method, target, nil)
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
