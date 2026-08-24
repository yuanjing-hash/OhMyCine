package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/middleware"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

type playerDeviceResponse struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	ClientKind        string    `json:"client_kind"`
	CreatedAt         time.Time `json:"created_at"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	IdleExpiresAt     time.Time `json:"idle_expires_at"`
	AbsoluteExpiresAt time.Time `json:"absolute_expires_at"`
}

type playerNoStoreWriter struct{ http.ResponseWriter }

func (writer playerNoStoreWriter) WriteHeader(statusCode int) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.ResponseWriter.WriteHeader(statusCode)
}

func playerDeviceDTO(device models.DeviceToken) playerDeviceResponse {
	return playerDeviceResponse{
		ID: device.ID, Name: device.DeviceName, ClientKind: device.ClientKind,
		CreatedAt: device.CreatedAt, LastSeenAt: device.LastSeenAt,
		IdleExpiresAt: device.IdleExpiresAt, AbsoluteExpiresAt: device.AbsoluteExpiresAt,
	}
}

func (a *API) PlayerLogin(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input struct {
		Username   string `json:"username" binding:"required"`
		Password   string `json:"password" binding:"required"`
		DeviceID   string `json:"device_id" binding:"required"`
		DeviceName string `json:"device_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		writeError(c, a.log, invalid("请输入 Server 账号、密码和设备信息", err))
		return
	}
	actor, token, device, err := a.auth.DeviceLogin(input.Username, input.Password, input.DeviceID, input.DeviceName, middleware.RequestContextFrom(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{
		"access_token": token, "token_type": "Bearer",
		"user": services.CurrentUserFromActor(actor), "device": playerDeviceDTO(device),
	})
}

func (a *API) PlayerLogout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	device, _ := middleware.DeviceFrom(c)
	if err := a.auth.DeviceLogout(middleware.DeviceTokenFrom(c), actor, device, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"logged_out": true})
}

func (a *API) PlayerBootstrap(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	device, _ := middleware.DeviceFrom(c)
	mediaLibraryCount := 0
	if actor.Can(authz.PermissionMediaLibrariesRead) {
		libraries, err := a.libraries.PlayerLibraries(actor)
		if err != nil {
			writeError(c, a.log, err)
			return
		}
		mediaLibraryCount = len(libraries)
	}
	embyInstances := []services.PlayerEmbyInstance{}
	if a.connections != nil {
		var err error
		embyInstances, err = a.connections.PlayerEmbyInstances(actor)
		if err != nil {
			writeError(c, a.log, err)
			return
		}
	}
	success(c, http.StatusOK, gin.H{
		"server": gin.H{"name": "OhMyCine Server", "api_version": "v1"},
		"user":   services.CurrentUserFromActor(actor), "device": playerDeviceDTO(device),
		"media_library_count": mediaLibraryCount,
		"capabilities":        []string{"media_catalog", "direct_stream"}, "emby_instances": embyInstances,
	})
}

func (a *API) PlayerDevices(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	devices, err := a.auth.ListDevices(actor)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	result := make([]playerDeviceResponse, 0, len(devices))
	for _, device := range devices {
		result = append(result, playerDeviceDTO(device))
	}
	success(c, http.StatusOK, gin.H{"list": result, "total": len(result)})
}

func (a *API) RevokePlayerDevice(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, _ := middleware.ActorFrom(c)
	id := strings.TrimSpace(c.Param("id"))
	if id == "" || len(id) > 64 {
		writeError(c, a.log, invalid("设备 ID 无效", nil))
		return
	}
	if err := a.auth.RevokeDevice(actor, id, middleware.RequestContextFrom(c)); err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"revoked": true})
}

func (a *API) PlayerMediaLibraries(c *gin.Context) {
	libraries, err := a.libraries.PlayerLibraries(mustActor(c))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, gin.H{"list": libraries, "total": len(libraries)})
}

func (a *API) PlayerMediaLibraryCategories(c *gin.Context) {
	id, ok := a.playerPathID(c, "id")
	if !ok {
		return
	}
	items, err := a.libraries.PlayerCategories(mustActor(c), id)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	if a.libraryArtwork != nil {
		items = a.libraryArtwork.DecorateMediaCategories(c.Request.Context(), id, items)
	}
	success(c, http.StatusOK, gin.H{"list": items, "total": len(items)})
}

func (a *API) PlayerMediaLibraryCatalog(c *gin.Context) {
	id, ok := a.playerPathID(c, "id")
	if !ok {
		return
	}
	query, err := mediaPageQuery(c, false)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	page, err := a.libraries.PlayerCatalog(mustActor(c), id, query)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, page)
}

func (a *API) PlayerMediaLibraryDetail(c *gin.Context) {
	id, ok := a.playerPathID(c, "id")
	if !ok {
		return
	}
	detail, err := a.libraries.PlayerCatalogDetail(mustActor(c), id, c.Param("work"))
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, detail)
}

func (a *API) PlayerSearch(c *gin.Context) {
	query, err := mediaPageQuery(c, false)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	page, err := a.libraries.PlayerSearch(mustActor(c), query)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	success(c, http.StatusOK, page)
}

func (a *API) PlayerMediaEntryStream(c *gin.Context) {
	id, ok := a.playerPathID(c, "id")
	if !ok {
		return
	}
	stream, err := a.signedProxy.ResolvePlayerEntry(c.Request.Context(), mustActor(c), id, c.GetHeader("User-Agent"), c.Request.RemoteAddr)
	if err != nil {
		writeError(c, a.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if stream.File != nil {
		defer stream.File.Close()
		http.ServeContent(playerNoStoreWriter{ResponseWriter: c.Writer}, c.Request, stream.Name, stream.ModifiedAt, stream.File)
		return
	}
	if stream.RedirectURL == "" {
		writeError(c, a.log, services.PlayerStreamUnavailableError())
		return
	}
	c.Header("Location", stream.RedirectURL)
	c.Status(http.StatusFound)
}

func mustActor(c *gin.Context) services.Actor {
	actor, _ := middleware.ActorFrom(c)
	return actor
}

func (a *API) playerPathID(c *gin.Context, name string) (uint, bool) {
	raw := strings.TrimSpace(c.Param(name))
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		writeError(c, a.log, invalid("资源 ID 无效", err))
		return 0, false
	}
	return uint(value), true
}
