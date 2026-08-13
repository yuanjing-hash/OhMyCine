package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

const (
	ContextActor        = "actor"
	ContextSessionToken = "session_token"
	ContextRequestID    = "request_id"
)

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := strings.TrimSpace(c.GetHeader("X-Request-ID"))
		if requestID == "" || len(requestID) > 64 {
			raw := make([]byte, 12)
			_, _ = rand.Read(raw)
			requestID = hex.EncodeToString(raw)
		}
		c.Set(ContextRequestID, requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

func Logger(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		event := log.Info()
		if c.Writer.Status() >= http.StatusInternalServerError {
			event = log.Error()
		} else if c.Writer.Status() >= http.StatusBadRequest {
			event = log.Warn()
		}
		event.Str("request_id", RequestIDFrom(c)).Str("method", c.Request.Method).Str("route", c.FullPath()).Int("status", c.Writer.Status()).Int64("duration_ms", time.Since(start).Milliseconds())
		if actor, ok := ActorFrom(c); ok {
			event = event.Uint("user_id", actor.User.ID)
		}
		event.Msg("HTTP request completed")
	}
}

func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		c.Next()
	}
}

func BrowserMutationProtection(allowedOrigins []string) gin.HandlerFunc {
	allowed := map[string]struct{}{}
	for _, origin := range allowedOrigins {
		allowed[strings.TrimRight(origin, "/")] = struct{}{}
	}
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		if site := strings.ToLower(c.GetHeader("Sec-Fetch-Site")); site == "cross-site" {
			abortJSON(c, http.StatusForbidden, "CROSS_SITE_REQUEST", "跨站请求已拒绝")
			return
		}
		origin := strings.TrimRight(strings.TrimSpace(c.GetHeader("Origin")), "/")
		if origin == "" {
			if referer := strings.TrimSpace(c.GetHeader("Referer")); referer != "" {
				if parsed, err := url.Parse(referer); err == nil {
					origin = parsed.Scheme + "://" + parsed.Host
				}
			}
		}
		if _, ok := allowed[origin]; !ok {
			abortJSON(c, http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "请求来源不受信任")
			return
		}
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || strings.ToLower(mediaType) != "application/json" {
			abortJSON(c, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "仅接受 application/json 请求")
			return
		}
		c.Next()
	}
}

func Auth(auth *services.AuthService, cookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(cookieName)
		if err != nil {
			abortJSON(c, http.StatusUnauthorized, services.CodeNotAuthenticated, "请先登录")
			return
		}
		actor, _, err := auth.Authenticate(token)
		if err != nil {
			abortJSON(c, http.StatusUnauthorized, services.ErrorCode(err), services.ErrorMessage(err))
			return
		}
		c.Set(ContextActor, actor)
		c.Set(ContextSessionToken, token)
		c.Next()
	}
}

func CSRF(auth *services.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
			c.Next()
			return
		}
		token, _ := c.Get(ContextSessionToken)
		if !auth.ValidateCSRF(stringValue(token), c.GetHeader("X-CSRF-Token")) {
			abortJSON(c, http.StatusForbidden, "CSRF_INVALID", "CSRF 校验失败")
			return
		}
		c.Next()
	}
}

func RequirePermission(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := ActorFrom(c)
		if !ok || !actor.Can(code) {
			abortJSON(c, http.StatusForbidden, services.CodePermissionDenied, "没有执行此操作的权限")
			return
		}
		c.Next()
	}
}

func NoStore() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

func ActorFrom(c *gin.Context) (services.Actor, bool) {
	value, ok := c.Get(ContextActor)
	if !ok {
		return services.Actor{}, false
	}
	actor, ok := value.(services.Actor)
	return actor, ok
}

func SessionTokenFrom(c *gin.Context) string {
	value, _ := c.Get(ContextSessionToken)
	return stringValue(value)
}
func RequestIDFrom(c *gin.Context) string {
	value, _ := c.Get(ContextRequestID)
	return stringValue(value)
}
func RequestContextFrom(c *gin.Context) services.RequestContext {
	return services.RequestContext{RequestID: RequestIDFrom(c), IPHint: c.ClientIP()}
}

func stringValue(value any) string { text, _ := value.(string); return text }

func abortJSON(c *gin.Context, status int, errorCode, message string) {
	c.AbortWithStatusJSON(status, gin.H{"code": status*100 + 1, "message": message, "data": gin.H{"error_code": errorCode}})
}
