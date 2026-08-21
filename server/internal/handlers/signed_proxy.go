package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

func (a *API) SignedSTRMProxy(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Header("Allow", "GET, HEAD")
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	expiry, err := strconv.ParseInt(strings.TrimSpace(c.Query("exp")), 10, 64)
	if err != nil || a.signedProxy == nil {
		c.Status(http.StatusForbidden)
		return
	}
	redirect, err := a.signedProxy.ResolveForClient(c.Request.Context(), c.Param("opaque"), c.Query("kid"), expiry, c.Query("sig"), c.GetHeader("User-Agent"), c.Request.RemoteAddr)
	if err != nil {
		c.Status(services.ProxyHTTPStatus(err))
		return
	}
	c.Header("Location", redirect.URL)
	c.Header("Referrer-Policy", "no-referrer")
	c.Status(http.StatusFound)
}
