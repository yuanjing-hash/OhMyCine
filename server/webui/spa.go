package webui

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

func Register(router *gin.Engine, assets fs.FS) {
	router.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}
		requestPath := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")
		if hasPathPrefix(requestPath, "api") || hasPathPrefix(requestPath, "ws") || hasPathPrefix(requestPath, "proxy") {
			c.Status(http.StatusNotFound)
			return
		}
		if requestPath == "." || requestPath == "" {
			requestPath = "index.html"
		}
		content, err := fs.ReadFile(assets, requestPath)
		if err != nil {
			if !isSPANavigationRequest(c.Request, requestPath) {
				c.Status(http.StatusNotFound)
				return
			}
			requestPath = "index.html"
			content, err = fs.ReadFile(assets, requestPath)
			if err != nil {
				c.Status(http.StatusNotFound)
				return
			}
		}
		if requestPath == "index.html" {
			c.Header("Cache-Control", "no-cache")
		} else if strings.HasPrefix(requestPath, "assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		contentType := mime.TypeByExtension(path.Ext(requestPath))
		if contentType == "" {
			contentType = http.DetectContentType(content)
		}
		c.Data(http.StatusOK, contentType, content)
	})
}

func hasPathPrefix(requestPath, prefix string) bool {
	return requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
}

func isSPANavigationRequest(request *http.Request, requestPath string) bool {
	if hasPathPrefix(requestPath, "assets") || path.Ext(path.Base(requestPath)) != "" {
		return false
	}
	return strings.Contains(request.Header.Get("Accept"), "text/html")
}
